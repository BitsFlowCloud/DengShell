package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPortableDataMigrationKeepsSettingsAndAssets(t *testing.T) {
	root := t.TempDir()
	old, e := OpenStore(root)
	if e != nil {
		t.Fatal(e)
	}
	a := old.List().Appearance
	a.Theme = "dark"
	if _, e = old.SaveAppearance(a); e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(root, "assets", "backgrounds"), 0700); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "assets", "backgrounds", "custom.png"), []byte("image"), 0600)
	target := filepath.Join(root, "data")
	os.MkdirAll(filepath.Join(target, "docs"), 0700)
	os.WriteFile(filepath.Join(target, "docs", "README.txt"), []byte("instructions"), 0600)
	if e = MigratePortableData(target); e != nil {
		t.Fatal(e)
	}
	next, e := OpenStore(target)
	if e != nil {
		t.Fatal(e)
	}
	if next.List().Appearance.Theme != "dark" {
		t.Fatal("lost theme")
	}
	if b, e := os.ReadFile(filepath.Join(target, "assets", "backgrounds", "custom.png")); e != nil || string(b) != "image" {
		t.Fatal("lost asset", e)
	}
	if _, e = os.Stat(filepath.Join(root, EncryptedConfigName)); !os.IsNotExist(e) {
		t.Fatal("old file remains")
	}
	if e = MigratePortableData(target); e != nil {
		t.Fatal(e)
	}
}

func TestPortableDataMigrationKeepsEncryptedAISettingsAndKey(t *testing.T) {
	root := t.TempDir()
	old, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	previous := &App{store: old}
	want := aiSettings{Provider: "migration-fixture", Timeout: 45, Providers: []aiProvider{{
		ID: "migration-fixture", Name: "Portable AI", Format: "openai",
		BaseURL: "https://migration.invalid/v1", Model: "fixture-model", APIKey: "portable-ai-test-secret",
	}}}
	if _, err := previous.saveAISettings(want); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(root, ConfigKeyName))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := os.ReadFile(filepath.Join(root, aiSettingsFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(want.Providers[0].APIKey)) || bytes.Contains(encrypted, []byte(want.Providers[0].BaseURL)) {
		t.Fatal("fixture AI settings were not encrypted")
	}
	target := filepath.Join(root, "data")
	if err := MigratePortableData(target); err != nil {
		t.Fatal(err)
	}
	for name, before := range map[string][]byte{ConfigKeyName: key, aiSettingsFile: encrypted} {
		after, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || !bytes.Equal(after, before) {
			t.Fatalf("migration lost or changed %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("old %s remains after successful migration: %v", name, err)
		}
	}
	next, err := OpenStore(target)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := (&App{store: next}).loadAISettings()
	if err != nil || !reflect.DeepEqual(loaded, want) {
		t.Fatalf("migrated AI settings cannot be read with migrated key: %v", err)
	}
	if err := MigratePortableData(target); err != nil {
		t.Fatal(err)
	}
	loaded, err = (&App{store: next}).loadAISettings()
	if err != nil || !reflect.DeepEqual(loaded, want) {
		t.Fatalf("repeat migration changed AI settings: %v", err)
	}
}
