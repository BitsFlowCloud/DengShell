package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type terminalMemoryFilecmd struct{ sftp.FileCmder }

func (m terminalMemoryFilecmd) Filecmd(r *sftp.Request) error {
	// InMemHandler does not implement directory chmod. Permissions have no
	// effect in this memory-only fixture, so acknowledge those metadata writes.
	if r.Method == "Setstat" {
		return nil
	}
	return m.FileCmder.Filecmd(r)
}

// The fixture executes no OS commands. All SFTP paths, including shell
// integration staging, exist only in an in-memory filesystem.
func terminalStartupFixture(t *testing.T, blocked string) (*App, *Session, string, <-chan struct{}) {
	t.Helper()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	var enteredOnce sync.Once
	mark := func() { enteredOnce.Do(func() { close(entered) }) }
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		server, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		count := 0
		for candidate := range channels {
			count++
			if blocked == "channel-open" && count > 1 {
				mark()
				continue
			}
			channel, requests, err := candidate.Accept()
			if err != nil {
				continue
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer channel.Close()
				for request := range requests {
					switch request.Type {
					case "subsystem":
						_ = request.Reply(true, nil)
						handlers := sftp.InMemHandler()
						handlers.FileCmd = terminalMemoryFilecmd{handlers.FileCmd}
						fs := sftp.NewRequestServer(channel, handlers)
						_ = fs.Serve()
						_ = fs.Close()
						return
					case "pty-req":
						if blocked == "pty" {
							mark()
							continue
						}
						_ = request.Reply(true, nil)
					case "shell":
						if blocked == "shell" {
							mark()
							continue
						}
						_ = request.Reply(true, nil)
						_, _ = io.Copy(channel, channel)
						return
					case "exec":
						var command struct{ Command string }
						_ = ssh.Unmarshal(request.Payload, &command)
						if blocked == "exec" && strings.HasPrefix(command.Command, "exec ") {
							mark()
							continue
						}
						if blocked == "exec" {
							_ = request.Reply(true, nil)
							_, _ = io.WriteString(channel, "__DENGSHELL_SHELL__/bin/bash\n__DENGSHELL_USER__fixture\n__DENGSHELL_HOST__fixture\n")
							_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
						_ = request.Reply(false, nil)
					default:
						_ = request.Reply(false, nil)
					}
				}
			}()
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	files, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	if blocked == "exec" {
		if err := files.Mkdir("/tmp"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(a.ctx)
	s := &Session{ID: randomID(), client: client, files: files, ctx: ctx, cancel: cancel}
	a.sessions[s.ID] = s
	httpServer := httptest.NewServer(a.Handler(fstest.MapFS{}))
	a.baseURL = httpServer.URL
	t.Cleanup(func() { a.Close(); listener.Close(); httpServer.Close(); workers.Wait() })
	return a, s, "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/sessions/" + s.ID + "/terminal?token=" + url.QueryEscape(a.Token()), entered
}

func TestTerminalStartupDeadlineClosesUnresponsiveTransport(t *testing.T) {
	for _, stage := range []string{"channel-open", "pty", "shell", "exec"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			_, s, address, entered := terminalStartupFixture(t, stage)
			ws, _, err := websocket.DefaultDialer.Dial(address, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("target startup stage was not reached")
			}
			// Keepalives remain responsive: transport health cannot substitute for
			// a deadline on individual terminal startup requests.
			if _, _, err := s.client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				t.Fatal(err)
			}
			select {
			case <-s.ctx.Done():
			case <-time.After(terminalStartupTimeout + 2*time.Second):
				t.Fatal("startup deadline did not release the session")
			}
			s.mu.Lock()
			ready := s.terminalReady
			s.mu.Unlock()
			if ready {
				t.Fatal("blocked terminal was marked ready")
			}
			_ = ws.SetReadDeadline(time.Now().Add(time.Second))
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					break
				}
			}
		})
	}
}

func TestSyntheticTerminalHandoffKeepsOneLiveSession(t *testing.T) {
	a, s, address, _ := terminalStartupFixture(t, "")
	old := dialRelayTest(t, address)
	old.command(t, "BEFORE_HANDOFF", "BEFORE_HANDOFF")
	nonce := "fixture-relay-handoff-000001"
	old.send(t, relayMessage{Type: "handoff-begin", Nonce: nonce})
	old.until(t, func(m relayMessage) bool { return m.Type == "handoff-ready" })
	if _, err := a.ReserveWindowHandoff(windowSnapshot(s.ID, nonce)); err != nil {
		t.Fatal(err)
	}
	next := dialRelayTest(t, address+"&handoff="+nonce)
	if attached, err := a.CancelWindowHandoff(nonce); err != nil || !attached {
		t.Fatal("late cancel changed owner", err)
	}
	next.command(t, "AFTER_HANDOFF", "AFTER_HANDOFF")
	s.mu.Lock()
	ready := s.terminalReady
	s.mu.Unlock()
	if !ready {
		t.Fatal("usable terminal was not marked ready")
	}
	next.ws.Close()
	relayWaitClosed(t, s)
}

func TestFailedTerminalUpgradeDoesNotBypassOrphanOrBlockRetry(t *testing.T) {
	_, s, address, _ := terminalStartupFixture(t, "")
	response, err := http.Get("http" + strings.TrimPrefix(address, "ws"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("invalid WebSocket upgrade accepted", response.StatusCode)
	}
	s.mu.Lock()
	started, relay := s.terminalStarted, s.terminalRelay
	s.mu.Unlock()
	if started || relay != nil {
		t.Fatal("failed upgrade retained reservation or escaped orphan deadline")
	}
	client := dialRelayTest(t, address)
	client.command(t, "RETRY_WORKS", "RETRY_WORKS")
	s.mu.Lock()
	ready, relay := s.terminalReady, s.terminalRelay
	s.mu.Unlock()
	if !ready || relay == nil {
		t.Fatal("valid retry did not establish an active relay")
	}
}
