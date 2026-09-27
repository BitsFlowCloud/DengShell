//go:build windows

package app

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var rdpUser32 = windows.NewLazySystemDLL("user32.dll")

func validateRDPParent(parent uintptr) error {
	var pid uint32
	rdpUser32.NewProc("GetWindowThreadProcessId").Call(parent, uintptr(unsafe.Pointer(&pid)))
	if parent == 0 || pid != uint32(os.Getpid()) {
		return errors.New("RDP 宿主窗口无效")
	}
	return nil
}

// Only windows owned by this session's engine may be moved into the viewport.
// All inactive surfaces are hidden before the selected one becomes visible.
func (s *rdpSession) nativeWindow() uintptr {
	if s.parent == 0 || s.cmd.Process == nil {
		return 0
	}
	if s.hwnd != 0 {
		var pid uint32
		rdpUser32.NewProc("GetWindowThreadProcessId").Call(s.hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == uint32(s.cmd.Process.Pid) {
			return s.hwnd
		}
		s.hwnd = 0
		s.displaySent = false
	}
	class, _ := windows.UTF16PtrFromString("FreeRDP")
	var handle uintptr
	for {
		handle, _, _ = rdpUser32.NewProc("FindWindowExW").Call(s.parent, handle, uintptr(unsafe.Pointer(class)), 0)
		if handle == 0 {
			return 0
		}
		var pid uint32
		rdpUser32.NewProc("GetWindowThreadProcessId").Call(handle, uintptr(unsafe.Pointer(&pid)))
		if pid == uint32(s.cmd.Process.Pid) {
			s.hwnd = handle
			s.displaySent = false
			return handle
		}
	}
}

func (a *App) RDPViewport(id string, x, y, width, height, viewportWidth float64, visible bool) error {
	if visible {
		if err := a.RequireUnlocked(); err != nil {
			return err
		}
	}
	for _, v := range []float64{x, y, width, height, viewportWidth} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 32768 {
			return errors.New("RDP 显示区域无效")
		}
	}
	a.rdp.mu.Lock()
	defer a.rdp.mu.Unlock()
	for sid, s := range a.rdp.sessions {
		hwnd := s.nativeWindow()
		if hwnd == 0 {
			continue
		}
		status := s.snapshot().Status
		if !s.displaySent && status == "connected" {
			s.postDisplaySize(hwnd)
		}
		shown, _, _ := rdpUser32.NewProc("IsWindowVisible").Call(hwnd)
		enabled, _, _ := rdpUser32.NewProc("IsWindowEnabled").Call(hwnd)
		if sid != id || !visible || status != "connected" {
			if enabled != 0 {
				rdpUser32.NewProc("EnableWindow").Call(hwnd, 0)
			}
			if shown != 0 {
				rdpUser32.NewProc("ShowWindow").Call(hwnd, 0)
			}
			continue
		}
		if err := validateRDPParent(s.parent); err != nil {
			return err
		}
		var rect struct{ Left, Top, Right, Bottom int32 }
		ok, _, _ := rdpUser32.NewProc("GetClientRect").Call(s.parent, uintptr(unsafe.Pointer(&rect)))
		if ok == 0 || viewportWidth < 1 || width < 1 || height < 1 {
			return errors.New("RDP 窗口尺寸无效")
		}
		// DOM rects already include application zoom; the client-width ratio also
		// handles WebView zoom and monitor DPI without applying scale twice.
		scale := float64(rect.Right-rect.Left) / viewportWidth
		px, py := int(math.Round(x*scale)), int(math.Round(y*scale))
		pw, ph := int(math.Round(width*scale)), int(math.Round(height*scale))
		if px+pw > int(rect.Right) {
			pw = int(rect.Right) - px
		}
		if py+ph > int(rect.Bottom) {
			ph = int(rect.Bottom) - py
		}
		if pw <= 0 || ph <= 0 {
			return errors.New("RDP 显示区域超出窗口")
		}
		// The UI periodically resends the viewport. Reapplying identical bounds
		// sends WM_SIZE and can repaint the entire native/WebView surface.
		var current struct{ Left, Top, Right, Bottom int32 }
		got, _, _ := rdpUser32.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&current)))
		if got != 0 {
			rdpUser32.NewProc("MapWindowPoints").Call(0, s.parent, uintptr(unsafe.Pointer(&current)), 2)
		}
		moved := got == 0 || int(current.Left) != px || int(current.Top) != py
		resized := got == 0 || int(current.Right-current.Left) != pw || int(current.Bottom-current.Top) != ph
		if enabled == 0 {
			rdpUser32.NewProc("EnableWindow").Call(hwnd, 1)
		}
		if moved || resized || shown == 0 {
			flags := uintptr(0x0010) // SWP_NOACTIVATE
			if !moved {
				flags |= 0x0002
			} // SWP_NOMOVE
			if !resized {
				flags |= 0x0001
			} // SWP_NOSIZE
			if shown == 0 {
				flags |= 0x0040
			} else {
				flags |= 0x0004
			} // SHOWWINDOW / NOZORDER
			ok, _, _ := rdpUser32.NewProc("SetWindowPos").Call(hwnd, 0, uintptr(px), uintptr(py), uintptr(pw), uintptr(ph), flags)
			if ok == 0 {
				return errors.New("无法调整 RDP 显示区域")
			}
		}
	}
	return nil
}

func (a *App) RDPSendSecureAttention(id string) error {
	if err := a.RequireUnlocked(); err != nil {
		return err
	}
	a.rdp.mu.Lock()
	defer a.rdp.mu.Unlock()
	s := a.rdp.sessions[id]
	if s == nil {
		return errors.New("RDP 会话不存在")
	}
	hwnd := s.nativeWindow()
	if hwnd == 0 || s.snapshot().Status != "connected" {
		return errors.New("远程桌面尚未连接")
	}
	rdpUser32.NewProc("PostMessageW").Call(hwnd, 0x0400+501, 0, 0)
	return nil
}

func configureRDPProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
func ownRDPProcess(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return nil, errors.New("无法管理 RDP 引擎生命周期，已停止启动")
	}
	return func() { _ = windows.CloseHandle(job) }, nil
}

// Resolution selection is separate from the native viewport. The engine always
// scales its current framebuffer to fit, including servers without DisplayControl.
func (a *App) RDPResolution(id string, width, height int) error {
	if err := a.RequireUnlocked(); err != nil {
		return err
	}
	switch [2]int{width, height} {
	case [2]int{0, 0}, [2]int{1280, 800}, [2]int{1600, 900}, [2]int{1920, 1080}, [2]int{1024, 768}:
	default:
		return errors.New("请选择支持的 RDP 分辨率")
	}
	a.rdp.mu.Lock()
	defer a.rdp.mu.Unlock()
	s := a.rdp.sessions[id]
	if s == nil {
		return errors.New("RDP 会话不存在")
	}
	if s.displayWidth == width && s.displayHeight == height && s.displaySent {
		return nil
	}
	s.displayWidth, s.displayHeight, s.displaySent = width, height, false
	if hwnd := s.nativeWindow(); hwnd != 0 {
		s.postDisplaySize(hwnd)
	}
	return nil
}
func (s *rdpSession) postDisplaySize(hwnd uintptr) {
	ok, _, _ := rdpUser32.NewProc("PostMessageW").Call(hwnd, 0x0400+502, uintptr(s.displayWidth), uintptr(s.displayHeight))
	s.displaySent = ok != 0
}

// Request remote text sizing at the local Windows DPI. This is independent of
// bitmap scaling: a matching-resolution desktop is copied pixel for pixel.
func rdpHostDisplayArguments(parent uintptr) string {
	dpi := uintptr(96)
	getDPI := rdpUser32.NewProc("GetDpiForWindow")
	if parent != 0 && getDPI.Find() == nil {
		if value, _, _ := getDPI.Call(parent); value >= 96 && value <= 480 {
			dpi = value
		}
	}
	scale := int((dpi*100 + 48) / 96)
	return fmt.Sprintf("/scale-desktop:%d\n/scale-device:100\n", scale)
}
