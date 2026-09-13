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
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestAuthenticationMethods(t *testing.T) {
	dir := t.TempDir()
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) == "test-password" {
				return nil, nil
			}
			return nil, errors.New("bad password")
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), signer.PublicKey().Marshal()) {
				return nil, nil
			}
			return nil, errors.New("bad key")
		},
	}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				sc, chans, reqs, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer sc.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if ch.ChannelType() != "session" {
						ch.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					channel, requests, err := ch.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for request := range requests {
							var args struct{ Name string }
							ssh.Unmarshal(request.Payload, &args)
							if request.Type == "subsystem" && args.Name == "sftp" {
								request.Reply(true, nil)
								server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(dir))
								if err == nil {
									server.Serve()
									server.Close()
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
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	a, err := New(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close(); listener.Close(); wg.Wait() }()
	trustFixtureHostKey(t, a, listener.Addr().String(), signer.PublicKey())
	block, err := ssh.MarshalPrivateKeyWithPassphrase(private, "test", []byte("test-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "encrypted-key")
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	// Keep the Unix socket path short even under a long test temp directory.
	socketDir, err := os.MkdirTemp(os.TempDir(), "cs-agent-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	sock := filepath.Join(socketDir, "s")
	agentListener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer agentListener.Close()
	keyring := agent.NewKeyring()
	keyring.Add(agent.AddedKey{PrivateKey: private})
	go func() {
		for {
			conn, err := agentListener.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); agent.ServeAgent(keyring, conn) }()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)
	for _, test := range []struct {
		name, auth, secret string
		success            bool
	}{{"password", "password", "test-password", true}, {"wrong password", "password", "bad", false}, {"encrypted key", "key", "test-passphrase", true}, {"wrong key passphrase", "key", "bad", false}, {"agent", "agent", "", true}} {
		t.Run(test.name, func(t *testing.T) {
			p, err := a.store.Save(Profile{Name: test.name, Host: host, Port: port, User: "tester", Auth: test.auth, KeyPath: keyFile}, false)
			if err != nil {
				t.Fatal(err)
			}
			s, err := a.Connect(context.Background(), p.ID, test.secret)
			if test.success != (err == nil) {
				t.Fatalf("success=%v error=%v", test.success, err)
			}
			if s != nil {
				a.disconnect(s.ID)
			}
		})
	}
}
