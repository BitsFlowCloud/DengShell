//go:build !windows

package app

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestParseICMPReplyTimeoutAndLocalFailure(t *testing.T) {
	for _, test := range []struct {
		output string
		code   int
		status string
		rtt    float64
	}{
		{"64 bytes from 127.0.0.1: icmp_seq=1 ttl=64 time=0.058 ms\n1 packets transmitted, 1 received, 0% packet loss\nrtt min/avg/max/mdev = 0.058/0.058/0.058/0.000 ms", 0, "reply", .058},
		{"1 packets transmitted, 0 received, 100% packet loss, time 0ms", 1, "timeout", 0},
		{"From 192.0.2.1 icmp_seq=1 Destination Host Unreachable\n1 packets transmitted, 0 received, +1 errors, 100% packet loss", 1, "unreachable", 0},
		{"ping: socket: Operation not permitted", 2, "unavailable", 0},
		{"ping: Network is unreachable", 2, "unavailable", 0},
		{"unrecognized output", 1, "unavailable", 0},
	} {
		got := parsePingOutput("127.0.0.1", test.output, test.code)
		if got.Status != test.status || got.Milliseconds != test.rtt {
			t.Fatalf("incorrect ICMP interpretation: %+v", got)
		}
	}
	unreachable := parsePingOutput("192.0.2.10", "From 192.0.2.1 icmp_seq=1 Destination Host Unreachable\n1 packets transmitted, 0 received, +1 errors, 100% packet loss", 1)
	if unreachable.Address != "192.0.2.1" || !unreachable.Responded {
		t.Fatal("ICMP error source was mislabeled as the target", unreachable)
	}
}

func TestICMPLoopbackAndCancellation(t *testing.T) {
	if _, err := exec.LookPath("ping"); err != nil {
		t.Skip("iputils ping is unavailable")
	}
	result := ProbeICMP(context.Background(), "127.0.0.1", time.Second)
	if result.Status != "reply" || result.Milliseconds < 0 || !result.Responded {
		t.Fatalf("real local ICMP probe failed: %+v", result)
	}
	result = ProbeICMP(context.Background(), "::ffff:127.0.0.1", time.Second)
	if result.Status != "reply" || result.Address != "127.0.0.1" {
		t.Fatalf("IPv4-mapped ICMP target was not normalized: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	result = ProbeICMP(ctx, "127.0.0.1", time.Second)
	if result.Status != "unavailable" || time.Since(start) > 100*time.Millisecond {
		t.Fatal("cancellation was counted as loss or did not return promptly")
	}
}
