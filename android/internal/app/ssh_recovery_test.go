package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func recoveryHeartbeatFixture(t *testing.T) (*Session, chan struct{}, *atomic.Int32) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	gate, done := make(chan struct{}), make(chan struct{})
	count := &atomic.Int32{}
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		server, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			conn.Close()
			return
		}
		defer server.Close()
		stop := context.AfterFunc(ctx, func() { server.Close() })
		defer stop()
		go func() {
			for ch := range channels {
				ch.Reject(ssh.UnknownChannelType, "unused")
			}
		}()
		for request := range requests {
			if count.Add(1) == 1 {
				select {
				case <-gate:
				case <-ctx.Done():
					return
				}
			}
			request.Reply(false, nil)
		}
	}()
	raw, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		cancel()
		listener.Close()
		t.Fatal(err)
	}
	activity := newSSHActivityConn(raw)
	conn, channels, requests, err := ssh.NewClientConn(activity, listener.Addr().String(), &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.FixedHostKey(signer.PublicKey())})
	if err != nil {
		raw.Close()
		cancel()
		listener.Close()
		t.Fatal(err)
	}
	client := ssh.NewClient(conn, channels, requests)
	s := &Session{ctx: ctx, cancel: cancel, client: client, activity: activity}
	t.Cleanup(func() {
		s.forceClose()
		listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("heartbeat worker leaked")
		}
	})
	return s, gate, count
}

func TestSSHHeartbeatRecoversAfterElevenSecondPause(t *testing.T) {
	t.Parallel()
	s, gate, count := recoveryHeartbeatFixture(t)
	go s.heartbeat()
	awaitNetworkCondition(t, time.Second, func() bool { return count.Load() == 1 })
	time.Sleep(11 * time.Second)
	if s.ctx.Err() != nil {
		t.Fatal("temporary eleven-second interruption closed SSH")
	}
	if count.Load() != 1 || !s.Latency().TimedOut {
		t.Fatal("heartbeat requests accumulated or late sample remained healthy")
	}
	close(gate)
	awaitNetworkCondition(t, 2*time.Second, func() bool { return s.Latency().Ready && count.Load() >= 2 })
}

func TestSSHHeartbeatClosesPermanentlyUnresponsiveTransport(t *testing.T) {
	s, _, count := recoveryHeartbeatFixture(t)
	s.diagnosticLog = &sshDiagnosticLog{}
	go s.heartbeatWithTimeout(150 * time.Millisecond)
	select {
	case <-s.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("permanently unresponsive SSH was retained")
	}
	if count.Load() != 1 {
		t.Fatalf("unbounded heartbeat requests: %d", count.Load())
	}
	s.diagnosticLog.mu.Lock()
	diagnostic := s.diagnosticLog.records[s.ID]
	s.diagnosticLog.mu.Unlock()
	if diagnostic.Code != "DS-201" {
		t.Fatalf("heartbeat diagnostic: %+v", diagnostic)
	}
}

func TestCancelledMonitorOpenSurvivesSlowAcknowledgement(t *testing.T) {
	for _, method := range []string{"network", "core"} {
		t.Run(method, func(t *testing.T) {
			s, _, opens := backgroundNetworkSSHFixture(t, false, func(context.Context, int) ([]byte, error) { return []byte("ok"), nil }, 700*time.Millisecond)
			s.cleanupGrace = 0 // Exercise the real production recovery window.
			read := s.runNetworkMonitor
			if method == "core" {
				read = func(ctx context.Context, command string) ([]byte, error) {
					return s.runBoundedSSH(ctx, command, monitorOutputLimit)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := read(ctx, networkMonitorCommand); done <- err }()
			awaitNetworkCondition(t, time.Second, func() bool { return opens.Load() == 1 })
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("late channel acknowledgement did not release cancelled operation")
			}
			if s.ctx.Err() != nil {
				t.Fatal("700 ms channel-open RTT disconnected the terminal")
			}
			if method == "network" || method == "core" {
				runner := &s.networkCollector
				if method == "core" {
					runner = &s.commandCollector
				}
				awaitNetworkCondition(t, 2*time.Second, func() bool {
					runner.mu.Lock()
					defer runner.mu.Unlock()
					select {
					case <-runner.job.done:
						return true
					default:
						return false
					}
				})
			}
			next, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			data, err := read(next, networkMonitorCommand)
			if err != nil || string(data) != "ok" {
				t.Fatalf("transport unusable after cancellation: %q %v", data, err)
			}
		})
	}
}

func TestSFTPCancelSurvivesDelayedCloseAcknowledgement(t *testing.T) {
	_, s, root, link := uploadSFTPFixture(t, 0)
	s.cleanupGrace = 0
	f, err := s.files.Create(filepath.Join(root, "temporary"))
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	link.mu.Lock()
	link.closeGate = gate
	link.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op := startSFTPOperation(ctx, s, time.Minute)
	defer op.close()
	done := make(chan error, 1)
	go func() { err := f.Close(); op.close(); done <- err }()
	awaitNetworkCondition(t, time.Second, func() bool { link.mu.Lock(); defer link.mu.Unlock(); return len(link.closes) > 0 })
	cancel()
	time.AfterFunc(700*time.Millisecond, func() { close(gate) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("late close acknowledgment broke SFTP: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SFTP cleanup did not finish")
	}
	if op.forced.Load() {
		t.Fatal("temporary SFTP delay disconnected SSH")
	}
	if _, err := s.files.Stat(root); err != nil {
		t.Fatalf("SFTP unusable after cancellation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "temporary")); err != nil {
		t.Fatal(err)
	}
}

func TestSSHHeartbeatRetainsConnectionWithOtherInboundTraffic(t *testing.T) {
	s, gate, count := recoveryHeartbeatFixture(t)
	go s.heartbeatWithTimeout(350 * time.Millisecond)
	awaitNetworkCondition(t, time.Second, func() bool { return count.Load() == 1 })
	// Hold the global reply while real channel-open responses continue arriving.
	// Eight observations span more than twice the heartbeat timeout.
	for i := 0; i < 8; i++ {
		_, _, _ = s.client.OpenChannel("fixture-activity", nil)
		time.Sleep(100 * time.Millisecond)
		if s.ctx.Err() != nil {
			t.Fatal("unanswered heartbeat killed a transport still exchanging SSH packets")
		}
	}
	if count.Load() != 1 {
		t.Fatal("concurrent heartbeat requests accumulated")
	}
	close(gate)
	awaitNetworkCondition(t, time.Second, func() bool { return s.Latency().Ready })
}
