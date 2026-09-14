package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBatchedTerminalBootstrapAcrossLoginShells(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			executable, args := testLoginShell(t, shell)
			root := t.TempDir()
			integration, script := terminalIntegrationScript(root, "#112233\n#abcdef\n")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, append(args, "-c", "exec "+posixShellCommand(script))...)
			cmd.Env = append(os.Environ(), "SHELL="+executable)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("batch failed: %v %s", err, output)
			}
			if !strings.Contains(string(output), "__DENGSHELL_"+integration.Nonce+"__SHELL="+executable) {
				t.Fatal("missing verified result marker")
			}
			info, err := os.Stat(integration.directory)
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("bootstrap is not private: %v %v", info, err)
			}
			stylePath := integration.files[0]
			built, contents := terminalBootstrap(terminalIntegration{Nonce: integration.Nonce, directory: integration.directory}, executable, stylePath)
			contents[stylePath] = "#112233\n#abcdef\n"
			for filename, want := range contents {
				got, err := os.ReadFile(filename)
				if err != nil || string(got) != want {
					t.Fatalf("staged bytes changed for %s: %v", filepath.Base(filename), err)
				}
				info, err := os.Stat(filename)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("staged file is not private: %v %v", info, err)
				}
			}
			entries, err := os.ReadDir(integration.directory)
			if err != nil || len(entries) != len(built.files) {
				t.Fatal("staged files for the wrong shell", err)
			}
			if output, err := exec.Command("sh", "-c", terminalIntegrationRemoveCommand(integration)).CombinedOutput(); err != nil {
				t.Fatalf("cleanup: %v %s", err, output)
			}
			entries, err = os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("bootstrap cleanup left files", err)
			}
		})
	}
}

func TestBatchedTerminalBootstrapFailureCleanup(t *testing.T) {
	requireLinuxShellFixture(t)
	for _, scenario := range []string{"unsupported", "directory-collision", "style-symlink", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			integration, script := terminalIntegrationScript(root, "\n\n")
			shell := "/bin/bash"
			var preserved string
			switch scenario {
			case "unsupported":
				shell = "/bin/sh"
			case "directory-collision":
				if err := os.Mkdir(integration.directory, 0700); err != nil {
					t.Fatal(err)
				}
				preserved = filepath.Join(integration.directory, "keep")
				if err := os.WriteFile(preserved, []byte("untouched"), 0600); err != nil {
					t.Fatal(err)
				}
			case "style-symlink":
				preserved = filepath.Join(root, "keep")
				if err := os.WriteFile(preserved, []byte("untouched"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(preserved, integration.files[0]); err != nil {
					t.Fatal(err)
				}
			case "interrupted":
				script = strings.Replace(script, "case \"$_deng_shell\" in\nbash)", "kill -TERM $$\ncase \"$_deng_shell\" in\nbash)", 1)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "-c", script)
			cmd.Env = append(os.Environ(), "SHELL="+shell)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("failure accepted: %s", output)
			}
			if preserved != "" {
				if data, err := os.ReadFile(preserved); err != nil || string(data) != "untouched" {
					t.Fatal("pre-existing data altered", err)
				}
			}
			if scenario != "directory-collision" {
				if _, err := os.Lstat(integration.directory); !os.IsNotExist(err) {
					t.Fatal("failed preparation left directory", err)
				}
			}
			if scenario != "style-symlink" {
				if _, err := os.Lstat(integration.files[0]); !os.IsNotExist(err) {
					t.Fatal("failed preparation left style file", err)
				}
			}
		})
	}
}

// A prewarmed connection can be closed before any terminal window attaches.
// Verify its files are gone using another independently authenticated session.
func TestPrewarmedTerminalCloseBeforeAttach(t *testing.T) {
	key := os.Getenv("DENGSHELL_MATRIX_KEY")
	port, _ := strconv.Atoi(os.Getenv("DENGSHELL_MATRIX_PORT"))
	if key == "" || port <= 0 {
		t.Skip("requires disposable loopback SSH fixture")
	}
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := a.store.Save(Profile{Name: "prewarm cleanup", Host: "127.0.0.1", Port: port, User: "qa_bash", Auth: "key", KeyPath: key}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = a.Connect(ctx, p.ID, "")
	var host *HostKeyError
	if !errors.As(err, &host) {
		t.Fatal("host key approval missing", err)
	}
	s, err := a.ConnectWithHostKeyApproval(ctx, p.ID, "", &HostKeyApproval{Host: host.Host, Fingerprint: host.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	observer, err := a.Connect(ctx, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	integration := s.prepareTerminalIntegration()
	if s.Home == "" || integration.Shell != "bash" || s.preparedIntegration == nil {
		t.Fatal("incomplete initialized session")
	}
	if s.prepareTerminalIntegration().Nonce != integration.Nonce {
		t.Fatal("bootstrap initialized twice")
	}
	data, err := s.files.Open(integration.files[0])
	if err != nil {
		t.Fatal("SFTP not ready", err)
	}
	data.Close()
	a.disconnect(s.ID)
	for _, name := range append(append([]string(nil), integration.files...), integration.directory) {
		if _, err := observer.files.Lstat(name); !os.IsNotExist(err) {
			t.Fatalf("closed connection left %s: %v", filepath.Base(name), err)
		}
	}
}
