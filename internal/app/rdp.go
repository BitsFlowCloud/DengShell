package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cloudshell/internal/rdpengine"
)

type rdpState struct {
	mu       sync.Mutex
	sessions map[string]*rdpSession
	wg       sync.WaitGroup
	closed   bool
	engine   func() (string, error) // per-App override for isolated integration tests
	command  func(context.Context, string, ...string) *exec.Cmd
}
type RDPInfo struct {
	ID               string    `json:"id"`
	ProfileID        string    `json:"profileId"`
	Name             string    `json:"name"`
	Status           string    `json:"status"`
	Message          string    `json:"message"`
	GraphicsPipeline bool      `json:"graphicsPipeline,omitempty"`
	RemoteWidth      int       `json:"remoteWidth,omitempty"`
	RemoteHeight     int       `json:"remoteHeight,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}
type rdpSession struct {
	mu                          sync.Mutex
	info                        RDPInfo
	cmd                         *exec.Cmd
	cancel                      context.CancelFunc
	bridge                      *rdpBridge
	parent                      uintptr
	hwnd                        uintptr
	displayWidth, displayHeight int
	displaySent                 bool
}

func (s *rdpSession) snapshot() RDPInfo { s.mu.Lock(); defer s.mu.Unlock(); return s.info }
func (s *rdpSession) stop(message string) {
	s.mu.Lock()
	if s.info.Status == "connecting" || s.info.Status == "connected" {
		s.info.Status = "disconnected"
		s.info.Message = message
	}
	s.mu.Unlock()
	s.cancel()
}
func normalizeRDPProfile(p *Profile) error {
	p.Protocol = strings.ToLower(strings.TrimSpace(p.Protocol))
	if p.Protocol == "ssh" {
		p.Protocol = ""
	}
	if p.Protocol == "" {
		p.Domain = ""
		p.RDPClipboard = false
		return nil
	}
	if p.Protocol != "rdp" {
		return errors.New("连接类型应为 SSH 或 RDP")
	}
	if p.Auth != "" && p.Auth != "password" {
		return errors.New("RDP 连接使用用户名和密码认证")
	}
	p.Auth = "password"
	p.KeyID = ""
	p.KeyPath = ""
	p.Domain = strings.TrimSpace(p.Domain)
	if len(p.Domain) > 255 || len(p.User) > 256 || len(p.Secret) > 4096 {
		return errors.New("RDP 用户名、域或密码过长")
	}
	for _, v := range []string{p.Name, p.Host, p.User, p.Domain, p.Secret} {
		if err := rdpSafeLine(v); err != nil {
			return err
		}
	}
	return nil
}

func rdpArguments(p Profile, password, address, token string) (string, error) {
	if err := normalizeRDPProfile(&p); err != nil {
		return "", err
	}
	if len(password) > 4096 {
		return "", errors.New("RDP 密码过长")
	}
	if err := rdpSafeLine(password); err != nil {
		return "", err
	}
	args := []string{"/v:" + net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), "/u:" + p.User, "/p:" + password,
		"/proxy:socks5://dengshell:" + token + "@" + address, "/size:1280x800", "/dynamic-resolution", "/gdi:sw", "/gfx:progressive,RFX,small-cache:off", "/bpp:32", "+fonts", "/title:DengShell RDP · " + p.Name, "/log-level:ERROR", "/timeout:15000", "-auto-reconnect", "-multitransport"}
	if p.Domain != "" {
		args = append(args, "/d:"+p.Domain)
	}
	if p.RDPClipboard {
		args = append(args, "+clipboard")
	} else {
		args = append(args, "-clipboard")
	}
	return strings.Join(args, "\n") + "\n", nil
}

func (a *App) startRDP(profileID, password string) (RDPInfo, error) {
	return a.startRDPEmbedded(profileID, password, 0)
}
func (a *App) StartRDPEmbedded(profileID, password string, parent uintptr) (RDPInfo, error) {
	if parent == 0 {
		return RDPInfo{}, errors.New("RDP 标签页需要 Windows 桌面窗口")
	}
	if err := validateRDPParent(parent); err != nil {
		return RDPInfo{}, err
	}
	return a.startRDPEmbedded(profileID, password, parent)
}
func (a *App) startRDPEmbedded(profileID, password string, parent uintptr) (RDPInfo, error) {
	if err := a.RequireUnlocked(); err != nil {
		return RDPInfo{}, err
	}
	p, err := a.store.Get(profileID)
	if err != nil {
		return RDPInfo{}, err
	}
	if p.Protocol != "rdp" {
		return RDPInfo{}, errors.New("请选择 RDP 连接")
	}
	if p.NeedsProxy {
		return RDPInfo{}, errors.New("请先为此连接选择代理或确认直连")
	}
	if password == "" {
		password = p.Secret
	}
	if password == "" {
		return RDPInfo{}, errors.New("请输入远程桌面密码")
	}
	if p.ProxyID != "" {
		p.Proxy, err = a.store.ResolveProxy(p.ProxyID)
		if err != nil {
			return RDPInfo{}, err
		}
	}
	a.rdp.mu.Lock()
	defer a.rdp.mu.Unlock()
	if a.rdp.closed || a.ctx.Err() != nil {
		return RDPInfo{}, errors.New("应用正在退出")
	}
	active := 0
	for _, s := range a.rdp.sessions {
		if status := s.snapshot().Status; status == "connected" || status == "connecting" {
			active++
		}
	}
	if active >= 16 {
		return RDPInfo{}, errors.New("RDP 测试版最多同时打开 16 个会话")
	}
	prepare := a.rdp.engine
	if prepare == nil {
		prepare = rdpengine.Prepare
	}
	engine, err := prepare()
	if err != nil {
		return RDPInfo{}, err
	}
	if _, err = a.store.Get(profileID); err != nil {
		return RDPInfo{}, err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	bridge, err := newRDPBridge(ctx, net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), p.Proxy)
	if err != nil {
		cancel()
		return RDPInfo{}, err
	}
	args, err := rdpArguments(p, password, bridge.listener.Addr().String(), bridge.token)
	if err != nil {
		cancel()
		bridge.close()
		return RDPInfo{}, err
	}
	command := a.rdp.command
	if command == nil {
		command = exec.CommandContext
	}
	if parent != 0 {
		args += fmt.Sprintf("/parent-window:%d\n-toggle-fullscreen\n", parent) + rdpHostDisplayArguments(parent)
	}
	cmd := command(ctx, engine, "/args-from:stdin")
	cmd.Stdin = strings.NewReader(args)
	// Only explicit lifecycle markers are retained; engine diagnostics may include
	// addresses, account names, or server-supplied text and are never logged here.
	cmd.Stderr = io.Discard
	configureRDPProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		bridge.close()
		return RDPInfo{}, err
	}
	if err = cmd.Start(); err != nil {
		cancel()
		bridge.close()
		return RDPInfo{}, errors.New("无法启动内置 RDP 引擎，请检查安全软件是否拦截")
	}
	releaseJob, err := ownRDPProcess(cmd)
	if err != nil {
		cancel()
		_ = cmd.Wait()
		bridge.close()
		return RDPInfo{}, err
	}
	s := &rdpSession{parent: parent, cmd: cmd, cancel: cancel, bridge: bridge, info: RDPInfo{ID: randomID(), ProfileID: p.ID, Name: p.Name, Status: "connecting", Message: "🔗  正在连接远程桌面…", CreatedAt: time.Now().UTC()}}
	if a.rdp.sessions == nil {
		a.rdp.sessions = make(map[string]*rdpSession)
	}
	// Keep a bounded recent-session list without terminating active connections.
	if len(a.rdp.sessions) >= 64 {
		for id, old := range a.rdp.sessions {
			if status := old.snapshot().Status; status != "connected" && status != "connecting" {
				delete(a.rdp.sessions, id)
			}
		}
	}
	a.rdp.sessions[s.info.ID] = s
	a.rdp.wg.Add(1)
	go func() {
		defer a.rdp.wg.Done()
		defer releaseJob()
		defer cancel()
		defer bridge.close()
		finished := make(chan struct{})
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-finished:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					if a.SecurityLockStatus().Locked {
						s.stop("🔒  已安全断开 RDP，解锁后可重新连接")
						return
					}
				}
			}
		}()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "DENGSHELL_RDP_SIZE:") {
				if width, height, ok := rdpDisplaySize(line); ok {
					s.mu.Lock()
					s.info.RemoteWidth, s.info.RemoteHeight = width, height
					s.mu.Unlock()
				}
				continue
			}
			switch line {
			case "DENGSHELL_RDP_READY":
				s.mu.Lock()
				if s.info.Status == "connecting" {
					s.info.Status = "connected"
					s.info.Message = "✅  远程桌面已连接"
				}
				s.mu.Unlock()
				_ = a.store.MarkConnected(p.ID, time.Now().UTC())
			case "DENGSHELL_RDP_GFX":
				s.mu.Lock()
				s.info.GraphicsPipeline = true
				s.mu.Unlock()
			case "DENGSHELL_RDP_ACTIVITY":
				a.LockActivity()
			}
		}
		err := cmd.Wait()
		close(finished)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.info.Status == "disconnected" {
			return
		}
		if err != nil && ctx.Err() == nil {
			code := -1
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
			s.info.Status, s.info.Message = rdpExitStatus(code, s.info.Status == "connected")
			if reason := bridge.failure(); reason != "" {
				s.info.Status = "error"
				s.info.Message = reason
			}
			if s.info.Status == "error" && code >= 0 {
				s.info.Message += fmt.Sprintf("（引擎退出码 %d）", code)
			}
		} else {
			s.info.Status = "disconnected"
			s.info.Message = "🔌  远程桌面已关闭"
		}
	}()
	return s.snapshot(), nil
}

func (a *App) rdpSessions() []RDPInfo {
	a.rdp.mu.Lock()
	defer a.rdp.mu.Unlock()
	items := []RDPInfo{}
	for _, s := range a.rdp.sessions {
		items = append(items, s.snapshot())
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}
func (a *App) stopRDPProfile(id string) {
	a.rdp.mu.Lock()
	defer a.rdp.mu.Unlock()
	for _, s := range a.rdp.sessions {
		if s.info.ProfileID == id {
			s.stop("🔌  此连接已移至回收站")
		}
	}
}
func (a *App) stopRDPLocked() {
	a.rdp.mu.Lock()
	defer a.rdp.mu.Unlock()
	for _, s := range a.rdp.sessions {
		s.stop("🔒  已安全断开 RDP，解锁后可重新连接")
	}
}
func (a *App) closeRDP() {
	a.rdp.mu.Lock()
	a.rdp.closed = true
	for _, s := range a.rdp.sessions {
		s.stop("🔌  应用已退出")
	}
	a.rdp.mu.Unlock()
	a.rdp.wg.Wait()
}
func (a *App) registerRDPHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/rdp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"available": rdpengine.Available(), "engine": "FreeRDP 3.32.0", "sessions": a.rdpSessions()})
	})
	mux.HandleFunc("DELETE /api/rdp/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.rdp.mu.Lock()
		s := a.rdp.sessions[r.PathValue("id")]
		if s != nil {
			s.stop("🔌  已断开远程桌面")
		}
		a.rdp.mu.Unlock()
		writeJSON(w, map[string]bool{"ok": true})
	})
}

func rdpDisplaySize(line string) (int, int, bool) {
	parts := strings.Split(line, ":")
	if len(parts) != 3 || parts[0] != "DENGSHELL_RDP_SIZE" {
		return 0, 0, false
	}
	w, ew := strconv.Atoi(parts[1])
	h, eh := strconv.Atoi(parts[2])
	return w, h, ew == nil && eh == nil && w >= 200 && w <= 8192 && h >= 200 && h <= 8192
}
