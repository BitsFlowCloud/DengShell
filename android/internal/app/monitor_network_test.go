package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"math"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestNetworkInterfacesAddressesAndRates(t *testing.T) {
	fixture := `__CS_STAT__
cpu 1 2 3 4 5 6 7 8
__CS_MEM__
MemTotal: 1024 kB
MemAvailable: 512 kB
__CS_NET__
lo: 999 0 0 0 0 0 0 0 999
ens18: 100 0 0 0 0 0 0 0 200
wg0: 40 0 0 0 0 0 0 0 80
eth1: 20 0 0 0 0 0 0 0 30
__CS_LINK__
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN mode DEFAULT
2: ens18: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP mode DEFAULT
3: wg0@if7: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1420 qdisc noqueue state UNKNOWN mode DEFAULT
4: eth1: <BROADCAST,MULTICAST,UP> mtu 1500 qdisc noqueue state DOWN mode DEFAULT
__CS_ADDR__
2: ens18 inet 10.0.0.2/24 brd 10.0.0.255 scope global ens18
2: ens18 inet6 2001:db8::2/64 scope global dynamic
3: wg0@if7 inet 10.20.0.1/24 scope global wg0
2: ens18 inet6 fe80::2/64 scope link
__CS_ROUTES__
default via 10.0.0.1 dev ens18 proto dhcp metric 100
default via fe80::1 dev ens18 proto ra metric 100
__CS_CONNECTION__
192.0.2.20 42000 10.0.0.2 2222
__CS_END__`
	before, err := parseStats([]byte(fixture), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Interfaces) != 4 || before.Interfaces[0].Name != "ens18" || !before.Interfaces[0].Default || !before.Interfaces[0].Up || before.Interfaces[0].Ready {
		t.Fatalf("bad network interface metadata: %+v", before.Interfaces)
	}
	for _, iface := range before.Interfaces {
		if iface.Name == "eth1" && iface.Up {
			t.Fatal("link down reported as up")
		}
		if iface.Name == "wg0" && (!iface.Up || len(iface.Addresses) != 1) {
			t.Fatal("tunnel name or UNKNOWN operational state mishandled")
		}
	}
	if len(before.ServerIPv4) != 0 || len(before.ServerIPv6) != 0 || before.SSHConnection.ServerPort != 2222 || before.SSHConnection.ClientAddress != "192.0.2.20" {
		t.Fatalf("bad address metadata: %+v", before.Stats)
	}
	changed := strings.ReplaceAll(fixture, "ens18: 100 0 0 0 0 0 0 0 200", "ens18: 400 0 0 0 0 0 0 0 800")
	after, err := parseStats([]byte(changed), before.SampledAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	applyInterfaceRates(&after, &before, 2)
	if got := after.Interfaces[0]; !got.Ready || got.RX != 150 || got.TX != 300 {
		t.Fatalf("per-interface rates are wrong: %+v", got)
	}
	for _, iface := range after.Interfaces {
		if iface.Name == "wg0" && (!iface.Ready || iface.RX != 0 || iface.TX != 0) {
			t.Fatal("idle interface zero rates are not valid")
		}
	}
	reset, _ := parseStats([]byte(fixture), after.SampledAt.Add(time.Second))
	applyInterfaceRates(&reset, &after, 1)
	if reset.Interfaces[0].Ready {
		t.Fatal("reset counters were used as a valid rate")
	}
}

func TestPingLossCountersExcludePendingAndUnavailable(t *testing.T) {
	s := &Session{ping: PingStats{Address: "192.0.2.10"}}
	for _, result := range []ICMPProbeResult{{Status: "reply", Milliseconds: 10}, {Status: "timeout"}, {Status: "unavailable", Error: "permission denied"}} {
		s.beginPing()
		s.finishPing(result, time.Now())
	}
	got := s.Ping()
	if got.Sent != 2 || got.Received != 1 || got.Lost != 1 || got.Pending != 0 || got.LossPercent != 50 || got.Available || got.WindowSent != 2 || got.WindowLossPercent != 50 {
		t.Fatalf("unavailable probes counted as loss: %+v", got)
	}
	s.beginPing()
	got = s.Ping()
	if got.Sent != 3 || got.Pending != 1 || got.LossPercent != 50 {
		t.Fatal("pending probe diluted the measured loss rate")
	}
	s.finishPing(ICMPProbeResult{Status: "unreachable", Address: "192.0.2.1", Responded: true}, time.Now())
	got = s.Ping()
	if got.Address != "192.0.2.10" || got.ReplyAddress != "192.0.2.1" {
		t.Fatal("router error source replaced the target address")
	}
	for i := 0; i < 60; i++ {
		s.beginPing()
		s.finishPing(ICMPProbeResult{Status: "reply", Milliseconds: 2}, time.Now())
	}
	got = s.Ping()
	if len(got.Samples) != 64 || got.WindowSent != 63 || got.WindowLost != 2 || math.Abs(got.WindowLossPercent-200.0/63) > .000001 || got.Lost != 2 {
		t.Fatal("actual last-minute samples or cumulative counters are incorrect")
	}
	got.Samples[0].Status = "mutated"
	if s.Ping().Samples[0].Status == "mutated" {
		t.Fatal("returned sample array aliases live state")
	}
}

func TestSSHHeartbeatTimeoutDoesNotAccumulateRequests(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	release := make(chan struct{})
	done := make(chan struct{})
	var count atomic.Int32
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		server, channels, requests, err := ssh.NewServerConn(connection, config)
		if err != nil {
			connection.Close()
			return
		}
		defer server.Close()
		go func() {
			for channel := range channels {
				channel.Reject(ssh.UnknownChannelType, "unused")
			}
		}()
		for request := range requests {
			if count.Add(1) == 1 {
				<-release
			}
			request.Reply(false, nil)
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "test", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{ctx: ctx, cancel: cancel, client: client}
	defer session.Close()
	go session.heartbeat()
	deadline := time.Now().Add(time.Second)
	for count.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(1100 * time.Millisecond)
	latency := session.Latency()
	if !latency.Pending || !latency.TimedOut || !latency.Stale || latency.Ready || count.Load() != 1 || latency.Ping.Sent != 0 {
		close(release)
		t.Fatalf("SSH timeout incorrectly queues requests or fabricates ping loss: %+v, requests=%d", latency, count.Load())
	}
	close(release)
	deadline = time.Now().Add(1500 * time.Millisecond)
	for count.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if count.Load() < 3 || !session.Latency().Ready {
		t.Fatal("heartbeat did not recover at one-second cadence")
	}
	session.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat cancellation left the SSH server running")
	}
}

func TestLocalNetworkSnapshotIntegration(t *testing.T) {
	key := os.Getenv("CLOUDSHELL_TEST_KEY")
	if key == "" {
		t.Skip("set CLOUDSHELL_TEST_KEY for the dedicated local sshd on port 19225")
	}
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	profile, err := a.store.Save(Profile{Name: "network snapshot", Host: "127.0.0.1", Port: 19225, User: "bitsflow", Auth: "key", KeyPath: key}, false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := connectLocalSSHFixture(t, a, profile.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var stats Stats
	for i := 0; i < 2; i++ {
		stats, err = session.Stats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(stats.Interfaces) == 0 || stats.SSHConnection.ServerAddress != "127.0.0.1" || stats.SSHConnection.ServerPort != 19225 || stats.SSHConnection.ClientAddress != "127.0.0.1" {
		t.Fatalf("live remote network collection failed: %+v", stats)
	}
	for _, iface := range stats.Interfaces {
		if !iface.Ready || iface.Name == "" || iface.RX < 0 || iface.TX < 0 {
			t.Fatalf("live interface rate invalid: %+v", iface)
		}
	}
	for _, address := range append(append([]string{}, stats.ServerIPv4...), stats.ServerIPv6...) {
		if _, ok := publicServerAddress(address); !ok {
			t.Fatalf("live public address field contains local/non-public IP: %s", address)
		}
	}
	if len(stats.ServerAddressInfo) != len(stats.ServerIPv4)+len(stats.ServerIPv6) {
		t.Fatal("live public addresses lost their provenance")
	}
	if session.connectionPeer != "" {
		t.Fatalf("localhost transport was retained as a public server peer: %s", session.connectionPeer)
	}
	deadline := time.Now().Add(4 * time.Second)
	for session.Ping().Received < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ping := session.Ping()
	if !ping.Available || ping.Received < 2 || ping.Lost != 0 || ping.Address != "127.0.0.1" {
		t.Fatalf("continuous real ICMP failed: %+v", ping)
	}
	spacing := ping.Samples[1].SampledAt.Sub(ping.Samples[0].SampledAt)
	if spacing < 800*time.Millisecond || spacing > 1300*time.Millisecond {
		t.Fatalf("ICMP probes are not on a one-second cadence: %s", spacing)
	}
	t.Logf("remote interfaces=%d, SSH server=%s:%d; ICMP reply=%.3fms, sent=%d received=%d lost=%d, cadence=%s", len(stats.Interfaces), stats.SSHConnection.ServerAddress, stats.SSHConnection.ServerPort, ping.Milliseconds, ping.Sent, ping.Received, ping.Lost, spacing)
}
