//go:build desktop

package main

import (
	"cloudshell/internal/app"
	"context"
	"errors"
	"github.com/gorilla/websocket"
	"net/url"
	"time"
)

type WindowDropResult struct {
	Target    *app.WindowViewInfo `json:"target"`
	Supported bool                `json:"supported"`
	Reason    string              `json:"reason,omitempty"`
}

func (d *Desktop) RegisterWindowView(input app.WindowViewUpdate) (app.WindowViewInfo, error) {
	handle, visible := platformWindowViewIdentity()
	input.NativeHandle = handle
	if handle != "" {
		input.Visible = input.Visible && visible
	}
	d.mu.Lock()
	input.Visible = input.Visible && !d.trayHidden && !d.windowClosed && !d.quitConfirmed
	d.mu.Unlock()
	if local, ok := d.app.(*app.App); ok {
		return local.RegisterWindowView(input)
	}
	remote, ok := d.app.(*remoteDesktopBackend)
	if !ok {
		return app.WindowViewInfo{}, errors.New("无法注册当前窗口")
	}
	ctx, cancel := context.WithTimeout(d.ctx, 3*time.Second)
	defer cancel()
	var result app.WindowViewInfo
	err := remote.request(ctx, "POST", "/api/windows/views", input, &result)
	return result, err
}
func (d *Desktop) WindowDropTarget(sourceID string) (WindowDropResult, error) {
	result := WindowDropResult{}
	handles, supported, reason := platformWindowPointerTargets()
	result.Supported = supported
	result.Reason = reason
	if !supported || len(handles) == 0 {
		return result, nil
	}
	var views []app.WindowViewInfo
	if local, ok := d.app.(*app.App); ok {
		views = local.WindowViews()
	} else if remote, ok := d.app.(*remoteDesktopBackend); ok {
		ctx, cancel := context.WithTimeout(d.ctx, time.Second)
		defer cancel()
		if err := remote.request(ctx, "GET", "/api/windows/views", nil, &views); err != nil {
			return result, err
		}
	}
	for _, handle := range handles {
		for i := range views {
			v := views[i]
			if v.ID != sourceID && v.Visible && v.NativeHandle != "" && v.NativeHandle == handle {
				result.Target = &v
				return result, nil
			}
		}
	}
	return result, nil
}

// A renderer may receive any number of handoffs, including a session it once
// owned. The per-transfer nonce is validated by the backend relay, independent
// of the process's original launch parameters.
func (d *Desktop) OpenTerminalWithHandoff(sessionID, nonce string) error {
	if nonce != "" {
		if !detachedNoncePattern.MatchString(nonce) {
			return errors.New("终端交接凭据无效")
		}
		var value app.WindowHandoffInfo
		if local, ok := d.app.(*app.App); ok {
			h, err := local.WindowHandoff(nonce)
			if err != nil {
				return err
			}
			if h.SessionID != sessionID || !local.TerminalHandoffReady(sessionID, nonce) {
				return errors.New("SSH 标签交接已结束或与当前会话不匹配")
			}
		} else if remote, ok := d.app.(*remoteDesktopBackend); ok {
			if err := remote.request(d.ctx, "GET", "/api/windows/handoff/"+url.PathEscape(nonce)+"/check", nil, &value); err != nil {
				return err
			}
			if value.SessionID != sessionID {
				return errors.New("SSH 标签交接与当前会话不匹配")
			}
		}
	}
	return d.openTerminalSocket(sessionID, nonce)
}

// Generation-aware bridge calls cannot act on a tab that migrated away and
// returned while an older renderer's async cleanup/input was still queued.
func (d *Desktop) SendTerminalWithHandoff(sessionID, nonce, data string) error {
	d.mu.Lock()
	terminal := d.terminals[sessionID]
	d.mu.Unlock()
	if terminal == nil || terminal.handoff != nonce {
		return errors.New("此终端窗口已交接或关闭")
	}
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	terminal.socket.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return terminal.socket.WriteMessage(websocket.TextMessage, []byte(data))
}
func (d *Desktop) CloseTerminalWithHandoff(sessionID, nonce string) {
	d.mu.Lock()
	terminal := d.terminals[sessionID]
	if terminal != nil && terminal.handoff == nonce {
		delete(d.terminals, sessionID)
	} else {
		terminal = nil
	}
	d.mu.Unlock()
	if terminal != nil {
		terminal.socket.Close()
	}
}
