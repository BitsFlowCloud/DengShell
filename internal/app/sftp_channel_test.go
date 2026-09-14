package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestBlockedSFTPBridgeClosesOnlyFileChannel(t *testing.T) {
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
	defer cancel()
	defer listener.Close()
	done, pending := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		server, channels, requests, err := ssh.NewServerConn(raw, config)
		if err != nil {
			return
		}
		defer server.Close()
		stop := context.AfterFunc(ctx, func() { server.Close() })
		defer stop()
		go ssh.DiscardRequests(requests)
		for incoming := range channels {
			ch, requests, err := incoming.Accept()
			if err != nil {
				continue
			}
			go func() {
				defer ch.Close()
				for req := range requests {
					if req.Type == "subsystem" {
						req.Reply(true, nil)
						init := make([]byte, 9)
						if _, err := io.ReadFull(ch, init); err != nil {
							return
						}
						ch.Write([]byte{0, 0, 0, 5, 2, 0, 0, 0, 3}) // SFTP VERSION 3
						header := make([]byte, 4)
						if _, err := io.ReadFull(ch, header); err != nil {
							return
						}
						close(pending)
						// Deliberately never answer Stat, even while SSH heartbeats still work.
						io.Copy(io.Discard, ch)
						return
					}
					if req.Type == "exec" {
						req.Reply(true, nil)
						ch.Write([]byte("terminal-still-works"))
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						return
					}
					req.Reply(false, nil)
				}
			}()
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{client: client, ctx: ctx, cancel: cancel, cleanupGrace: 100 * time.Millisecond}
	defer func() {
		s.forceClose()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("server worker did not exit")
		}
	}()
	s.files, err = s.openFileClient()
	if err != nil {
		t.Fatal(err)
	}
	go s.heartbeat()
	opctx, stop := context.WithCancel(ctx)
	defer stop()
	op := startSFTPOperation(opctx, s, time.Minute)
	result := make(chan error, 1)
	go func() { _, err := s.files.Stat("/fixture"); result <- op.err(err); op.close() }()
	select {
	case <-pending:
	case <-time.After(time.Second):
		t.Fatal("SFTP request not received")
	}
	stop()
	select {
	case err := <-result:
		if err == nil || !op.fileChannelClosed.Load() {
			t.Fatal("blocked file operation not isolated", err)
		}
	case <-time.After(time.Second):
		t.Fatal("file operation did not release")
	}
	if ctx.Err() != nil {
		t.Fatal("SFTP failure closed SSH")
	}
	awaitNetworkCondition(t, time.Second, func() bool { return s.Latency().Ready })
	sh, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	out, err := sh.Output("true")
	if err != nil || string(out) != "terminal-still-works" {
		t.Fatal("SSH no longer usable", string(out), err)
	}
}
