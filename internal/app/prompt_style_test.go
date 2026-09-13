package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPromptStyleShellHooksColorAndRestoreOriginal(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			executable, err := exec.LookPath(shell)
			if err != nil {
				t.Skip("shell unavailable")
			}
			dir := t.TempDir()
			style := filepath.Join(dir, "style")
			hook := filepath.Join(dir, "hook")
			if err := os.WriteFile(style, []byte("#112233\n#abcdef\n"), 0600); err != nil {
				t.Fatal(err)
			}
			body, err := shellIntegrationAssets.ReadFile("shell_integration/prompt-" + shell + ".sh")
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.ReplaceAll(body, []byte("@DENGSHELL_STYLE_FILE@"), []byte(style))
			if err := os.WriteFile(hook, body, 0600); err != nil {
				t.Fatal(err)
			}
			original := `custom \u@\h:\w\$ `
			if shell == "zsh" {
				original = "custom %n@%m:%~%# "
			}
			script := "PS1=" + terminalQuote(original) + "; original=$PS1; . " + terminalQuote(hook) + "; __dengshell_apply_prompt_style; printf 'COLORED=%s\\n' \"$PS1\"; printf '\\n\\n' > " + terminalQuote(style) + "; __dengshell_apply_prompt_style; [ \"$PS1\" = \"$original\" ] || exit 47; printf 'RESET=%s\\n' \"$PS1\""
			output, err := exec.Command(executable, "-f", "-c", script).CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			expected := "38;2;17;34;51m"
			if shell == "zsh" {
				expected = "%F{#112233}%n%f"
			}
			if !strings.Contains(string(output), expected) || !strings.Contains(string(output), "RESET="+original) {
				t.Fatal("prompt styles failed or original was not restored", string(output))
			}
		})
	}
}

func TestPromptStyleValidationNeverExecutesFileContent(t *testing.T) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	dir := t.TempDir()
	style := filepath.Join(dir, "style")
	marker := filepath.Join(dir, "must-not-exist")
	if err := os.WriteFile(style, []byte("$(touch "+marker+")\n#123456\n"), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := shellIntegrationAssets.ReadFile("shell_integration/prompt-bash.sh")
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dir, "hook")
	body = bytes.ReplaceAll(body, []byte("@DENGSHELL_STYLE_FILE@"), []byte(style))
	if err := os.WriteFile(hook, body, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(shell, "-c", "PS1='unchanged'; . "+terminalQuote(hook)+"; __dengshell_apply_prompt_style; [ \"$PS1\" = unchanged ]").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("style file was evaluated as code")
	}
	session := &Session{}
	if _, err := session.savePromptStyle(context.Background(), PromptStyle{UsernameColor: "$(touch /tmp/example)"}); err == nil {
		t.Fatal("non-color value accepted")
	}
}

func TestPromptStyleLiveSSHUpdatesWithoutInjectedKeystrokes(t *testing.T) {
	s := monitorFixtureSession(t)
	s.promptUsernameColor, s.promptHostnameColor = "#112233", "#abcdef"
	integration := s.prepareTerminalIntegration()
	if integration.Shell != "bash" {
		t.Skip("dedicated fixture does not use Bash")
	}
	defer s.cleanupTerminalIntegration(integration)
	if integration.Username == "" || integration.Hostname == "" {
		t.Fatal("actual remote identity absent")
	}
	stylePath := s.promptStylePath
	sh, err := s.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	if err := sh.RequestPty("xterm-256color", 30, 100, nil); err != nil {
		t.Fatal(err)
	}
	input, err := sh.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := sh.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sh.Start(integration.command); err != nil {
		t.Fatal(err)
	}
	chunks := make(chan string, 32)
	go func() {
		defer close(chunks)
		data := make([]byte, 4096)
		for {
			n, err := output.Read(data)
			if n > 0 {
				chunks <- string(data[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	all := ""
	wait := func(marker string) {
		t.Helper()
		timer := time.NewTimer(6 * time.Second)
		defer timer.Stop()
		for !strings.Contains(all, marker) {
			select {
			case chunk, ok := <-chunks:
				if !ok {
					t.Fatal("terminal ended before marker", marker, all)
				}
				all += chunk
			case <-timer.C:
				t.Fatal("terminal timeout", marker, all)
			}
		}
	}
	wait("DengShell;prompt;" + integration.Nonce)
	_, err = io.WriteString(input, "HISTFILE=/dev/null; printf 'STYLE-%s\\n' 'COMMAND-WAIT'; read -r answer; printf 'STYLE-INPUT=%s\\n' \"$answer\"\r")
	if err != nil {
		t.Fatal(err)
	}
	wait("STYLE-COMMAND-WAIT")
	response, err := s.savePromptStyle(context.Background(), PromptStyle{UsernameColor: "#445566", HostnameColor: "#778899"})
	if err != nil || response["supported"] != true {
		t.Fatal("live style update failed", response, err)
	}
	if _, err := io.WriteString(input, "actual-user-input\r"); err != nil {
		t.Fatal(err)
	}
	wait("STYLE-INPUT=actual-user-input")
	wait("38;2;68;85;102m")
	if _, err := s.savePromptStyle(context.Background(), PromptStyle{}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(input, "printf 'STYLE-%s\\n' 'RESET'; exit\r"); err != nil {
		t.Fatal(err)
	}
	wait("STYLE-RESET")
	_ = sh.Wait()
	s.Close()
	if _, err := os.Stat(stylePath); !os.IsNotExist(err) {
		t.Fatal("session prompt state not cleaned on close", err)
	}
}
