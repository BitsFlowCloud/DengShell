package app

import (
	"context"
	"embed"
	"encoding/json"
	"path"
	"strings"
	"time"
)

//go:embed shell_integration/*
var shellIntegrationAssets embed.FS

var terminalIntegrationSlots = make(chan struct{}, 4)
var terminalIntegrationCleanupSlots = make(chan struct{}, 4)

type terminalIntegration struct {
	Shell     string `json:"shell"`
	Nonce     string `json:"nonce"`
	Username  string `json:"promptUsername,omitempty"`
	Hostname  string `json:"promptHostname,omitempty"`
	command   string
	directory string
	files     []string
	inline    bool // staging runs inside the terminal request; paths are not pre-verified
}

func terminalQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// Production sessions hold a locally generated launch plan. Fixtures that
// construct a Session directly retain the bounded remote-preparation fallback.
func (s *Session) prepareTerminalIntegration() terminalIntegration {
	if s.preparedIntegration != nil {
		return *s.preparedIntegration
	}
	return s.prepareTerminalIntegrationContext(s.ctx)
}

func (s *Session) prepareTerminalIntegrationContext(parent context.Context) terminalIntegration {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	return boundedTerminalIntegration(ctx, func() terminalIntegration { return s.prepareTerminalIntegrationFiles(ctx, "/tmp") }, s.cleanupTerminalIntegration)
}

func boundedTerminalIntegration(ctx context.Context, prepare func() terminalIntegration, cleanup func(terminalIntegration)) terminalIntegration {
	select {
	case terminalIntegrationSlots <- struct{}{}:
	default:
		return terminalIntegration{}
	}
	ready := make(chan terminalIntegration)
	go func() {
		defer func() { <-terminalIntegrationSlots }()
		integration := prepare()
		select {
		case ready <- integration:
		case <-ctx.Done():
			cleanup(integration)
		}
	}()
	select {
	case integration := <-ready:
		return integration
	case <-ctx.Done():
		return terminalIntegration{}
	}
}

// A single exec detects the login shell and stages its bootstrap. File writes
// happen on the server, avoiding a round trip for every SFTP open/chmod/write/
// close. No command is injected into the interactive terminal or user rc files.
func (s *Session) prepareTerminalIntegrationFiles(ctx context.Context, stagingRoot string) terminalIntegration {
	s.promptStyleMu.Lock()
	initialStyle := s.promptUsernameColor + "\n" + s.promptHostnameColor + "\n"
	s.promptStyleMu.Unlock()
	integration, script := terminalIntegrationScript(stagingRoot, initialStyle)
	output, err := runMTRScript(ctx, s, script, 4096)
	if err != nil {
		return terminalIntegration{}
	}
	prefix := "__DENGSHELL_" + integration.Nonce + "__"
	var shellPath, username, hostname string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, prefix+"SHELL=") {
			shellPath = strings.TrimPrefix(line, prefix+"SHELL=")
		}
		if strings.HasPrefix(line, prefix+"USER=") {
			username = strings.TrimPrefix(line, prefix+"USER=")
		}
		if strings.HasPrefix(line, prefix+"HOST=") {
			hostname = strings.TrimPrefix(line, prefix+"HOST=")
		}
	}
	if !strings.HasPrefix(shellPath, "/") || strings.ContainsAny(shellPath, "\x00\r\n") || (path.Base(shellPath) != "bash" && path.Base(shellPath) != "zsh" && path.Base(shellPath) != "fish") || ctx.Err() != nil {
		s.cleanupTerminalIntegration(integration)
		return terminalIntegration{}
	}
	stylePath := path.Join(stagingRoot, "dengshell-prompt-"+integration.Nonce)
	// Keep the complete cleanup plan even though only one shell branch was written.
	built, _ := terminalBootstrap(integration, shellPath, stylePath)
	integration.Shell, integration.command = built.Shell, "exec "+posixShellCommand(built.command)
	integration.Username, integration.Hostname = username, hostname
	s.promptStyleMu.Lock()
	if current := s.promptUsernameColor + "\n" + s.promptHostnameColor + "\n"; current != initialStyle {
		err = s.writePromptStyleFile(ctx, stylePath, PromptStyle{s.promptUsernameColor, s.promptHostnameColor})
		if err != nil {
			s.promptStyleMu.Unlock()
			s.cleanupTerminalIntegration(integration)
			return terminalIntegration{}
		}
	}
	s.promptStylePath, s.promptUsername, s.promptHostname = stylePath, username, hostname
	s.promptStyleMu.Unlock()
	return integration
}

func terminalIntegrationScript(stagingRoot, initialStyle string) (terminalIntegration, string) {
	return terminalIntegrationStageScript(stagingRoot, initialStyle, true)
}

func terminalIntegrationStageScript(stagingRoot, initialStyle string, metadata bool) (terminalIntegration, string) {
	integration := terminalIntegration{Nonce: randomID(), directory: path.Join(stagingRoot, "dengshell-session-"+randomID())}
	stylePath := path.Join(stagingRoot, "dengshell-prompt-"+integration.Nonce)
	integration.files = []string{stylePath, stylePath + ".new"}
	var branches strings.Builder
	for _, shell := range []string{"bash", "zsh", "fish"} {
		built, contents := terminalBootstrap(integration, "/"+shell, stylePath)
		branches.WriteString(shell + ")\n")
		for _, file := range built.files[len(integration.files):] {
			branches.WriteString("printf '%s' " + terminalQuote(contents[file]) + " > " + terminalQuote(file) + " || exit 1\n")
		}
		branches.WriteString(";;\n")
	}
	// List all possible bootstrap files for cancellation/orphan cleanup.
	for _, name := range []string{"bashrc", ".zshenv", ".zprofile", ".zshrc", ".zlogin", "init.fish"} {
		integration.files = append(integration.files, path.Join(integration.directory, name))
	}
	prefix := "__DENGSHELL_" + integration.Nonce + "__"
	metadataScript := ""
	if metadata {
		metadataScript = `printf '\n%s%s\n' ` + terminalQuote(prefix+"SHELL=") + ` "$SHELL"
printf '%s' ` + terminalQuote(prefix+"USER=") + `; id -un
printf '%s' ` + terminalQuote(prefix+"HOST=") + `; hostname -s
`
	}
	script := `umask 077
case "$SHELL" in /*) ;; *) exit 1;; esac
_deng_shell=${SHELL##*/}
case "$_deng_shell" in bash|zsh|fish) ;; *) exit 1;; esac
_deng_dir_owned=0
_deng_style_owned=0
_deng_cleanup() {
 if [ "$_deng_dir_owned" = 1 ]; then
  ` + terminalIntegrationRemoveCommand(terminalIntegration{directory: integration.directory, files: integration.files[2:]}) + `
 fi
 if [ "$_deng_style_owned" = 1 ]; then command rm -f -- ` + terminalQuote(stylePath) + `; fi
}
trap '_deng_cleanup' 0
trap 'exit 1' 1 2 15
command mkdir -m 700 -- ` + terminalQuote(integration.directory) + ` || exit 1
_deng_dir_owned=1
# Noclobber also refuses a pre-existing symlink; never chmod/truncate it.
(set -C; : > ` + terminalQuote(stylePath) + `) || exit 1
_deng_style_owned=1
printf '%s' ` + terminalQuote(initialStyle) + ` > ` + terminalQuote(stylePath) + ` || exit 1
case "$_deng_shell" in
` + branches.String() + `esac
` + metadataScript + `trap - 0 1 2 15
`
	return integration, script
}

// The terminal request stages and starts the login shell in one remote exec.
// Staging uses a subshell so umask, variables and traps cannot leak into the
// user's shell. A failed optional bootstrap falls back to a normal login shell.
func terminalLaunchScript(stagingRoot, initialStyle string) terminalIntegration {
	integration, stage := terminalIntegrationStageScript(stagingRoot, initialStyle, false)
	stylePath := integration.files[0]
	var launch strings.Builder
	launch.WriteString("if (\n" + stage + ") 2>/dev/null; then\n")
	// The successful staging subshell proved ownership. Keep cleanup armed until
	// exec succeeds; never remove collision paths on the fallback branch.
	launch.WriteString("trap " + terminalQuote(terminalIntegrationRemoveCommand(integration)) + " 0\n")
	launch.WriteString("case \"${SHELL##*/}\" in\n")
	for _, shell := range []string{"bash", "zsh", "fish"} {
		built, _ := terminalBootstrap(integration, "/"+shell, stylePath)
		command := strings.Replace(built.command, terminalQuote("/"+shell), "\"$SHELL\"", 1)
		launch.WriteString(shell + ") " + command + ";;\n")
	}
	launch.WriteString("esac\nfi\ncase \"$SHELL\" in /*) exec \"$SHELL\" -l;; *) exec /bin/sh -l;; esac\n")
	integration.command = "exec " + posixShellCommand(launch.String())
	integration.inline = true
	return integration
}

// No remote operation is needed before publishing a terminal-first session.
func (s *Session) prepareTerminalLaunch() terminalIntegration {
	s.promptStyleMu.Lock()
	defer s.promptStyleMu.Unlock()
	return terminalLaunchScript("/tmp", s.promptUsernameColor+"\n"+s.promptHostnameColor+"\n")
}

// Generate the same shell-specific startup hooks for batching and launching.
func terminalBootstrap(integration terminalIntegration, shellPath, stylePath string) (terminalIntegration, map[string]string) {
	shell := path.Base(shellPath)
	integration.Shell = shell
	integration.files = append([]string(nil), integration.files...)
	contents := make(map[string]string)
	write := func(name, body string) {
		filename := path.Join(integration.directory, name)
		integration.files = append(integration.files, filename)
		contents[filename] = body
	}
	styleHook, _ := shellIntegrationAssets.ReadFile("shell_integration/prompt-" + shell + ".sh")
	styleContent := strings.ReplaceAll(string(styleHook), "@DENGSHELL_STYLE_FILE@", stylePath)
	if shell == "bash" {
		body, _ := shellIntegrationAssets.ReadFile("shell_integration/bash.sh")
		content := strings.ReplaceAll(string(body), "@DENGSHELL_NONCE@", integration.Nonce)
		content = strings.ReplaceAll(content, "# @DENGSHELL_PROMPT_STYLE@", styleContent)
		cleanup := "command rm -f -- " + terminalQuote(path.Join(integration.directory, "bashrc")) + " 2>/dev/null\ncommand rmdir -- " + terminalQuote(integration.directory) + " 2>/dev/null || true"
		content = strings.ReplaceAll(content, "# @DENGSHELL_CLEANUP@", cleanup)
		write("bashrc", content)
		integration.command = "exec " + terminalQuote(shellPath) + " --noprofile --rcfile " + terminalQuote(path.Join(integration.directory, "bashrc")) + " -i"
	} else if shell == "zsh" {
		body, _ := shellIntegrationAssets.ReadFile("shell_integration/zshrc.zsh")
		for _, name := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin"} {
			content := ""
			if name == ".zshenv" {
				content = "typeset -g __dengshell_original_zdotdir=${DENGSHELL_ORIGINAL_ZDOTDIR:-$HOME}\nunset DENGSHELL_ORIGINAL_ZDOTDIR\n"
			}
			content += "ZDOTDIR=$__dengshell_original_zdotdir\nif [[ -r $ZDOTDIR/" + name + " ]]; then source \"$ZDOTDIR/" + name + "\"; fi\n__dengshell_original_zdotdir=${ZDOTDIR:-$HOME}\n"
			if name == ".zlogin" {
				cleanup := "command rm -f --"
				for _, temporary := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin"} {
					cleanup += " " + terminalQuote(path.Join(integration.directory, temporary))
				}
				cleanup += " 2>/dev/null\ncommand rmdir -- " + terminalQuote(integration.directory) + " 2>/dev/null || true"
				content += strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(string(body), "@DENGSHELL_NONCE@", integration.Nonce), "# @DENGSHELL_CLEANUP@", cleanup), "# @DENGSHELL_PROMPT_STYLE@", styleContent)
			} else {
				content += "ZDOTDIR=" + terminalQuote(integration.directory) + "\n"
			}
			write(name, content)
		}
		integration.command = "DENGSHELL_ORIGINAL_ZDOTDIR=\"${ZDOTDIR:-$HOME}\" ZDOTDIR=" + terminalQuote(integration.directory) + " exec " + terminalQuote(shellPath) + " -il"
	} else {
		body, _ := shellIntegrationAssets.ReadFile("shell_integration/fish.fish")
		content := strings.ReplaceAll(string(body), "@DENGSHELL_NONCE@", integration.Nonce)
		content = strings.ReplaceAll(content, "# @DENGSHELL_PROMPT_STYLE@", styleContent)
		cleanup := "command rm -f -- " + loginShellQuote(path.Join(integration.directory, "init.fish")) + " 2>/dev/null\ncommand rmdir -- " + loginShellQuote(integration.directory) + " 2>/dev/null; or true"
		content = strings.ReplaceAll(content, "# @DENGSHELL_CLEANUP@", cleanup)
		write("init.fish", content)
		// Fish reads its normal configuration first, then installs only these
		// session-local hooks. No persistent config or universal variable changes.
		integration.command = "exec " + terminalQuote(shellPath) + " -il --init-command " + terminalQuote("source "+loginShellQuote(path.Join(integration.directory, "init.fish")))
	}

	return integration, contents
}

func terminalIntegrationRemoveCommand(integration terminalIntegration) string {
	command := "command rm -f --"
	for _, file := range integration.files {
		command += " " + terminalQuote(file)
	}
	return command + " 2>/dev/null; command rmdir -- " + terminalQuote(integration.directory) + " 2>/dev/null || true"
}

func (s *Session) cleanupTerminalIntegration(integration terminalIntegration) {
	if integration.directory == "" || integration.inline {
		return
	}
	select {
	case terminalIntegrationCleanupSlots <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-terminalIntegrationCleanupSlots }()
		ctx, cancel := context.WithTimeout(s.ctx, time.Second)
		defer cancel()
		_, _ = runMTRScript(ctx, s, terminalIntegrationRemoveCommand(integration), 2048)
	}()
}

func terminalIntegrationReady(integration terminalIntegration) []byte {
	data, _ := json.Marshal(map[string]any{"type": "ready", "message": "", "integration": integration})
	return data
}
