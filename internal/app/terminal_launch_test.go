package app

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Run the actual production launch command with an isolated home and config.
// Only disposable startup files and staging paths are exposed to these shells.
func runTerminalLaunchFixture(t *testing.T, integration terminalIntegration, shell, home, input string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "umask 027; "+integration.command)
	cmd.Dir = home
	cmd.Env = []string{
		"HOME=" + home, "SHELL=" + shell, "PATH=" + os.Getenv("PATH"),
		"TERM=xterm-256color", "LC_ALL=C", "USER=dengshell-fixture", "LOGNAME=dengshell-fixture",
		"ZDOTDIR=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		// Debian global Zsh completion otherwise caches into the bootstrap ZDOTDIR.
		"skip_global_compinit=1",
	}
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terminal launch failed: %v\n%s", err, output)
	}
	return string(output)
}

func terminalLaunchFixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	files := map[string]string{
		".bash_profile": ". \"$HOME/.bashrc\"\n",
		".bashrc":       "PS1='DENGSHELL_TEST> '; HISTFILE=\"$HOME/history\"; HISTSIZE=100; HISTCONTROL=''\n",
		".zshrc":        "PS1='DENGSHELL_TEST> '; HISTFILE=\"$HOME/history\"; HISTSIZE=100; SAVEHIST=100\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestInlineTerminalLaunchPreservesShellIntegration(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			executable, _ := testLoginShell(t, shell)
			home := terminalLaunchFixtureHome(t)
			root := t.TempDir()
			integration := terminalLaunchScript(root, "#112233\n#abcdef\n")
			dir := filepath.Join(home, "directory with spaces")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			input := "printf '__DENG_UMASK__'; umask\nprintf '__DENG_COMMAND_OK__\\n'\ncd " + terminalQuote(dir) + "\nprintf '__DENG_CWD_OK__\\n'\nexit\n"
			output := runTerminalLaunchFixture(t, integration, executable, home, input)
			for _, marker := range []string{
				"\x1b]777;DengShell;ready;" + integration.Nonce + ";" + shell + "\x07",
				"\x1b]777;DengShell;prompt;" + integration.Nonce + "\x07",
				"\x1b]777;DengShell;cwd;" + integration.Nonce + ";" + base64.StdEncoding.EncodeToString([]byte(dir)) + "\x07",
				"\x1b]777;DengShell;command;" + integration.Nonce + ";" + base64.StdEncoding.EncodeToString([]byte("printf '__DENG_COMMAND_OK__\\n'")) + "\x07",
				"__DENG_COMMAND_OK__\n", "__DENG_CWD_OK__\n",
			} {
				if !strings.Contains(output, marker) {
					t.Fatalf("missing shell integration/command marker %q\n%s", marker, output)
				}
			}
			// Bash prints four octal digits; Zsh omits the leading zero.
			if !regexp.MustCompile(`__DENG_UMASK__0*27\n`).MatchString(output) {
				t.Fatalf("shell umask changed: %q", output)
			}
			if _, err := os.Stat(integration.directory); !os.IsNotExist(err) {
				entries, _ := os.ReadDir(integration.directory)
				names := make([]string, 0, len(entries))
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				t.Fatal("inline bootstrap directory remained after launch", err, names)
			}
			if _, err := os.Stat(integration.files[0]); !os.IsNotExist(err) {
				t.Fatal("prompt style remained after shell exit", err)
			}
		})
	}
}

func TestInlineTerminalLaunchFallsBackWithoutTouchingExistingPaths(t *testing.T) {
	requireLinuxShellFixture(t)
	for _, scenario := range []string{"unsupported", "restricted-staging", "directory-collision", "style-symlink"} {
		t.Run(scenario, func(t *testing.T) {
			home := terminalLaunchFixtureHome(t)
			root := t.TempDir()
			if scenario == "restricted-staging" {
				root = "/proc"
			}
			integration := terminalLaunchScript(root, "\n\n")
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
			case "style-symlink":
				preserved = filepath.Join(root, "keep")
			}
			if preserved != "" {
				if err := os.WriteFile(preserved, []byte("untouched"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "style-symlink" {
				if err := os.Symlink(preserved, integration.files[0]); err != nil {
					t.Fatal(err)
				}
			}
			output := runTerminalLaunchFixture(t, integration, shell, home, "printf '__DENG_FALLBACK_OK__\\n'; printf '__DENG_UMASK__'; umask; exit\n")
			if !strings.Contains(output, "__DENG_FALLBACK_OK__\n") || !strings.Contains(output, "__DENG_UMASK__0027") {
				t.Fatalf("fallback shell unusable or umask changed: %q", output)
			}
			if strings.Contains(output, "DengShell;ready;"+integration.Nonce) {
				t.Fatal("failed/unsupported integration claimed readiness")
			}
			if preserved != "" {
				if data, err := os.ReadFile(preserved); err != nil || string(data) != "untouched" {
					t.Fatal("pre-existing path modified", err)
				}
			}
			if scenario != "directory-collision" {
				if _, err := os.Lstat(integration.directory); !os.IsNotExist(err) {
					t.Fatal("failed staging leaked directory", err)
				}
			}
			if scenario == "style-symlink" {
				if info, err := os.Lstat(integration.files[0]); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("pre-existing style symlink removed", err)
				}
			} else if _, err := os.Lstat(integration.files[0]); !os.IsNotExist(err) {
				t.Fatal("failed staging leaked style file", err)
			}
		})
	}
}
