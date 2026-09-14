package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func networkCounterFixture(uptime float64, rx, tx uint64) []byte {
	return []byte(fmt.Sprintf("__CS_UPTIME__\n%.2f 1.00\n__CS_NET__\nInter-| Receive | Transmit\neth0: %d 0 0 0 0 0 0 0 %d 0 0 0 0 0 0 0\nlo: 8 0 0 0 0 0 0 0 8 0 0 0 0 0 0 0\n", uptime, rx, tx))
}

func TestNetworkSamplerUsesRemoteElapsedAndResetsBaseline(t *testing.T) {
	at := time.Now()
	before, err := parseNetworkSnapshot(networkCounterFixture(100, 100, 200), at)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := applyNetworkSnapshotRates(&before, nil); elapsed != 0 || before.SampleReady || before.Interfaces[0].Ready {
		t.Fatal("first snapshot invented a rate")
	}
	// SSH delivers the response 2.4 seconds later, but the real remote counters
	// cover 1.2 seconds: network delivery must not distort the denominator.
	after, _ := parseNetworkSnapshot(networkCounterFixture(101.2, 220, 440), at.Add(2400*time.Millisecond))
	elapsed := applyNetworkSnapshotRates(&after, &before)
	if math.Abs(elapsed-1200) > 1e-6 || !after.SampleReady || math.Abs(after.NetworkRX-100) > 1e-6 || math.Abs(after.NetworkTX-200) > 1e-6 {
		t.Fatalf("incorrect elapsed/rates: %.3f %+v", elapsed, after)
	}
	for _, tc := range []struct {
		name string
		up   float64
		rx   uint64
		tx   uint64
	}{
		{"counter-reset", 102.2, 3, 4},
		{"reboot", 2, 999, 999},
		{"same-remote-tick", 101.2, 999, 999},
		{"resume-after-pause", 121.2, 999, 999},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, _ := parseNetworkSnapshot(networkCounterFixture(tc.up, tc.rx, tc.tx), at.Add(3*time.Second))
			applyNetworkSnapshotRates(&value, &after)
			if value.SampleReady || value.Interfaces[0].Ready || value.NetworkRX != 0 || value.NetworkTX != 0 {
				t.Fatal("reset or missing interval shown as measured rate", value)
			}
		})
	}
	newNIC, _ := parseNetworkSnapshot([]byte(strings.ReplaceAll(string(networkCounterFixture(102.2, 999, 999)), "eth0:", "eth9:")), at.Add(3*time.Second))
	applyNetworkSnapshotRates(&newNIC, &after)
	if newNIC.Interfaces[0].Ready || newNIC.SampleReady {
		t.Fatal("new interface inherited another interface's counters")
	}
	loopReset, _ := parseNetworkSnapshot([]byte(strings.ReplaceAll(string(networkCounterFixture(102.2, 999, 999)), "lo: 8", "lo: 0")), at.Add(3*time.Second))
	applyNetworkSnapshotRates(&loopReset, &after)
	if loopReset.SampleReady {
		t.Fatal("aggregate readiness ignored a reset loopback interface")
	}
	for _, invalid := range []string{"", "__CS_UPTIME__\nNaN\n__CS_NET__\neth0: 1", strings.ReplaceAll(string(networkCounterFixture(100, 100, 200)), "eth0: 100", "eth0: bogus")} {
		// Remove loopback so a malformed sole device cannot become a zero rate.
		invalid = strings.Split(invalid, "\nlo:")[0]
		if _, err := parseNetworkSnapshot([]byte(invalid), at); err == nil {
			t.Fatal("malformed network data accepted", invalid)
		}
	}
}

func TestNetworkSamplerCommandAndAnchoredCadence(t *testing.T) {
	for _, forbidden := range []string{"/proc/[0-9]", "smaps", "df ", "ip ", "date", "/proc/cpuinfo", "/proc/loadavg"} {
		if strings.Contains(networkMonitorCommand, forbidden) {
			t.Fatal("one-second sampler does unnecessary remote work", forbidden)
		}
	}
	for _, required := range []string{"awk", "/proc/net/dev", "/proc/uptime"} {
		if !strings.Contains(networkMonitorCommand, required) {
			t.Fatal("sampler missing counters or remote monotonic timestamp", required)
		}
	}
	start := time.Unix(1000, 0)
	for _, period := range []time.Duration{networkSampleInterval, monitorMinimumInterval} {
		due := advanceSampleDue(time.Time{}, start, period)
		if !due.Equal(start.Add(period)) {
			t.Fatal("bad first deadline")
		}
		// A request just before the tick retains a 1ms deadline; it does not
		// add another whole interval and silently halve the sampling frequency.
		if got := advanceSampleDue(due, due.Add(-time.Millisecond), period); !got.Equal(due) {
			t.Fatal("early request moved cadence")
		}
		if got := advanceSampleDue(due, due.Add(150*time.Millisecond), period); !got.Equal(start.Add(2 * period)) {
			t.Fatal("SSH collection duration accumulated into the cadence")
		}
		if got := advanceSampleDue(due, start.Add(17*period+time.Millisecond), period); !got.Equal(start.Add(18 * period)) {
			t.Fatal("hidden interval was not skipped")
		}
	}
	if monitorMinimumInterval != 5*time.Second || networkSampleInterval != time.Second {
		t.Fatal("incorrect core/network sampling intervals")
	}
}

func TestNetworkSamplerMetadataCacheAndRoute(t *testing.T) {
	current, _ := parseNetworkSnapshot(networkCounterFixture(100, 100, 200), time.Now())
	s := &Session{networkPrevious: &current, networkNextSampleAt: time.Now().Add(time.Second)}
	meta := Stats{MetadataSampledAt: time.Now(), Interfaces: []NetworkInterface{{Name: "eth0", Up: true, State: "up", Default: true, Addresses: []string{"192.0.2.5/24"}}}}
	s.cacheNetworkMetadata(meta)
	meta.Interfaces[0].Addresses[0] = "mutated"
	// Holding statsMu must not block an independent network cache read.
	s.statsMu.Lock()
	result, err := s.networkForDisplay(context.Background())
	s.statsMu.Unlock()
	if err != nil || !result.Cached || result.Interfaces[0].Addresses[0] != "192.0.2.5/24" || !result.Interfaces[0].Up || !result.Interfaces[0].Default || result.MetadataSampledAt.IsZero() {
		t.Fatalf("metadata lost/aliased: %+v %v", result, err)
	}
	result.Interfaces[0].Addresses[0] = "mutated-again"
	again, _ := s.networkForDisplay(context.Background())
	if again.Interfaces[0].Addresses[0] != "192.0.2.5/24" {
		t.Fatal("returned metadata aliases cache")
	}
	// Exercise the actual registered route and its standard application auth.
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	s.ctx = context.Background()
	a.sessions["sample"] = s
	defer delete(a.sessions, "sample") // this cached unit fixture owns no SSH client
	a.baseURL = "http://127.0.0.1"
	handler := a.Handler(fstest.MapFS{"index.html": {Data: []byte("test")}})
	for _, token := range []string{"", a.Token()} {
		r := httptest.NewRequest("GET", "http://127.0.0.1/api/sessions/sample/network", nil)
		r.Header.Set("X-CloudShell-Token", token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if token == "" {
			if w.Code != 403 {
				t.Fatal("network route bypasses auth", w.Code)
			}
			continue
		}
		var body NetworkStats
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.SampledAt.IsZero() || body.SamplingIntervalMilliseconds != 1000 || body.NextSampleInMilliseconds <= 0 {
			t.Fatal("network route/schema failed", w.Code, w.Body.String())
		}
	}
}

func TestNetworkAndCoreActualTwelveSecondCadence(t *testing.T) {
	s := monitorFixtureSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
	defer cancel()
	firstCore, err := s.statsForDisplay(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	coreSamples := []Stats{firstCore}
	coreErrors := make(chan error, 1)
	var coreDone sync.WaitGroup
	coreDone.Add(1)
	go func() {
		defer coreDone.Done()
		for len(coreSamples) < 3 {
			last := coreSamples[len(coreSamples)-1]
			time.Sleep(time.Duration(last.NextSampleInMilliseconds+5) * time.Millisecond)
			next, err := s.statsForDisplay(ctx, false)
			if err != nil {
				coreErrors <- err
				return
			}
			coreSamples = append(coreSamples, next)
		}
	}()
	networkSamples := []NetworkStats{}
	for i := 0; i < 13; i++ {
		var value NetworkStats
		awaitNetworkCondition(t, 2*time.Second, func() bool {
			var err error
			value, err = s.networkForDisplay(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return !value.SampledAt.IsZero() && (i == 0 || value.SampledAt.After(networkSamples[i-1].SampledAt))
		})
		networkSamples = append(networkSamples, value)
		if !value.Cached || (i > 0 && !value.SampleReady) {
			t.Fatalf("missing background tick or incorrect readiness at %d: %+v", i, value)
		}
		nextDue := time.Now().Add(time.Duration(value.NextSampleInMilliseconds+5) * time.Millisecond)
		var workers sync.WaitGroup
		for window := 0; window < 6; window++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				shared, err := s.networkForDisplay(ctx)
				if err != nil || !shared.Cached || shared.SampledAt != value.SampledAt {
					t.Errorf("concurrent window did not reuse observation: %v %+v", err, shared)
				}
			}()
		}
		workers.Wait()
		if i < 12 {
			time.Sleep(450 * time.Millisecond)
			half, err := s.networkForDisplay(ctx)
			if err != nil || !half.Cached || half.SampledAt != value.SampledAt {
				t.Fatal("half-second request caused an extra remote observation", err)
			}
			time.Sleep(max(0, time.Until(nextDue)))
		}
	}
	coreDone.Wait()
	close(coreErrors)
	for err := range coreErrors {
		t.Fatal(err)
	}
	networkSpacing := []float64{}
	coreSpacing := []float64{}
	for i := 1; i < len(networkSamples); i++ {
		spacing := networkSamples[i].SampledAt.Sub(networkSamples[i-1].SampledAt).Seconds()
		networkSpacing = append(networkSpacing, spacing)
		if spacing < .75 || spacing > 1.35 || networkSamples[i].ElapsedMilliseconds < 750 || networkSamples[i].ElapsedMilliseconds > 1350 {
			t.Fatalf("network cadence skipped/drifted: %.3fs, remote %.3fms", spacing, networkSamples[i].ElapsedMilliseconds)
		}
	}
	for i := 1; i < len(coreSamples); i++ {
		spacing := coreSamples[i].SampledAt.Sub(coreSamples[i-1].SampledAt).Seconds()
		coreSpacing = append(coreSpacing, spacing)
		if coreSamples[i].Cached || spacing < 4.6 || spacing > 5.5 || coreSamples[i].Load == "" || coreSamples[i].MetadataSampledAt != firstCore.MetadataSampledAt {
			t.Fatalf("core five-second cadence or metadata cache failed: %.3fs %+v", spacing, coreSamples[i])
		}
	}
	report := map[string]any{"networkIntervalsSeconds": networkSpacing, "coreIntervalsSeconds": coreSpacing, "networkSamples": networkSamples, "coreSamples": coreSamples, "concurrentWindows": 6, "halfSecondCacheProbes": 12}
	data, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("real SSH network intervals=%v; core/load intervals=%v; six concurrent windows reuse each observation", networkSpacing, coreSpacing)
	if path := os.Getenv("DENGSHELL_NETWORK_RESULT"); path != "" {
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
