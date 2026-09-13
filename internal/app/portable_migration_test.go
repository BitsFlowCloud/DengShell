package app

import (
	"os"
	"path/filepath"
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
