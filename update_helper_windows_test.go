//go:build windows

package main

import (
	"cloudshell/internal/app"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "--dengshell-update-helper" {
		// This hook exists only in the test executable. The test uses the real
		// copy/start/validate/ready path, with a cold startup over the old limit.
		if os.Getenv("DENGSHELL_TEST_SLOW_REAL_HELPER") == "1" {
			time.Sleep(4500 * time.Millisecond)
		}
		if handleUpdateHelper() {
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func TestWindowsActualUpdateLaunchAcceptsSlowHelper(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "up.exe")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyUpdateFile(executable, file, 0700); err != nil {
		t.Fatal(err)
	}
	hash, err := app.FileSHA256(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DENGSHELL_TEST_SLOW_REAL_HELPER", "1")
	job := app.UpdateDownload{File: file, Package: app.UpdatePackage{Build: app.ApplicationBuild + 1, Version: app.ApplicationVersion, SHA256: hash, ExecutableSHA256: hash}}
	// Keep this test process alive so the helper can never replace its target.
	t.Cleanup(func() {
		for attempt := 0; attempt < 50; attempt++ {
			var status struct {
				PID int `json:"pid"`
			}
			b, _ := os.ReadFile(filepath.Join(dir, "helper-status.json"))
			if json.Unmarshal(b, &status) == nil && status.PID > 0 && status.PID != os.Getpid() {
				if p, err := os.FindProcess(status.PID); err == nil {
					_ = p.Kill()
					_, _ = p.Wait()
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	started := time.Now()
	if err := launchUpdateHelper(job, dir, nil); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 4*time.Second {
		t.Fatal("slow real helper was not exercised")
	}
	log, err := os.ReadFile(filepath.Join(dir, "installer.log"))
	if err != nil || !strings.Contains(string(log), "[hash]") || !strings.Contains(string(log), "[format]") {
		t.Fatalf("preparation evidence missing: %v", err)
	}
	if after, err := app.FileSHA256(executable); err != nil || after != hash {
		t.Fatal("preparation unexpectedly changed the running program")
	}
}
