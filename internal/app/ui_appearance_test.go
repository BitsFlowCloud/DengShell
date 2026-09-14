package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestUIFontRestartBoundaryAndDeletion(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	font, err := os.ReadFile(filepath.Join("..", "..", "web", "assets", "fonts", "jetbrains-mono.woff2"))
	if err != nil {
		t.Fatal(err)
	}
	// A previously usable built-in remains active while a new import is pending.
	if _, err = a.store.PatchAppearance(preferencePatch(t, `{"uiFontId":"builtin:ui-ibm-plex-sans-sc"}`)); err != nil {
		t.Fatal(err)
	}
	if got := a.uiFontRuntime(); got.ActiveID != "builtin:ui-ibm-plex-sans-sc" {
		t.Fatal(got)
	}
	asset, err := a.store.ImportAsset("ui-font", "ui.woff2", "测试界面字体", font)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.PatchAppearance(preferencePatch(t, `{"uiFontId":"`+asset.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	// Recreating a Handler / reloading a window does not recreate the App.
	for i := 0; i < 3; i++ {
		handler := a.Handler(fstest.MapFS{"index.html": {Data: []byte("test")}})
		req := httptest.NewRequest("GET", "/api/ui-fonts/runtime", nil)
		req.Header.Set("X-CloudShell-Token", a.Token())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("runtime: %d %s", response.Code, response.Body.String())
		}
		var got uiFontState
		if err = json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.ActiveID != "builtin:ui-ibm-plex-sans-sc" || got.PendingID != asset.ID || len(got.AvailableIDs) != 0 {
			t.Fatalf("activated without restart: %+v", got)
		}
	}
	b, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got := b.uiFontRuntime(); got.ActiveID != asset.ID || got.PendingID != "" || !reflect.DeepEqual(got.AvailableIDs, []string{asset.ID}) {
		t.Fatalf("restart did not register font: %+v", got)
	}
	if err = b.store.DeleteAsset(asset.ID); err != nil {
		t.Fatal(err)
	}
	if got := b.uiFontRuntime(); got.ActiveID != defaultUIFontID || got.PendingID != "" || len(got.AvailableIDs) != 0 {
		t.Fatalf("deleted font remained active: %+v", got)
	}
}

func TestUITextColorsMigrationValidationAndPatchIsolation(t *testing.T) {
	var old Appearance
	if err := json.Unmarshal([]byte(`{"fontId":"builtin:fira-code","fontColors":{"builtin:fira-code":"#123456"}}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.UIFontID != defaultUIFontID || old.UITextColors == nil || old.FontColors[old.FontID] != "#123456" {
		t.Fatalf("migration lost preferences: %+v", old)
	}
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, patch := range []string{
		`{"fontColors":{"builtin:fira-code":"#123456"},"fontBold":{"builtin:fira-code":true},"backgroundOpacity":0.6,"uiFontId":"builtin:ui-ibm-plex-sans-sc","uiTextColors":{"light":"#ABCDEF"}}`,
		`{"uiTextColors":{"dark":"#FEDCBA"}}`,
		`{"uiTextColors":{"light":"#243B4E"}}`,
	} {
		if _, err = s.PatchAppearance(preferencePatch(t, patch)); err != nil {
			t.Fatal(err)
		}
	}
	before := s.List().Appearance
	if before.UITextColors["light"] != "#243b4e" || before.UITextColors["dark"] != "#fedcba" {
		t.Fatal(before.UITextColors)
	}
	for _, patch := range []string{
		`{"uiTextColors":{"system":"#123456"}}`, `{"uiTextColors":{"light":"red"}}`, `{"uiTextColors":{"light":"#000;url(x)"}}`,
		`{"uiFontId":"builtin:missing"}`, `{"uiFontId":"../../outside"}`, `{"uiTextColors":{"light":123}}`,
	} {
		if _, err = s.PatchAppearance(preferencePatch(t, patch)); err == nil {
			t.Fatal("accepted invalid patch", patch)
		}
		if !reflect.DeepEqual(before, s.List().Appearance) {
			t.Fatal("invalid patch mutated settings")
		}
	}
	before.UITextColors["dark"] = "#000000"
	if s.List().Appearance.UITextColors["dark"] != "#fedcba" {
		t.Fatal("returned map aliases saved colors")
	}
	if _, err = s.PatchAppearance(preferencePatch(t, `{"uiTextColors":{"light":null}}`)); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.List().Appearance
	if len(got.UITextColors) != 1 || got.UITextColors["dark"] != "#fedcba" || got.UIFontID != "builtin:ui-ibm-plex-sans-sc" || got.FontColors["builtin:fira-code"] != "#123456" || !got.FontBold["builtin:fira-code"] || got.BackgroundOpacity != .6 {
		t.Fatalf("patch/reset changed unrelated settings: %+v", got)
	}
}

func TestUIFontImportKindsAreSeparate(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	font, err := os.ReadFile(filepath.Join("..", "..", "web", "assets", "fonts", "jetbrains-mono.woff2"))
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := s.ImportAsset("font", "font.woff2", "终端字体", font)
	if err != nil {
		t.Fatal(err)
	}
	ui, err := s.ImportAsset("ui-font", "ui.woff2", "界面字体", font)
	if err != nil {
		t.Fatal(err)
	}
	for _, patch := range []string{`{"uiFontId":"` + terminal.ID + `"}`, `{"fontId":"` + ui.ID + `"}`, `{"backgroundId":"` + ui.ID + `"}`} {
		if _, err = s.PatchAppearance(preferencePatch(t, patch)); err == nil {
			t.Fatal("asset kinds were mixed", patch)
		}
	}
	if _, err = s.ImportAsset("ui-font", "evil.woff2", "坏字体", []byte("not a font")); err == nil {
		t.Fatal("invalid font accepted")
	}
	if _, err = s.AssetData(ui.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PatchAppearance(preferencePatch(t, `{"uiFontId":"`+ui.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
}
