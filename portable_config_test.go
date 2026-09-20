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

func TestMacOSBundleRecognition(t *testing.T) {
	for _, tc := range []struct{ executable, bundle string }{
		{"/Applications/DengShell.app/Contents/MacOS/DengShell", "/Applications/DengShell.app"},
		{"/Volumes/DengShell/DengShell.app/Contents/MacOS/DengShell", "/Volumes/DengShell/DengShell.app"},
		{"/tmp/ordinary/Contents/MacOS/DengShell", ""},
		{"/tmp/DengShell.app/DengShell", ""},
		{"/tmp/DengShell", ""},
	} {
		if got := macOSApplicationBundle(filepath.FromSlash(tc.executable)); got != filepath.FromSlash(tc.bundle) {
			t.Fatalf("bundle for %q = %q; want %q", tc.executable, got, tc.bundle)
		}
	}
}

func TestMacOSBundleConfigurationSurvivesMovingApplication(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Application Support path")
	}
	root, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, location := range []string{"Applications", "Read Only Disk Image"} {
		executable := filepath.Join(t.TempDir(), location, "DengShell.app", "Contents", "MacOS", "DengShell")
		if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(executable, []byte("fixture"), 0500); err != nil {
			t.Fatal(err)
		}
		got, err := portableDirectoryForExecutable(executable)
		if err != nil || got != filepath.Join(root, "DengShell") {
			t.Fatalf("bundle configuration = %q, %v", got, err)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(executable), "data")); !os.IsNotExist(err) {
			t.Fatal("configuration was written into the application bundle")
		}
	}
}
