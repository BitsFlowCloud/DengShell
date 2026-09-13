package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDiagnosticTargetValidation(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "::1", "2001:db8::1", "fe80::1%eth0"} {
		if _, err := validateDiagnosticIP(value); err != nil {
			t.Errorf("valid target %q: %v", value, err)
		}
	}
	for _, value := range []string{"example.com", "127.0.0.1; touch /tmp/no", "$(echo x)", "--help", "fe80::1%eth0'; touch /tmp/no"} {
		if _, err := validateDiagnosticIP(value); err == nil {
			t.Errorf("accepted invalid target %q", value)
		}
	}
	if got, err := resolveDiagnosticTarget(context.Background(), "127.0.0.1"); err != nil || got != "127.0.0.1" {
		t.Fatalf("literal target %s %v", got, err)
	}
}
func TestDiagnosticConcurrentOutputAndCancellation(t *testing.T) {
	d := &Diagnostic{report: DiagnosticReport{Status: "running"}}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for n := 0; n < 100; n++ {
				d.Write([]byte(strings.Repeat("x", 1000)))
				_ = d.snapshot()
			}
		}()
	}
	group.Wait()
	if len(d.snapshot().Output) != diagnosticOutputLimit {
		t.Fatal("output was not capped")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.finish(ctx, context.Canceled)
	if d.snapshot().Status != "cancelled" || d.snapshot().FinishedAt == nil {
		t.Fatal("cancellation status lost")
	}
}
func TestDiagnosticHopLossExcludesUnavailable(t *testing.T) {
	var hop diagnosticHop
	hop.add(ICMPProbeResult{Status: "unavailable", Error: "permission denied"})
	hop.add(ICMPProbeResult{Status: "timeout"})
	hop.add(ICMPProbeResult{Status: "unreachable", Responded: true, TTLExpired: true, Address: "192.0.2.1", Milliseconds: 10})
	hop.add(ICMPProbeResult{Status: "reply", Responded: true, Address: "192.0.2.1", Milliseconds: 30})
	if hop.Sent != 3 || hop.Received != 2 || hop.Total != 40 || hop.Best != 10 || hop.Worst != 30 {
		t.Fatalf("bad hop counters: %+v", hop)
	}
	report := windowsDiagnosticOutput("test\n", []diagnosticHop{hop}, 4)
	if !strings.Contains(report, "33.3") || !strings.Contains(report, "20.0") || !strings.Contains(report, "192.0.2.1") {
		t.Fatal(report)
	}
}
func TestDiagnosticConcurrencyLimit(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.cancel()
	p, err := a.store.Save(Profile{Name: "diagnostic", Host: "127.0.0.1", Port: 22, User: "test", Auth: "password"}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{ID: "test-session", ProfileID: p.ID, ctx: a.ctx}
	a.sessions[s.ID] = s
	a.diagnostics = map[string]*Diagnostic{}
	for _, id := range []string{"1", "2", "3", "4"} {
		a.diagnostics[id] = &Diagnostic{report: DiagnosticReport{ID: id, Status: "running", StartedAt: time.Now()}}
	}
	if _, err := a.startDiagnostic(context.Background(), s.ID, "local", ""); err == nil {
		t.Fatal("unbounded parallel diagnostics")
	}
}
