package app

import (
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type NetworkInterface struct {
	Name      string   `json:"name"`
	RX        float64  `json:"rx"`
	TX        float64  `json:"tx"`
	RXBytes   uint64   `json:"rxBytes"`
	TXBytes   uint64   `json:"txBytes"`
	Up        bool     `json:"up"`
	State     string   `json:"state"`
	Default   bool     `json:"default"`
	Loopback  bool     `json:"loopback"`
	Addresses []string `json:"addresses"`
	Ready     bool     `json:"ready"`
}

type SSHConnectionInfo struct {
	ClientAddress string `json:"clientAddress"`
	ClientPort    int    `json:"clientPort"`
	ServerAddress string `json:"serverAddress"`
	ServerPort    int    `json:"serverPort"`
}

type interfaceCounters struct{ rx, tx uint64 }

func interfaceName(name string) string {
	name = strings.TrimSuffix(name, ":")
	name, _, _ = strings.Cut(name, "@")
	return name
}

func appendUnique(values []string, value string) []string {
	for _, old := range values {
		if old == value {
			return values
		}
	}
	return append(values, value)
}

func parseNetworkStats(r *rawStats, sections map[string][]string) {
	r.network = map[string]interfaceCounters{}
	indexes := map[string]int{}
	for _, line := range sections["__CS_NET__"] {
		device, values, ok := strings.Cut(line, ":")
		device = strings.TrimSpace(device)
		fields := strings.Fields(values)
		if !ok || device == "" || len(fields) < 9 {
			continue
		}
		counters := interfaceCounters{number(fields[0]), number(fields[8])}
		r.network[device] = counters
		indexes[device] = len(r.Interfaces)
		r.Interfaces = append(r.Interfaces, NetworkInterface{Name: device, RXBytes: counters.rx, TXBytes: counters.tx, State: "unknown", Loopback: device == "lo", Addresses: []string{}})
		if device != "lo" {
			r.rx += counters.rx
			r.tx += counters.tx
		}
	}
	for _, line := range sections["__CS_LINK__"] {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		index, ok := indexes[interfaceName(fields[1])]
		if !ok {
			continue
		}
		iface := &r.Interfaces[index]
		for _, flag := range strings.Split(strings.Trim(fields[2], "<>"), ",") {
			if flag == "UP" {
				iface.Up = true
			}
			if flag == "LOOPBACK" {
				iface.Loopback = true
			}
		}
		for i := 3; i+1 < len(fields); i++ {
			if fields[i] == "state" {
				iface.State = strings.ToLower(fields[i+1])
				break
			}
		}
		if iface.State == "down" || iface.State == "lowerlayerdown" || iface.State == "notpresent" {
			iface.Up = false
		}
	}
	for _, line := range sections["__CS_ADDR__"] {
		fields := strings.Fields(line)
		if len(fields) < 4 || (fields[2] != "inet" && fields[2] != "inet6") {
			continue
		}
		// Require the scope returned by ip, also rejecting malformed fixtures.
		global := false
		for i := 4; i+1 < len(fields); i++ {
			if fields[i] == "scope" && fields[i+1] == "global" {
				global = true
			}
		}
		prefix, err := netip.ParsePrefix(fields[3])
		if err != nil {
			// Point-to-point iproute output can put the prefix on the peer,
			// leaving the local address bare: inet A peer B/32 scope global.
			if address, addressErr := netip.ParseAddr(fields[3]); addressErr == nil {
				prefix, err = netip.PrefixFrom(address, address.BitLen()), nil
			}
		}
		if err != nil {
			continue
		}
		if index, ok := indexes[interfaceName(fields[1])]; ok {
			iface := &r.Interfaces[index]
			// All local addresses belong in the NIC tooltip, even link-local,
			// private and tentative addresses. They are not all public addresses.
			iface.Addresses = appendUnique(iface.Addresses, prefix.String())
			usable := global && !iface.Loopback && iface.State != "down" && iface.State != "lowerlayerdown" && iface.State != "notpresent"
			for _, field := range fields[4:] {
				if field == "tentative" || field == "dadfailed" || field == "deprecated" {
					usable = false
				}
			}
			if ip, public := publicServerAddress(prefix.Addr().String()); usable && public {
				r.ServerAddressInfo = append(r.ServerAddressInfo, ServerAddressInfo{ip.String(), "interface", iface.Name})
			}
		}
	}
	for _, line := range sections["__CS_ROUTES__"] {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "default" {
			continue
		}
		for i := 1; i+1 < len(fields); i++ {
			if fields[i] == "dev" {
				if index, ok := indexes[interfaceName(fields[i+1])]; ok {
					r.Interfaces[index].Default = true
				}
			}
		}
	}
	connection := strings.Fields(strings.Join(sections["__CS_CONNECTION__"], " "))
	if len(connection) == 4 {
		client, clientErr := netip.ParseAddr(connection[0])
		server, serverErr := netip.ParseAddr(connection[2])
		clientPort, cpErr := strconv.Atoi(connection[1])
		serverPort, spErr := strconv.Atoi(connection[3])
		if clientErr == nil && serverErr == nil && cpErr == nil && spErr == nil && clientPort > 0 && clientPort <= 65535 && serverPort > 0 && serverPort <= 65535 {
			r.SSHConnection = SSHConnectionInfo{client.Unmap().String(), clientPort, server.Unmap().String(), serverPort}
		}
	}
	// A default/up interface appears before loopback when the UI has no saved choice.
	sortNetworkInterfaces(r.Interfaces)
	selectServerAddresses(&r.Stats, "", "")
}

func sortNetworkInterfaces(interfaces []NetworkInterface) {
	sort.SliceStable(interfaces, func(i, j int) bool {
		a, b := interfaces[i], interfaces[j]
		if a.Default != b.Default {
			return a.Default
		}
		if a.Loopback != b.Loopback {
			return !a.Loopback
		}
		if a.Up != b.Up {
			return a.Up
		}
		return a.Name < b.Name
	})
}

func applyInterfaceRates(current, previous *rawStats, seconds float64) {
	if seconds <= 0 {
		return
	}
	for i := range current.Interfaces {
		iface := &current.Interfaces[i]
		old, exists := previous.network[iface.Name]
		if !exists || iface.RXBytes < old.rx || iface.TXBytes < old.tx {
			continue
		}
		iface.RX = float64(iface.RXBytes-old.rx) / seconds
		iface.TX = float64(iface.TXBytes-old.tx) / seconds
		iface.Ready = true
	}
}
