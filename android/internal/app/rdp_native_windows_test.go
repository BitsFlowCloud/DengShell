//go:build windows

package app

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/windows"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestRDPEmbeddedNativeIntegration(t *testing.T) {
	if os.Getenv("DENGSHELL_RDP_NATIVE_QA") != "1" {
		t.Skip("isolated RDP server required")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("DengShell RDP Embedded QA")
	windowWidth, windowHeight := uintptr(1300), uintptr(920)
	if os.Getenv("DENGSHELL_RDP_QA_BIG") == "1" {
		windowWidth, windowHeight = 2300, 1500
	}
	root, _, e := rdpUser32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0x10CF0000, 30, 30, windowWidth, windowHeight, 0, 0, 0, 0)
	if root == 0 {
		t.Fatal(e)
	}
	defer rdpUser32.NewProc("DestroyWindow").Call(root)
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	proxyConfig := ProxyConfig{Type: "direct"}
	switch os.Getenv("DENGSHELL_RDP_QA_PROXY") {
	case "socks5":
		upstream, e := newRDPBridge(context.Background(), "127.0.0.1:33989", ProxyConfig{Type: "direct"})
		if e != nil {
			t.Fatal(e)
		}
		defer upstream.close()
		host, portText, _ := net.SplitHostPort(upstream.listener.Addr().String())
		port, _ := strconv.Atoi(portText)
		proxyConfig = ProxyConfig{Type: "socks5", Host: host, Port: port, User: "dengshell", Password: upstream.token}
	case "http":
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "CONNECT" || r.Host != "127.0.0.1:33989" {
				http.Error(w, "blocked", 403)
				return
			}
			upstream, e := net.DialTimeout("tcp", r.Host, 5*time.Second)
			if e != nil {
				http.Error(w, "unreachable", 502)
				return
			}
			defer upstream.Close()
			c, rw, e := w.(http.Hijacker).Hijack()
			if e != nil {
				return
			}
			defer c.Close()
			_, _ = rw.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
			_ = rw.Flush()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(upstream, rw); _ = upstream.Close(); _ = c.Close(); close(done) }()
			_, _ = io.Copy(c, upstream)
			_ = c.Close()
			_ = upstream.Close()
			<-done
		}))
		defer server.Close()
		host, portText, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
		port, _ := strconv.Atoi(portText)
		proxyConfig = ProxyConfig{Type: "http", Host: host, Port: port}
	}
	profile := Profile{Name: "Embedded native QA", Protocol: "rdp", Host: "127.0.0.1", Port: 33989, User: "rdptest", Secret: "Temporary-RDP-QA-Only!", RDPClipboard: true, Proxy: proxyConfig}
	if path := os.Getenv("DENGSHELL_RDP_QA_CONNECTION"); path != "" {
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal("cannot read private connection input")
		}
		if json.Unmarshal(raw, &profile) != nil {
			t.Fatal("invalid private connection input")
		}
		profile.Protocol = "rdp"
		profile.RDPClipboard = false
	}
	secret := profile.Secret
	profile.Secret = ""
	p, err := a.store.Save(profile, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.StartRDPEmbedded(p.ID, secret, root)
	if err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("DENGSHELL_RDP_QA_STATE")
	control := os.Getenv("DENGSHELL_RDP_QA_CONTROL")
	deadline := time.Now().Add(10 * time.Minute)
	var msg struct {
		Hwnd           uintptr
		Message        uint32
		WParam, LParam uintptr
		Time           uint32
		X, Y           int32
		Private        uint32
	}
	var last string
	seen := false
	for time.Now().Before(deadline) {
		for {
			has, _, _ := rdpUser32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
			if has == 0 {
				break
			}
			rdpUser32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
			rdpUser32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
		}
		command, _ := os.ReadFile(control)
		action := string(command)
		if action == "close" {
			if !seen {
				t.Fatal("never received a connected desktop")
			}
			return
		}
		if action == "lock" {
			a.securityLock.mu.Lock()
			a.securityLock.locked = true
			a.securityLock.mu.Unlock()
			a.SecurityLockStatus()
		} else {
			width, height := 1000.0, 650.0
			if os.Getenv("DENGSHELL_RDP_QA_BIG") == "1" {
				width, height = 2000, 1250
			}
			if action == "auto" || action == "auto-resize" {
				_ = a.RDPResolution(info.ID, 0, 0)
			}
			if action == "fixed" {
				_ = a.RDPResolution(info.ID, 1024, 768)
			}
			if action == "large" {
				width, height = 1180, 820
				_ = a.RDPResolution(info.ID, 1280, 800)
			}
			if action == "resize" || action == "auto-resize" {
				width, height = 800, 540
			}
			var client struct{ Left, Top, Right, Bottom int32 }
			rdpUser32.NewProc("GetClientRect").Call(root, uintptr(unsafe.Pointer(&client)))
			if err = a.RDPViewport(info.ID, 100, 80, width, height, float64(client.Right), action != "hide"); err != nil {
				t.Fatal(err)
			}
			if action == "cad" && last != "cad" {
				_ = a.RDPSendSecureAttention(info.ID)
			}
		}
		last = action
		states := a.rdpSessions()
		if len(states) > 0 && states[0].Status == "connected" {
			seen = true
			if os.Getenv("DENGSHELL_RDP_QA_AUTO") == "1" && sNativeVisible(a, info.ID) {
				return
			}
		}
		a.rdp.mu.Lock()
		s := a.rdp.sessions[info.ID]
		child := s.nativeWindow()
		a.rdp.mu.Unlock()
		var childRect struct{ Left, Top, Right, Bottom int32 }
		var visible uintptr
		if child != 0 {
			rdpUser32.NewProc("GetWindowRect").Call(child, uintptr(unsafe.Pointer(&childRect)))
			rdpUser32.NewProc("MapWindowPoints").Call(0, root, uintptr(unsafe.Pointer(&childRect)), 2)
			visible, _, _ = rdpUser32.NewProc("IsWindowVisible").Call(child)
		}
		data, _ := json.Marshal(map[string]any{"root": root, "child": child, "visible": visible != 0, "rect": childRect, "sessions": states})
		_ = os.WriteFile(output, data, 0600)
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("native test timed out")
}

func sNativeVisible(a *App, id string) bool {
	a.rdp.mu.Lock()
	defer a.rdp.mu.Unlock()
	s := a.rdp.sessions[id]
	if s == nil {
		return false
	}
	hwnd := s.nativeWindow()
	if hwnd == 0 {
		return false
	}
	v, _, _ := rdpUser32.NewProc("IsWindowVisible").Call(hwnd)
	return v != 0
}

// Repeated viewport reports must not trigger WM_SIZE/WM_WINDOWPOSCHANGED.
// That regression is visible as flashing even with a completely static desktop.
func TestRDPViewportDoesNotRepaintUnchangedWindow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	var emptyTitle, _ = windows.UTF16PtrFromString("RDP viewport test")
	root, _, _ := rdpUser32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(emptyTitle)), 0x10CF0000, 0, 0, 1400, 1000, 0, 0, 0, 0)
	if root == 0 {
		t.Fatal("root window not created")
	}
	defer rdpUser32.NewProc("DestroyWindow").Call(root)
	child, _, _ := rdpUser32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0x40000000, 0, 0, 10, 10, root, 0, 0, 0)
	if child == 0 {
		t.Fatal("child not created")
	}
	changes := 0
	old, _, _ := rdpUser32.NewProc("GetWindowLongPtrW").Call(child, ^uintptr(3)) // GWLP_WNDPROC = -4
	callback := syscall.NewCallback(func(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
		if msg == 0x0047 || msg == 0x0005 {
			changes++
		} // WINDOWPOSCHANGED / SIZE
		result, _, _ := rdpUser32.NewProc("CallWindowProcW").Call(old, hwnd, uintptr(msg), w, l)
		return result
	})
	rdpUser32.NewProc("SetWindowLongPtrW").Call(child, ^uintptr(3), callback)
	defer rdpUser32.NewProc("SetWindowLongPtrW").Call(child, ^uintptr(3), old)
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	s := &rdpSession{parent: root, hwnd: child, displaySent: true, cmd: &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}}, cancel: func() {}, info: RDPInfo{ID: "display", Status: "connected"}}
	a.rdp.sessions = map[string]*rdpSession{"display": s}
	var rect struct{ Left, Top, Right, Bottom int32 }
	rdpUser32.NewProc("GetClientRect").Call(root, uintptr(unsafe.Pointer(&rect)))
	update := func(w float64, visible bool) {
		t.Helper()
		if err := a.RDPViewport("display", 100, 80, w, 600, float64(rect.Right), visible); err != nil {
			t.Fatal(err)
		}
	}
	update(1000, true)
	first := changes
	if first == 0 {
		t.Fatal("initial viewport was not applied")
	}
	for i := 0; i < 100; i++ {
		update(1000, true)
	}
	if changes != first {
		t.Fatalf("unchanged viewport produced %d redundant window events", changes-first)
	}
	update(900, true)
	if changes == first {
		t.Fatal("real resize was skipped")
	}
	update(900, false)
	hidden := changes
	for i := 0; i < 20; i++ {
		update(900, false)
	}
	if changes != hidden {
		t.Fatal("hidden surface repeatedly updated")
	}
	visible, _, _ := rdpUser32.NewProc("IsWindowVisible").Call(child)
	if visible != 0 {
		t.Fatal("child not hidden")
	}
	update(900, true)
	visible, _, _ = rdpUser32.NewProc("IsWindowVisible").Call(child)
	if visible == 0 {
		t.Fatal("child not restored")
	}
}
