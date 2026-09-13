package app

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Opt in only for disposable OpenSSH users qa_bash, qa_zsh and qa_fish on
// loopback. No real user home/config, credentials, or utility action is used.
func TestShellCompatibilitySSHMatrix(t *testing.T) {
	key := os.Getenv("DENGSHELL_MATRIX_KEY")
	port, _ := strconv.Atoi(os.Getenv("DENGSHELL_MATRIX_PORT"))
	if key == "" || port <= 0 {
		t.Skip("requires isolated Bash/Zsh/Fish OpenSSH fixture")
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			a, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			p, err := a.store.Save(Profile{Name: "Shell matrix " + shell, Host: "127.0.0.1", Port: port, User: "qa_" + shell, Auth: "key", KeyPath: key}, false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			_, err = a.Connect(ctx, p.ID, "")
			var host *HostKeyError
			if !errors.As(err, &host) {
				t.Fatal("fixture host key challenge missing", err)
			}
			s, err := a.ConnectWithHostKeyApproval(ctx, p.ID, "", &HostKeyApproval{Host: host.Host, Fingerprint: host.Fingerprint})
			if err != nil {
				t.Fatal(err)
			}
			defer a.disconnect(s.ID)
			stats, err := s.Stats(ctx)
			if err != nil || stats.MemoryTotal == 0 || !stats.ProcessSample.Available || len(stats.Processes) == 0 || len(stats.Disks) == 0 {
				t.Fatalf("monitor failed: %v %+v", err, stats)
			}
			probe, err := runMTRScript(ctx, s, utilityProbe(linuxUtilityPaths), 128<<10)
			if err != nil {
				t.Fatal(err)
			}
			inspection, err := parseUtilityInspection(probe)
			if err != nil || inspection.sections["OS"] != "Linux" {
				t.Fatal("utility probe failed", inspection, err)
			}
			for _, request := range []UtilityRequest{{Kind: "bbr", Mode: "conservative"}, {Kind: "clean"}, {Kind: "swap-status"}} {
				if plan := buildUtilityPlan(request, inspection, linuxUtilityPaths); plan.Kind != request.Kind {
					t.Fatal("read-only utility plan failed", request, plan)
				}
			}
			integration := s.prepareTerminalIntegration()
			if integration.Shell != shell || integration.Nonce == "" {
				t.Fatalf("integration not prepared: %+v", integration)
			}
			defer s.cleanupTerminalIntegration(integration)
			terminal, err := s.client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer terminal.Close()
			if err := terminal.RequestPty("xterm-256color", 30, 100, nil); err != nil {
				t.Fatal(err)
			}
			input, _ := terminal.StdinPipe()
			output, _ := terminal.StdoutPipe()
			chunks := make(chan string, 256)
			go func() {
				defer close(chunks)
				buffer := make([]byte, 4096)
				for {
					n, err := output.Read(buffer)
					if n > 0 {
						select {
						case chunks <- string(buffer[:n]):
						case <-ctx.Done():
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
			if err := terminal.Start(integration.command); err != nil {
				t.Fatal(err)
			}
			all := ""
			wait := func(marker string, after int) {
				t.Helper()
				for !strings.Contains(all[after:], marker) {
					select {
					case text, ok := <-chunks:
						if !ok {
							t.Fatalf("PTY closed before %q: %q", marker, all)
						}
						all += text
					case <-ctx.Done():
						t.Fatalf("timeout waiting for %q: %q", marker, all)
					}
				}
			}
			prompt := "DengShell;prompt;" + integration.Nonce
			history := func(command string) string {
				return "DengShell;command;" + integration.Nonce + ";" + base64.StdEncoding.EncodeToString([]byte(command))
			}
			send := func(command string) int {
				t.Helper()
				offset := len(all)
				if _, err := io.WriteString(input, command+"\r"); err != nil {
					t.Fatal(err)
				}
				return offset
			}
			wait("DengShell;ready;"+integration.Nonce+";"+shell, 0)
			wait(prompt, 0)
			if _, err := s.files.Stat(integration.directory); !os.IsNotExist(err) {
				t.Fatal("temporary bootstrap did not remove itself", err)
			}
			command := `printf '__CONFIG__%s\n' "$DENGSHELL_QA_CONFIG_LOADED"`
			offset := send(command)
			wait("__CONFIG__"+shell, offset)
			wait(prompt, offset)
			wait(history(command), offset)
			offset = send(`sh -c 'printf "\n__BUSY_STARTED__\n"; exec sleep 20'`)
			wait("DengShell;busy;"+integration.Nonce, offset)
			wait("\n__BUSY_STARTED__\r\n", offset)
			io.WriteString(input, "\x03")
			wait(prompt, offset)
			// Input delivered to a program must never become command history.
			offset = send(`sh -c 'printf "\n__INPUT_READY__\n"; exec cat'`)
			wait("DengShell;busy;"+integration.Nonce, offset)
			wait("\n__INPUT_READY__\r\n", offset)
			io.WriteString(input, "secret_fixture_input\n\x04")
			wait(prompt, offset)
			if strings.Contains(all[offset:], history("secret_fixture_input")) {
				t.Fatal("program input leaked into shell command history")
			}
			directory := path.Join(s.Home, "cwd space%#?中文\nline")
			if err := s.files.Mkdir(directory); err != nil {
				t.Fatal(err)
			}
			defer s.files.RemoveDirectory(directory)
			command = "cd " + loginShellQuote(directory)
			offset = len(all)
			io.WriteString(input, "\x1b[200~"+command+"\x1b[201~\r")
			wait("DengShell;cwd;"+integration.Nonce+";"+base64.StdEncoding.EncodeToString([]byte(directory)), offset)
			wait(history(command), offset)
			wait(prompt, offset)
			if _, err := s.savePromptStyle(ctx, PromptStyle{"#112233", "#abcdef"}); err != nil {
				t.Fatal(err)
			}
			offset = send("")
			wait("38;2;17;34;51m", offset)
			if _, err := s.savePromptStyle(ctx, PromptStyle{}); err != nil {
				t.Fatal(err)
			}
			offset = send("")
			wait("ORIGINAL_"+strings.ToUpper(shell)+">", offset)
			offset = send("false")
			wait(prompt, offset)
			status := `printf '\n__LAST_STATUS__%s\n' "$?"`
			if shell == "fish" {
				status = `printf '\n__LAST_STATUS__%s\n' "$status"`
			}
			offset = send(status)
			wait("\n__LAST_STATUS__1\r\n", offset)
			wait(prompt, offset)
			if shell == "fish" {
				offset = send("set -g fish_history ''")
				wait(prompt, offset)
				private := `printf '\n__PRIVATE_HISTORY__\n'`
				offset = send(private)
				wait("\n__PRIVATE_HISTORY__\r\n", offset)
				wait(prompt, offset)
				if strings.Contains(all[offset:], history(private)) {
					t.Fatal("Fish disabled history was not respected")
				}
			}
			send("exit")
			t.Logf("%s: SSH/SFTP, monitor (%d processes), utility plans, startup config, history, busy/Ctrl+C, program-input exclusion, multiline cwd and prompt color/restore passed", shell, len(stats.Processes))
		})
	}
}
