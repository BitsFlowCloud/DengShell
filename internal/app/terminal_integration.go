package app

import (
	"context"
	"embed"
	"encoding/json"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

//go:embed shell_integration/*
var shellIntegrationAssets embed.FS

// An optional integration must never hold terminal startup hostage to a server
// that accepts SSH keepalives but stops answering SFTP or channel-open requests.
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
}

func terminalQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// The SSH exec request starts an interactive shell with a temporary bootstrap.
// Nothing is injected as keystrokes, and no remote profile/rc file is modified.
func (s *Session) prepareTerminalIntegration() terminalIntegration {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
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

func (s *Session) prepareTerminalIntegrationFiles(ctx context.Context, stagingRoot string) terminalIntegration {
	output, err := runMTRScript(ctx, s, `printf '\n__DENGSHELL_SHELL__%s\n' "$SHELL"; printf '__DENGSHELL_USER__'; id -un; printf '__DENGSHELL_HOST__'; hostname -s`, 4096)
	if err != nil {
		return terminalIntegration{}
	}
	var shellPath, username, hostname string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "__DENGSHELL_USER__") {
			username = strings.TrimPrefix(line, "__DENGSHELL_USER__")
		}
		if strings.HasPrefix(line, "__DENGSHELL_HOST__") {
			hostname = strings.TrimPrefix(line, "__DENGSHELL_HOST__")
		}
		if strings.HasPrefix(line, "__DENGSHELL_SHELL__") {
			shellPath = strings.TrimPrefix(line, "__DENGSHELL_SHELL__")
		}
	}
	if !strings.HasPrefix(shellPath, "/") || strings.ContainsAny(shellPath, "\x00\r\n") {
		return terminalIntegration{}
	}
	shell := path.Base(shellPath)
	if shell != "bash" && shell != "zsh" {
		return terminalIntegration{}
	}
	if ctx.Err() != nil {
		return terminalIntegration{}
	}
	integration := terminalIntegration{Shell: shell, Nonce: randomID(), Username: username, Hostname: hostname, directory: path.Join(stagingRoot, "dengshell-session-"+randomID())}
	if err = s.files.Mkdir(integration.directory); err != nil {
		return terminalIntegration{}
	}
	if err = s.files.Chmod(integration.directory, 0700); err != nil {
		s.cleanupTerminalIntegration(integration)
		return terminalIntegration{}
	}
	write := func(name, body string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		filePath := path.Join(integration.directory, name)
		integration.files = append(integration.files, filePath)
		file, err := s.files.Create(filePath)
		if err != nil {
			return err
		}
		if err = file.Chmod(0600); err != nil {
			file.Close()
			return err
		}
		_, err = io.WriteString(file, body)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	// Keep only this small session-owned file after bootstrap self-cleanup.
	// Shell hooks read data, never source/eval the file as code.
	stylePath := path.Join(stagingRoot, "dengshell-prompt-"+integration.Nonce)
	s.promptStyleMu.Lock()
	initialStyle := s.promptUsernameColor + "\n" + s.promptHostnameColor + "\n"
	s.promptStyleMu.Unlock()
	styleFile, styleErr := s.files.OpenFile(stylePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if styleErr == nil {
		integration.files = append(integration.files, stylePath, stylePath+".new")
		styleErr = styleFile.Chmod(0600)
		if styleErr == nil {
			_, styleErr = io.WriteString(styleFile, initialStyle)
		}
		closeErr := styleFile.Close()
		if styleErr == nil {
			styleErr = closeErr
		}
	}
	if styleErr != nil {
		s.cleanupTerminalIntegration(integration)
		return terminalIntegration{}
	}
	styleHook, _ := shellIntegrationAssets.ReadFile("shell_integration/prompt-" + shell + ".sh")
	styleContent := strings.ReplaceAll(string(styleHook), "@DENGSHELL_STYLE_FILE@", stylePath)
	if shell == "bash" {
		body, _ := shellIntegrationAssets.ReadFile("shell_integration/bash.sh")
		content := strings.ReplaceAll(string(body), "@DENGSHELL_NONCE@", integration.Nonce)
		content = strings.ReplaceAll(content, "# @DENGSHELL_PROMPT_STYLE@", styleContent)
		cleanup := "command rm -f -- " + terminalQuote(path.Join(integration.directory, "bashrc")) + " 2>/dev/null\ncommand rmdir -- " + terminalQuote(integration.directory) + " 2>/dev/null || true"
		content = strings.ReplaceAll(content, "# @DENGSHELL_CLEANUP@", cleanup)
		if err = write("bashrc", content); err == nil {
			integration.command = "exec " + terminalQuote(shellPath) + " --noprofile --rcfile " + terminalQuote(path.Join(integration.directory, "bashrc")) + " -i"
		}
	} else {
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
			if err = write(name, content); err != nil {
				break
			}
		}
		if err == nil {
			integration.command = "DENGSHELL_ORIGINAL_ZDOTDIR=\"${ZDOTDIR:-$HOME}\" ZDOTDIR=" + terminalQuote(integration.directory) + " exec " + terminalQuote(shellPath) + " -il"
		}
	}
	if err != nil {
		s.cleanupTerminalIntegration(integration)
		return terminalIntegration{}
	}
	if ctx.Err() != nil {
		s.cleanupTerminalIntegration(integration)
		return terminalIntegration{}
	}
	s.promptStyleMu.Lock()
	if current := s.promptUsernameColor + "\n" + s.promptHostnameColor + "\n"; current != initialStyle {
		// Preferences may change while optional bootstrap files are being staged.
		// Commit the newest requested colors before making this file addressable.
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

func (s *Session) cleanupTerminalIntegration(integration terminalIntegration) {
	if integration.directory == "" {
		return
	}
	select {
	case terminalIntegrationCleanupSlots <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-terminalIntegrationCleanupSlots }()
		for _, file := range integration.files {
			_ = s.files.Remove(file)
		}
		_ = s.files.RemoveDirectory(integration.directory)
	}()
}

func terminalIntegrationReady(integration terminalIntegration) []byte {
	data, _ := json.Marshal(map[string]any{"type": "ready", "message": "", "integration": integration})
	return data
}
