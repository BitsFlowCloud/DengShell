package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFontColorsPersistPerFamilyAndCannotMutateStore(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value := defaultAppearance()
	value.FontColors = map[string]string{"builtin:jetbrains-mono": "#BADA55", "builtin:fira-code": "#99ccff"}
	value.BackgroundOpacity = .9
	saved, err := s.SaveAppearance(value)
	if err != nil {
		t.Fatal(err)
	}
	value.FontColors["builtin:jetbrains-mono"] = "#000000"
	saved.FontColors["builtin:fira-code"] = "#000000"
	listed := s.List()
	listed.Appearance.FontColors["builtin:fira-code"] = "#000000"
	if s.List().Appearance.FontColors["builtin:jetbrains-mono"] != "#bada55" || s.List().Appearance.FontColors["builtin:fira-code"] != "#99ccff" {
		t.Fatal("caller mutation changed the stored per-font colors")
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.List().Appearance.FontColors["builtin:jetbrains-mono"] != "#bada55" || reopened.List().Appearance.BackgroundOpacity != .9 {
		t.Fatal("font colors or expanded background strength did not persist")
	}
	for _, color := range []string{"red", "#123", "#gg0000", "#12345678", "url(example)"} {
		invalid := defaultAppearance()
		invalid.FontColors = map[string]string{"builtin:jetbrains-mono": color}
		if _, err := s.SaveAppearance(invalid); err == nil {
			t.Fatalf("invalid color accepted: %q", color)
		}
	}
}

func TestBackgroundDefaultMigrationPreservesExplicitChoices(t *testing.T) {
	for _, test := range []struct {
		name, data string
		want       float64
	}{
		{"legacy default", `{"fontId":"builtin:jetbrains-mono","backgroundId":"builtin:mist","backgroundOpacity":0.18,"uiScale":1,"terminalFontSize":14}`, .42},
		{"legacy zero", `{"backgroundOpacity":0}`, 0},
		{"legacy custom", `{"backgroundOpacity":0.55}`, .55},
		{"new explicit 18", `{"backgroundOpacity":0.18,"backgroundVersion":2}`, .18},
		{"new default", `{}`, .42},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value Appearance
			if err := json.Unmarshal([]byte(test.data), &value); err != nil {
				t.Fatal(err)
			}
			if value.BackgroundOpacity != test.want || value.BackgroundVersion != 2 {
				t.Fatalf("wrong migration: %+v", value)
			}
		})
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"appearance":{"fontId":"builtin:jetbrains-mono","backgroundId":"builtin:none","backgroundOpacity":0.18,"uiScale":1,"terminalFontSize":14}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if value := s.List().Appearance; value.BackgroundID != "builtin:none" || value.BackgroundOpacity != .42 {
		t.Fatalf("legacy pure-color selection changed: %+v", value)
	}
}

func TestLegacyBoldMigratesOnlyToSelectedFont(t *testing.T) {
	for _, test := range []struct {
		name, data string
		want       map[string]bool
	}{
		{"legacy selected bold", `{"fontId":"builtin:fira-code","terminalBold":true,"fontColors":{"builtin:fira-code":"#99ccff"}}`, map[string]bool{"builtin:fira-code": true}},
		{"legacy regular", `{"fontId":"builtin:fira-code","terminalBold":false}`, map[string]bool{}},
		{"legacy default family", `{"terminalBold":true}`, map[string]bool{"builtin:jetbrains-mono": true}},
		{"explicit regular overrides legacy", `{"fontId":"builtin:fira-code","terminalBold":true,"fontBold":{"builtin:fira-code":false}}`, map[string]bool{"builtin:fira-code": false}},
		{"new empty map overrides legacy", `{"terminalBold":true,"fontBold":{}}`, map[string]bool{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value Appearance
			if err := json.Unmarshal([]byte(test.data), &value); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(value.FontBold, test.want) || value.TerminalBold {
				t.Fatalf("unexpected bold migration: %+v", value)
			}
			value.FontID = "builtin:source-code-pro"
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "terminalBold") {
				t.Fatal("deprecated global bold survived migration")
			}
			var reopened Appearance
			if err := json.Unmarshal(data, &reopened); err != nil || !reflect.DeepEqual(reopened.FontBold, test.want) || reopened.FontBold[reopened.FontID] {
				t.Fatalf("changing font repeated legacy migration: %+v / %v", reopened, err)
			}
		})
	}
}

func TestFontBoldAndColorsPersistAndAreIndependentCopies(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value := defaultAppearance()
	value.FontID = "builtin:fira-code"
	value.FontBold = map[string]bool{"builtin:fira-code": true, "builtin:jetbrains-mono": false}
	value.FontColors = map[string]string{"builtin:fira-code": "#A9DFBF", "builtin:jetbrains-mono": "#9ED8FF"}
	saved, err := s.SaveAppearance(value)
	if err != nil {
		t.Fatal(err)
	}
	value.FontBold["builtin:fira-code"] = false
	saved.FontBold["builtin:fira-code"] = false
	listed := s.List()
	listed.Appearance.FontBold["builtin:fira-code"] = false
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*Store{s, reopened} {
		got := store.List().Appearance
		if !got.FontBold["builtin:fira-code"] || got.FontBold["builtin:jetbrains-mono"] || got.FontColors["builtin:fira-code"] != "#a9dfbf" || got.FontColors["builtin:jetbrains-mono"] != "#9ed8ff" {
			t.Fatalf("per-font styles lost or changed by caller: %+v", got)
		}
	}
	invalid := defaultAppearance()
	invalid.FontBold = map[string]bool{"../font": true}
	if _, err := s.SaveAppearance(invalid); err == nil {
		t.Fatal("invalid style font ID accepted")
	}
	var malformed Appearance
	if err := json.Unmarshal([]byte(`{"fontBold":{"builtin:fira-code":"bold"}}`), &malformed); err == nil {
		t.Fatal("nonboolean font style accepted")
	}
}

func TestDeletingCustomFontCleansStyleAndRollbackPreservesIt(t *testing.T) {
	font, err := os.ReadFile(filepath.Join("..", "..", "web", "assets", "fonts", "jetbrains-mono.woff2"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	asset, err := s.ImportAsset("font", "qa-font.woff2", "自定义字体", font)
	if err != nil {
		t.Fatal(err)
	}
	value := defaultAppearance()
	value.FontID = asset.ID
	value.FontBold[asset.ID] = true
	value.FontBold["builtin:fira-code"] = true
	value.FontColors[asset.ID] = "#9ed8ff"
	value.FontColors["builtin:fira-code"] = "#a9dfbf"
	if _, err := s.SaveAppearance(value); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, EncryptedConfigName)
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAsset(asset.ID); err == nil {
		t.Fatal("expected config-write failure")
	}
	if got := s.List().Appearance; !got.FontBold[asset.ID] || got.FontColors[asset.ID] != "#9ed8ff" || got.FontID != asset.ID {
		t.Fatalf("failed deletion mutated original maps: %+v", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".saved", path); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAsset(asset.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.List().Appearance
	_, hasBold := got.FontBold[asset.ID]
	_, hasColor := got.FontColors[asset.ID]
	if hasBold || hasColor || got.FontID != defaultAppearance().FontID || !got.FontBold["builtin:fira-code"] || got.FontColors["builtin:fira-code"] != "#a9dfbf" {
		t.Fatalf("custom style deletion or other-family isolation failed: %+v", got)
	}
}
