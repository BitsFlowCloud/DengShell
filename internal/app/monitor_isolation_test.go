package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Drop only a channel's CLOSE, while real encrypted requests/replies on the
// transport remain functional. This reproduces an unacknowledged collector
// shutdown without artificially making the whole SSH server unreachable.
type monitorIgnoreCloseConn struct{ ssh.Conn }
type monitorIgnoreCloseChannel struct{ ssh.Channel }

func (monitorIgnoreCloseChannel) Close() error { return nil }
func (c monitorIgnoreCloseConn) OpenChannel(kind string, data []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	ch, requests, err := c.Conn.OpenChannel(kind, data)
	if err != nil {
		return nil, nil, err
	}
	return monitorIgnoreCloseChannel{ch}, requests, nil
}

func TestMonitorStuckCleanupKeepsHealthySSHAndBoundsWorkers(t *testing.T) {
	s, commands, opens := backgroundNetworkSSHFixture(t, false, func(ctx context.Context, n int) ([]byte, error) {
		if n == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []byte("terminal-still-works"), nil
	})
	s.client = &ssh.Client{Conn: monitorIgnoreCloseConn{s.client.Conn}}
	s.diagnosticLog = &sshDiagnosticLog{}
	go s.heartbeatWithTimeout(2 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err := s.processCollector.run(ctx, s, "processes", networkMonitorCommand, monitorOutputLimit)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	// Exceed this fixture's former forced-disconnect grace with working SSH.
	time.Sleep(350 * time.Millisecond)
	if s.ctx.Err() != nil {
		t.Fatal("a timed-out collector disconnected the terminal")
	}
	for i := 0; i < 100; i++ {
		_, err = s.processCollector.run(context.Background(), s, "processes", networkMonitorCommand, monitorOutputLimit)
		if !errors.Is(err, errMonitorCleaningUp) {
			t.Fatal("stalled worker was replaced or duplicate work was admitted", err)
		}
	}
	if opens.Load() != 1 || commands.Load() != 1 {
		t.Fatal("repeated process clicks accumulated SSH channels", opens.Load(), commands.Load())
	}
	awaitNetworkCondition(t, time.Second, func() bool { return s.Latency().Ready })
	probe, err := s.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	data, err := probe.Output("exec " + posixShellCommand(networkMonitorCommand))
	if err != nil || string(data) != "terminal-still-works" {
		t.Fatalf("independent SSH command was affected: %q %v", data, err)
	}
	if err := s.collectProcessesOnly(context.Background()); err != nil {
		t.Fatal("optional process failure escaped into core stats", err)
	}
	if s.processPrevious.ProcessSample.Available || !strings.Contains(s.processPrevious.ProcessSample.Error, "清理") || time.Until(s.processNextSampleAt) < 29*time.Second {
		t.Fatal("failed process scan fabricated a result or retried immediately")
	}
	s.diagnosticLog.mu.Lock()
	diagnostic := s.diagnosticLog.records[s.ID]
	s.diagnosticLog.mu.Unlock()
	if diagnostic.Code != "" {
		t.Fatalf("healthy SSH has a false disconnect code: %+v", diagnostic)
	}
}

func TestMonitorCancelledOpenDoesNotCloseTransportOrAccumulateWorkers(t *testing.T) {
	s, _, opens := backgroundNetworkSSHFixture(t, true, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := s.runNetworkMonitor(ctx, networkMonitorCommand); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := s.runNetworkMonitor(context.Background(), networkMonitorCommand); !errors.Is(err, errMonitorCleaningUp) {
			t.Fatal(err)
		}
	}
	if s.ctx.Err() != nil || opens.Load() != 1 {
		t.Fatal("cancelled open killed transport or accumulated opening workers")
	}
	if _, _, err := s.client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Fatal("healthy underlying transport was closed", err)
	}
}

func TestMonitorThirtyFiveSecondDelayKeepsProductionSSH(t *testing.T) {
	t.Parallel()
	s, _, opens := backgroundNetworkSSHFixture(t, false, func(context.Context, int) ([]byte, error) { return []byte("ok"), nil }, 35*time.Second)
	s.cleanupGrace = 0 // real production settings, with no shortened fixture grace
	go s.heartbeat()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := s.runNetworkMonitor(ctx, networkMonitorCommand); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	awaitNetworkCondition(t, 36*time.Second, func() bool {
		if s.ctx.Err() != nil {
			t.Fatal("collector shutdown killed healthy SSH before the delayed acknowledgement")
		}
		s.networkCollector.mu.Lock()
		defer s.networkCollector.mu.Unlock()
		select {
		case <-s.networkCollector.job.done:
			return true
		default:
			return false
		}
	})
	if opens.Load() != 1 || !s.Latency().Ready {
		t.Fatal("long collector delay accumulated requests or broke heartbeat")
	}
	t.Log("35-second collector delay: the same SSH transport stayed connected, heartbeat remained responsive, and the single worker was released")
}
