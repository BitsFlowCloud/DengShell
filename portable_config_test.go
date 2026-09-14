package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPortableConfigurationFollowsExecutableAndNeverOldUserDirectory(t *testing.T) {
	folder := t.TempDir()
	executable := filepath.Join(folder, "DengShell.exe")
	if err := os.WriteFile(executable, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "previous-config"))
	t.Setenv("APPDATA", filepath.Join(t.TempDir(), "previous-appdata"))
	folder, err := filepath.EvalSymlinks(folder) // Windows may expand an 8.3 temporary path.
	if err != nil {
		t.Fatal(err)
	}
	got, err := portableDirectoryForExecutable(executable)
	if err != nil || got != filepath.Join(folder, "data") {
		t.Fatal("portable config used old user directory", got, err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "shortcut")
		if err := os.Symlink(executable, link); err != nil {
			t.Fatal(err)
		}
		got, err = portableDirectoryForExecutable(link)
		if err != nil || got != filepath.Join(folder, "data") {
			t.Fatal("symlink changed portable folder", got, err)
		}
	}
}
