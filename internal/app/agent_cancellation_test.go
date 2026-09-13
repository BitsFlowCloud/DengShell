//go:build !windows

package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type blockedSigningAgent struct {
	agent.Agent
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *blockedSigningAgent) Sign(ssh.PublicKey, []byte) (*ssh.Signature, error) {
	a.once.Do(func() { close(a.entered) })
	<-a.release
	return nil, io.EOF
}

func TestSSHAgentCancellationBoundsIdentityAndSigningRequests(t *testing.T) {
	for _, phase := range []string{"identities", "signing"} {
		for _, action := range []string{"request cancel", "application close", "deadline"} {
			t.Run(phase+"/"+action, func(t *testing.T) {
				a, host, port := authenticationFixture(t, &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }})
				// Unix socket names have a short platform limit; this stays in the
				// caller's isolated TMPDIR while avoiding a long t.TempDir suffix.
				dir, err := os.MkdirTemp(os.TempDir(), "a-")
				if err != nil {
					t.Fatal(err)
				}
				defer os.RemoveAll(dir)
				socket := filepath.Join(dir, "s")
				listener, err := net.Listen("unix", socket)
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				t.Setenv("SSH_AUTH_SOCK", socket)
				p, err := a.store.Save(Profile{Name: "blocked agent", Host: host, Port: port, User: "tester", Auth: "agent"}, false)
				if err != nil {
					t.Fatal(err)
				}
				accepted := make(chan net.Conn, 1)
				go func() {
					conn, err := listener.Accept()
					if err == nil {
						accepted <- conn
					}
				}()
				ctx, cancel := context.WithCancel(context.Background())
				if action == "deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
				}
				defer cancel()
				result := make(chan error, 1)
				go func() { _, err := a.Connect(ctx, p.ID, ""); result <- err }()
				var conn net.Conn
				select {
				case conn = <-accepted:
				case <-time.After(2 * time.Second):
					t.Fatal("agent was not contacted")
				}
				defer conn.Close()
				if phase == "identities" {
					_ = conn.SetReadDeadline(time.Now().Add(time.Second))
					if _, err := io.ReadFull(conn, make([]byte, 5)); err != nil {
						t.Fatal(err)
					}
				} else {
					_, private, _ := ed25519.GenerateKey(rand.Reader)
					keyring := agent.NewKeyring()
					if err := keyring.Add(agent.AddedKey{PrivateKey: private}); err != nil {
						t.Fatal(err)
					}
					blocked := &blockedSigningAgent{Agent: keyring, entered: make(chan struct{}), release: make(chan struct{})}
					done := make(chan struct{})
					go func() { defer close(done); _ = agent.ServeAgent(blocked, conn) }()
					defer func() { close(blocked.release); _ = conn.Close(); <-done }()
					select {
					case <-blocked.entered:
					case <-time.After(2 * time.Second):
						t.Fatal("agent signing phase not reached")
					}
				}
				if action == "request cancel" {
					cancel()
				}
				if action == "application close" {
					a.Close()
				}
				select {
				case err := <-result:
					if err == nil {
						t.Fatal("blocked agent unexpectedly connected")
					}
				case <-time.After(time.Second):
					t.Fatal("Connect ignored cancellation/deadline while Agent request was blocked")
				}
			})
		}
	}
}
