package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// This fixture uses real SSH channels but controlled procfs observations, so
// timing, failures and stalled channel-open behavior can be exercised without
// touching a user server or relying on a system sshd.
func backgroundNetworkSSHFixture(t *testing.T, holdChannel bool, sample func(context.Context, int) ([]byte, error), openDelay ...time.Duration) (*Session, *atomic.Int32, *atomic.Int32) {
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
	var commands, opens atomic.Int32
	done := make(chan struct{})
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
		stop := context.AfterFunc(ctx, func() { server.Close() })
		defer stop()
		go ssh.DiscardRequests(requests)
		var workers sync.WaitGroup
		defer workers.Wait()
		for incoming := range channels {
			opens.Add(1)
			workers.Add(1)
			go func(incoming ssh.NewChannel) {
				defer workers.Done()
				if holdChannel {
					<-ctx.Done()
					return
				}
				if len(openDelay) != 0 {
					select {
					case <-time.After(openDelay[0]):
					case <-ctx.Done():
						return
					}
				}
				channel, requests, err := incoming.Accept()
				if err != nil {
					return
				}
				defer channel.Close()
				for request := range requests {
					if request.Type != "exec" {
						request.Reply(false, nil)
						continue
					}
					if len(openDelay) > 1 {
						_ = request.Reply(false, nil)
						<-ctx.Done()
						return
					}
					var command struct{ Value string }
					if ssh.Unmarshal(request.Payload, &command) != nil || command.Value != networkMonitorCommand {
						request.Reply(false, nil)
						return
					}
					request.Reply(true, nil)
					value, err := sample(ctx, int(commands.Add(1)))
					status := uint32(0)
					if err != nil {
						status = 1
					} else {
						channel.Write(value)
					}
					channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
					return
				}
			}(incoming)
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second})
	if err != nil {
		cancel()
		listener.Close()
		t.Fatal(err)
	}
	session := &Session{ctx: ctx, cancel: cancel, client: client}
	t.Cleanup(func() {
		session.Close()
		listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("network SSH fixture leaked its server/channel worker")
		}
	})
	return session, &commands, &opens
}

func awaitNetworkCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("network sampling condition timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNetworkBackgroundContinuesWithoutHTTPAndSharesHistory(t *testing.T) {
	t.Parallel()
	start := time.Now()
	session, commands, _ := backgroundNetworkSSHFixture(t, false, func(_ context.Context, n int) ([]byte, error) {
		return networkCounterFixture(100+time.Since(start).Seconds(), uint64(n*100), uint64(n*200)), nil
	})
	session.startNetworkSampler()
	session.startNetworkSampler() // detaching/merging must not create another loop
	// No HTTP requests and no selected/visible view: the remote sampler still runs.
	awaitNetworkCondition(t, 3*time.Second, func() bool { return commands.Load() >= 3 })
	awaitNetworkCondition(t, time.Second, func() bool {
		session.networkMu.Lock()
		defer session.networkMu.Unlock()
		return len(session.networkHistory) >= 3
	})
	value, err := session.networkForDisplay(context.Background())
	if err != nil || len(value.History) != 3 || !value.Cached || !value.SampleReady || value.History[0].Interfaces[0].Ready {
		t.Fatalf("hidden session lost real observations: %+v %v", value, err)
	}
	for i := 1; i < len(value.History); i++ {
		sample := value.History[i]
		if !sample.Interfaces[0].Ready || sample.ElapsedMilliseconds < 750 || sample.ElapsedMilliseconds > 1300 || sample.Interfaces[0].RX <= 0 {
			t.Fatalf("invalid independent one-second observation: %+v", sample)
		}
	}
	value.History[1].Interfaces[0].RX = -999
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for n := 0; n < 100; n++ {
				view, err := session.networkForDisplay(context.Background())
				if err != nil || view.History[1].Interfaces[0].RX == -999 {
					t.Error("history aliases shared state or read failed", err)
				}
			}
		}()
	}
	readers.Wait()
	if commands.Load() != 3 {
		t.Fatal("concurrent background cache reads issued extra remote commands", commands.Load())
	}
	session.Close()
	before := commands.Load()
	time.Sleep(1100 * time.Millisecond)
	if commands.Load() != before {
		t.Fatal("sampler continued after session close")
	}
	t.Logf("No HTTP for 2 seconds: %d real samples; 800 concurrent reads reused them; repeated start and close verified", before)
}

func TestNetworkBackgroundStalledServersDoNotBlockOtherSessions(t *testing.T) {
	t.Parallel()
	var stalled []*Session
	var opened []*atomic.Int32
	for i := 0; i < 4; i++ {
		session, _, opens := backgroundNetworkSSHFixture(t, true, nil)
		stalled, opened = append(stalled, session), append(opened, opens)
		session.startNetworkSampler()
	}
	awaitNetworkCondition(t, time.Second, func() bool {
		for _, count := range opened {
			if count.Load() != 1 {
				return false
			}
		}
		return true
	})
	start := time.Now()
	fast, commands, _ := backgroundNetworkSSHFixture(t, false, func(_ context.Context, n int) ([]byte, error) {
		return networkCounterFixture(100+time.Since(start).Seconds(), uint64(n*100), uint64(n*200)), nil
	})
	fast.startNetworkSampler()
	awaitNetworkCondition(t, 2500*time.Millisecond, func() bool { return commands.Load() >= 3 })
	// Stalled opens survive a sample timeout but must not multiply next tick.
	time.Sleep(2200 * time.Millisecond)
	for i, session := range stalled {
		if opened[i].Load() != 1 {
			t.Fatalf("slow server accumulated channel opens: %d", opened[i].Load())
		}
		closedAt := time.Now()
		session.Close()
		if time.Since(closedAt) > 250*time.Millisecond {
			t.Fatal("stalled channel open delayed closing the SSH session")
		}
		awaitNetworkCondition(t, time.Second, func() bool { return len(session.networkOpenSlot) == 0 })
	}
	if commands.Load() < 5 {
		t.Fatal("slow sessions blocked the independent fast sampler", commands.Load())
	}
	t.Logf("4 unresponsive channel-open servers, fast session recorded %d samples; each slow server retained one opening worker and closed cleanly", commands.Load())
}

func TestNetworkBackgroundContextCancellationInterruptsRead(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &Session{ctx: ctx}
	entered, exited := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	session.startNetworkSamplerWithReader(func(readCtx context.Context, _ string) ([]byte, error) {
		calls.Add(1)
		close(entered)
		<-readCtx.Done()
		close(exited)
		return nil, readCtx.Err()
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("background sampler did not start")
	}
	cancel()
	select {
	case <-exited:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("session cancellation failed to cancel its in-flight read")
	}
	time.Sleep(1100 * time.Millisecond)
	session.networkMu.Lock()
	defer session.networkMu.Unlock()
	if calls.Load() != 1 || len(session.networkHistory) != 0 {
		t.Fatal("canceled session restarted sampling or recorded a fabricated error frame")
	}
}

func TestNetworkBackgroundFailureRetainsHistoryAndRequiresFreshBaseline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &Session{ctx: ctx, networkBackground: true}
	for n := 1; n <= 5; n++ {
		session.networkNextSampleAt = time.Time{}
		_, err := session.collectNetworkSnapshot(ctx, func(context.Context, string) ([]byte, error) {
			if n == 3 {
				return nil, errors.New("controlled missing observation")
			}
			return networkCounterFixture(100+float64(n), uint64(100*n), uint64(200*n)), nil
		})
		if (err != nil) != (n == 3) {
			t.Fatal("unexpected observation error", n, err)
		}
		value, err := session.networkForDisplay(ctx)
		if err != nil || len(value.History) != n {
			t.Fatal("failed sample hid existing history", value, err)
		}
		if n == 3 && (value.SampleError == "" || value.SampleReady || len(value.History[2].Interfaces) != 0) {
			t.Fatal("failed observation presented as a valid zero", value)
		}
		if n == 4 && (value.SampleReady || value.Interfaces[0].Ready) {
			t.Fatal("post-error baseline invented a one-second rate", value)
		}
		if n == 5 && (!value.SampleReady || value.SampleError != "" || value.Interfaces[0].RX != 100) {
			t.Fatal("history failed to recover after a fresh baseline", value)
		}
	}
}

func TestNetworkBackgroundHistoryBoundedAndPruned(t *testing.T) {
	session := &Session{}
	start := time.Now().Add(-60 * time.Second)
	for i := 0; i < 100; i++ {
		session.retainNetworkHistory(NetworkHistorySample{SampledAt: start.Add(time.Duration(i) * time.Second), Interfaces: []NetworkHistoryInterface{{Name: fmt.Sprint(i)}}})
	}
	if len(session.networkHistory) != 31 || session.networkHistory[0].Interfaces[0].Name != "69" {
		t.Fatal("history exceeds actual 30-second window", len(session.networkHistory))
	}
	for i := 0; i < 500; i++ {
		session.retainNetworkHistory(NetworkHistorySample{SampledAt: start.Add(100 * time.Second), Interfaces: []NetworkHistoryInterface{{Name: "burst"}}})
	}
	if len(session.networkHistory) != networkHistoryLimit {
		t.Fatal("history memory has no absolute sample cap", len(session.networkHistory))
	}
}
