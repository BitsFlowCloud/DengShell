//go:build desktop

package main

import (
	"bytes"
	"cloudshell/internal/app"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

var detachedNoncePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{24,96}$`)

func runDetachedProcess(content fs.FS) error {
	var launch detachedLaunch
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 16384)).Decode(&launch); err != nil {
		return errors.New("独立窗口启动参数无法读取")
	}
	parsed, err := url.Parse(launch.Base)
	if err != nil || parsed.Scheme != "http" || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1") || parsed.Port() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !detachedNoncePattern.MatchString(launch.Nonce) || len(launch.Token) != 48 || !filepath.IsAbs(launch.ConfigDir) {
		return errors.New("独立窗口启动参数无效")
	}
	remote := &remoteDesktopBackend{launch: launch, client: &http.Client{Transport: &http.Transport{Proxy: nil}}}
	var state app.WindowHandoffInfo
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err = remote.request(ctx, "GET", "/api/windows/handoff/"+launch.Nonce+"/check", nil, &state); err != nil {
		return err
	}
	if state.SessionID != launch.SessionID {
		return errors.New("独立窗口会话不匹配")
	}
	if platformWebviewDataPath(launch.ConfigDir) != "" {
		release, err := acquireDetachedWebviewCache(launch.ConfigDir, launch.Nonce)
		if err != nil {
			return fmt.Errorf("无法准备独立窗口缓存：%w", err)
		}
		defer func() { release(); cleanupDetachedWebviewCache(launch.ConfigDir, launch.Nonce) }()
	}
	return runDesktopBackend(remote, content, launch.ConfigDir, launch.Nonce)
}
func (d *Desktop) DetachWindow(input app.WindowHandoffRequest) error {
	if err := d.app.RequireUnlocked(); err != nil {
		return err
	}

	if remote, ok := d.app.(*remoteDesktopBackend); ok {
		return remote.request(d.ctx, "POST", "/api/windows/launch", input, nil)
	}
	return d.launchDetached(d.ctx, input)
}

// A launch occupies a child slot before any remote reservation or process start.
// Update/quit checks share d.mu, so they cannot race past this placeholder.
func (d *Desktop) reserveDetachedStart(nonce string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.updateInstalling || d.quitConfirmed || d.quitPending {
		return errors.New("主窗口正在退出或更新，暂时不能拆分终端")
	}
	if d.children == nil {
		d.children = map[string]*exec.Cmd{}
	}
	if d.pendingWindows == nil {
		d.pendingWindows = map[string]bool{}
	}
	if _, exists := d.children[nonce]; exists {
		return errors.New("此独立窗口正在启动")
	}
	d.children[nonce] = nil
	d.pendingWindows[nonce] = true
	return nil
}
func (d *Desktop) launchDetached(ctx context.Context, input app.WindowHandoffRequest) error {
	if err := d.app.RequireUnlocked(); err != nil {
		return err
	}

	nonce, sessionID := input.Nonce, input.SessionID
	application, ok := d.app.(*app.App)
	if !ok {
		return errors.New("只能由主窗口服务创建独立窗口")
	}
	if err := d.reserveDetachedStart(nonce); err != nil {
		return err
	}
	var command *exec.Cmd
	var ownershipConfirmed atomic.Bool
	keepChild, reserved, attached := false, false, false
	defer func() {
		if attached {
			application.ReleaseWindowSnapshot(nonce)
		}
		if !keepChild && reserved {
			_, _ = application.CancelWindowHandoff(nonce)
		}
		d.mu.Lock()
		delete(d.pendingWindows, nonce)
		if child, exists := d.children[nonce]; !keepChild && exists && (child == nil || child == command) {
			delete(d.children, nonce)
		}
		childPresent := d.children[nonce] != nil
		// A failed launch must never terminate an owner which may just have resumed.
		last := attached && len(d.children) == 0 && d.windowClosed
		if last {
			d.quitConfirmed = true
		}
		d.mu.Unlock()
		if reserved && !childPresent {
			application.ForgetWindowHandoff(nonce)
		}
		if last && d.ctx != nil {
			runtime.Quit(d.ctx)
		}
	}()
	if _, err := application.ReserveWindowHandoff(input); err != nil {
		return err
	}
	reserved = true
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	launch := detachedLaunch{Base: application.URL(), Token: application.Token(), ConfigDir: application.ConfigDirectory(), Nonce: nonce, SessionID: sessionID}
	data, _ := json.Marshal(launch)
	command = exec.Command(executable, "--detached-window")
	command.Stdin = bytes.NewReader(data)
	// Credentials are sent through an inherited pipe, never process arguments or disk.
	command.Dir = filepath.Dir(executable)
	if err = command.Start(); err != nil {
		return fmt.Errorf("无法启动独立窗口：%w", err)
	}
	d.mu.Lock()
	d.children[nonce] = command
	d.mu.Unlock()
	exited := make(chan error, 1)
	go func() {
		err := command.Wait()
		if platformWebviewDataPath(launch.ConfigDir) != "" {
			go cleanupDetachedWebviewCache(launch.ConfigDir, nonce)
		}
		exited <- err
		d.mu.Lock()
		owned := d.children[nonce] == command
		if owned {
			delete(d.children, nonce)
		}
		pending := d.pendingWindows[nonce]
		last := owned && !pending && ownershipConfirmed.Load() && len(d.children) == 0 && d.windowClosed
		if last {
			d.quitConfirmed = true
		}
		d.mu.Unlock()
		if !pending {
			application.ForgetWindowHandoff(nonce)
		}
		if last && d.ctx != nil {
			runtime.Quit(d.ctx)
		}
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	accepted := make(chan error, 1)
	go func() { accepted <- application.WaitTerminalHandoff(waitCtx, sessionID, nonce) }()
	cancelLaunch := func(cause error) error {
		ownershipTransferred, cancelErr := application.CancelWindowHandoff(nonce)
		if ownershipTransferred {
			keepChild = true
			attached = true
			ownershipConfirmed.Store(true)
			return nil
		}
		if cancelErr != nil {
			// An ACK may have won the race. Never kill a child without proof that
			// the relay atomically returned ownership to the source window.
			keepChild = true
			return fmt.Errorf("DENGSHELL_HANDOFF_UNCERTAIN:交接结果暂时无法确认，已保留独立窗口，请检查窗口状态：%w", cancelErr)
		}
		_ = command.Process.Kill()
		return fmt.Errorf("独立窗口未能接手，原终端已恢复：%w", cause)
	}
	select {
	case err = <-accepted:
		if err != nil {
			return cancelLaunch(err)
		}
		keepChild = true
		attached = true
		ownershipConfirmed.Store(true)
		return nil
	case <-exited:
		return cancelLaunch(errors.New("独立窗口已退出"))
	case <-waitCtx.Done():
		return cancelLaunch(waitCtx.Err())
	}
}
func (d *Desktop) WindowSessionMode() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return map[string]any{"detached": d.detachedNonce != "", "independentWindows": len(d.children)}
}
func (d *Desktop) watchParent(ctx context.Context) {
	remote, ok := d.app.(*remoteDesktopBackend)
	if !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		failures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probe, cancel := context.WithTimeout(ctx, 2*time.Second)
				err := remote.request(probe, "GET", "/api/windows/alive", nil, nil)
				cancel()
				if err != nil {
					failures++
				} else {
					failures = 0
				}
				if failures >= 3 {
					d.mu.Lock()
					d.quitConfirmed = true
					d.mu.Unlock()
					runtime.Quit(ctx)
					return
				}
			}
		}
	}()
}
func detachedInstanceID(configDir, nonce string) string {
	if nonce == "" {
		return desktopInstanceID(configDir)
	}
	return desktopInstanceID(configDir) + "-window-" + strings.ReplaceAll(nonce, "-", "")
}

func desktopWebviewPath(configDir, nonce string) string {
	if nonce == "" {
		return platformWebviewDataPath(configDir)
	}
	return platformWebviewDataPath(filepath.Join(configDir, "window-cache", nonce))
}
