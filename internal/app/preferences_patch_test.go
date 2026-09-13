package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func preferencePatch(t *testing.T, input string) map[string]json.RawMessage {
	t.Helper()
	var result map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAppearancePatchPreservesOtherWindowsAndDeletesOneMapKey(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveWindowState(1200, 800, true); err != nil {
		t.Fatal(err)
	}
	for _, patch := range []string{
		`{"theme":"dark","terminalFontSize":14.5,"fontColors":{"builtin:fira-code":"#ABCDEF"},"fontBold":{"builtin:fira-code":true},"layout":{"dengshell.nic.first":"eth0"}}`,
		`{"backgroundOpacity":0.6,"fontColors":{"builtin:jetbrains-mono":"#123456"},"fontBold":{"builtin:jetbrains-mono":false},"layout":{"dengshell.nic.second":"eth1"}}`,
		`{"fontColors":{"builtin:fira-code":null},"fontBold":{"builtin:fira-code":null},"layout":{"dengshell.nic.first":null}}`,
	} {
		if _, err = s.PatchAppearance(preferencePatch(t, patch)); err != nil {
			t.Fatal(err)
		}
	}
	s, err = OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s.List().Appearance
	if got.Theme != "dark" || got.TerminalFontSize != 14.5 || got.BackgroundOpacity != .6 || got.WindowWidth != 1200 || got.WindowHeight != 800 || !got.WindowMaximised {
		t.Fatalf("unrelated fields overwritten: %+v", got)
	}
	if len(got.FontColors) != 1 || got.FontColors["builtin:jetbrains-mono"] != "#123456" || len(got.FontBold) != 1 {
		t.Fatal("map siblings were removed")
	}
	if _, ok := got.FontBold["builtin:jetbrains-mono"]; !ok {
		t.Fatal("explicit false map value was lost")
	}
	if len(got.Layout) != 1 || string(got.Layout["dengshell.nic.second"]) != `"eth1"` {
		t.Fatal("unrelated history was removed")
	}
}

func TestAppearancePatchConcurrentMergesAreAtomic(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const count = 24
	start := make(chan struct{})
	failures := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			raw := fmt.Sprintf(`{"fontColors":{"builtin:test-%d":"#aabbcc"},"fontBold":{"builtin:test-%d":true},"layout":{"dengshell.nic.window%d":"eth%d"}}`, i, i, i, i)
			var patch map[string]json.RawMessage
			_ = json.Unmarshal([]byte(raw), &patch)
			if _, err := s.PatchAppearance(patch); err != nil {
				failures <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	s, err = OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	a := s.List().Appearance
	if len(a.FontColors) != count || len(a.FontBold) != count || len(a.Layout) != count {
		t.Fatalf("concurrent updates lost: colors=%d bold=%d layout=%d", len(a.FontColors), len(a.FontBold), len(a.Layout))
	}
}

func TestAppearancePatchInvalidAndFailedWritesAreAtomic(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := s.List().Appearance
	for _, raw := range []string{`null`, `{"unknown":true}`, `{"theme":null}`, `{"windowWidth":300}`, `{"fontBold":null}`, `{"fontColors":[]}`, `{"fontBold":{"builtin:fira-code":"yes"}}`, `{"theme":"dark","fontColors":{"builtin:fira-code":"red"}}`, `{"layout":{"unexpected-key":[]}}`, `{"terminalFontSize":14.25}`} {
		if _, err = s.PatchAppearance(preferencePatch(t, raw)); err == nil {
			t.Fatalf("invalid patch accepted: %s", raw)
		}
		if !reflect.DeepEqual(s.List().Appearance, original) {
			t.Fatalf("partial invalid patch applied: %s", raw)
		}
	}
	if err = os.WriteFile(filepath.Join(s.dir, ConfigKeyName), bytes.Repeat([]byte{1}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PatchAppearance(preferencePatch(t, `{"theme":"dark","fontBold":{"builtin:fira-code":true}}`)); err == nil {
		t.Fatal("write failure ignored")
	}
	if !reflect.DeepEqual(s.List().Appearance, original) {
		t.Fatal("failed disk write leaked into live settings")
	}
}

func TestAppearancePatchHTTPAndLegacyAPI(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	handler := a.Handler(fstest.MapFS{"index.html": {Data: []byte("fixture")}})
	request := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://wails.localhost"+path, strings.NewReader(body))
		req.Header.Set("X-CloudShell-Token", a.Token())
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		return r
	}
	if r := request("/api/appearance/patch", `{"theme":"dark"}`); r.Code != 200 {
		t.Fatalf("patch route: %d %s", r.Code, r.Body)
	}
	if r := request("/api/appearance/patch", `{"notASetting":true}`); r.Code == 200 {
		t.Fatal("unknown field accepted")
	}
	legacy := a.store.List().Appearance
	legacy.Theme = "light"
	body, _ := json.Marshal(legacy)
	if r := request("/api/appearance", string(body)); r.Code != 200 || a.store.List().Appearance.Theme != "light" {
		t.Fatal("legacy appearance endpoint broken")
	}
}
