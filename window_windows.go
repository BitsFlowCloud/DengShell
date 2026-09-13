//go:build desktop && windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	windowUser32   = windows.NewLazySystemDLL("user32.dll")
	windowKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	windowShell32  = windows.NewLazySystemDLL("shell32.dll")
)

func platformWebviewDataPath(configDir string) string {
	return filepath.Join(configDir, "webview")
}

func preparePlatformWindow() {
	// A stable identity keeps running taskbar windows grouped as DengShell.
	id, _ := windows.UTF16PtrFromString("DengShell.Desktop")
	result, _, _ := windowShell32.NewProc("SetCurrentProcessExplicitAppUserModelID").Call(uintptr(unsafe.Pointer(id)))
	if int32(result) < 0 {
		log.Printf("DengShell taskbar identity could not be set: 0x%x", result)
	}
}

func platformWindowHandle() uintptr {
	class, _ := windows.UTF16PtrFromString("DengShellWindow")
	var handle uintptr
	// Inspect every matching top-level window, rather than assuming the first
	// class match belongs to this process (for example during a parallel launch).
	for {
		handle, _, _ = windowUser32.NewProc("FindWindowExW").Call(0, handle, uintptr(unsafe.Pointer(class)), 0)
		if handle == 0 {
			return 0
		}
		var processID uint32
		windowUser32.NewProc("GetWindowThreadProcessId").Call(handle, uintptr(unsafe.Pointer(&processID)))
		if processID == uint32(os.Getpid()) {
			break
		}
	}
	return handle
}

func installPlatformWindowIcon() {
	handle := platformWindowHandle()
	if handle == 0 {
		log.Print("DengShell window icon: window not found")
		return
	}
	module, _, _ := windowKernel32.NewProc("GetModuleHandleW").Call(0)
	dpi := uintptr(96)
	getDPI := windowUser32.NewProc("GetDpiForWindow")
	if getDPI.Find() == nil {
		if value, _, _ := getDPI.Call(handle); value != 0 {
			dpi = value
		}
	}
	metricsForDPI := windowUser32.NewProc("GetSystemMetricsForDpi")
	// rsrc v0.10.2 reserves ID 1 for the manifest and ID 2 for the icon group.
	// Wails' default loader expects ID 3, so set both sizes explicitly. LR_SHARED
	// keeps the process-lifetime icon handles valid without manual destruction.
	for _, icon := range []struct{ kind, metric uintptr }{{0, 49}, {1, 11}} {
		size, _, _ := windowUser32.NewProc("GetSystemMetrics").Call(icon.metric)
		if metricsForDPI.Find() == nil {
			if value, _, _ := metricsForDPI.Call(icon.metric, dpi); value != 0 {
				size = value
			}
		}
		image, _, err := windowUser32.NewProc("LoadImageW").Call(module, 2, 1, size, size, 0x8000)
		if image == 0 {
			log.Printf("DengShell window icon could not be loaded: %v", err)
			continue
		}
		windowUser32.NewProc("SendMessageW").Call(handle, 0x80, icon.kind, image)
	}
}
