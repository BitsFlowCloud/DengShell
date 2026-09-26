package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSSHDiagnosticPreservesFirstTriggerAndSurvivesRemoval(t *testing.T) {
	a := &App{sessions: map[string]*Session{}}
	a.sshDiagnostics.path = filepath.Join(t.TempDir(), "logs", "ssh-disconnect.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{ID: "test-session", ctx: ctx, cancel: cancel, diagnosticLog: &a.sshDiagnostics, diagnosticStarted: time.Now().Add(-2 * time.Minute)}
	a.sessions[s.ID] = s
	s.latency = LatencySample{Pending: true, PendingSince: time.Now().Add(-3 * time.Second)}
	s.forceCloseDiagnostic("DS-230", "ssh-channel-open-cleanup-timeout")
	s.noteDisconnect("DS-202", "local-socket-closed")
	a.disconnect(s.ID)
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("id", s.ID)
	w := httptest.NewRecorder()
	a.sshDisconnectDiagnosticHTTP(w, r)
	var result SSHDisconnectDiagnostic
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != "DS-230" || result.TraceID != "test-session" || result.AgeSeconds < 120 || result.LogPath == "" {
		t.Fatalf("wrong trigger: %+v", result)
	}
	data, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	var event sshDiagnosticEvent
	if err = json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if !event.HeartbeatPending || event.HeartbeatPendingSeconds < 3 {
		t.Fatalf("missing pre-disconnect heartbeat: %+v", event)
	}
}

func TestSSHDiagnosticsBoundedRotatingLogAndNoRawErrors(t *testing.T) {
	l := &sshDiagnosticLog{path: filepath.Join(t.TempDir(), "ssh-disconnect.jsonl")}
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(l.path, make([]byte, 1<<20), 0600); err != nil {
			t.Fatal(err)
		}
		l.record("one", sshDiagnosticEvent{Kind: "sample"})
	}
	if _, err := os.Stat(l.path + ".2"); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Dir(l.path))
	if err != nil || len(files) != 3 {
		t.Fatalf("unbounded files: %v %v", files, err)
	}
	for _, f := range files {
		info, _ := f.Info()
		if info.Size() > 1<<20 {
			t.Fatalf("oversized diagnostic file %s", f.Name())
		}
	}
	secret := "not-a-real-password; command-and-private-key-text"
	kind := sshDiagnosticErrorKind(errors.New(secret))
	if strings.Contains(kind, "password") || kind != "transport-or-channel-error" || sshDiagnosticErrorKind(io.EOF) != "eof" {
		t.Fatal("error classification retained untrusted text")
	}
	l.record("two", sshDiagnosticEvent{SSHDisconnectDiagnostic: SSHDisconnectDiagnostic{Detail: kind}, Kind: "transport-end"})
	data, _ := os.ReadFile(l.path)
	if strings.Contains(string(data), secret) {
		t.Fatal("raw error leaked to diagnostics")
	}
}

func TestSSHDiagnosticConcurrentReasonsAndWriteFailure(t *testing.T) {
	l := &sshDiagnosticLog{path: filepath.Join(t.TempDir(), "blocked", "file")}
	if err := os.WriteFile(filepath.Dir(l.path), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Session{ID: "same", diagnosticLog: l, diagnosticStarted: time.Now()}
	s.noteDisconnect("DS-299", "terminal-relay-ended")
	s.noteDisconnect("DS-201", "heartbeat-timeout")
	var jobs sync.WaitGroup
	for i := 0; i < 20; i++ {
		jobs.Go(func() { s.noteDisconnect("DS-202", "local-socket-closed") })
	}
	jobs.Wait()
	l.mu.Lock()
	record := l.records[s.ID]
	l.mu.Unlock()
	if record.Code != "DS-201" || !record.LogWriteFailed {
		t.Fatalf("lost diagnosis after log write failure: %+v", record)
	}
}

func TestSSHDiagnosticClosePreventsLateLogWrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	log := &sshDiagnosticLog{path: filepath.Join(dir, "ssh-disconnect.jsonl")}
	log.record("one", sshDiagnosticEvent{Kind: "connected"})
	log.close()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() { log.record("one", sshDiagnosticEvent{Kind: "transport-end"}) })
	}
	wg.Wait()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("closed diagnostic logger recreated its directory", err)
	}
}
