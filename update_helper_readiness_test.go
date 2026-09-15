package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run in a real child process, including on native Windows CI. It simulates
// delayed preparation without modifying or replacing any application binary.
func TestUpdateReadinessChild(t *testing.T) {
	mode := os.Getenv("DENGSHELL_TEST_UPDATE_CHILD")
	if mode == "" {
		return
	}
	dir := os.Getenv("DENGSHELL_TEST_UPDATE_DIR")
	recordUpdateStage(filepath.Join(dir, "plan.json"), "hash")
	if mode == "failure" {
		_ = os.WriteFile(filepath.Join(dir, "error.txt"), []byte("fixture: update hash failed"), 0600)
		os.Exit(7)
	}
	if mode == "slow" {
		time.Sleep(4500 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestUpdateHelperAcceptsSlowPreparationAndReportsStage(t *testing.T) {
	dir := t.TempDir()
	cmd, done := startReadinessFixture(t, dir, "slow")
	seenHash := false
	started := time.Now()
	err := waitUpdateHelperReady(dir, done, updateHelperReadyTimeout, cmd.Process.Kill, func(message string) {
		seenHash = seenHash || message == updateStageMessage("hash")
	})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 4*time.Second || !seenHash {
		t.Fatalf("delayed readiness or preparation progress was not exercised: %v", seenHash)
	}
}

func TestUpdateHelperTimeoutStopsProcessBeforeRetry(t *testing.T) {
	dir := t.TempDir()
	cmd, done := startReadinessFixture(t, dir, "hang")
	started := time.Now()
	err := waitUpdateHelperReady(dir, done, 2*time.Second, cmd.Process.Kill, nil)
	if err == nil || !strings.Contains(err.Error(), "准备超时") || !strings.Contains(err.Error(), "校验更新文件") {
		t.Fatalf("missing timeout phase: %v", err)
	}
	if time.Since(started) > 8*time.Second || cmd.ProcessState == nil {
		t.Fatal("unresponsive helper was not reaped before returning")
	}
	if _, err := os.Stat(filepath.Join(dir, "ready")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("timed-out helper unexpectedly became ready")
	}
}

func TestUpdateHelperPreservesEarlyFailure(t *testing.T) {
	dir := t.TempDir()
	cmd, done := startReadinessFixture(t, dir, "failure")
	err := waitUpdateHelperReady(dir, done, updateHelperReadyTimeout, cmd.Process.Kill, nil)
	if err == nil || err.Error() != "fixture: update hash failed" {
		t.Fatalf("helper error was lost: %v", err)
	}
}

func TestUpdateHelperChecksReadyAtDeadline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	err := waitUpdateHelperReady(dir, make(chan error), 0, func() error {
		t.Fatal("ready helper was killed at the deadline")
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateHelperRejectsReadyFromExitedProcess(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	done <- errors.New("fixture exited after validation")
	if err := waitUpdateHelperReady(dir, done, 0, func() error { t.Fatal("exited process was killed"); return nil }, nil); err == nil {
		t.Fatal("exited helper caused application shutdown")
	}
}

func TestUpdateHelperFailedTerminationPreventsRetry(t *testing.T) {
	err := waitUpdateHelperReady(t.TempDir(), make(chan error), 0, func() error { return errors.New("fixture termination denied") }, nil)
	var pending *updateHelperExitPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("live helper retry was not blocked: %v", err)
	}
}

func startReadinessFixture(t *testing.T, dir, mode string) (*exec.Cmd, <-chan error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestUpdateReadinessChild$")
	cmd.Env = append(os.Environ(), "DENGSHELL_TEST_UPDATE_CHILD="+mode, "DENGSHELL_TEST_UPDATE_DIR="+dir)
	prepareUpdaterProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	})
	return cmd, done
}
