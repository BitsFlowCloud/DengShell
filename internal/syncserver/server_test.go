package syncserver

import (
	"cloudshell/internal/syncvault"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Server, *Client, syncvault.Metadata, []byte) {
	t.Helper()
	s, e := Open(t.TempDir(), "127.0.0.1", 0)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Start("127.0.0.1", 0); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	m, master, e := syncvault.NewMetadataFor(s.Info().Vault, "fixture-sync-password", false)
	if e != nil {
		t.Fatal(e)
	}
	c, e := NewClient(s.Info(), s.Info().Bootstrap)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	d := Join{ID: syncvault.ID(), Name: "owner", Token: syncvault.Encode(syncvault.Random())}
	setup := Setup{Metadata: m, Device: d}
	if e = c.Request(context.Background(), "POST", "/v1/setup", setup, nil); e != nil {
		t.Fatal(e)
	}
	// Retrying a response lost after commit is safe.
	if e = c.Request(context.Background(), "POST", "/v1/setup", setup, nil); e != nil {
		t.Fatal(e)
	}
	c.Token = d.Token
	return s, c, m, master
}
func TestServicePairRevokeTLSAndPersistence(t *testing.T) {
	s, c, m, master := fixture(t)
	ctx := context.Background()
	// The service CA is scoped to this endpoint; the OS trust store is unchanged.
	system := &http.Client{Timeout: time.Second}
	if r, e := system.Get(s.Info().URL + "/v1/meta"); e == nil {
		r.Body.Close()
		t.Fatal("private CA unexpectedly trusted")
	}
	wrong := s.Info()
	other, e := Open(t.TempDir(), "127.0.0.1", 0)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	wrong.CA = other.Info().CA
	bad, _ := NewClient(wrong, c.Token)
	defer bad.Close()
	if _, e = bad.Metadata(ctx); e == nil {
		t.Fatal("wrong CA accepted")
	}
	var invite Invited
	if e = c.Request(ctx, "POST", "/v1/invite", struct{}{}, &invite); e != nil {
		t.Fatal(e)
	}
	if _, e = invite.Metadata.Unlock("incorrect-password", ""); e == nil {
		t.Fatal("incorrect password")
	}
	if _, e = invite.Metadata.Unlock("", syncvault.Encode(master)); e != nil {
		t.Fatal(e)
	}
	peer, _ := NewClient(invite.Connection, "")
	defer peer.Close()
	join := Join{ID: syncvault.ID(), Name: "peer", Token: syncvault.Encode(syncvault.Random()), Invite: invite.Invite}
	if e = peer.Request(ctx, "POST", "/v1/join", join, nil); e != nil {
		t.Fatal(e)
	}
	if e = peer.Request(ctx, "POST", "/v1/join", join, nil); e != nil {
		t.Fatal("retry failed", e)
	}
	replay := join
	replay.ID = syncvault.ID()
	replay.Token = syncvault.Encode(syncvault.Random())
	if e = peer.Request(ctx, "POST", "/v1/join", replay, nil); e == nil {
		t.Fatal("invitation replay")
	}
	peer.Token = join.Token
	snap := syncvault.Snapshot{Version: 1, Vault: m.Vault, Device: join.ID, Sequence: 1, Clock: syncvault.Clock{join.ID: 1}, Entries: map[string]syncvault.Entry{"server/abcdefgh": {Value: json.RawMessage(`"fixture secret"`), Clock: syncvault.Clock{join.ID: 1}}}}
	o, data, e := syncvault.EncodeSnapshot(snap, master)
	if e != nil {
		t.Fatal(e)
	}
	if e = peer.Write(ctx, o, data); e != nil {
		t.Fatal(e)
	}
	if e = peer.Write(ctx, o, data); e != nil {
		t.Fatal(e)
	}
	got, e := c.Read(ctx, o)
	if e != nil || strings.Contains(string(got), "fixture secret") {
		t.Fatal(e)
	}
	if e = c.Request(ctx, "POST", "/v1/revoke", map[string]string{"id": join.ID}, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = peer.List(ctx); e == nil {
		t.Fatal("revoked token accepted")
	}
	if heads, e := c.List(ctx); e != nil || len(heads) != 1 {
		t.Fatal("revocation destroyed data", e)
	}
	req, _ := http.NewRequest("GET", s.Info().URL+"/v1/meta", nil)
	req.Header.Set("Origin", "http://malicious.invalid")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	r, e := c.HTTP.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal("browser origin accepted", r.StatusCode)
	}
}
func TestUninitializedRestartAndOwnerRecovery(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir, "127.0.0.1", 0)
	if e != nil {
		t.Fatal(e)
	}
	old := s.Info()
	s.Close()
	s, e = Open(dir, "127.0.0.1", 0)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Info().Bootstrap == "" || s.Info().Bootstrap == old.Bootstrap || s.Info().CA != old.CA || s.Info().Vault != old.Vault {
		t.Fatal("restart lost identity or bootstrap")
	}
	server, c, _, master := fixture(t)
	join := Join{ID: syncvault.ID(), Name: "recovered owner", Token: syncvault.Encode(syncvault.Random())}
	if e = server.RestoreOwner(join, syncvault.Random()); e == nil {
		t.Fatal("bad recovery")
	}
	if e = server.RestoreOwner(join, master); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Metadata(context.Background()); e == nil {
		t.Fatal("old owner credential survived recovery")
	}
	c.Token = join.Token
	if _, e = c.Metadata(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestWindowsFileURIAndExclusiveServerDirectory(t *testing.T) {
	u := sqliteFileURI("C:/Users/test 用户/#sync?/sync.db")
	if u.Host != "" || u.Path != "/C:/Users/test 用户/#sync?/sync.db" || !strings.HasPrefix(u.String(), "file:///C:/") || strings.Contains(u.String(), "#") {
		t.Fatal("invalid Windows SQLite URI", u.String())
	}
	dir := t.TempDir()
	one, e := Open(dir, "127.0.0.1", 0)
	if e != nil {
		t.Fatal(e)
	}
	if two, e := Open(dir, "127.0.0.1", 0); e == nil {
		two.Close()
		t.Fatal("concurrent services accepted same data directory")
	}
	one.Close()
	next, e := Open(dir, "127.0.0.1", 0)
	if e != nil {
		t.Fatal("service lease did not release", e)
	}
	next.Close()
}

func TestSnapshotRetryRejectsChangedIdentity(t *testing.T) {
	s, c, _, _ := fixture(t)
	device, e := s.authenticate(c.Token)
	if e != nil {
		t.Fatal(e)
	}
	data := []byte(strings.Repeat("ciphertext fixture", 4))
	o := syncvault.Object{Device: device.ID, Sequence: 1, Hash: syncvault.Hash(data), Size: int64(len(data))}
	if e = c.Write(context.Background(), o, data); e != nil {
		t.Fatal(e)
	}
	o.Sequence = 2
	if e = c.Write(context.Background(), o, data); e == nil {
		t.Fatal("same ciphertext was acknowledged as a different snapshot sequence")
	}
}

func TestHistoryQuotaRetainsCurrentHeadsAndAllowsFurtherSync(t *testing.T) {
	s, c, _, _ := fixture(t)
	s.maxStorageBytes = 1000
	d, e := s.authenticate(c.Token)
	if e != nil {
		t.Fatal(e)
	}
	for seq := uint64(1); seq <= 25; seq++ {
		data := []byte(fmt.Sprintf("%010d%s", seq, strings.Repeat("encrypted-data", 20)))
		o := syncvault.Object{Device: d.ID, Sequence: seq, Hash: syncvault.Hash(data), Size: int64(len(data))}
		if e = c.Write(context.Background(), o, data); e != nil {
			t.Fatalf("history blocked new snapshot %d: %v", seq, e)
		}
	}
	objects, e := c.List(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	heads, e := syncvault.Heads(objects)
	if e != nil || len(heads) != 1 || heads[0].Sequence != 25 {
		t.Fatal(heads, e)
	}
	if len(objects) > 20 || len(objects) < 2 {
		t.Fatalf("incorrect history retention: %d", len(objects))
	}
	before := mustJSON(objects)
	s.maxStorageBytes = 40
	data := []byte(strings.Repeat("would not fit", 8))
	o := syncvault.Object{Device: d.ID, Sequence: 26, Hash: syncvault.Hash(data), Size: int64(len(data))}
	if e = c.Write(context.Background(), o, data); e == nil {
		t.Fatal("current head capacity limit ignored")
	}
	after, e := c.List(context.Background())
	if e != nil || !syncvault.EqualJSON(before, mustJSON(after)) {
		t.Fatal("failed upload pruned existing history", e)
	}
}
