package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/crypto/ssh"
)

func issueRequest(t *testing.T, a *App, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "http://wails.localhost"+path, bytes.NewReader(data))
	req.Header.Set("X-CloudShell-Token", a.Token())
	w := httptest.NewRecorder()
	a.Handler(fstest.MapFS{"index.html": {Data: []byte("fixture")}}).ServeHTTP(w, req)
	return w
}
func TestQuickSSHParsing(t *testing.T) {
	for _, tc := range []struct {
		input, host, user string
		port              int
	}{
		{"alice@example.com", "example.com", "alice", 22}, {"ssh -p 2222 alice@example.com", "example.com", "alice", 2222},
		{"ssh -l admin localhost -p 2022", "localhost", "admin", 2022}, {"root@[2001:db8::1]:2200", "2001:db8::1", "root", 2200}, {"ssh root@2001:db8::1", "2001:db8::1", "root", 22},
	} {
		p, err := parseQuickSSH(tc.input)
		if err != nil || p.Host != tc.host || p.User != tc.user || p.Port != tc.port {
			t.Fatalf("%q: %+v %v", tc.input, p, err)
		}
	}
	for _, input := range []string{"", "ssh", "ssh -p 0 host", "ssh -p 65536 host", "ssh -p abc host", "ssh -p", "ssh -o ProxyCommand=evil host", "ssh a@host;evil", "ssh a@host command", "ssh a@host\nping x", "@host", "a@-host", "ssh a@host:abc", "ping localhost"} {
		if _, err := parseQuickSSH(input); err == nil {
			t.Fatalf("unsafe/invalid connection accepted: %q", input)
		}
	}
}
func TestQuickConnectionOnlyPersistsWhenExplicitlySaved(t *testing.T) {
	a, host, port := authenticationFixture(t, &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) { return nil, nil }})
	before := a.store.List()
	response := issueRequest(t, a, "POST", "/api/quick-connect", map[string]any{"command": "ssh -p " + strconvI(port) + " demo@" + host})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var p Profile
	json.Unmarshal(response.Body.Bytes(), &p)
	if !p.Temporary || p.ID == "" || len(a.store.List().Servers) != len(before.Servers) {
		t.Fatal("temporary connection was saved")
	}
	if issueRequest(t, a, "POST", "/api/quick-connect/"+p.ID+"/save", map[string]string{"name": "too soon"}).Code == 200 {
		t.Fatal("unconnected profile saved")
	}
	session, err := a.Connect(context.Background(), p.ID, "synthetic-only")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Get(p.ID); err == nil {
		t.Fatal("connect implicitly persisted profile")
	}
	if a.configWithQuickProfiles().TemporaryServers[0].Secret != "" {
		t.Fatal("temporary secret exposed")
	}
	response = issueRequest(t, a, "POST", "/api/quick-connect/"+p.ID+"/save", map[string]any{"name": "Saved quick session", "group": "Quick", "remember": false, "secret": "must not persist"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	saved, err := a.store.Get(p.ID)
	if err != nil || saved.Temporary || saved.Name != "Saved quick session" || saved.Secret != "" || session.ProfileID != saved.ID {
		t.Fatal("save changed live identity or leaked credential")
	}
	if len(a.configWithQuickProfiles().TemporaryServers) != 0 {
		t.Fatal("saved profile remains temporary")
	}
	if issueRequest(t, a, "POST", "/api/quick-connect/"+p.ID+"/save", map[string]string{"name": "duplicate"}).Code != 200 || len(a.store.List().Servers) != 1 {
		t.Fatal("save retry not idempotent")
	}
	reopened, err := OpenStore(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := reopened.Get(saved.ID); err != nil || p.Name != saved.Name {
		t.Fatal("saved quick connection missing after restart")
	}
}
func strconvI(n int) string { data, _ := json.Marshal(n); return string(data) }
func TestClearCompletedTransfersPreservesActiveAndRetryable(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	now := time.Now()
	cancelled := false
	a.transfers = map[string]*Transfer{"done": {ID: "done", Status: "done", FinishedAt: now}, "running": {ID: "running", Status: "uploading", cancel: func() { cancelled = true }}, "failed": {ID: "failed", Status: "failed", FinishedAt: now, Retryable: true}, "queued": {ID: "queued", Status: "queued"}, "cancelled": {ID: "cancelled", Status: "cancelled", FinishedAt: now}}
	w := issueRequest(t, a, "POST", "/api/transfers/clear-completed", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var data struct {
		IDs []string `json:"ids"`
	}
	json.Unmarshal(w.Body.Bytes(), &data)
	if !reflect.DeepEqual(data.IDs, []string{"done"}) || cancelled || len(a.transfers) != 4 || !a.transfers["failed"].Retryable {
		t.Fatal("bulk clear changed an unfinished/failed transfer")
	}
}
func TestFinalShellProxyImportsBlockedUntilConfigured(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	dir := t.TempDir()
	raw := finalShellFixture("proxy", 1)
	raw["proxy_id"] = "external-proxy"
	writeFinalShellFixture(t, dir, "with-proxy", raw)
	result, err := a.store.importFinalShell(context.Background(), dir)
	if err != nil || result.Imported != 1 || result.NeedsProxy != 1 {
		t.Fatalf("import: %+v %v", result, err)
	}
	p, _ := a.store.Get(result.Items[0].ProfileID)
	if !p.NeedsProxy {
		t.Fatal("proxy requirement lost")
	}
	if _, err = a.Connect(context.Background(), p.ID, ""); err == nil || !strings.Contains(err.Error(), "代理") {
		t.Fatal("import silently dialed directly")
	}
	p.NeedsProxy = false
	if _, err = a.store.Save(p, false); err != nil {
		t.Fatal(err)
	}
	p, _ = a.store.Get(p.ID)
	if p.NeedsProxy {
		t.Fatal("explicit direct choice did not clear draft")
	}
}

// Optional regression against the user's read-only export: import into an
// isolated Store, report counts only and never connect to exported hosts.
func TestFinalShellProvidedExport(t *testing.T) {
	directory := os.Getenv("DENG_FINALSHELL_EXPORT_QA")
	if directory == "" {
		t.Skip("set DENG_FINALSHELL_EXPORT_QA for private read-only export")
	}
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	filepath.WalkDir(directory, func(name string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() {
			before[name], _ = os.ReadFile(name)
		}
		return e
	})
	result, err := s.importFinalShell(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported == 0 {
		t.Fatalf("imported=%d failed=%d", result.Imported, result.Failed)
	}
	for _, item := range result.Items {
		if item.Status == "failed" && item.Message != "仅支持 FinalShell SSH 连接（类型 100）" {
			t.Fatal("SSH export failed: " + item.Message)
		}
	}
	repeat, err := s.importFinalShell(context.Background(), directory)
	if err != nil || repeat.Imported != 0 || repeat.Skipped != result.Imported {
		t.Fatal("repeated export import duplicated profiles")
	}
	for name, data := range before {
		after, _ := os.ReadFile(name)
		if !bytes.Equal(data, after) {
			t.Fatal("export was changed")
		}
	}
	t.Logf("Imported %d connections; %d require proxy configuration; repeat imported zero; original files unchanged", result.Imported, result.NeedsProxy)
}

func TestLockedWindowSocketExitPreservesOtherWindow(t *testing.T) {
	a, first, address := newRelayFixture(t)
	one := dialRelayTest(t, address)
	second, err := connectLocalSSHFixture(t, a, first.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	two := dialRelayTest(t, strings.Replace(address, first.ID, second.ID, 1))
	enableTestPassword(t, a, 0)
	if _, err = a.LockNow(); err != nil {
		t.Fatal(err)
	}
	one.ws.Close()
	relayWaitClosed(t, first)
	select {
	case <-second.ctx.Done():
		t.Fatal("closing locked window closed another window")
	default:
	}
	if !a.SecurityLockStatus().Locked {
		t.Fatal("closing a window unlocked application")
	}
	if _, err = a.Unlock(LockProof{"password", "1234"}); err != nil {
		t.Fatal(err)
	}
	two.command(t, "printf 'independent_%s\\n' window_ok", "independent_window_ok")
}
