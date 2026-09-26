package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/crypto/ssh"
)

// Observe actual SSH requests, without executing commands or touching a real
// remote home. The file subsystem deliberately never acknowledges its request.
type terminalPriorityFixture struct {
	app    *App
	host   string
	port   int
	done   chan struct{}
	mu     sync.Mutex
	events []string
}

func (f *terminalPriorityFixture) record(event string) {
	f.mu.Lock()
	f.events = append(f.events, event)
	f.mu.Unlock()
}

func (f *terminalPriorityFixture) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func newTerminalPriorityFixture(t *testing.T, rejectExec bool) *terminalPriorityFixture {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(t.TempDir())
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	trustFixtureHostKey(t, a, listener.Addr().String(), signer.PublicKey())
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	f := &terminalPriorityFixture{app: a, host: host, port: port, done: make(chan struct{})}
	go func() {
		defer close(f.done)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		server, channels, requests, err := ssh.NewServerConn(connection, config)
		if err != nil {
			return
		}
		var workers sync.WaitGroup
		defer func() { server.Close(); workers.Wait() }()
		go ssh.DiscardRequests(requests)
		channelID := 0
		for pending := range channels {
			channelID++
			id := channelID
			f.record(fmt.Sprintf("channel:%d", id))
			if pending.ChannelType() != "session" {
				pending.Reject(ssh.UnknownChannelType, "unsupported")
				continue
			}
			channel, requests, err := pending.Accept()
			if err != nil {
				continue
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer channel.Close()
				for request := range requests {
					switch request.Type {
					case "pty-req":
						f.record(fmt.Sprintf("pty:%d", id))
						request.Reply(true, nil)
					case "exec":
						f.record(fmt.Sprintf("exec:%d", id))
						if rejectExec || id != 1 {
							request.Reply(false, nil)
							continue
						}
						request.Reply(true, nil)
						f.record(fmt.Sprintf("prompt:%d", id))
						io.WriteString(channel, "FIXTURE_PROMPT> ")
						io.Copy(channel, channel)
						return
					case "shell":
						f.record(fmt.Sprintf("shell:%d", id))
						request.Reply(true, nil)
						f.record(fmt.Sprintf("prompt:%d", id))
						io.WriteString(channel, "FIXTURE_PROMPT> ")
						io.Copy(channel, channel)
						return
					case "subsystem":
						var subsystem struct{ Name string }
						_ = ssh.Unmarshal(request.Payload, &subsystem)
						f.record(fmt.Sprintf("subsystem:%d:%s", id, subsystem.Name))
						// Leave the SFTP request pending while the terminal remains usable.
					default:
						request.Reply(false, nil)
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		a.Close()
		listener.Close()
		select {
		case <-f.done:
		case <-time.After(3 * time.Second):
			t.Error("isolated SSH fixture did not close")
		}
	})
	return f
}

func (f *terminalPriorityFixture) connect(t *testing.T) *Session {
	t.Helper()
	p, err := f.app.store.Save(Profile{Name: "terminal priority fixture", Host: f.host, Port: f.port, User: "fixture", Auth: "password", Secret: "fixture"}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := f.app.Connect(ctx, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTerminalPriorityUnattachedConnectionDoesNoRemoteWork(t *testing.T) {
	f := newTerminalPriorityFixture(t, false)
	s := f.connect(t)
	// An unattached connection should remain idle, including optional workers.
	time.Sleep(100 * time.Millisecond)
	if events := f.snapshot(); len(events) != 0 {
		t.Fatalf("SSH authentication opened remote session channels before terminal attach: %v", events)
	}
	s.Close()
	select {
	case <-f.done:
	case <-time.After(3 * time.Second):
		t.Fatal("unattached SSH transport did not close")
	}
	if events := f.snapshot(); len(events) != 0 {
		t.Fatalf("closing an unattached terminal performed remote cleanup work: %v", events)
	}
}

func TestTerminalPriorityPromptAndInputPrecedeStalledSFTP(t *testing.T) {
	for _, rejectExec := range []bool{false, true} {
		t.Run(fmt.Sprintf("execRejected=%t", rejectExec), func(t *testing.T) {
			f := newTerminalPriorityFixture(t, rejectExec)
			s := f.connect(t)
			hs := httptest.NewServer(f.app.Handler(fstest.MapFS{}))
			defer hs.Close()
			f.app.baseURL = hs.URL
			address := "ws" + strings.TrimPrefix(hs.URL, "http") + "/api/sessions/" + s.ID + "/terminal?token=" + url.QueryEscape(f.app.Token())
			terminal := dialRelayTest(t, address)
			terminal.command(t, "FIRST_INPUT", "FIRST_INPUT")
			if !strings.Contains(terminal.data.String(), "FIXTURE_PROMPT> ") {
				t.Fatal("terminal input became available without the initial remote prompt")
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				events := f.snapshot()
				if strings.Contains(strings.Join(events, ","), ":sftp") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("file service never started after terminal output: %v", events)
				}
				time.Sleep(10 * time.Millisecond)
			}
			events := f.snapshot()
			prefix := []string{"channel:1", "pty:1", "exec:1"}
			if rejectExec {
				prefix = append(prefix, "shell:1")
			}
			prefix = append(prefix, "prompt:1")
			if strings.Join(events[:len(prefix)], ",") != strings.Join(prefix, ",") {
				t.Fatalf("terminal was not the first remote session or files preceded its prompt: %v", events)
			}
			// The subsystem is still blocked; a second interactive command must work.
			terminal.command(t, "AFTER_SFTP_STALL", "AFTER_SFTP_STALL")
		})
	}
}
