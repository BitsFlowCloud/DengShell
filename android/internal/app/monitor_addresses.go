package app

import (
	"net"
	"net/netip"
	"sort"
	"strings"
)

// Address provenance describes an observed SSH endpoint or a remote interface,
// never an inferred Internet egress address. It is collected without HTTP/DNS.
type ServerAddressInfo struct {
	Address   string `json:"address"`
	Source    string `json:"source"`
	Interface string `json:"interface,omitempty"`
}

func publicServerAddress(value string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Zone() != "" {
		return netip.Addr{}, false
	}
	ip = ip.Unmap()
	// asnQueryName is a pure formatter with the shared public-address filter;
	// this does NOT perform an ASN or DNS lookup. It excludes private, CGNAT,
	// benchmark/TUN fake-IP, documentation, loopback and link-local ranges.
	if asnQueryName(ip.String()) == "" {
		return netip.Addr{}, false
	}
	return ip, true
}

func directSSHServerPeer(peer net.Addr, proxyType string) string {
	if peer == nil || (proxyType != "" && proxyType != "direct") {
		return ""
	}
	host, _, err := net.SplitHostPort(peer.String())
	if err != nil {
		return ""
	}
	if ip, ok := publicServerAddress(host); ok {
		return ip.String()
	}
	return ""
}

func (s *Session) applyServerAddresses(stats *Stats) {
	selectServerAddresses(stats, s.connectionHost, s.connectionPeer)
}

func selectServerAddresses(stats *Stats, connectionHost, connectionPeer string) {
	candidates := append([]ServerAddressInfo{}, stats.ServerAddressInfo...)
	rank := make(map[string]int, len(stats.Interfaces))
	for index, iface := range stats.Interfaces {
		rank[iface.Name] = index
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if rank[a.Interface] != rank[b.Interface] {
			return rank[a.Interface] < rank[b.Interface]
		}
		if a.Interface != b.Interface {
			return a.Interface < b.Interface
		}
		aa, _ := netip.ParseAddr(a.Address)
		bb, _ := netip.ParseAddr(b.Address)
		return aa.Compare(bb) < 0
	})
	stats.ServerIPv4, stats.ServerIPv6 = []string{}, []string{}
	stats.ServerAddressInfo = []ServerAddressInfo{}
	seen := make(map[netip.Addr]bool)
	appendAddress := func(value, source, device string) {
		ip, ok := publicServerAddress(value)
		if !ok || seen[ip] {
			return
		}
		seen[ip] = true
		if ip.Is4() {
			stats.ServerIPv4 = append(stats.ServerIPv4, ip.String())
		} else {
			stats.ServerIPv6 = append(stats.ServerIPv6, ip.String())
		}
		stats.ServerAddressInfo = append(stats.ServerAddressInfo, ServerAddressInfo{ip.String(), source, device})
	}
	// SSH_CONNECTION reports the remote socket's server address, not the client
	// or proxy address. On NAT hosts it is often private and therefore skipped.
	appendAddress(stats.SSHConnection.ServerAddress, "ssh-server", "")
	// A numeric configured target is the confirmed SSH entrance after successful
	// authentication. A hostname is never re-resolved: proxy DNS may differ.
	appendAddress(strings.Trim(strings.TrimSpace(connectionHost), "[]"), "ssh-entry", "")
	appendAddress(connectionPeer, "ssh-entry", "")
	for _, candidate := range candidates {
		if candidate.Source == "interface" {
			appendAddress(candidate.Address, "interface", candidate.Interface)
		}
	}
}
