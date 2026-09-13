package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestASNQueriesOnlyPublicHopIPs(t *testing.T) {
	if query := asnQueryName("8.8.4.4"); query != "4.4.8.8.origin.asn.cymru.com." {
		t.Fatal(query)
	}
	if query := asnQueryName("::ffff:8.8.4.4"); query != "4.4.8.8.origin.asn.cymru.com." {
		t.Fatal(query)
	}
	query := asnQueryName("2001:4860:b002::68")
	if !strings.HasPrefix(query, "8.6.0.0.") || !strings.HasSuffix(query, ".2.0.0.b.0.6.8.4.1.0.0.2.origin6.asn.cymru.com.") || strings.Count(strings.TrimSuffix(query, "origin6.asn.cymru.com."), ".") != 32 {
		t.Fatal(query)
	}
	for _, value := range []string{"", "example.com", "127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "100.64.0.1", "198.18.0.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "0.0.0.0", "255.255.255.255", "224.0.0.1", "::1", "fe80::1%eth0", "fc00::1", "2001:db8::1", "3fff::1", "64:ff9b::808:808"} {
		if query := asnQueryName(value); query != "" {
			t.Fatalf("non-public address %s queried %s", value, query)
		}
	}
}
func TestASNParsingValidatesPrefixAndRetainsMultipleOrigins(t *testing.T) {
	got := parseASNRecords([]string{"15169 15169 13335 | 8.8.8.0/24 | US | arin | 1992-12-01", "9999 | 9.9.9.0/24 | US", "not-a-number | 8.8.8.0/24 | US"}, "8.8.8.8")
	if got != "AS13335,AS15169" {
		t.Fatal(got)
	}
	for _, record := range []string{"", "0 | 8.8.8.0/24", "4294967296 | 8.8.8.0/24", "15169 | 9.9.9.0/24", "15169 | invalid"} {
		if got := parseASNRecords([]string{record}, "8.8.8.8"); got != "N/A" {
			t.Fatalf("fabricated ASN: %q => %s", record, got)
		}
	}
}
func TestASNCachesRealAnswersAndLookupFailures(t *testing.T) {
	var calls atomic.Int32
	cache := &asnLookupCache{entries: map[string]asnCacheEntry{}, slots: make(chan struct{}, 1), lookupTXT: func(ctx context.Context, name string) ([]string, error) {
		calls.Add(1)
		if strings.HasPrefix(name, "8.8.8.8.") {
			return []string{"15169 | 8.8.8.0/24 | US"}, nil
		}
		return nil, errors.New("DNS unavailable")
	}}
	for range 2 {
		if got := cache.lookup(context.Background(), "8.8.8.8"); got != "AS15169" {
			t.Fatal(got)
		}
		if got := cache.lookup(context.Background(), "1.1.1.1"); got != "N/A" {
			t.Fatal(got)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("cache miss: %d", calls.Load())
	}
	if got := cache.lookup(context.Background(), "192.168.1.1"); got != "N/A" || calls.Load() != 2 {
		t.Fatal("queried private address")
	}
}
func TestASNLookupCancellation(t *testing.T) {
	cache := &asnLookupCache{entries: map[string]asnCacheEntry{}, slots: make(chan struct{}, 1), lookupTXT: func(ctx context.Context, _ string) ([]string, error) { <-ctx.Done(); return nil, ctx.Err() }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if got := cache.lookup(ctx, "8.8.8.8"); got != "N/A" {
		t.Fatal(got)
	}
	if time.Since(start) > time.Second {
		t.Fatal("lookup ignored cancellation")
	}
	if len(cache.entries) != 0 {
		t.Fatal("cancelled answer poisoned cache")
	}
}

func TestASNBoundedWhenNativeDNSIgnoresCancellation(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	cache := &asnLookupCache{entries: map[string]asnCacheEntry{}, slots: make(chan struct{}, 1), lookupTXT: func(context.Context, string) ([]string, error) {
		close(started)
		<-release
		return nil, errors.New("native query finished")
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string, 1)
	go func() { done <- cache.lookup(ctx, "8.8.8.8") }()
	<-started
	cancel()
	select {
	case value := <-done:
		if value != "N/A" {
			t.Fatal(value)
		}
	case <-time.After(time.Second):
		t.Fatal("native DNS blocked cancellation")
	}
	if len(cache.slots) != 1 {
		t.Fatal("released native DNS slot while call still running")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for len(cache.slots) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("native DNS slot not released")
		}
		time.Sleep(time.Millisecond)
	}
}
