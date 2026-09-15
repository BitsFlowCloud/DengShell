//go:build !windows

package app

import (
	"context"
	"net/netip"
	"os/exec"
	"reflect"
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

func TestMacOSPingArgumentsAndBSDExitStatus(t *testing.T) {
	for _, tc := range []struct {
		platform, address, tool string
		args                    []string
	}{
		{"darwin", "127.0.0.1", "/sbin/ping", []string{"-n", "-c", "1", "-W", "1000", "-t", "1", "127.0.0.1"}},
		{"darwin", "::1", "/sbin/ping6", []string{"-n", "-c", "1", "-X", "1", "::1"}},
		{"linux", "127.0.0.1", "ping", []string{"-4", "-n", "-c", "1", "-W", "1.000", "--", "127.0.0.1"}},
	} {
		tool, args := pingCommandForPlatform(tc.platform, netip.MustParseAddr(tc.address), time.Second)
		if tool != tc.tool || !reflect.DeepEqual(args, tc.args) {
			t.Fatalf("%s %s: %s %q", tc.platform, tc.address, tool, args)
		}
	}
	output := "1 packets transmitted, 0 packets received, 100.0% packet loss"
	if got := parsePingOutputForPlatform("darwin", "::1", output, 2); got.Status != "timeout" {
		t.Fatal("BSD packet loss was not recognized", got)
	}
	if got := parsePingOutputForPlatform("darwin", "::1", output, 1); got.Status != "unavailable" {
		t.Fatal("BSD invocation error was counted as packet loss", got)
	}
	if got := parsePingOutputForPlatform("linux", "::1", output, 2); got.Status != "unavailable" {
		t.Fatal("Linux local failure was counted as packet loss", got)
	}
	output = "1 packets transmitted, 1 packets received, 0.0% packet loss\nround-trip min/avg/max/stddev = 0.041/0.041/0.041/0.000 ms"
	if got := parsePingOutputForPlatform("darwin", "::1", output, 0); got.Status != "reply" || got.Milliseconds != .041 {
		t.Fatal("BSD ICMP round trip was not parsed", got)
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
