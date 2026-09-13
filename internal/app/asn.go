package app

import (
	"context"
	"encoding/hex"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Team Cymru documents these fixed TXT query zones and pipe-delimited records:
// https://www.team-cymru.com/ip-asn-mapping
// No HTTP endpoint or user-supplied hostname is queried. Only numeric public
// responding-hop addresses are sent through the system DNS resolver.
var asnExcludedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"), netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("3fff::/20"),
}

func asnQueryName(value string) string {
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Zone() != "" {
		return ""
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return ""
	}
	for _, prefix := range asnExcludedPrefixes {
		if prefix.Contains(ip) {
			return ""
		}
	}
	if ip.Is4() {
		b := ip.As4()
		return strconv.Itoa(int(b[3])) + "." + strconv.Itoa(int(b[2])) + "." + strconv.Itoa(int(b[1])) + "." + strconv.Itoa(int(b[0])) + ".origin.asn.cymru.com."
	}
	// Public IPv6 allocations currently lie in global-unicast 2000::/3. Exclude
	// translated/unspecified/local addresses instead of leaking their local part.
	if !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return ""
	}
	b := ip.As16()
	digits := hex.EncodeToString(b[:])
	var out strings.Builder
	for i := len(digits) - 1; i >= 0; i-- {
		out.WriteByte(digits[i])
		out.WriteByte('.')
	}
	out.WriteString("origin6.asn.cymru.com.")
	return out.String()
}
func parseASNRecords(records []string, address string) string {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return "N/A"
	}
	ip = ip.Unmap()
	seen := map[uint64]bool{}
	for _, record := range records {
		fields := strings.Split(record, "|")
		if len(fields) < 2 {
			continue
		}
		prefix, err := netip.ParsePrefix(strings.TrimSpace(fields[1]))
		if err != nil || !prefix.Contains(ip) {
			continue
		}
		for _, value := range strings.Fields(fields[0]) {
			n, err := strconv.ParseUint(value, 10, 32)
			if err == nil && n > 0 {
				seen[n] = true
			}
		}
	}
	values := make([]uint64, 0, len(seen))
	for n := range seen {
		values = append(values, n)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) == 0 {
		return "N/A"
	}
	parts := make([]string, 0, len(values))
	for _, n := range values {
		parts = append(parts, "AS"+strconv.FormatUint(n, 10))
	}
	return strings.Join(parts, ",")
}

type asnCacheEntry struct {
	value   string
	expires time.Time
}
type asnLookupCache struct {
	mu        sync.Mutex
	entries   map[string]asnCacheEntry
	slots     chan struct{}
	lookupTXT func(context.Context, string) ([]string, error)
}

var diagnosticASNCache = &asnLookupCache{entries: map[string]asnCacheEntry{}, slots: make(chan struct{}, 8), lookupTXT: net.DefaultResolver.LookupTXT}

func (c *asnLookupCache) lookup(ctx context.Context, address string) string {
	query := asnQueryName(address)
	if query == "" {
		return "N/A"
	}
	c.mu.Lock()
	entry, ok := c.entries[query]
	c.mu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.value
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return "N/A"
	}
	// Windows' system LookupTXT uses synchronous DnsQuery and may outlive
	// ctx. Bound the caller separately while retaining its concurrency slot
	// until that native query really returns; cancellations cannot pile up DNS.
	type answer struct {
		records []string
		err     error
	}
	ready := make(chan answer, 1)
	go func() {
		defer func() { <-c.slots }()
		records, err := c.lookupTXT(ctx, query)
		ready <- answer{records, err}
	}()
	var records []string
	var err error
	select {
	case result := <-ready:
		records, err = result.records, result.err
	case <-ctx.Done():
		return "N/A"
	}
	if ctx.Err() != nil {
		return "N/A"
	}
	value := "N/A"
	ttl := 2 * time.Minute
	if err == nil {
		value = parseASNRecords(records, address)
		if value != "N/A" {
			ttl = time.Hour
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= 1024 {
		for key, entry := range c.entries {
			if time.Now().After(entry.expires) {
				delete(c.entries, key)
			}
		}
		if len(c.entries) >= 1024 {
			for key := range c.entries {
				delete(c.entries, key)
				break
			}
		}
	}
	c.entries[query] = asnCacheEntry{value: value, expires: time.Now().Add(ttl)}
	return value
}

// ASN queries run alongside ICMP probes; slow DNS never stretches ICMP cadence.
// Only the diagnostic loop mutates hops. Workers publish lookup values here.
type diagnosticASNTracker struct {
	mu      sync.Mutex
	values  map[string]string
	pending int
	changed chan struct{}
}

func newDiagnosticASNTracker() *diagnosticASNTracker {
	return &diagnosticASNTracker{values: map[string]string{}, changed: make(chan struct{}, 1)}
}
func (t *diagnosticASNTracker) apply(ctx context.Context, hops []diagnosticHop) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range hops {
		address := hops[i].Address
		value, seen := t.values[address]
		if address == "" {
			hops[i].ASN = "N/A"
			continue
		}
		if !seen {
			value = "N/A"
			t.values[address] = value
			if asnQueryName(address) != "" {
				t.pending++
				go func(address string) {
					result := diagnosticASNCache.lookup(ctx, address)
					t.mu.Lock()
					t.values[address] = result
					t.pending--
					t.mu.Unlock()
					select {
					case t.changed <- struct{}{}:
					default:
					}
				}(address)
			}
		}
		hops[i].ASN = value
	}
}
func (t *diagnosticASNTracker) wait(ctx context.Context) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		t.mu.Lock()
		pending := t.pending
		t.mu.Unlock()
		if pending == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case <-t.changed:
		}
	}
}
