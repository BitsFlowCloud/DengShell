package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
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
)

func optionalSFTPFixture(t *testing.T, mode string) (*App, string, int) {
	t.Helper()
	config := &ssh.ServerConfig{NoClientAuth: true}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// This fixture owns the key material; trust that exact key independently of
	// the network handshake so authentication tests cannot silently accept MITM.
	trustFixtureHostKey(t, app, listener.Addr().String(), signer.PublicKey())
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer connection.Close()
				server, channels, requests, err := ssh.NewServerConn(connection, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for request := range channels {
					if request.ChannelType() != "session" {
						request.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					channel, requests, err := request.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for request := range requests {
							var subsystem struct{ Name string }
							_ = ssh.Unmarshal(request.Payload, &subsystem)
							if request.Type == "subsystem" && subsystem.Name == "sftp" {
								switch mode {
								case "reject":
									request.Reply(false, nil)
									continue
								case "request-stall":
									continue
								case "close":
									request.Reply(true, nil)
									return
								case "handshake-stall":
									request.Reply(true, nil)
									io.Copy(io.Discard, channel)
									return
								case "home-stall":
									request.Reply(true, nil)
									init := make([]byte, 9)
									io.ReadFull(channel, init)
									channel.Write([]byte{0, 0, 0, 5, 2, 0, 0, 0, 3})
									io.Copy(io.Discard, channel)
									return
								default:
									request.Reply(true, nil)
									server := sftp.NewRequestServer(channel, sftp.InMemHandler())
									server.Serve()
									server.Close()
								}
								return
							}
							if request.Type == "pty-req" {
								request.Reply(true, nil)
								continue
							}
							if request.Type == "shell" {
								request.Reply(true, nil)
								io.Copy(channel, channel)
								return
							}
							request.Reply(false, nil)
						}
					}()
				}
			}()
		}
	}()
	t.Cleanup(func() { app.Close(); listener.Close(); workers.Wait() })
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	return app, host, port
}

func TestOptionalSFTPKeepsSSHUsable(t *testing.T) {
	for _, mode := range []string{"reject", "request-stall", "close", "handshake-stall", "home-stall", "available"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			a, host, port := optionalSFTPFixture(t, mode)
			p, err := a.store.Save(Profile{Name: "optional files", Host: host, Port: port, User: "fixture", Auth: "password", Secret: "fixture"}, false)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			s, err := a.Connect(context.Background(), p.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if time.Since(started) > 9*time.Second {
				t.Fatal("optional file service blocked connection")
			}
			if s.SFTPAvailable != (mode == "available") || (s.files != nil) != s.SFTPAvailable {
				t.Fatal("incorrect SFTP capability")
			}
			if s.Home == "" {
				t.Fatal("missing fallback path")
			}
			hs := httptest.NewServer(a.Handler(fstest.MapFS{}))
			defer hs.Close()
			a.baseURL = hs.URL
			address := "ws" + strings.TrimPrefix(hs.URL, "http") + "/api/sessions/" + s.ID + "/terminal?token=" + url.QueryEscape(a.Token())
			terminal := dialRelayTest(t, address)
			terminal.command(t, "SSH_STILL_USABLE", "SSH_STILL_USABLE")
			if !s.SFTPAvailable {
				if _, err := a.fileSession(s.ID); err != errSFTPUnavailable {
					t.Fatal("missing file guard", err)
				}
				for _, endpoint := range []string{"files", "download", "file-content", "file-permissions", "archive", "directory-favorites"} {
					request := httptest.NewRequest("GET", hs.URL+"/api/sessions/"+s.ID+"/"+endpoint+"?path=/", nil)
					request.Header.Set("X-CloudShell-Token", a.Token())
					result := httptest.NewRecorder()
					a.Handler(fstest.MapFS{}).ServeHTTP(result, request)
					if result.Code != 400 || !strings.Contains(result.Body.String(), "SFTP") {
						t.Fatalf("%s returned %d %s", endpoint, result.Code, result.Body.String())
					}
				}
				if err := a.DownloadTo(s.ID, "/test", "unused"); err != errSFTPUnavailable {
					t.Fatal(err)
				}
			} else if _, err := s.files.ReadDir("/"); err != nil {
				t.Fatal(err)
			}
			terminal.command(t, "AFTER_FILE_REQUEST", "AFTER_FILE_REQUEST")
		})
	}
}
