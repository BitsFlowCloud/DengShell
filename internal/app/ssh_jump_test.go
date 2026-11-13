package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSSHJumpHostAuthenticatesEachHopAndCarriesSFTP(t *testing.T) {
	a, targetHost, targetPort := optionalSFTPFixture(t, "available")
	targetAddress := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	var forwards atomic.Int32
	config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if meta.User() != "jump" || string(password) != "jump-only" {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer raw.Close()
				server, channels, requests, err := ssh.NewServerConn(raw, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					var request struct {
						Host       string
						Port       uint32
						Origin     string
						OriginPort uint32
					}
					if incoming.ChannelType() != "direct-tcpip" || ssh.Unmarshal(incoming.ExtraData(), &request) != nil ||
						net.JoinHostPort(request.Host, strconv.Itoa(int(request.Port))) != targetAddress {
						_ = incoming.Reject(ssh.Prohibited, "fixture only forwards to its local SSH server")
						continue
					}
					channel, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					go ssh.DiscardRequests(requests)
					go func() {
						defer channel.Close()
						conn, err := net.DialTimeout("tcp", targetAddress, time.Second)
						if err != nil {
							return
						}
						defer conn.Close()
						forwards.Add(1)
						go func() { _, _ = io.Copy(conn, channel); _ = conn.Close() }()
						_, _ = io.Copy(channel, conn)
					}()
				}
			}()
		}
	}()
	jumpHost, jumpPortText, _ := net.SplitHostPort(listener.Addr().String())
	jumpPort, _ := strconv.Atoi(jumpPortText)
	jump, err := a.store.Save(Profile{Name: "跳板机", Host: jumpHost, Port: jumpPort, User: "jump"}, false)
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.store.Save(Profile{Name: "目标", Host: targetHost, Port: targetPort, User: "target", Secret: "target-only", JumpHostID: jump.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	request := func(secret string, approval *HostKeyApproval) *sessionJumpResponse {
		w := issueRequest(t, a, "POST", "/api/sessions", map[string]any{
			"profileId": target.ID, "jumpSecret": secret, "hostKeyApproval": approval,
		})
		var got sessionJumpResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		got.status = w.Code
		return &got
	}
	if got := request("", nil); got.status != http.StatusBadRequest || got.Code != "ssh_password_required" || got.JumpHostID != jump.ID {
		t.Fatalf("missing jump credential: %+v", got)
	}
	if got := request("jump-only", nil); got.Code != "ssh_host_key_unknown" || got.JumpHostID != jump.ID || got.HostKey == nil || got.HostKey.Host != listener.Addr().String() || forwards.Load() != 0 {
		t.Fatalf("jump key was not checked first: %+v", got)
	}
	jumpApproval := &HostKeyApproval{Host: listener.Addr().String(), Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}
	if err := a.store.ResetHostKey(target); err != nil {
		t.Fatal(err)
	}
	got := request("jump-only", jumpApproval)
	if got.Code != "ssh_host_key_unknown" || got.JumpHostID != "" || got.HostKey == nil || got.HostKey.Host != targetAddress {
		t.Fatalf("target key was not checked independently: %+v", got)
	}
	got = request("jump-only", &HostKeyApproval{Host: got.HostKey.Host, Fingerprint: got.HostKey.Fingerprint})
	if got.status != http.StatusOK || got.ID == "" {
		t.Fatalf("target connection failed: %+v", got)
	}
	defer a.disconnect(got.ID)
	session, err := a.session(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	session.startFileInitialization()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := session.waitFileInitialization(ctx); err != nil {
		t.Fatal(err)
	}
	files, _ := session.fileClientSnapshot()
	if files == nil {
		t.Fatal("no SFTP client")
	}
	if _, err := files.ReadDir("/"); err != nil {
		t.Fatal(err)
	}
	if forwards.Load() != 2 {
		t.Fatalf("expected exactly two connections via the jump, got %d", forwards.Load())
	}
	jump.JumpHostID = target.ID
	if _, err := a.store.Save(jump, false); err == nil {
		t.Fatal("accepted a jump host cycle")
	}
}

type sessionJumpResponse struct {
	status     int
	ID         string
	Code       string
	JumpHostID string
	HostKey    *HostKeyError
}

func TestSSHJumpHostSyncPreservesLocalSelection(t *testing.T) {
	a, b, _ := syncTestPair(t, false)
	jump := syncSaveProfile(t, a, "local jump")
	jump.Host = "jump.fixture.invalid"
	if _, err := a.store.Save(jump, false); err != nil {
		t.Fatal(err)
	}
	target := syncSaveProfile(t, a, "target")
	target.JumpHostID = jump.ID
	if _, err := a.store.Save(target, false); err != nil {
		t.Fatal(err)
	}
	syncCycle(t, a)
	syncCycle(t, b)
	remote, err := b.store.Get(target.ID)
	if err != nil || !remote.NeedsProxy || remote.JumpHostID != "" {
		t.Fatalf("synced target should require local setup: %+v, %v", remote, err)
	}
	remote.Name = "renamed target"
	if _, err := b.store.Save(remote, false); err != nil {
		t.Fatal(err)
	}
	syncCycle(t, b)
	syncCycle(t, a)
	local, err := a.store.Get(target.ID)
	if err != nil || local.Name != remote.Name || local.JumpHostID != jump.ID || local.NeedsProxy {
		t.Fatalf("synced edit did not preserve local jump: %+v, %v", local, err)
	}
}
