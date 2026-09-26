package app

import (
	"context"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTerminalIntegrationTimeoutAndBoundedWorkers(t *testing.T) {
	blocked := make(chan struct{})
	var workers sync.WaitGroup
	var cleaned sync.WaitGroup
	for range cap(terminalIntegrationSlots) {
		workers.Add(1)
		cleaned.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		result := boundedTerminalIntegration(ctx, func() terminalIntegration { defer workers.Done(); <-blocked; return terminalIntegration{Shell: "bash"} }, func(terminalIntegration) { cleaned.Done() })
		cancel()
		if result.Shell != "" {
			t.Fatal("timed-out integration was enabled")
		}
	}
	start := time.Now()
	result := boundedTerminalIntegration(context.Background(), func() terminalIntegration { t.Error("overflow worker started"); return terminalIntegration{} }, func(terminalIntegration) {})
	if result.Shell != "" || time.Since(start) > time.Second {
		t.Fatal("optional integration blocked terminal fallback")
	}
	close(blocked)
	workers.Wait()
	cleaned.Wait()
}

func TestTerminalIntegrationTemporaryFilesAndRestrictedFallback(t *testing.T) {
	key := os.Getenv("CLOUDSHELL_TEST_KEY")
	if key == "" {
		t.Skip("requires dedicated sshd on 19225")
	}
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := a.store.Save(Profile{Name: "Shell integration QA", Host: "127.0.0.1", Port: 19225, User: "bitsflow", Group: "QA", Auth: "key", KeyPath: key}, false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := connectLocalSSHFixture(t, a, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.disconnect(s.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopSession := context.AfterFunc(ctx, s.forceClose)
	defer stopSession()
	// Exercise the explicit legacy preparation helper independently of the new
	// production inline launch. Staging targets /tmp, never the user home.
	s.startFileInitialization()
	if err := s.waitFileInitialization(ctx); err != nil {
		t.Fatal("fixture SFTP initialization", err)
	}
	files, _ := s.fileClientSnapshot()
	if files == nil {
		t.Fatal("fixture SFTP unavailable")
	}
	integration := s.prepareTerminalIntegrationFiles(ctx, "/tmp")
	if integration.Shell != "bash" || !strings.HasPrefix(integration.directory, "/tmp/dengshell-session-") {
		t.Fatalf("unexpected preparation: %+v", integration)
	}
	defer s.cleanupTerminalIntegration(integration)
	file, err := files.Open(path.Join(integration.directory, "bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	// An unavailable staging location simply returns no integration metadata.
	fallback := s.prepareTerminalIntegrationFiles(ctx, "/proc")
	if fallback.Shell != "" || fallback.Nonce != "" || fallback.command != "" {
		t.Fatal("restricted staging did not select ordinary shell fallback")
	}
	// Start the real temporary rcfile. It removes all bootstrap files before its
	// ready marker, and does not depend on an eventual clean SSH disconnect.
	sh, err := s.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	if err = sh.RequestPty("xterm-256color", 30, 100, nil); err != nil {
		t.Fatal(err)
	}
	in, err := sh.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := sh.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = sh.Start(integration.command); err != nil {
		t.Fatal(err)
	}
	ready := make(chan bool, 1)
	go func() {
		data := make([]byte, 4096)
		text := ""
		for {
			n, err := out.Read(data)
			text += string(data[:n])
			if strings.Contains(text, "DengShell;ready;"+integration.Nonce+";bash") {
				ready <- true
				return
			}
			if err != nil {
				ready <- false
				return
			}
		}
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("shell integration readiness missing")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = files.Stat(integration.directory); err == nil {
		t.Fatal("bootstrap directory remained after readiness")
	}
	_, _ = in.Write([]byte("HISTFILE=/dev/null; exit\r"))
	_ = sh.Close()
}
