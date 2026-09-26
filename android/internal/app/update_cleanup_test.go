package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const inactiveUpdatePID = 2147483647

func cleanupFixture(t *testing.T, config string, n int, build uint64) string {
	t.Helper()
	dir := filepath.Join(config, fmt.Sprintf(".update-%d", n))
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, stringValue string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(stringValue), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("up.exe", "downloaded package")
	write("previous-program.exe", fmt.Sprintf("old program %d", build))
	write("dengshell-updater-123.exe", "helper")
	write("ready", "ready")
	write("installer.log", "installed")
	old, _ := FileSHA256(filepath.Join(dir, "previous-program.exe"))
	hash, _ := FileSHA256(filepath.Join(dir, "up.exe"))
	p := cleanupUpdatePlan{Schema: 2, Build: build, ParentPID: inactiveUpdatePID, ConfigDir: config, Staged: filepath.Join(dir, "up.exe"), Target: filepath.Join(config, "DengShell.exe"), OldSHA256: old, PackageSHA256: hash, ExecutableSHA256: hash}
	b, _ := json.Marshal(p)
	write("plan.json", string(b))
	write("helper-status.json", fmt.Sprintf(`{"pid":%d,"stage":"restart"}`, inactiveUpdatePID))
	return dir
}

func TestUpdateCleanupMigratesLegacyAndBoundsRecovery(t *testing.T) {
	config := t.TempDir()
	for _, name := range []string{EncryptedConfigName, ConfigKeyName, "dengshell.security-lock.enc", "update-receipt.json"} {
		if err := os.WriteFile(filepath.Join(config, name), []byte("user data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []int{3, 1, 2} {
		cleanupFixture(t, config, n, uint64(100+n))
	}
	cleanupUpdateDirectories(config)
	dirs, _ := filepath.Glob(filepath.Join(config, ".update-*"))
	if len(dirs) != 0 {
		t.Fatalf("legacy residue: %v", dirs)
	}
	b, err := os.ReadFile(filepath.Join(config, "update-backup", "previous-program.exe"))
	if err != nil || string(b) != "old program 103" {
		t.Fatalf("wrong retained backup: %q, %v", b, err)
	}
	for _, name := range []string{EncryptedConfigName, ConfigKeyName, "dengshell.security-lock.enc", "update-receipt.json"} {
		b, err := os.ReadFile(filepath.Join(config, name))
		if err != nil || string(b) != "user data" {
			t.Fatal("user file changed", name)
		}
	}
	cleanupUpdateDirectories(config)
}

func TestUpdateCleanupPreservesActiveLegacyAndUnknownData(t *testing.T) {
	for _, kind := range []string{"parent", "helper", "unknown-file", "nested-directory", "wrong-config", "malformed-plan", "unowned-backup", "corrupt-backup"} {
		t.Run(kind, func(t *testing.T) {
			config := t.TempDir()
			dir := cleanupFixture(t, config, 1, 1)
			b, _ := os.ReadFile(filepath.Join(dir, "plan.json"))
			var p cleanupUpdatePlan
			json.Unmarshal(b, &p)
			switch kind {
			case "parent":
				p.ParentPID = os.Getpid()
			case "helper":
				os.WriteFile(filepath.Join(dir, "helper-status.json"), []byte(fmt.Sprintf(`{"pid":%d}`, os.Getpid())), 0600)
			case "unknown-file":
				os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("user note"), 0600)
			case "nested-directory":
				os.Mkdir(filepath.Join(dir, "keys"), 0700)
			case "wrong-config":
				p.ConfigDir = t.TempDir()
			case "malformed-plan":
				p.Schema = 0
			case "unowned-backup":
				os.Mkdir(filepath.Join(config, "update-backup"), 0700)
			case "corrupt-backup":
				os.WriteFile(filepath.Join(dir, "previous-program.exe"), []byte("corrupt"), 0600)
			}
			b, _ = json.Marshal(p)
			os.WriteFile(filepath.Join(dir, "plan.json"), b, 0600)
			cleanupUpdateDirectories(config)
			if _, err := os.Stat(filepath.Join(dir, "up.exe")); err != nil {
				t.Fatalf("protected directory modified: %v", err)
			}
		})
	}
}

func TestUpdateCleanupSharedLeaseHandoff(t *testing.T) {
	config := t.TempDir()
	dir, err := os.MkdirTemp(config, ".update-")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := holdUpdateDirectory(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer parent()
	helper, err := HoldUpdateDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer helper()
	for _, release := range []func(){func() {}, parent} {
		release()
		cleanupUpdateDirectories(config)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("live lease deleted: %v", err)
		}
	}
	helper()
	cleanupUpdateDirectories(config)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("released cache not reclaimed: %v", err)
	}
}

func TestUpdateCleanupLeaseChild(t *testing.T) {
	dir := os.Getenv("DENGSHELL_QA_UPDATE_LEASE")
	if dir == "" {
		return
	}
	release, err := HoldUpdateDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
}

func TestUpdateCleanupCrashedProcessReleasesLease(t *testing.T) {
	config := t.TempDir()
	dir, _ := os.MkdirTemp(config, ".update-")
	release, err := holdUpdateDirectory(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	release()
	cmd := exec.Command(os.Args[0], "-test.run=^TestUpdateCleanupLeaseChild$")
	cmd.Env = append(os.Environ(), "DENGSHELL_QA_UPDATE_LEASE="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lease child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cleanupUpdateDirectories(config)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("child lease ignored", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	cleanupUpdateDirectories(config)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("crashed process cache remains", err)
	}
}

func TestUpdateCleanupDoesNotFollowLinks(t *testing.T) {
	config := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep.txt")
	os.WriteFile(sentinel, []byte("keep"), 0600)
	if err := os.Symlink(outside, filepath.Join(config, ".update-99")); err != nil {
		t.Skip("symlink unavailable", err)
	}
	dir := cleanupFixture(t, config, 1, 1)
	os.Remove(filepath.Join(dir, "up.exe"))
	if err := os.Symlink(sentinel, filepath.Join(dir, "up.exe")); err != nil {
		t.Fatal(err)
	}
	cleanupUpdateDirectories(config)
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "keep" {
		t.Fatal("link target changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "plan.json")); err != nil {
		t.Fatal("linked cache changed")
	}
}

func TestUpdateCleanupKeepsLatestBoundedError(t *testing.T) {
	config := t.TempDir()
	for _, n := range []int{2, 1, 3} {
		dir := cleanupFixture(t, config, n, uint64(n))
		os.WriteFile(filepath.Join(dir, "error.txt"), []byte(fmt.Sprintf("failure %d", n)), 0600)
		stamp := time.Unix(int64(n), 0)
		os.Chtimes(filepath.Join(dir, "error.txt"), stamp, stamp)
	}
	cleanupUpdateDirectories(config)
	b, err := os.ReadFile(filepath.Join(config, "update-backup", "last-error.json"))
	if err != nil {
		t.Fatal(err)
	}
	var log struct{ Error string }
	if json.Unmarshal(b, &log) != nil || log.Error != "failure 3" {
		t.Fatalf("wrong error retained: %s", b)
	}
}
