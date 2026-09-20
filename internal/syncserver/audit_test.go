package syncserver

import (
	"cloudshell/internal/syncvault"
	"context"
	"crypto/tls"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type signaledReader struct {
	io.Reader
	once    sync.Once
	started chan struct{}
}

func (r *signaledReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.Reader.Read(p)
}
func TestSlowJoinDoesNotBlockAuthenticatedRequests(t *testing.T) {
	s, c, _, _ := fixture(t)
	reader, writer := io.Pipe()
	body := &signaledReader{Reader: reader, started: make(chan struct{})}
	r := httptest.NewRequest("POST", "/v1/join", body)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); s.ServeHTTP(w, r) }()
	<-body.started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, err := c.Metadata(ctx)
	cancel()
	writer.Close()
	reader.Close()
	<-done
	if err != nil {
		t.Fatal("an incomplete unauthenticated body blocked other devices", err)
	}
}

func TestInitializedRestartPreservesSnapshotsAndCapabilities(t *testing.T) {
	s, c, _, _ := fixture(t)
	d, e := s.authenticate(c.Token)
	if e != nil {
		t.Fatal(e)
	}
	data := []byte(strings.Repeat("opaque fixture", 8))
	o := syncvault.Object{Device: d.ID, Sequence: 1, Hash: syncvault.Hash(data), Size: int64(len(data))}
	if e = c.Write(context.Background(), o, data); e != nil {
		t.Fatal(e)
	}
	info := s.Info()
	dir := s.cert.dir
	s.Close()
	next, e := Open(dir, "127.0.0.1", 0)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	if e = next.Start("127.0.0.1", 0); e != nil {
		t.Fatal(e)
	}
	if next.Info().CA != info.CA || next.Info().Vault != info.Vault || next.Info().Bootstrap != "" {
		t.Fatal("initialized restart changed identity")
	}
	c.Connection.URL = next.Info().URL
	got, e := c.Read(context.Background(), syncvault.Object{ID: o.Hash})
	if e != nil || string(got) != string(data) {
		t.Fatal("restart lost data or authorization", e)
	}
}

func TestMissingIdentityIsNeverSilentlyReplaced(t *testing.T) {
	s, _, _, _ := fixture(t)
	dir := s.cert.dir
	s.Close()
	path := filepath.Join(dir, "identity.pem")
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if next, e := Open(dir, "127.0.0.1", 0); e == nil {
		next.Close()
		t.Fatal("missing identity was silently regenerated")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("failed open wrote a new identity", e)
	}
}

func TestLeafRotationAndRootExpiry(t *testing.T) {
	c, e := openCertificates(t.TempDir(), "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	old, _ := c.get(nil)
	c.leaf.Leaf.NotAfter = time.Now().Add(20 * 24 * time.Hour)
	rotated, e := c.get(nil)
	if e != nil || rotated == old {
		t.Fatal("leaf did not rotate", e)
	}
	c.root.NotAfter = time.Now().Add(10 * 24 * time.Hour).Truncate(time.Second)
	c.leaf = nil
	last, e := c.get(nil)
	if e != nil {
		t.Fatal(e)
	}
	again, e := c.get(nil)
	if e != nil || last != again {
		t.Fatal("root expiry caused rotation on every handshake", e)
	}
	c.root.NotAfter = time.Now().Add(-time.Second)
	if _, e = c.get(nil); e == nil {
		t.Fatal("expired identity remained usable")
	}
}

func TestInvitationsExpireAndUnprivilegedDevicesCannotManage(t *testing.T) {
	s, owner, _, _ := fixture(t)
	var invite Invited
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if e := owner.Request(ctx, "POST", "/v1/invite", struct{}{}, &invite); e != nil {
			t.Fatal(e)
		}
	}
	if e := owner.Request(ctx, "POST", "/v1/invite", struct{}{}, &invite); e == nil {
		t.Fatal("pending invitation limit ignored")
	}
	s.mu.Lock()
	_, e := s.db.Exec("UPDATE invites SET expires=?", time.Now().Add(-time.Minute).Unix())
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	peer, e := NewClient(invite.Connection, "")
	if e != nil {
		t.Fatal(e)
	}
	defer peer.Close()
	join := Join{ID: syncvault.ID(), Name: "unprivileged fixture", Token: syncvault.Encode(syncvault.Random()), Invite: invite.Invite}
	if e = peer.Request(ctx, "POST", "/v1/join", join, nil); e == nil {
		t.Fatal("expired invitation accepted")
	}
	if e = owner.Request(ctx, "POST", "/v1/invite", struct{}{}, &invite); e != nil {
		t.Fatal("expired invites not cleared", e)
	}
	join.Invite = invite.Invite
	if e = peer.Request(ctx, "POST", "/v1/join", join, nil); e != nil {
		t.Fatal(e)
	}
	peer.Token = join.Token
	for _, path := range []string{"/v1/invite", "/v1/revoke", "/api/config", "/api/profiles"} {
		if e = peer.Request(ctx, "POST", path, map[string]string{"id": join.ID}, nil); e == nil {
			t.Fatal("unprivileged management route accepted", path)
		}
	}
}

func TestClientRejectsTLSFallbackHostnameMismatchAndRedirect(t *testing.T) {
	s, c, _, _ := fixture(t)
	for _, mode := range []string{"old TLS", "wrong hostname"} {
		t.Run(mode, func(t *testing.T) {
			client, e := NewClient(s.Info(), c.Token)
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			transport := client.HTTP.Transport.(*http.Transport)
			if mode == "old TLS" {
				transport.TLSClientConfig.MinVersion = tls.VersionTLS12
				transport.TLSClientConfig.MaxVersion = tls.VersionTLS12
			} else {
				transport.TLSClientConfig.ServerName = "127.0.0.2"
			}
			if _, e = client.Metadata(context.Background()); e == nil {
				t.Fatal("untrusted connection accepted")
			}
		})
	}
	targetCalled := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalled <- struct{}{} }))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: redirect.Certificate().Raw}))
	client, e := NewClient(syncvault.Connection{Version: 1, URL: redirect.URL, CA: ca, Vault: syncvault.ID()}, "fixture-token")
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	if _, e = client.Metadata(context.Background()); e == nil {
		t.Fatal("redirect accepted")
	}
	select {
	case <-targetCalled:
		t.Fatal("credential request followed redirect")
	default:
	}
}
