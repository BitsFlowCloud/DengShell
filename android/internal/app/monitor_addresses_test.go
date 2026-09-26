package app

import (
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestPublicServerAddressClassification(t *testing.T) {
	for _, value := range []string{"8.8.8.8", "1.1.1.1", "::ffff:8.8.8.8", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		if _, ok := publicServerAddress(value); !ok {
			t.Errorf("public literal rejected: %s", value)
		}
	}
	for _, value := range []string{"", "host.example", "127.0.0.1", "10.1.2.3", "172.16.2.3", "192.168.1.1", "100.64.0.1", "198.18.0.1", "198.19.255.254", "0.0.0.0", "169.254.1.2", "224.0.0.1", "255.255.255.255", "240.0.0.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "::", "::1", "::ffff:192.168.1.1", "fe80::1", "fe80::1%eth0", "fd00::1", "ff02::1", "2001:db8::1", "3fff::1", "64:ff9b:1::1", "100::1", "2001:4860::1%eth0"} {
		if ip, ok := publicServerAddress(value); ok {
			t.Errorf("non-public address accepted: %s => %s", value, ip)
		}
	}
}

func TestDirectSSHPeerNeverIncludesExplicitProxy(t *testing.T) {
	peer := &net.TCPAddr{IP: net.ParseIP("8.8.4.4"), Port: 1080}
	for _, proxy := range []string{"socks5", "http"} {
		if got := directSSHServerPeer(peer, proxy); got != "" {
			t.Errorf("%s proxy leaked as server: %s", proxy, got)
		}
	}
	if got := directSSHServerPeer(peer, "direct"); got != "8.8.4.4" {
		t.Fatal(got)
	}
	for _, value := range []string{"127.0.0.1", "198.18.0.1", "192.168.1.1"} {
		if got := directSSHServerPeer(&net.TCPAddr{IP: net.ParseIP(value), Port: 22}, "direct"); got != "" {
			t.Errorf("local/TUN peer accepted: %s", got)
		}
	}
}

func publicNetworkFixture() map[string][]string {
	return map[string][]string{
		"__CS_NET__":  {"lo: 0 0 0 0 0 0 0 0 0", "wan: 1 0 0 0 0 0 0 0 1", "other: 2 0 0 0 0 0 0 0 2", "down: 3 0 0 0 0 0 0 0 3"},
		"__CS_LINK__": {"1: lo: <LOOPBACK,UP> mtu 65536 state UNKNOWN", "2: wan: <UP,LOWER_UP> mtu 1500 state UP", "3: other: <UP,LOWER_UP> mtu 1500 state UP", "4: down: <BROADCAST> mtu 1500 state DOWN"},
		"__CS_ADDR__": {
			"2: wan inet 8.8.8.8/24 scope global wan", "2: wan inet 1.1.1.1/24 scope global secondary wan", "2: wan inet 192.168.1.2/24 scope global wan", "2: wan inet6 fe80::1/64 scope link", "2: wan inet6 2606:4700:4700::1111/64 scope global", "2: wan inet6 2001:4860:4860::8888/64 scope global", "2: wan inet6 2606:4700::1001/64 scope global tentative", "2: wan inet6 2606:4700::1002/64 scope global dadfailed", "2: wan inet6 2606:4700::1003/64 scope global deprecated", "2: wan inet 8.8.8.7/24 scope link", "3: other inet 9.9.9.9/24 scope global other", "4: down inet 4.2.2.1/24 scope global down", "1: lo inet 127.0.0.1/8 scope host lo"},
		"__CS_ROUTES__":     {"default via 1.1.1.254 dev wan"},
		"__CS_CONNECTION__": {"192.168.10.1 45000 10.0.0.2 22"},
	}
}

func TestServerAddressSelectionSourcesAndStableInterfaceOrder(t *testing.T) {
	sections := publicNetworkFixture()
	var first rawStats
	parseNetworkStats(&first, sections)
	if !reflect.DeepEqual(first.ServerIPv4, []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"}) {
		t.Fatalf("wrong public IPv4/order: %v", first.ServerIPv4)
	}
	if !reflect.DeepEqual(first.ServerIPv6, []string{"2001:4860:4860::8888", "2606:4700:4700::1111"}) {
		t.Fatalf("wrong public IPv6/order: %v", first.ServerIPv6)
	}
	if got := strings.Join(first.Interfaces[0].Addresses, ","); !strings.Contains(got, "192.168.1.2/24") || !strings.Contains(got, "fe80::1/64") {
		t.Fatalf("NIC tooltip lost non-public addresses: %s", got)
	}
	if first.ServerAddressInfo[0].Source != "interface" || first.ServerAddressInfo[0].Interface != "wan" {
		t.Fatalf("incorrect provenance: %+v", first.ServerAddressInfo)
	}
	// ip's enumeration order can change as DHCP/IPv6 addresses are renewed.
	for i, j := 0, len(sections["__CS_ADDR__"])-1; i < j; i, j = i+1, j-1 {
		sections["__CS_ADDR__"][i], sections["__CS_ADDR__"][j] = sections["__CS_ADDR__"][j], sections["__CS_ADDR__"][i]
	}
	var second rawStats
	parseNetworkStats(&second, sections)
	if !reflect.DeepEqual(first.ServerAddressInfo, second.ServerAddressInfo) {
		t.Fatalf("address order depends on collection order: %v != %v", first.ServerAddressInfo, second.ServerAddressInfo)
	}
}

func TestServerAddressNATEntryProxyAndPrivateOnly(t *testing.T) {
	cases := []struct {
		name, host, peer, remote string
		want                     []string
		source                   string
	}{
		{"NAT numeric target", "8.8.4.4", "", "10.0.0.2", []string{"8.8.4.4"}, "ssh-entry"},
		{"NAT direct hostname actual peer", "server.example", "1.0.0.1", "10.0.0.2", []string{"1.0.0.1"}, "ssh-entry"},
		{"proxy hostname cannot infer IP", "server.example", "", "10.0.0.2", []string{}, ""},
		{"private-only", "192.168.1.2", "", "10.0.0.2", []string{}, ""},
		{"TUN fake target", "198.18.0.1", "", "10.0.0.2", []string{}, ""},
		{"remote socket wins", "8.8.4.4", "8.8.4.4", "1.1.1.1", []string{"1.1.1.1", "8.8.4.4"}, "ssh-server"},
		{"numeric target before actual peer", "8.8.4.4", "1.0.0.1", "10.0.0.2", []string{"8.8.4.4", "1.0.0.1"}, "ssh-entry"},
		{"mapped IPv4 duplicate", "8.8.4.4", "8.8.4.4", "::ffff:8.8.4.4", []string{"8.8.4.4"}, "ssh-server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Session{connectionHost: tc.host, connectionPeer: tc.peer}
			stats := Stats{SSHConnection: SSHConnectionInfo{ServerAddress: tc.remote}}
			s.applyServerAddresses(&stats)
			if !reflect.DeepEqual(stats.ServerIPv4, tc.want) {
				t.Fatalf("got %v want %v", stats.ServerIPv4, tc.want)
			}
			if len(tc.want) > 0 && stats.ServerAddressInfo[0].Source != tc.source {
				t.Fatalf("wrong source: %+v", stats.ServerAddressInfo)
			}
		})
	}
}

func TestServerAddressMetadataCopiesDoNotAlias(t *testing.T) {
	var parsed rawStats
	parseNetworkStats(&parsed, publicNetworkFixture())
	original := parsed.ServerAddressInfo[0]
	shown := displayStats(parsed.Stats, false, false, LatencySample{})
	shown.ServerAddressInfo[0].Address = "mutated"
	if parsed.ServerAddressInfo[0] != original {
		t.Fatal("display aliases cached address provenance")
	}
	var current rawStats
	applyStaticMetadata(&current, &parsed.Stats)
	current.ServerAddressInfo[0].Source = "mutated"
	if parsed.ServerAddressInfo[0] != original {
		t.Fatal("static metadata aliases address provenance")
	}
}

func TestServerAddressPointToPointLocalAddress(t *testing.T) {
	var parsed rawStats
	parseNetworkStats(&parsed, map[string][]string{
		"__CS_NET__":  {"ppp0: 1 0 0 0 0 0 0 0 1"},
		"__CS_LINK__": {"5: ppp0: <POINTOPOINT,UP,LOWER_UP> mtu 1492 state UNKNOWN"},
		"__CS_ADDR__": {"5: ppp0 inet 8.8.4.4 peer 1.0.0.1/32 scope global ppp0"},
	})
	if !reflect.DeepEqual(parsed.ServerIPv4, []string{"8.8.4.4"}) || !reflect.DeepEqual(parsed.Interfaces[0].Addresses, []string{"8.8.4.4/32"}) {
		t.Fatalf("point-to-point local address was lost or confused with peer: %+v", parsed.Stats)
	}
}
