//go:build desktop

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var aiWindowIDPattern = regexp.MustCompile(`^[0-9a-f]{48}$`)

type aiWindowLaunch struct {
	Base       string `json:"base"`
	Token      string `json:"token"`
	ConfigDir  string `json:"configDir"`
	AIWindowID string `json:"aiWindowId"`
}

type aiWindowControl struct {
	Action string `json:"action"`
}

// AI windows mirror their owner's state; they never own an SSH PTY or enter
// the terminal handoff lifecycle. The inherited pipe also detects owner exit.
type aiWindowProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	done    chan struct{}
	mu      sync.Mutex
	writeMu sync.Mutex
}

func (p *aiWindowProcess) send(value any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.mu.Lock()
	input := p.input
	p.mu.Unlock()
	if input == nil {
		return errors.New("AI 窗口已关闭")
	}
	if file, ok := input.(*os.File); ok {
		_ = file.SetWriteDeadline(time.Now().Add(time.Second))
	}
	return json.NewEncoder(input).Encode(value)
}

func (p *aiWindowProcess) closeInput() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.input != nil {
		_ = p.input.Close()
		p.input = nil
	}
}

func validateAIWindowLaunch(launch aiWindowLaunch) error {
	parsed, err := url.Parse(launch.Base)
	if err != nil {
		return errors.New("AI 窗口启动参数无效")
	}
	port, portErr := strconv.Atoi(parsed.Port())
	if parsed.Scheme != "http" || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1") || portErr != nil || port < 1 || port > 65535 || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !aiWindowIDPattern.MatchString(launch.AIWindowID) || !aiWindowIDPattern.MatchString(launch.Token) || !filepath.IsAbs(launch.ConfigDir) {
		return errors.New("AI 窗口启动参数无效")
	}
	return nil
}

func decodeAIWindowLaunch(input io.Reader) (aiWindowLaunch, *json.Decoder, error) {
	reader := bufio.NewReaderSize(input, 16384)
	line, err := reader.ReadSlice('\n')
	var launch aiWindowLaunch
	if err != nil || json.Unmarshal(line, &launch) != nil {
		return launch, nil, errors.New("AI 窗口启动参数无法读取")
	}
	if err := validateAIWindowLaunch(launch); err != nil {
		return launch, nil, err
	}
	// Reuse this buffered reader: it may already contain an immediate focus or
	// close command following the launch JSON. Limit only the initial payload.
	return launch, json.NewDecoder(reader), nil
}

func aiRemoteBackend(launch aiWindowLaunch) *remoteDesktopBackend {
	return &remoteDesktopBackend{
		launch: detachedLaunch{Base: launch.Base, Token: launch.Token, ConfigDir: launch.ConfigDir},
		client: &http.Client{Transport: &http.Transport{Proxy: nil}},
	}
}

func aiWindowProbe(remote *remoteDesktopBackend, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var state struct {
		Closed bool `json:"closed"`
	}
	if err := remote.request(ctx, http.MethodGet, "/api/ai/windows/"+id+"?side=assistant&after=0&probe=1", nil, &state); err != nil {
		return err
	}
	if state.Closed {
		return errors.New("AI 对话已关闭，请重新打开")
	}
	return nil
}

func closeAIWindowBroker(remote *remoteDesktopBackend, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = remote.request(ctx, http.MethodDelete, "/api/ai/windows/"+id, nil, nil)
}

func (d *Desktop) OpenAIWindow(id string) error {
	if d.aiWindowID != "" || !aiWindowIDPattern.MatchString(id) {
		return errors.New("AI 窗口标识无效")
	}
	if err := d.app.RequireUnlocked(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.updateInstalling || d.quitConfirmed || d.quitPending || d.windowClosed {
		d.mu.Unlock()
		return errors.New("当前窗口正在关闭或更新，暂时无法打开 AI")
	}
	existing := d.aiChildren[id]
	d.mu.Unlock()
	if existing != nil {
		return existing.send(aiWindowControl{Action: "focus"})
	}
	launch := aiWindowLaunch{Base: d.app.URL(), Token: d.app.Token(), ConfigDir: d.app.ConfigDirectory(), AIWindowID: id}
	remote := aiRemoteBackend(launch)
	defer remote.client.CloseIdleConnections()
	if err := aiWindowProbe(remote, id); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	d.mu.Lock()
	if d.updateInstalling || d.quitConfirmed || d.quitPending || d.windowClosed {
		d.mu.Unlock()
		return errors.New("当前窗口正在关闭或更新，暂时无法打开 AI")
	}
	if existing := d.aiChildren[id]; existing != nil {
		d.mu.Unlock()
		return existing.send(aiWindowControl{Action: "focus"})
	}
	command := exec.Command(executable, "--ai-window")
	command.Dir = filepath.Dir(executable)
	input, err := command.StdinPipe()
	if err != nil {
		d.mu.Unlock()
		return err
	}
	child := &aiWindowProcess{command: command, input: input, done: make(chan struct{})}
	// Hold the lifecycle lock through Start so shutdown cannot miss a process
	// that was reserved but not yet created. No credentials enter argv or disk.
	if err = command.Start(); err != nil {
		d.mu.Unlock()
		child.closeInput()
		return fmt.Errorf("无法启动 AI 窗口：%w", err)
	}
	if d.aiChildren == nil {
		d.aiChildren = map[string]*aiWindowProcess{}
	}
	d.aiChildren[id] = child
	// The first pipe message must precede any repeated-open focus command.
	err = child.send(launch)
	d.mu.Unlock()
	go func() {
		_ = command.Wait()
		child.closeInput()
		close(child.done)
		closeAIWindowBroker(remote, id)
		remote.client.CloseIdleConnections()
		d.mu.Lock()
		if d.aiChildren[id] == child {
			delete(d.aiChildren, id)
		}
		d.mu.Unlock()
		if platformWebviewDataPath(launch.ConfigDir) != "" {
			cleanupDetachedWebviewCache(launch.ConfigDir, "ai-"+id)
		}
	}()
	if err != nil {
		_ = command.Process.Kill()
		return fmt.Errorf("无法传递 AI 窗口启动参数：%w", err)
	}
	return nil
}

func (d *Desktop) closeAIWindows() {
	d.mu.Lock()
	d.quitConfirmed = true
	d.mu.Unlock()
	d.closeOwnedAIWindows()
}

// Closing the owner's visible workspace also closes its AI mirror, including
// when the process must keep serving detached SSH terminals in the background.
// Do not mark that still-running SSH service as confirmed for process exit.
func (d *Desktop) closeOwnedAIWindows() {
	d.mu.Lock()
	children := make([]*aiWindowProcess, 0, len(d.aiChildren))
	for _, child := range d.aiChildren {
		children = append(children, child)
	}
	d.mu.Unlock()
	for _, child := range children {
		// EOF is the child's normal close signal. Closing the pipe also wakes a
		// stalled control write, so a frozen child cannot block owner shutdown.
		child.closeInput()
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for _, child := range children {
		select {
		case <-child.done:
		case <-deadline.C:
			for _, remaining := range children {
				select {
				case <-remaining.done:
				default:
					_ = remaining.command.Process.Kill()
				}
			}
			return
		}
	}
}

func (d *Desktop) SetAIWindowAlwaysOnTop(value bool) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.aiWindowID == "" || d.ctx == nil {
		return false, errors.New("置顶仅适用于独立 AI 窗口")
	}
	runtime.WindowSetAlwaysOnTop(d.ctx, value)
	d.aiAlwaysOnTop = value
	return d.aiAlwaysOnTop, nil
}

type aiWindowAssets struct{ fs.FS }

func (assets aiWindowAssets) Open(name string) (fs.File, error) {
	if name == "index.html" {
		name = "ai-window.html"
	}
	return assets.FS.Open(name)
}

func aiWindowAssetHandler(remote *remoteDesktopBackend, assets fs.FS, id string) http.Handler {
	fallback := nativeAssetHandler(remote, assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			content, err := fs.ReadFile(assets, "ai-window.html")
			if err != nil {
				http.Error(w, "AI 窗口界面不可用", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(content)
			return
		}
		if r.URL.Path != "/boot.js" {
			fallback.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, remote.URL()+"/boot.js", nil)
		request.Header.Set("X-CloudShell-Token", remote.Token())
		response, err := remote.client.Do(request)
		if err != nil {
			http.Error(w, "主窗口服务已关闭", http.StatusServiceUnavailable)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			http.Error(w, "AI 窗口启动凭据已失效", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = io.Copy(w, io.LimitReader(response.Body, 4<<20))
		encodedID, _ := json.Marshal(id)
		fmt.Fprintf(w, "\nwindow.CLOUDSHELL.aiWindowId=%s;window.CLOUDSHELL.startupAnimation=false;", encodedID)
	})
}

func initialAIWindowBounds(bounds initialWindowBounds) initialWindowBounds {
	width, height := min(450, bounds.WorkWidth), min(760, bounds.WorkHeight)
	bounds.X += (bounds.Width - width) / 2
	bounds.Y += (bounds.Height - height) / 2
	bounds.Width, bounds.Height = width, height
	bounds.MinWidth, bounds.MinHeight = min(320, width), min(420, height)
	return bounds
}

func readAIWindowControls(decoder *json.Decoder, dispatch func(string)) {
	for {
		var control aiWindowControl
		if err := decoder.Decode(&control); err != nil {
			dispatch("close")
			return
		}
		if control.Action == "focus" || control.Action == "close" {
			dispatch(control.Action)
			if control.Action == "close" {
				return
			}
		}
	}
}

func runAIWindowProcess(content fs.FS) error {
	launch, controls, err := decodeAIWindowLaunch(os.Stdin)
	if err != nil {
		return err
	}
	remote := aiRemoteBackend(launch)
	defer remote.client.CloseIdleConnections()
	if err := aiWindowProbe(remote, launch.AIWindowID); err != nil {
		return err
	}
	defer closeAIWindowBroker(remote, launch.AIWindowID)
	cacheNonce := "ai-" + launch.AIWindowID
	if platformWebviewDataPath(launch.ConfigDir) != "" {
		release, err := acquireDetachedWebviewCache(launch.ConfigDir, cacheNonce)
		if err != nil {
			return fmt.Errorf("无法准备 AI 窗口缓存：%w", err)
		}
		defer func() { release(); cleanupDetachedWebviewCache(launch.ConfigDir, cacheNonce) }()
	}
	return runAIWindowDesktop(remote, content, launch, controls)
}

func runAIWindowDesktop(remote *remoteDesktopBackend, content fs.FS, launch aiWindowLaunch, controls *json.Decoder) error {
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()
	preparePlatformWindow()
	initial, err := initialPlatformWindow()
	if err != nil {
		return err
	}
	initial = initialAIWindowBounds(initial)
	desktop := &Desktop{app: remote, aiWindowID: launch.AIWindowID}
	assets := aiWindowAssets{content}
	var lifetimeCancel context.CancelFunc
	return wails.Run(&options.App{
		Title: "DengShell AI", Width: initial.Width, Height: initial.Height,
		MinWidth: initial.MinWidth, MinHeight: initial.MinHeight,
		Frameless:        true,
		StartHidden:      goruntime.GOOS == "windows",
		BackgroundColour: options.NewRGB(240, 243, 247),
		AssetServer:      &assetserver.Options{Assets: assets, Handler: aiWindowAssetHandler(remote, assets, launch.AIWindowID)},
		Bind:             []interface{}{desktop},
		OnBeforeClose:    desktop.beforeClose,
		OnStartup: func(ctx context.Context) {
			desktop.ctx = ctx
			lifetime, cancel := context.WithCancel(ctx)
			lifetimeCancel = cancel
			desktop.watchParent(lifetime)
			go readAIWindowControls(controls, func(action string) {
				if lifetime.Err() != nil {
					return
				}
				if action == "focus" {
					desktop.RestoreWindow()
				} else {
					runtime.Quit(ctx)
				}
			})
		},
		OnDomReady: func(ctx context.Context) {
			installPlatformWindowIcon()
			finalizeInitialPlatformWindow(ctx, initial)
		},
		OnShutdown: func(context.Context) {
			if lifetimeCancel != nil {
				lifetimeCancel()
			}
			closeAIWindowBroker(remote, launch.AIWindowID)
		},
		Linux:   &linux.Options{ProgramName: "dengshell-ai", Icon: desktopIcon, WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand},
		Mac:     &mac.Options{DisableZoom: true, DisableEscapeExitsFullscreen: true},
		Windows: &windows.Options{WindowClassName: "DengShellWindow", IsZoomControlEnabled: false, DisablePinchZoom: true, WebviewUserDataPath: desktopWebviewPath(launch.ConfigDir, "ai-"+launch.AIWindowID)},
	})
}

// Compile-time check that the index adapter does not change the filesystem
// contract expected by Wails' asset server.
var _ fs.FS = aiWindowAssets{}
