package app

import (
	"bytes"
	"cloudshell/internal/syncserver"
	"cloudshell/internal/syncvault"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const syncTestPassword = "test-only-sync-passphrase"

func TestSyncRemovedProviderRoutesAreUnavailable(t *testing.T) {
	a := syncTestApp(t)
	mux := http.NewServeMux()
	a.registerSyncHTTP(mux)
	for _, route := range []string{"config", "start", "cancel", "finish", "reconnect"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/sync/google/"+route, strings.NewReader("{}")))
		if w.Code != http.StatusNotFound {
			t.Fatalf("removed route %s returned %d", route, w.Code)
		}
	}
	if bytes.Contains(syncRaw(a.syncStatus()), []byte("google")) {
		t.Fatal("removed provider still exposed in status")
	}
	if e := a.useSyncProfileLocked(&syncProfile{Provider: "google"}, syncTestPassword); e == nil {
		t.Fatal("removed provider can still be configured")
	}
}

func TestSyncRemovedProviderPreservesExistingData(t *testing.T) {
	a, _, _ := syncTestPair(t, false)
	server := syncSaveProfile(t, a, "preserved connection")
	s := &a.syncState
	// Emulate an older encrypted profile with now-unknown credential fields.
	var legacy map[string]any
	if e := json.Unmarshal(syncRaw(s.profile), &legacy); e != nil {
		t.Fatal(e)
	}
	legacy["provider"] = "google"
	legacy["google"] = map[string]string{"clientId": "old.apps.googleusercontent.com"}
	legacy["tokens"] = map[string]string{"access_token": "isolated-fixture-token"}
	ciphertext, e := syncvault.Protect(s.key, s.salt, syncRaw(legacy))
	if e != nil {
		t.Fatal(e)
	}
	a.pauseSyncLocked()
	path := filepath.Join(a.store.dir, syncConfigName)
	if e = atomicConfigFile(path, ciphertext); e != nil {
		t.Fatal(e)
	}
	if e = a.unlockSync(syncTestPassword); e == nil || !strings.Contains(e.Error(), "已停用") {
		t.Fatal("legacy profile was not safely rejected", e)
	}
	if saved, e := os.ReadFile(path); e != nil || !bytes.Equal(saved, ciphertext) {
		t.Fatal("legacy profile was modified", e)
	}
	if s.profile != nil || s.backend != nil || len(s.key) != 0 {
		t.Fatal("legacy provider activated")
	}
	if p, e := a.store.Get(server.ID); e != nil || p.Name != server.Name {
		t.Fatal("local connection changed", e)
	}
	if e = a.disconnectSync(); e != nil {
		t.Fatal(e)
	}
	backups, e := filepath.Glob(filepath.Join(a.store.dir, "dengshell.sync.disconnected-*.enc"))
	if e != nil || len(backups) != 1 {
		t.Fatal("disconnect did not archive legacy profile", e)
	}
	if saved, e := os.ReadFile(backups[0]); e != nil || !bytes.Equal(saved, ciphertext) {
		t.Fatal("archived legacy profile differs", e)
	}
	if a.syncStatus().Configured {
		t.Fatal("disconnect did not allow new self-hosted setup")
	}
}

func syncTestApp(t *testing.T) *App {
	t.Helper()
	a, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	// Deterministic tests explicitly drive cycles, without the 30-second worker.
	a.cancel()
	a.syncState.wg.Wait()
	t.Cleanup(a.Close)
	return a
}
func syncTestPair(t *testing.T, secrets bool) (*App, *App, *syncserver.Server) {
	t.Helper()
	ctx := context.Background()
	host, e := syncserver.Open(t.TempDir(), "127.0.0.1", 0)
	if e != nil {
		t.Fatal(e)
	}
	if e = host.Start("127.0.0.1", 0); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(host.Close)
	owner, peer := syncTestApp(t), syncTestApp(t)
	info := host.Info()
	m, master, e := syncvault.NewMetadataFor(info.Vault, syncTestPassword, secrets)
	if e != nil {
		t.Fatal(e)
	}
	id, token := syncvault.ID(), syncvault.Encode(syncvault.Random())
	c, _ := syncserver.NewClient(info, info.Bootstrap)
	defer c.Close()
	if e = c.Request(ctx, "POST", "/v1/setup", syncserver.Setup{Metadata: m, Device: syncserver.Join{ID: id, Name: "owner", Token: token}}, nil); e != nil {
		t.Fatal(e)
	}
	info.Bootstrap = ""
	owner.syncState.mu.Lock()
	e = owner.useSyncProfileLocked(&syncProfile{Provider: "local", Name: "owner", Device: id, Token: token, Connection: info, Owner: true, Metadata: m, Master: master}, syncTestPassword)
	owner.syncState.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	c.Token = token
	var invite syncserver.Invited
	if e = c.Request(ctx, "POST", "/v1/invite", struct{}{}, &invite); e != nil {
		t.Fatal(e)
	}
	code, _ := syncvault.Pack(invite)
	if _, e = peer.joinLocalSync(ctx, code, "peer", "wrong-sync-password", ""); e == nil {
		t.Fatal("wrong sync password accepted")
	}
	if _, e = peer.joinLocalSync(ctx, code, "peer", syncTestPassword, ""); e != nil {
		t.Fatal("wrong password consumed invitation", e)
	}
	return owner, peer, host
}
func syncCycle(t *testing.T, a *App) {
	t.Helper()
	if e := a.synchronize(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
}
func syncSaveProfile(t *testing.T, a *App, name string) Profile {
	t.Helper()
	p, e := a.store.Save(Profile{Name: name, Host: "fixture.invalid", Port: 22, User: "root", Auth: "password", Secret: "private-test-password", Proxy: ProxyConfig{Type: "direct"}}, false)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestSyncTwoDevicesMergeConflictDeleteAndExclusions(t *testing.T) {
	a, b, _ := syncTestPair(t, false)
	p := syncSaveProfile(t, a, "first")
	syncCycle(t, a)
	syncCycle(t, b)
	received, e := b.store.Get(p.ID)
	if e != nil || received.Secret != "" {
		t.Fatal("default secret exclusion", e)
	}
	received.Name = "B edit"
	if _, e = b.store.Save(received, false); e != nil {
		t.Fatal(e)
	}
	local, _ := a.store.Get(p.ID)
	local.Name = "A edit"
	if _, e = a.store.Save(local, false); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, a)
	if e = b.synchronize(context.Background(), nil); e == nil {
		t.Fatal("concurrent edit silently overwritten")
	}
	if got, _ := b.store.Get(p.ID); got.Name != "B edit" {
		t.Fatal("conflict overwrote local")
	}
	cs := b.syncStatus().Conflicts
	if len(cs) != 1 {
		t.Fatal(cs)
	}
	if e = b.synchronize(context.Background(), map[string]string{cs[0].Key: cs[0].Choices[0].ID}); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, a)
	if got, _ := a.store.Get(p.ID); got.Name != "B edit" || got.Secret != "private-test-password" {
		t.Fatal("resolution/secret preservation", got.Name)
	}
	// Hard deletion is a tombstone; reconnecting an unchanged stale device must
	// not recreate the server. Other groups and commands continue to merge.
	a.store.mu.Lock()
	a.store.config.Servers = nil
	e = a.store.writeLocked()
	a.store.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	syncCycle(t, a)
	syncCycle(t, b)
	syncCycle(t, a)
	if len(b.store.List().Servers) != 0 {
		t.Fatal("deleted server resurrected")
	}
	if _, e = a.store.SaveCommand(QuickCommand{Name: "A cmd", Body: "uname -a", Color: "blue"}); e != nil {
		t.Fatal(e)
	}
	if _, e = b.store.SaveCommand(QuickCommand{Name: "B cmd", Body: "uptime", Color: "blue"}); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, a)
	syncCycle(t, b)
	syncCycle(t, a)
	if len(a.store.List().Commands) != 2 || len(b.store.List().Commands) != 2 {
		t.Fatal("independent changes lost")
	}
	config, e := os.ReadFile(filepath.Join(a.store.dir, syncConfigName))
	if e != nil || bytes.Contains(config, []byte("private-test-password")) || bytes.Contains(config, []byte(a.syncState.profile.Token)) {
		t.Fatal("plaintext sync credential persisted", e)
	}
}

type failingSyncBackend struct {
	syncvault.Backend
	fail bool
}

func (f *failingSyncBackend) Write(ctx context.Context, o syncvault.Object, b []byte) error {
	if e := f.Backend.Write(ctx, o, b); e != nil {
		return e
	}
	if f.fail {
		f.fail = false
		return errors.New("simulated response lost after remote commit")
	}
	return nil
}
func TestSyncInterruptedWriteRetryRollbackAndLock(t *testing.T) {
	a, b, _ := syncTestPair(t, true)
	p := syncSaveProfile(t, a, "fixture")
	a.syncState.backend = &failingSyncBackend{a.syncState.backend, true}
	if e := a.synchronize(context.Background(), nil); e == nil || a.syncState.profile.Pending == nil {
		t.Fatal("missing durable retry")
	}
	a.syncState.mu.Lock()
	a.pauseSyncLocked()
	a.syncState.mu.Unlock()
	if e := a.unlockSync(syncTestPassword); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, a)
	syncCycle(t, b)
	if got, e := b.store.Get(p.ID); e != nil || got.Secret != "private-test-password" {
		t.Fatal("pending retry lost data", e)
	}
	original := a.syncState.backend
	// A missing previously acknowledged remote head must not erase local data.
	a.syncState.backend = &emptyListSyncBackend{original}
	if e := a.synchronize(context.Background(), nil); e == nil {
		t.Fatal("rollback accepted")
	}
	if _, e := a.store.Get(p.ID); e != nil {
		t.Fatal("rollback erased local data")
	}
	a.syncState.backend = original
	enableTestPassword(t, a, 0)
	if _, e := a.LockNow(); e != nil {
		t.Fatal(e)
	}
	if a.syncState.profile != nil || a.syncState.key != nil {
		t.Fatal("lock retained sync key")
	}
	if e := a.synchronize(context.Background(), nil); e == nil {
		t.Fatal("locked sync accepted")
	}
}

type emptyListSyncBackend struct{ syncvault.Backend }

func (b *emptyListSyncBackend) List(context.Context) ([]syncvault.Object, error) { return nil, nil }
func TestSyncInvalidRemoteGraphDoesNotPublishOrApply(t *testing.T) {
	a, b, _ := syncTestPair(t, false)
	p := syncSaveProfile(t, a, "fixture")
	syncCycle(t, a)
	syncCycle(t, b)
	before := b.store.List()
	local, _ := b.store.syncProjection(false)
	bad := map[string]json.RawMessage{}
	for k, v := range local {
		bad[k] = v
	}
	bad["server/"+p.ID] = syncRaw(Profile{ID: p.ID, Name: "invalid", Host: "bad host", User: "root", Port: 22, Auth: "password"})
	if e := b.store.applySync(local, bad, false); e == nil {
		t.Fatal("invalid record accepted")
	}
	if !syncvault.EqualJSON(syncRaw(before), syncRaw(b.store.List())) {
		t.Fatal("invalid graph partially applied")
	}
	// Credentials copied into a second live process cannot overwrite the first.
	raw, _ := os.ReadFile(filepath.Join(b.store.dir, syncConfigName))
	if e := atomicConfigFile(filepath.Join(b.store.dir, syncConfigName), append(raw, ' ')); e != nil {
		t.Fatal(e)
	}
	if e := b.synchronize(context.Background(), nil); e == nil {
		t.Fatal("concurrent config overwrite accepted")
	}
}

func TestSyncManagedPrivateKeysAndHistoryRestore(t *testing.T) {
	a, b, _ := syncTestPair(t, true)
	key, e := a.store.SaveKey(KeyInput{Name: "isolated generated test key", Generate: true})
	if e != nil {
		t.Fatal(e)
	}
	p, e := a.store.Save(Profile{Name: "key test", Host: "fixture.invalid", User: "root", Port: 22, Auth: "key", KeyID: key.ID, Proxy: ProxyConfig{Type: "direct"}}, false)
	if e != nil {
		t.Fatal(e)
	}
	syncCycle(t, a)
	syncCycle(t, b)
	original, e := a.store.KeyData(key.ID)
	if e != nil {
		t.Fatal(e)
	}
	copied, e := b.store.KeyData(key.ID)
	if e != nil || !bytes.Equal(original, copied) {
		t.Fatal("private key mismatch", e)
	}
	objects, e := a.syncState.backend.List(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	old := ""
	for _, o := range objects {
		if o.Device == a.syncState.profile.Device {
			old = o.ID
		}
	}
	p.Name = "renamed"
	if _, e = a.store.Save(p, false); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, a)
	handler := a.Handler(os.DirFS("../../web"))
	body, _ := json.Marshal(map[string]string{"id": old})
	r := httptest.NewRequest("POST", "/api/sync/restore", bytes.NewReader(body))
	r.Header.Set("X-CloudShell-Token", a.Token())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if got, _ := a.store.Get(p.ID); got.Name != "key test" {
		t.Fatal("history not restored")
	}
	syncCycle(t, a)
	syncCycle(t, b)
	if got, _ := b.store.Get(p.ID); got.Name != "key test" {
		t.Fatal("restored value did not sync")
	}
	files, e := os.ReadDir(filepath.Join(a.store.dir, "sync-backups"))
	if e != nil || len(files) == 0 {
		t.Fatal("missing pre-restore backup", e)
	}
	enableTestPassword(t, a, 0)
	a.LockNow()
	for _, path := range []string{"/api/sync/history", "/api/sync/status", "/api/sync/devices"} {
		r = httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-CloudShell-Token", a.Token())
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 423 {
			t.Fatal("locked sync route accessible", path, w.Code)
		}
	}
}
func TestSyncIndependentSameNameGroupsArePreserved(t *testing.T) {
	a, b, _ := syncTestPair(t, false)
	pa := syncSaveProfile(t, a, "A")
	pb := syncSaveProfile(t, b, "B")
	syncCycle(t, a)
	syncCycle(t, b)
	syncCycle(t, a)
	for _, client := range []*App{a, b} {
		cfg := client.store.List()
		if len(cfg.Servers) != 2 || len(cfg.GroupNodes) != 2 {
			t.Fatal("separate groups lost", cfg.GroupNodes)
		}
		if e := validateGroupGraph(cfg.GroupNodes); e != nil {
			t.Fatal(e)
		}
		for _, id := range []string{pa.ID, pb.ID} {
			if _, e := client.store.Get(id); e != nil {
				t.Fatal(e)
			}
		}
	}
}

func TestSyncRejectWindowsAlternateStreamsAndPathNames(t *testing.T) {
	for _, id := range []string{"existing:stream", "../outsidekey", "key\\outside", "key?wildcard", "trailing-space ", "bad|pipe"} {
		if safeSyncID(id) {
			t.Fatal("unsafe cross-platform identifier", id)
		}
	}
	if !safeSyncID(randomID()) {
		t.Fatal("ordinary generated identifier rejected")
	}
}
