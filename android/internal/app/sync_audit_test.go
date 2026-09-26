package app

import (
	"cloudshell/internal/syncserver"
	"cloudshell/internal/syncvault"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type auditSyncHeads struct {
	syncvault.Backend
	objects []syncvault.Object
	data    map[string][]byte
}

type auditCountingWrites struct {
	syncvault.Backend
	writes int
}

func (b *auditCountingWrites) Write(ctx context.Context, o syncvault.Object, data []byte) error {
	b.writes++
	return b.Backend.Write(ctx, o, data)
}
func TestSyncFailedJournalMustPersistBeforeRetryingUpload(t *testing.T) {
	a, _, _ := syncTestPair(t, false)
	syncCycle(t, a)
	b := &auditCountingWrites{Backend: a.syncState.backend}
	a.syncState.backend = b
	syncSaveProfile(t, a, "not yet journaled")
	backup := filepath.Join(a.store.dir, syncConfigName+".bak")
	if e := os.Remove(backup); e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if e := os.Mkdir(backup, 0700); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e := a.synchronize(context.Background(), nil); e == nil {
			t.Fatal("simulated journal failure ignored")
		}
		if b.writes != 0 {
			t.Fatal("upload was attempted without a durable pending journal")
		}
	}
	if e := os.Remove(backup); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, a)
	if b.writes != 1 {
		t.Fatalf("recovery uploads: %d", b.writes)
	}
}

func (b *auditSyncHeads) List(context.Context) ([]syncvault.Object, error) { return b.objects, nil }
func (b *auditSyncHeads) Read(_ context.Context, o syncvault.Object) ([]byte, error) {
	return b.data[o.ID], nil
}

func TestSyncThreeDeviceLatestVersions(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		t.Run(map[bool]string{false: "concurrent latest", true: "already resolved"}[resolved], func(t *testing.T) {
			a, _, _ := syncTestPair(t, false)
			p := syncSaveProfile(t, a, "local old value")
			syncCycle(t, a)
			state := a.syncState.profile
			backend := &auditSyncHeads{Backend: a.syncState.backend, data: map[string][]byte{}}
			localClock := state.Snapshot.Clock[state.Device]
			bID, cID := strings.Repeat("1", 64), strings.Repeat("2", 64)
			for _, id := range []string{bID, cID} {
				snapshot := syncvault.Clone(state.Snapshot)
				snapshot.Device = id
				snapshot.Sequence = 1
				snapshot.Clock[id] = 1
				value := p
				clock := syncvault.Clock{state.Device: localClock, id: 1}
				if id == bID {
					value.Name = "other concurrent value"
					delete(clock, state.Device)
				} else {
					value.Name = "latest remote value"
					if resolved {
						clock[bID] = 1
						snapshot.Clock[bID] = 1
					}
				}
				snapshot.Entries["server/"+p.ID] = syncvault.Entry{Value: syncRaw(value), Clock: clock}
				o, data, e := syncvault.EncodeSnapshot(snapshot, state.Master)
				if e != nil {
					t.Fatal(e)
				}
				backend.objects = append(backend.objects, o)
				backend.data[o.ID] = data
			}
			// Include the previously observed own head, so rollback protection is active.
			for _, o := range state.Seen {
				backend.objects = append(backend.objects, o)
			}
			a.syncState.backend = backend
			err := a.synchronize(context.Background(), nil)
			if resolved {
				if err != nil {
					t.Fatalf("obsolete conflict was retained: %v", err)
				}
				got, _ := a.store.Get(p.ID)
				if got.Name != "latest remote value" {
					t.Fatal(got.Name)
				}
				return
			}
			if err == nil || len(a.syncState.conflicts) != 1 {
				t.Fatalf("missing concurrent conflict: %v", err)
			}
			latest, _ := syncvault.DecodeSnapshot(backend.objects[1], backend.data[backend.objects[1].ID], state.Master, state.Metadata.Vault)
			wanted := choiceID(latest.Entries["server/"+p.ID])
			for _, choice := range a.syncState.conflicts[0].Choices {
				if choice.ID == wanted {
					return
				}
			}
			t.Fatal("latest remote version is absent from conflict choices")
		})
	}
}

type auditBlockingSyncList struct {
	syncvault.Backend
	started chan struct{}
}

func (b *auditBlockingSyncList) List(ctx context.Context) ([]syncvault.Object, error) {
	close(b.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestSyncLockCancelsHistoryRequest(t *testing.T) {
	a, _, _ := syncTestPair(t, false)
	enableTestPassword(t, a, 0)
	b := &auditBlockingSyncList{a.syncState.backend, make(chan struct{})}
	a.syncState.backend = b
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "/api/sync/history", nil).WithContext(ctx)
	r.Header.Set("X-CloudShell-Token", a.Token())
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); a.Handler(os.DirFS("../../mobile/assets/web")).ServeHTTP(w, r) }()
	<-b.started
	if _, e := a.LockNow(); e != nil {
		cancel()
		<-done
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("locking did not cancel an active sync history request")
	}
	if w.Code != http.StatusLocked {
		t.Fatalf("locked request returned %d", w.Code)
	}
	if a.syncState.profile != nil || len(a.syncState.key) != 0 {
		t.Fatal("sync secrets survived lock")
	}
}

func TestSyncDeviceLocalProxyDoesNotCauseEndlessUploads(t *testing.T) {
	a, b, _ := syncTestPair(t, false)
	p := syncSaveProfile(t, a, "proxy fixture")
	p.Proxy = ProxyConfig{Type: "socks5", Host: "127.0.0.1", Port: 1080}
	if _, e := a.store.Save(p, false); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, a)
	syncCycle(t, b)
	local, e := b.store.Get(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	local.Proxy = ProxyConfig{Type: "direct"}
	local.NeedsProxy = false
	if _, e = b.store.Save(local, false); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, b)
	syncCycle(t, a)
	aCount := &auditCountingWrites{Backend: a.syncState.backend}
	a.syncState.backend = aCount
	bCount := &auditCountingWrites{Backend: b.syncState.backend}
	b.syncState.backend = bCount
	for i := 0; i < 3; i++ {
		syncCycle(t, b)
		syncCycle(t, a)
	}
	if aCount.writes != 0 || bCount.writes != 0 {
		t.Fatalf("device-local proxy choices keep causing uploads: A=%d B=%d", aCount.writes, bCount.writes)
	}
	pa, _ := a.store.Get(p.ID)
	pb, _ := b.store.Get(p.ID)
	if pa.Proxy.Type != "socks5" || pb.Proxy.Type != "direct" || pb.NeedsProxy {
		t.Fatal("local proxy preferences changed")
	}
}

func TestSyncPullOnlyMergeEnforcesCombinedRecordLimit(t *testing.T) {
	a, _, _ := syncTestPair(t, false)
	syncCycle(t, a)
	p := a.syncState.profile
	b := &auditSyncHeads{Backend: a.syncState.backend, data: map[string][]byte{}}
	for device := 0; device < 2; device++ {
		id := syncvault.ID()
		snapshot := syncvault.Snapshot{Version: 1, Vault: p.Metadata.Vault, Device: id, Sequence: 1, Clock: syncvault.Clock{id: 1}, Entries: map[string]syncvault.Entry{}}
		for i := 0; i <= syncvault.MaxEntries/2; i++ {
			snapshot.Entries[fmt.Sprintf("server/%d-%048d", device, i)] = syncvault.Entry{Value: json.RawMessage("null"), Clock: snapshot.Clock}
		}
		o, data, e := syncvault.EncodeSnapshot(snapshot, p.Master)
		if e != nil {
			t.Fatal(e)
		}
		b.objects = append(b.objects, o)
		b.data[o.ID] = data
	}
	for _, o := range p.Seen {
		b.objects = append(b.objects, o)
	}
	a.syncState.backend = b
	before := syncRaw(a.store.List())
	if e := a.synchronize(context.Background(), nil); e == nil {
		t.Fatal("oversized pull-only merge accepted")
	}
	if !syncvault.EqualJSON(before, syncRaw(a.store.List())) {
		t.Fatal("rejected merge changed local data")
	}
}

func TestSyncTrashAndTemporaryConnectionsRemainUsable(t *testing.T) {
	a, b, _ := syncTestPair(t, false)
	p := syncSaveProfile(t, a, "trash fixture")
	syncCycle(t, a)
	syncCycle(t, b)
	temporary := Profile{ID: randomID(), Temporary: true, Name: "local quick connection", Host: "fixture.invalid", User: "root", Port: 22, Auth: "password"}
	b.quickProfiles = map[string]quickProfile{temporary.ID: {Profile: temporary}}
	if e := a.store.Delete(p.ID); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, a)
	syncCycle(t, b)
	if cfg := b.configWithQuickProfiles(); len(cfg.Trash) != 1 || len(cfg.TemporaryServers) != 1 {
		t.Fatal("trash or temporary connection lost")
	}
	if _, e := b.store.Restore(p.ID); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, b)
	syncCycle(t, a)
	if _, e := a.store.Get(p.ID); e != nil {
		t.Fatal("restoration was not synchronized", e)
	}
	if e := b.store.Delete(p.ID); e != nil {
		t.Fatal(e)
	}
	if e := b.store.Purge(p.ID); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, b)
	syncCycle(t, a)
	if cfg := a.store.List(); len(cfg.Servers) != 0 || len(cfg.Trash) != 0 {
		t.Fatal("purged connection resurrected")
	}
}

func TestSyncVariantFrontierIndependentOfArrivalOrder(t *testing.T) {
	a, b := syncvault.ID(), syncvault.ID()
	versions := []syncVariant{
		{syncvault.Entry{Value: json.RawMessage(`"old"`), Clock: syncvault.Clock{a: 1}}, "A"},
		{syncvault.Entry{Value: json.RawMessage(`"other"`), Clock: syncvault.Clock{b: 1}}, "B"},
		{syncvault.Entry{Value: json.RawMessage(`"new"`), Clock: syncvault.Clock{a: 2}}, "A"},
	}
	for _, order := range [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		var frontier []syncVariant
		for _, i := range order {
			frontier = addSyncVariant(frontier, versions[i])
		}
		frontier = coalesceSyncVariants(frontier)
		if len(frontier) != 2 {
			t.Fatal(order, len(frontier))
		}
		for _, v := range frontier {
			if string(v.entry.Value) == `"old"` {
				t.Fatal("obsolete choice", order)
			}
		}
	}
}

type auditBarrierList struct {
	syncvault.Backend
	arrived chan<- struct{}
	release <-chan struct{}
}

func (b *auditBarrierList) List(ctx context.Context) ([]syncvault.Object, error) {
	objects, err := b.Backend.List(ctx)
	b.arrived <- struct{}{}
	select {
	case <-b.release:
		return objects, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestSyncThreeLiveDevicesConcurrentEditsConverge(t *testing.T) {
	a, b, _ := syncTestPair(t, false)
	c := syncTestApp(t)
	var invite syncserver.Invited
	if e := a.syncState.backend.(*syncserver.Client).Request(context.Background(), "POST", "/v1/invite", struct{}{}, &invite); e != nil {
		t.Fatal(e)
	}
	code, e := syncvault.Pack(invite)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.joinLocalSync(context.Background(), code, "third", syncTestPassword, ""); e != nil {
		t.Fatal(e)
	}
	p := syncSaveProfile(t, a, "shared initial")
	syncCycle(t, a)
	syncCycle(t, b)
	syncCycle(t, c)
	syncCycle(t, a)
	syncCycle(t, b)
	clients := []*App{a, b, c}
	arrived := make(chan struct{}, len(clients))
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	errors := make(chan error, len(clients))
	for i, client := range clients {
		value, e := client.store.Get(p.ID)
		if e != nil {
			t.Fatal(e)
		}
		value.Name = fmt.Sprintf("concurrent %d", i)
		if _, e = client.store.Save(value, false); e != nil {
			t.Fatal(e)
		}
		client.syncState.backend = &auditBarrierList{client.syncState.backend, arrived, release}
		go func() { errors <- client.synchronize(ctx, nil) }()
	}
	for range clients {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("clients did not reach concurrent merge barrier", ctx.Err())
		}
	}
	close(release)
	for range clients {
		if e := <-errors; e != nil {
			t.Fatal(e)
		}
	}
	for _, client := range clients {
		client.syncState.backend = client.syncState.backend.(*auditBarrierList).Backend
	}
	wanted := choiceID(c.syncState.profile.Snapshot.Entries["server/"+p.ID])
	if e = a.synchronize(context.Background(), nil); e == nil || len(a.syncState.conflicts) != 1 || len(a.syncState.conflicts[0].Choices) != 3 {
		t.Fatalf("three-way conflict missing: %v %+v", e, a.syncState.conflicts)
	}
	if e = a.synchronize(context.Background(), map[string]string{"server/" + p.ID: wanted}); e != nil {
		t.Fatal(e)
	}
	syncCycle(t, b)
	syncCycle(t, c)
	syncCycle(t, a)
	for _, client := range clients {
		value, e := client.store.Get(p.ID)
		if e != nil || value.Name != "concurrent 2" {
			t.Fatal("devices did not converge", e, value.Name)
		}
	}
}

func TestSyncFiveThousandConnectionsRemainStable(t *testing.T) {
	a, b, _ := syncTestPair(t, false)
	group := a.store.List().GroupNodes[0].ID
	a.store.mu.Lock()
	for i := 0; i < 5000; i++ {
		a.store.config.Servers = append(a.store.config.Servers, Profile{ID: fmt.Sprintf("%048x", i+1), Name: fmt.Sprintf("connection %d", i), Host: "fixture.invalid", User: "root", Port: 22, Auth: "password", GroupID: group, Proxy: ProxyConfig{Type: "direct"}})
	}
	err := a.store.writeLocked()
	a.store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	syncCycle(t, a)
	syncCycle(t, b)
	syncCycle(t, a)
	if len(b.store.List().Servers) != 5000 {
		t.Fatal("large sync lost connections")
	}
	counted := &auditCountingWrites{Backend: a.syncState.backend}
	a.syncState.backend = counted
	syncCycle(t, a)
	syncCycle(t, b)
	if counted.writes != 0 {
		t.Fatal("unchanged large configuration uploaded again")
	}
	t.Logf("5000 connections synchronized and stabilized in %s", time.Since(started))
}
