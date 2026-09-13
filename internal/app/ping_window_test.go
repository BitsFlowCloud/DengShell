package app

import (
	"math"
	"testing"
	"time"
)

func TestPingWindowUsesElapsedMinuteAndExpiresWithoutNewProbes(t *testing.T) {
	now := time.Now()
	p := PingStats{Pending: 1, pendingSince: now.Add(-time.Second), Samples: []PingSample{
		{SampledAt: now.Add(-90 * time.Second), Status: "timeout"},
		{SampledAt: now.Add(-60 * time.Second), Status: "timeout"},
		{SampledAt: now.Add(-59 * time.Second), Status: "reply"},
		{SampledAt: now.Add(-40 * time.Second), Status: "timeout"},
		{SampledAt: now.Add(-20 * time.Second), Status: "unavailable"},
		{SampledAt: now.Add(-2 * time.Second), Status: "reply"},
	}}
	refreshPingWindow(&p, now)
	if len(p.Samples) != 4 || p.WindowSent != 3 || p.WindowReceived != 2 || p.WindowLost != 1 || math.Abs(p.WindowLossPercent-100.0/3) > .000001 || !p.WindowReady || p.WindowPending != 1 || p.WindowSeconds != 60 {
		t.Fatalf("incorrect elapsed window: %+v", p)
	}
	refreshPingWindow(&p, now.Add(61*time.Second))
	if len(p.Samples) != 0 || p.WindowSent != 0 || p.WindowReady || p.WindowPending != 0 {
		t.Fatalf("old probes survived a suspended minute: %+v", p)
	}
}

func TestPingReadDoesNotKeepExpiredLossOrExposeSampleAliases(t *testing.T) {
	s := &Session{ping: PingStats{Sent: 1, Lost: 1, LossPercent: 100, Samples: []PingSample{{SampledAt: time.Now().Add(-61 * time.Second), Status: "timeout"}}}}
	got := s.Ping()
	if got.WindowReady || got.WindowLost != 0 || len(got.Samples) != 0 || got.Lost != 1 {
		t.Fatal("read did not expire old sample independently of lifetime history", got)
	}
	if len(s.ping.Samples) != 1 {
		t.Fatal("read mutated state under read lock")
	}
}
