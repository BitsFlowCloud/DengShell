package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProfileNotesPersistenceValidationAndSync(t *testing.T) {
	a, b, _ := syncTestPair(t, false)
	p := syncSaveProfile(t, a, "备注测试")
	notes := "续费：https://example.test/billing\n到期：2027-01-01\n<script>literal text</script>"
	p.Notes = notes
	p, err := a.store.Save(p, false)
	if err != nil || p.Notes != notes {
		t.Fatal("save notes", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(a.store.dir, EncryptedConfigName))
	if err != nil || bytes.Contains(onDisk, []byte("example.test")) {
		t.Fatal("notes must be encrypted", err)
	}
	reopened, err := OpenStore(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(p.ID)
	if err != nil || got.Notes != notes {
		t.Fatal("reopen lost notes", err)
	}
	// The native configuration document round-trip and backup keep the new field.
	raw, _ := json.Marshal(reopened.config)
	var restored Config
	if json.Unmarshal(raw, &restored) != nil || restored.Servers[0].Notes != notes {
		t.Fatal("config round-trip lost notes")
	}
	syncCycle(t, a)
	syncCycle(t, b)
	got, err = b.store.Get(p.ID)
	if err != nil || got.Notes != notes || got.Secret != "" {
		t.Fatal("default sync notes/secret behavior", err)
	}
	got.Notes = ""
	if _, err = b.store.Save(got, false); err != nil {
		t.Fatal(err)
	}
	syncCycle(t, b)
	syncCycle(t, a)
	got, _ = a.store.Get(p.ID)
	if got.Notes != "" {
		t.Fatal("cleared notes returned during sync")
	}
	for _, invalid := range []string{strings.Repeat("字", 21), strings.Repeat("a", 41), "一\n二\n三\n四", "一\r二\r三\r四", "bad\x00note"} {
		got.Notes = invalid
		if _, err = a.store.Save(got, false); err == nil {
			t.Fatal("invalid note accepted")
		}
	}
	got.Notes = strings.Repeat("字", 20) + "\n" + strings.Repeat("a", 40) + "\n" + strings.Repeat("字a", 13) + "a"
	if _, err = a.store.Save(got, false); err != nil {
		t.Fatal("three lines of width 40 must fit", err)
	}
}

func TestProfileNotesEditLimitsAndLegacyPreservation(t *testing.T) {
	for _, value := range []string{"", "一\n二\n三", "一\r\n二\r\n三", strings.Repeat("😀", 20), strings.Repeat("字", 20) + "\n\n", strings.Repeat("字", 10) + strings.Repeat("a", 20), strings.Repeat("\t", 10)} {
		if err := validateEditedProfileNotes(value); err != nil {
			t.Fatalf("valid boundary rejected: %v", err)
		}
	}
	for _, value := range []string{strings.Repeat("😀", 21), "一\r\n二\r\n三\r\n四", "\n\n\n", strings.Repeat("字", 10) + strings.Repeat("a", 21), strings.Repeat("\t", 11), "第一行\n" + strings.Repeat("a", 41)} {
		if validateEditedProfileNotes(value) == nil {
			t.Fatal("invalid boundary accepted")
		}
	}
	a, b, _ := syncTestPair(t, false)
	p := syncSaveProfile(t, a, "旧备注")
	legacy := strings.Repeat("历史资料\r\n", 80)
	// Simulate a profile already saved by an earlier client/import.
	a.store.mu.Lock()
	for i := range a.store.config.Servers {
		if a.store.config.Servers[i].ID == p.ID {
			a.store.config.Servers[i].Notes = legacy
		}
	}
	a.store.mu.Unlock()
	p.Notes = legacy
	p.Name = "仅修改服务器名"
	saved, err := a.store.Save(p, false)
	if err != nil || saved.Notes != legacy {
		t.Fatal("unchanged legacy notes must be preserved", err)
	}
	syncCycle(t, a)
	syncCycle(t, b)
	remote, err := b.store.Get(p.ID)
	if err != nil || remote.Notes != legacy {
		t.Fatal("legacy sync lost notes", err)
	}
	p.Notes = normalizeProfileNoteLines(legacy)
	if _, err = a.store.Save(p, false); err != nil {
		t.Fatal("textarea newline normalization must preserve legacy notes", err)
	}
	p.Notes += "修改"
	if _, err = a.store.Save(p, false); err == nil {
		t.Fatal("edited legacy note bypassed limit")
	}
	p.Notes = "已缩短\n第二行\n第三行"
	if _, err = a.store.Save(p, false); err != nil {
		t.Fatal("shortened legacy note rejected", err)
	}
	// Import validation remains backwards compatible and does not truncate.
	if validateProfileNotes(legacy) != nil {
		t.Fatal("legacy import rejected")
	}
}

func TestFinalShellDescriptionImportsAsNotes(t *testing.T) {
	data := []byte(`{"id":"sample","name":"sample","host":"fixture.invalid","port":22,"user_name":"root","authentication_type":1,"conection_type":100,"description":"到期：2027-01-01\n续费：https://example.test"}`)
	p, _, err := parseFinalShellProfile(data)
	if err != nil || p.Notes != "到期：2027-01-01\n续费：https://example.test" {
		t.Fatal("FinalShell description lost", err)
	}
}

// Opt-in real API/SFTP fixture used by the browser regression script. All
// profiles and remote files live in isolated temporary directories.
func TestNotesHistoryBrowserFixture(t *testing.T) {
	root := os.Getenv("DENG_ISSUES_QA_DIR")
	if root == "" {
		t.Skip("opt-in browser fixture")
	}
	a, err := New(filepath.Join(root, "browser-data"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.mu.Lock(); a.sessions = map[string]*Session{}; a.mu.Unlock(); a.Close() }()
	type fixtureSession struct {
		ID        string `json:"id"`
		ProfileID string `json:"profileId"`
		Home      string `json:"home"`
		File      string `json:"file"`
		Directory string `json:"directory"`
	}
	fixtures := []fixtureSession{}
	for i := 0; i < 2; i++ {
		_, s, remote, _ := uploadSFTPFixture(t, 0)
		p, err := a.store.Save(Profile{Name: fmt.Sprintf("测试服务器-%d", i+1), Host: fmt.Sprintf("192.0.2.%d", i+10), Port: 22, User: "root", Notes: "续费：https://example.test/billing\n到期：2027-01-01"}, false)
		if err != nil {
			t.Fatal(err)
		}
		s.ID = fmt.Sprintf("notes-history-browser-%d", i)
		s.ProfileID = p.ID
		s.fileWriteIdentity = fmt.Sprintf("isolated-browser-identity-%d", i)
		a.sessions[s.ID] = s
		dir := filepath.Join(remote, "etc", "xray")
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "config.json")
		if err = os.WriteFile(file, []byte(fmt.Sprintf("{\"server\":%d}\n", i)), 0600); err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, fixtureSession{s.ID, p.ID, remote, file, dir})
	}
	handler := a.Handler(os.DirFS(filepath.Join("..", "..", "web")))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/updates/check" {
			writeJSON(w, map[string]string{"status": "none"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/stats") || strings.HasSuffix(r.URL.Path, "/network") || strings.HasSuffix(r.URL.Path, "/latency") {
			writeError(w, 400, fmt.Errorf("隔离 SFTP 测试未启用 SSH 监控"))
			return
		}
		handler.ServeHTTP(w, r)
	}))
	a.baseURL = "http://" + server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	data, _ := json.Marshal(map[string]any{"url": a.BrowserURL(), "token": a.Token(), "sessions": fixtures})
	if err = os.WriteFile(filepath.Join(root, "browser-fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(root, "stop-browser")); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("browser fixture timed out")
}

func TestPathHistoryPersistenceIsolationBoundsAndRollback(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"server-a", "server-b"} {
		if _, err := store.saveProfile(Profile{ID: id, Name: id, Host: "fixture.invalid", User: "root"}, false, true); err != nil {
			t.Fatal(err)
		}
	}
	one, two := strings.Repeat("a", 64), strings.Repeat("b", 64)
	put := func(profile, identity, path string) {
		t.Helper()
		if _, err := store.pathHistory(profile, identity, &PathHistoryItem{Path: path, Kind: "folder"}, false); err != nil {
			t.Fatal(err)
		}
	}
	put("server-a", one, "/private/one")
	put("server-b", one, "/private/two")
	put("server-a", two, "/new-host")
	before := store.diskDigest
	put("server-a", one, "/private/one")
	if store.diskDigest != before {
		t.Fatal("repeated directory from a different active tab rewrote config")
	}
	reopened, err := OpenStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := reopened.pathHistory("server-a", one, nil, false)
	if err != nil || len(entries) != 1 || entries[0].Path != "/private/one" {
		t.Fatal("history lost or crossed server/identity", entries, err)
	}
	public, _ := json.Marshal(store.List())
	if bytes.Contains(public, []byte("/private/")) {
		t.Fatal("global config API exposed local path history")
	}
	projection, err := store.syncProjection(false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(projection)
	if bytes.Contains(raw, []byte("/private/")) {
		t.Fatal("local history entered sync")
	}
	if _, err = store.pathHistory("server-a", one, nil, true); err != nil {
		t.Fatal(err)
	}
	if entries, _ = store.pathHistory("server-b", one, nil, false); len(entries) != 1 {
		t.Fatal("clear affected another server")
	}
	if entries, _ = store.pathHistory("server-a", two, nil, false); len(entries) != 1 {
		t.Fatal("clear affected another endpoint")
	}
	for i := 0; i < 205; i++ {
		put("server-a", one, fmt.Sprintf("/visited/%03d", i))
	}
	entries, _ = store.pathHistory("server-a", one, nil, false)
	if len(entries) != 200 || entries[0].Path != "/visited/204" || entries[199].Path != "/visited/005" {
		t.Fatal("per-server eviction/order", len(entries))
	}
	put("server-a", one, "/visited/100")
	entries, _ = store.pathHistory("server-a", one, nil, false)
	if len(entries) != 200 || entries[0].Path != "/visited/100" {
		t.Fatal("duplicate not moved to front")
	}
	for _, p := range []string{"relative", "/bad\x00", "/" + strings.Repeat("a", 4097)} {
		if _, err = store.pathHistory("server-a", one, &PathHistoryItem{Path: p, Kind: "file"}, false); err == nil {
			t.Fatal("invalid path accepted")
		}
	}
	// Another process altering the file must leave the in-memory list unchanged.
	path := filepath.Join(store.dir, EncryptedConfigName)
	data, _ := os.ReadFile(path)
	data[len(data)-1] ^= 1
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pathHistory("server-a", one, nil, true); err == nil {
		t.Fatal("write conflict was ignored")
	}
	after, _ := store.pathHistory("server-a", one, nil, false)
	if len(after) != len(entries) || after[0] != entries[0] {
		t.Fatal("failed clear changed in-memory history")
	}
	large := make([]PathHistoryEntry, 5100)
	for i := range large {
		large[i] = PathHistoryEntry{fmt.Sprintf("p%d", i), one, PathHistoryItem{Path: "/a", Kind: "folder", VisitedAt: time.Now()}}
	}
	if len(boundedPathHistory(large)) != 5000 {
		t.Fatal("global cap missing")
	}
}

func TestPathHistoryConcurrentWindows(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.saveProfile(Profile{ID: "shared", Name: "shared", Host: "fixture.invalid", User: "root"}, false, true); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("c", 64)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.pathHistory("shared", id, &PathHistoryItem{Path: fmt.Sprintf("/item/%d", i), Kind: "file"}, false); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	entries, err := s.pathHistory("shared", id, nil, false)
	if err != nil || len(entries) != 20 {
		t.Fatal("concurrent history was lost", len(entries), err)
	}
}

func TestPathHistoryCannotReturnAfterPermanentDeletion(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "删除回归", Host: "fixture.invalid", User: "root"}, false)
	if err != nil {
		t.Fatal(err)
	}
	identity := strings.Repeat("a", 64)
	entry := &PathHistoryItem{Path: "/private/file", Kind: "file"}
	if _, err = s.pathHistory(p.ID, identity, entry, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Purge(p.ID); err != nil {
		t.Fatal(err)
	}
	// A successful SFTP response may arrive after the profile has been purged.
	if _, err = s.pathHistory(p.ID, identity, entry, false); err == nil {
		t.Fatal("late SFTP completion resurrected permanently deleted path history")
	}
	if len(s.config.PathHistory) != 0 {
		t.Fatal("deleted history returned")
	}
}

func TestPathHistoryOnlySuccessfulSFTPVisits(t *testing.T) {
	a, s, root, _ := uploadSFTPFixture(t, 0)
	var err error
	a.store, err = OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.ProfileID = "history-test"
	if _, err := a.store.saveProfile(Profile{ID: s.ProfileID, Name: "history", Host: "fixture.invalid", User: "root"}, false, true); err != nil {
		t.Fatal(err)
	}
	s.fileWriteIdentity = "local fixture endpoint"
	target := filepath.Join(root, "xray.conf")
	if err = os.WriteFile(target, []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request := func(kind, path, extra string) int {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/sessions/"+s.ID+"/"+kind+"?path="+url.QueryEscape(path)+extra, nil)
		r.SetPathValue("id", s.ID)
		w := httptest.NewRecorder()
		if kind == "files" {
			a.listFiles(w, r)
		} else {
			a.readTextHTTP(w, r)
		}
		return w.Code
	}
	if request("files", root, "&directories=1") != 200 || request("files", filepath.Join(root, "missing"), "") == 200 {
		t.Fatal("fixture directory request")
	}
	entries, _ := a.store.pathHistory(s.ProfileID, s.pathHistoryIdentity(), nil, false)
	if len(entries) != 0 {
		t.Fatal("tree prefetch or failed read entered history")
	}
	if request("files", root, "") != 200 || request("file-content", target, "") != 200 {
		t.Fatal("fixture read")
	}
	if request("file-content", root, "") == 200 {
		t.Fatal("directory read as text")
	}
	entries, _ = a.store.pathHistory(s.ProfileID, s.pathHistoryIdentity(), nil, false)
	// Text reads resolve symlinks; macOS temp directories use /var -> /private/var.
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Path != resolvedTarget || entries[0].Kind != "file" || entries[1].Kind != "folder" {
		t.Fatal("successful visits not recorded", entries)
	}
	mux := http.NewServeMux()
	a.registerPathHistoryHTTP(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions/"+s.ID+"/path-history?q=XRAY", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "xray.conf") || strings.Contains(w.Body.String(), "identity") {
		t.Fatal("search contract", w.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.ctx = ctx
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/sessions/"+s.ID+"/path-history", nil))
	if w.Code == 200 {
		t.Fatal("disconnected session can clear history")
	}
}
