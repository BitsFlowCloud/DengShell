package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAppearanceWorkspacePromptDefaultsAndPersistence(t *testing.T) {
	var legacy Appearance
	if err := json.Unmarshal([]byte(`{"fontId":"builtin:fira-code","fontColors":{"builtin:fira-code":"#aabbcc"}}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.MonitorSide != "left" || legacy.FilesPosition != "bottom" || legacy.MinimizeAction != "ask" || legacy.PromptUsernameColor != "" || legacy.ExternalEditor != "" {
		t.Fatalf("legacy defaults changed: %+v", legacy)
	}
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacy.MonitorSide = "right"
	legacy.FilesPosition = "top"
	legacy.MinimizeAction = "tray"
	legacy.PromptUsernameColor = "#ABCDEF"
	legacy.PromptHostnameColor = "#0123EF"
	legacy.ExternalEditor = `C:\Program Files\Editor\edit.exe`
	if _, err := store.SaveAppearance(legacy); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	saved := reopened.List().Appearance
	if saved.MonitorSide != "right" || saved.FilesPosition != "top" || saved.MinimizeAction != "tray" || saved.PromptUsernameColor != "#abcdef" || saved.PromptHostnameColor != "#0123ef" || saved.ExternalEditor != legacy.ExternalEditor || saved.FontColors["builtin:fira-code"] != "#aabbcc" {
		t.Fatalf("appearance did not persist: %+v", saved)
	}
	saved.PromptUsernameColor = ""
	saved.PromptHostnameColor = ""
	saved.MinimizeAction = "minimize"
	if _, err := reopened.SaveAppearance(saved); err != nil {
		t.Fatal(err)
	}
}
func TestAppearanceWorkspacePromptRejectsInvalidPreferences(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*Appearance)
	}{
		{"monitor", func(a *Appearance) { a.MonitorSide = "center" }},
		{"files", func(a *Appearance) { a.FilesPosition = "beside" }},
		{"minimize", func(a *Appearance) { a.MinimizeAction = "quit" }},
		{"username short", func(a *Appearance) { a.PromptUsernameColor = "#fff" }},
		{"hostname invalid", func(a *Appearance) { a.PromptHostnameColor = "red;exit" }},
		{"editor newline", func(a *Appearance) { a.ExternalEditor = "/bin/editor\n/bin/other" }},
		{"editor nul", func(a *Appearance) { a.ExternalEditor = "editor\x00.exe" }},
		{"editor long", func(a *Appearance) { a.ExternalEditor = strings.Repeat("e", 4097) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a := defaultAppearance()
			test.change(&a)
			if _, err := store.SaveAppearance(a); err == nil {
				t.Fatal("invalid preference accepted")
			}
			if store.List().Appearance.MonitorSide != "left" || store.List().Appearance.PromptUsernameColor != "" {
				t.Fatal("invalid save mutated store")
			}
		})
	}
}
