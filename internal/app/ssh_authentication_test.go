package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func authenticationFixture(t *testing.T, config *ssh.ServerConfig) (*App, string, int) {
	t.Helper()
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
								request.Reply(true, nil)
								server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(app.store.dir))
								if err == nil {
									_ = server.Serve()
									_ = server.Close()
								}
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

func TestSSHBadSavedPasswordCanRetryWithExplicitCorrectCredential(t *testing.T) {
	var attempts atomic.Int32
	a, host, port := authenticationFixture(t, &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		attempts.Add(1)
		if meta.User() == "correct-user" && string(password) == "correct-secret" {
			return nil, nil
		}
		return nil, errors.New("rejected by fixture")
	}})
	p, err := a.store.Save(Profile{Name: "fixture", Host: host, Port: port, User: "correct-user", Auth: "password", Secret: "old-secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Connect(context.Background(), p.ID, "")
	var authError *AuthenticationError
	if !errors.As(err, &authError) || authError.Code() != "ssh_authentication_failed" || !strings.Contains(err.Error(), "无法确定") {
		t.Fatal("unqualified or unstructured authentication failure", err)
	}
	for _, secret := range []string{"old-secret", "correct-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("credential exposed in error")
		}
	}
	if len(a.store.List().ConnectionHistory) != 0 {
		t.Fatal("failed attempt recorded as success")
	}
	session, err := a.Connect(context.Background(), p.ID, "correct-secret")
	if err != nil {
		t.Fatal("fresh explicit credential did not override saved password", err)
	}
	a.disconnect(session.ID)
	if attempts.Load() != 2 || len(a.store.List().ConnectionHistory) != 1 {
		t.Fatal("unexpected automatic retry or success history", attempts.Load())
	}
	saved, _ := a.store.Get(p.ID)
	if saved.Secret != "old-secret" {
		t.Fatal("connection retry silently overwrote saved credential")
	}
}

func TestSSHPasswordModeDetectsUnsupportedServerMethod(t *testing.T) {
	a, host, port := authenticationFixture(t, &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
		return nil, errors.New("key required")
	}})
	p, err := a.store.Save(Profile{Name: "fixture", Host: host, Port: port, User: "fixture", Auth: "password"}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Connect(context.Background(), p.ID, "unused-secret")
	var classified *AuthenticationError
	if !errors.As(err, &classified) || classified.Code() != "ssh_authentication_unsupported" || !strings.Contains(err.Error(), "公钥") || !strings.Contains(err.Error(), "不能证明密码错误") {
		t.Fatal("server method policy was misreported", err)
	}
}

func TestSSHKeyboardInteractiveOnlySuppliesRecognizedPassword(t *testing.T) {
	for _, tc := range []struct {
		name      string
		questions []string
		echo      []bool
		success   bool
	}{
		{"password", []string{"Password: "}, []bool{false}, true},
		{"otp", []string{"One-time password: "}, []bool{false}, false},
		{"multiple", []string{"Password:", "Verification code:"}, []bool{false, false}, false},
		{"echo", []string{"Password:"}, []bool{true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var answered atomic.Bool
			a, host, port := authenticationFixture(t, &ssh.ServerConfig{KeyboardInteractiveCallback: func(meta ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				answers, err := challenge(meta.User(), "", tc.questions, tc.echo)
				if err != nil {
					return nil, err
				}
				answered.Store(true)
				if len(answers) == 1 && answers[0] == "interactive-secret" {
					return nil, nil
				}
				return nil, errors.New("invalid response")
			}})
			p, err := a.store.Save(Profile{Name: tc.name, Host: host, Port: port, User: "fixture", Auth: "password"}, false)
			if err != nil {
				t.Fatal(err)
			}
			session, err := a.Connect(context.Background(), p.ID, "interactive-secret")
			if tc.success {
				if err != nil {
					t.Fatal(err)
				}
				a.disconnect(session.ID)
			} else {
				var classified *AuthenticationError
				if !errors.As(err, &classified) || classified.Code() != "ssh_authentication_unsupported" {
					t.Fatal("interactive method was not identified", err)
				}
				if answered.Load() {
					t.Fatal("password was supplied to an unsupported challenge")
				}
			}
		})
	}
}

func TestSSHUnencryptedKeyIgnoresAnUnneededCachedPassphrase(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	a, host, port := authenticationFixture(t, &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if bytes.Equal(key.Marshal(), signer.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, errors.New("wrong public key")
	}})
	block, err := ssh.MarshalPrivateKey(private, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(filename, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := a.store.Save(Profile{Name: "key", Host: host, Port: port, User: "fixture", Auth: "key", KeyPath: filename, Secret: "obsolete-passphrase"}, false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := a.Connect(context.Background(), p.ID, "")
	if err != nil {
		t.Fatal("plain key incorrectly required cached passphrase", err)
	}
	a.disconnect(session.ID)
}

func TestTransportFailuresAreNotClassifiedAsWrongCredentials(t *testing.T) {
	original := errors.New("connection reset by peer")
	err := explainSSHAuthentication(original, "password", authenticationTrace{})
	var classified *AuthenticationError
	if errors.As(err, &classified) || !errors.Is(err, original) || strings.Contains(err.Error(), "密码错误") {
		t.Fatal("transport failure misclassified", err)
	}
}
