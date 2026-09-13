package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type PromptStyle struct {
	UsernameColor string `json:"usernameColor"`
	HostnameColor string `json:"hostnameColor"`
}

func (a *App) registerPromptStyleHTTP(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/{id}/prompt-style", func(w http.ResponseWriter, r *http.Request) {
		var style PromptStyle
		if !decode(w, r, &style) {
			return
		}
		s, err := a.session(r.PathValue("id"))
		if err != nil {
			respond(w, nil, err)
			return
		}
		result, err := s.savePromptStyle(r.Context(), style)
		respond(w, result, err)
	})
}

func (s *Session) savePromptStyle(ctx context.Context, style PromptStyle) (map[string]any, error) {
	for _, color := range []string{style.UsernameColor, style.HostnameColor} {
		if color != "" && !validTerminalColor(color) {
			return nil, errors.New("提示符颜色应为 #RRGGBB 或留空恢复默认")
		}
	}
	s.promptStyleMu.Lock()
	defer s.promptStyleMu.Unlock()
	style.UsernameColor, style.HostnameColor = strings.ToLower(style.UsernameColor), strings.ToLower(style.HostnameColor)
	if s.promptStylePath == "" {
		s.promptUsernameColor, s.promptHostnameColor = style.UsernameColor, style.HostnameColor
		return map[string]any{"supported": false, "deferred": true, "message": "颜色已保存；需要受支持的 Bash/Zsh 提示符集成，下次连接时应用"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.writePromptStyleFile(ctx, s.promptStylePath, style); err != nil {
		return nil, fmt.Errorf("无法更新远端提示符颜色：%w", err)
	}
	s.promptUsernameColor, s.promptHostnameColor = style.UsernameColor, style.HostnameColor
	return map[string]any{"supported": true, "deferred": true, "promptUsername": s.promptUsername, "promptHostname": s.promptHostname, "message": "将在下一个提示符出现时应用"}, nil
}

func (s *Session) writePromptStyleFile(ctx context.Context, filename string, style PromptStyle) error {
	command := "umask 077; printf '%s\\n' " + terminalQuote(style.UsernameColor) + " " + terminalQuote(style.HostnameColor) + " > " + terminalQuote(filename+".new") + " && mv -f -- " + terminalQuote(filename+".new") + " " + terminalQuote(filename)
	_, err := runMTRScript(ctx, s, command, 2048)
	return err
}

func (s *Session) removePromptStyleBeforeClose() {
	// A single best-effort SFTP cleanup is bounded by transport closure. No
	// remote command or keystroke is injected while the terminal is shutting down.
	if s.files == nil {
		return
	}
	if !s.promptStyleMu.TryLock() {
		return
	}
	stylePath := s.promptStylePath
	s.promptStylePath = ""
	s.promptStyleMu.Unlock()
	if stylePath == "" {
		return
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = s.files.Remove(stylePath); _ = s.files.Remove(stylePath + ".new") }()
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}
