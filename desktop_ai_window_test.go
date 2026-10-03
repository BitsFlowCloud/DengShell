//go:build desktop

package main

import (
	"bytes"
	"cloudshell/internal/app"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func TestAIWindowLaunchRejectsNonlocalOrMalformedCredentials(t *testing.T) {
	valid := aiWindowLaunch{Base: "http://127.0.0.1:1234", Token: strings.Repeat("a", 48), ConfigDir: t.TempDir(), AIWindowID: strings.Repeat("b", 48)}
	if err := validateAIWindowLaunch(valid); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"https://127.0.0.1:1234", "http://localhost:1234", "http://example.com:1234", "http://127.0.0.1:0", "http://127.0.0.1:70000", "http://user@127.0.0.1:1234", "http://127.0.0.1:1234/path", "http://127.0.0.1:1234?token=x"} {
		input := valid
		input.Base = address
		if validateAIWindowLaunch(input) == nil {
			t.Fatalf("accepted nonlocal or malformed server %s", address)
		}
	}
	for _, change := range []func(*aiWindowLaunch){
		func(v *aiWindowLaunch) { v.AIWindowID = "../" + strings.Repeat("b", 45) },
		func(v *aiWindowLaunch) { v.Token = strings.Repeat("z", 48) },
		func(v *aiWindowLaunch) { v.ConfigDir = "relative" },
	} {
		input := valid
		change(&input)
		if validateAIWindowLaunch(input) == nil {
			t.Fatal("accepted malformed credentials or configuration directory")
		}
	}
}

func TestAIWindowPrivatePipePreservesBufferedCommandsAndClosesOnOwnerExit(t *testing.T) {
	launch := aiWindowLaunch{Base: "http://127.0.0.1:1234", Token: strings.Repeat("a", 48), ConfigDir: t.TempDir(), AIWindowID: strings.Repeat("b", 48)}
	data, err := json.Marshal(launch)
	if err != nil {
		t.Fatal(err)
	}
	// All bytes arrive in one read, including commands buffered after startup.
	input := append(data, []byte("\n{\"action\":\"focus\"}\n{\"action\":\"ignored\"}\n")...)
	got, controls, err := decodeAIWindowLaunch(bytes.NewReader(input))
	if err != nil || got != launch {
		t.Fatal("launch could not be decoded", err)
	}
	var actions []string
	readAIWindowControls(controls, func(action string) { actions = append(actions, action) })
	if !reflect.DeepEqual(actions, []string{"focus", "close"}) {
		t.Fatalf("lost buffered focus or owner EOF: %v", actions)
	}
	if _, _, err := decodeAIWindowLaunch(strings.NewReader(strings.Repeat("x", 20000))); err == nil {
		t.Fatal("accepted oversized initial pipe payload")
	}
}

func TestAIWindowRootAndBootstrapAreSeparateFromSSH(t *testing.T) {
	a, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	assets := fstest.MapFS{"index.html": {Data: []byte("ssh-root")}, "ai-window.html": {Data: []byte("ai-root")}}
	if err := a.Start("127.0.0.1:0", assets); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("c", 48)
	remote := aiRemoteBackend(aiWindowLaunch{Base: a.URL(), Token: a.Token(), ConfigDir: a.ConfigDirectory(), AIWindowID: id})
	defer remote.client.CloseIdleConnections()
	adapter := aiWindowAssets{assets}
	index, err := fs.ReadFile(adapter, "index.html")
	if err != nil || string(index) != "ai-root" {
		t.Fatal("Wails would load the SSH workspace for the AI window", err)
	}
	handler := aiWindowAssetHandler(remote, adapter, id)
	for _, path := range []string{"/", "/index.html"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://wails.localhost"+path, nil))
		if response.Code != 200 || response.Body.String() != "ai-root" {
			t.Fatal("AI route served the SSH workspace", path, response.Code)
		}
	}
	requestBoot := func(handler http.Handler) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://wails.localhost/boot.js", nil))
		return response
	}
	boot := requestBoot(handler)
	if boot.Code != 200 || !strings.Contains(boot.Body.String(), a.Token()) || !strings.Contains(boot.Body.String(), "window.CLOUDSHELL.aiWindowId=\""+id+"\"") || strings.Contains(boot.Body.String(), "detachedNonce") {
		t.Fatal("AI bootstrap lost credentials or entered SSH handoff mode", boot.Code)
	}
	primary := requestBoot(nativeAssetHandler(a, assets))
	if primary.Code != 200 || strings.Contains(primary.Body.String(), "aiWindowId") {
		t.Fatal("AI mode leaked into the main bootstrap")
	}
	remote.launch.Token = strings.Repeat("f", 48)
	stale := requestBoot(handler)
	if stale.Code != 403 || strings.Contains(stale.Body.String(), "window.CLOUDSHELL") {
		t.Fatal("stale AI credentials were served as executable bootstrap")
	}
	response, err := http.Get(a.URL() + "/boot.js")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	public, _ := io.ReadAll(response.Body)
	if response.StatusCode != 403 || bytes.Contains(public, []byte(a.Token())) {
		t.Fatal("AI native bootstrap credentials leaked through the public listener")
	}
}

type aiWindowTestPipe struct{ bytes.Buffer }

func (*aiWindowTestPipe) Close() error { return nil }

func TestAIWindowRepeatedOpenOnlyFocusesExistingChild(t *testing.T) {
	id, token := strings.Repeat("a", 48), strings.Repeat("b", 48)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CloudShell-Token") != token {
			t.Error("probe omitted private authorization")
			w.WriteHeader(403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/ai/windows/") && r.URL.Query().Get("probe") != "1" {
			t.Error("native probe should not consume events or update assistant heartbeat")
		}
		_, _ = io.WriteString(w, `{"closed":false}`)
	}))
	defer server.Close()
	backend := aiRemoteBackend(aiWindowLaunch{Base: server.URL, Token: token, ConfigDir: t.TempDir()})
	defer backend.client.CloseIdleConnections()
	input := &aiWindowTestPipe{}
	child := &aiWindowProcess{input: input}
	d := &Desktop{app: backend, detachedNonce: "existing-ssh-owner", children: map[string]*exec.Cmd{}, aiChildren: map[string]*aiWindowProcess{id: child}}
	if err := d.OpenAIWindow(id); err != nil {
		t.Fatal(err)
	}
	if input.String() != "{\"action\":\"focus\"}\n" || len(d.children) != 0 || len(d.aiChildren) != 1 || d.aiChildren[id] != child {
		t.Fatal("reopening changed SSH ownership or launched another AI child")
	}
	d.quitConfirmed = true
	if err := d.OpenAIWindow(id); err == nil {
		t.Fatal("AI child opened during owner shutdown")
	}
	if input.String() != "{\"action\":\"focus\"}\n" {
		t.Fatal("shutdown guard allowed a late focus command")
	}
}

func TestAIWindowStartupProbeAndNativeCloseUseAuthenticatedBroker(t *testing.T) {
	id, token := strings.Repeat("a", 48), strings.Repeat("b", 48)
	var closed, deleted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CloudShell-Token") != token || r.URL.Path != "/api/ai/windows/"+id {
			t.Error("native lifecycle contacted the wrong broker or omitted its private token")
			w.WriteHeader(403)
			return
		}
		if r.Method == http.MethodDelete {
			deleted.Store(true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Query().Get("probe") != "1" || r.URL.Query().Get("side") != "assistant" {
			t.Error("native startup must not consume UI events or heartbeat")
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"closed": closed.Load()})
	}))
	defer server.Close()
	remote := aiRemoteBackend(aiWindowLaunch{Base: server.URL, Token: token, ConfigDir: t.TempDir(), AIWindowID: id})
	defer remote.client.CloseIdleConnections()
	if err := aiWindowProbe(remote, id); err != nil {
		t.Fatal(err)
	}
	closed.Store(true)
	if err := aiWindowProbe(remote, id); err == nil {
		t.Fatal("native process accepted a closed AI room")
	}
	closeAIWindowBroker(remote, id)
	if !deleted.Load() {
		t.Fatal("native close did not cancel the owner's AI room")
	}
}

func TestAIWindowDoesNotPinMainWindowOrPersistSSHBounds(t *testing.T) {
	if state := (&Desktop{aiWindowID: strings.Repeat("a", 48)}).WindowState(); !state.Frameless {
		t.Fatal("AI native titlebar is not reported as frameless")
	}
	main := &Desktop{ctx: context.Background()}
	if value, err := main.SetAIWindowAlwaysOnTop(true); err == nil || value {
		t.Fatal("main SSH window accepted AI-only pinning")
	}
	child := &Desktop{aiWindowID: strings.Repeat("a", 48), ctx: context.Background()}
	if err := child.CaptureWindowState(); err != nil || child.beforeClose(context.Background()) {
		t.Fatal("AI child attempted to persist SSH bounds or show an SSH quit confirmation")
	}
	if child.quitPending {
		t.Fatal("AI close entered the SSH confirmation lifecycle")
	}
}

func TestAIWindowInitialBoundsFitSmallAndSecondaryScreens(t *testing.T) {
	for _, monitor := range []monitorBounds{
		{Width: 1920, Height: 1080, WorkWidth: 1920, WorkHeight: 1040},
		{Width: 800, Height: 600, WorkWidth: 300, WorkHeight: 280},
		{X: -1920, Width: 1920, Height: 1080, WorkX: -1920, WorkY: 32, WorkWidth: 1920, WorkHeight: 1048},
	} {
		bounds := initialAIWindowBounds(initialWindowForMonitor(monitor))
		if bounds.Width != min(450, monitor.WorkWidth) || bounds.Height != min(760, monitor.WorkHeight) || bounds.MinWidth > bounds.Width || bounds.MinHeight > bounds.Height || bounds.X < monitor.WorkX || bounds.Y < monitor.WorkY || bounds.X+bounds.Width > monitor.WorkX+monitor.WorkWidth || bounds.Y+bounds.Height > monitor.WorkY+monitor.WorkHeight {
			t.Fatalf("AI window does not fit display: %+v => %+v", monitor, bounds)
		}
	}
}

func TestAIWindowProcessHelper(t *testing.T) {
	if os.Getenv("DENGSHELL_AI_PROCESS_TEST_HELPER") != "1" {
		return
	}
	closed := false
	readAIWindowControls(json.NewDecoder(os.Stdin), func(action string) { closed = action == "close" })
	if !closed {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestAIWindowOwnerShutdownClosesChildWithoutSSHChildCleanup(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "hidden-owner-with-live-SSH"
		if shutdown {
			name = "process-shutdown"
		}
		t.Run(name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestAIWindowProcessHelper$")
			command.Env = append(os.Environ(), "DENGSHELL_AI_PROCESS_TEST_HELPER=1")
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = command.Process.Kill() })
			child := &aiWindowProcess{command: command, input: input, done: make(chan struct{})}
			result := make(chan error, 1)
			go func() {
				err := command.Wait()
				close(child.done)
				result <- err
			}()
			desktop := &Desktop{aiChildren: map[string]*aiWindowProcess{"ai-child": child}, children: map[string]*exec.Cmd{"ssh-child": {}}}
			if shutdown {
				desktop.closeAIWindows()
			} else {
				desktop.windowClosed = true
				desktop.closeOwnedAIWindows()
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatal("owner shutdown did not gracefully close its AI child", err)
				}
			case <-time.After(time.Second):
				t.Fatal("AI child survived owner shutdown")
			}
			if desktop.quitConfirmed != shutdown || len(desktop.children) != 1 || desktop.children["ssh-child"] == nil {
				t.Fatal("AI cleanup changed the independent SSH child lifecycle")
			}

		})
	}
}

type stalledAIWindowPipe struct {
	entered chan struct{}
	closed  chan struct{}
}

func (p *stalledAIWindowPipe) Write([]byte) (int, error) {
	close(p.entered)
	<-p.closed
	return 0, io.ErrClosedPipe
}

func (p *stalledAIWindowPipe) Close() error { close(p.closed); return nil }

func TestAIWindowOwnerCloseInterruptsStalledFocusWrite(t *testing.T) {
	input := &stalledAIWindowPipe{entered: make(chan struct{}), closed: make(chan struct{})}
	child := &aiWindowProcess{input: input}
	writeResult := make(chan error, 1)
	go func() { writeResult <- child.send(aiWindowControl{Action: "focus"}) }()
	<-input.entered
	closed := make(chan struct{})
	go func() { child.closeInput(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("owner shutdown was blocked by a stalled AI child")
	}
	if err := <-writeResult; err == nil {
		t.Fatal("closing the control pipe did not interrupt a stalled write")
	}
}
