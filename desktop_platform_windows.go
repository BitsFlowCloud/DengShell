//go:build desktop && windows

package main

import (
	"context"
	"fmt"
	"log"
	"unsafe"

	"github.com/wailsapp/go-webview2/webviewloader"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

type desktopRect struct{ Left, Top, Right, Bottom int32 }
type desktopMonitorInfo struct {
	Size          uint32
	Monitor, Work desktopRect
	Flags         uint32
}

// Written once on the startup thread, before Wails creates any callbacks. Win32
// window coordinates are physical pixels; Wails sizes are 96-DPI logical units.
var initialPhysicalWindow initialWindowBounds
var initialLogicalWindow initialWindowBounds

func initialPlatformWindow() (initialWindowBounds, error) {
	var point struct{ X, Y int32 }
	windowUser32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	packedPoint := uint64(uint32(point.X)) | uint64(uint32(point.Y))<<32
	monitor, _, _ := windowUser32.NewProc("MonitorFromPoint").Call(uintptr(packedPoint), 2)
	info := desktopMonitorInfo{}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, err := windowUser32.NewProc("GetMonitorInfoW").Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return initialWindowBounds{}, fmt.Errorf("无法读取当前显示器信息: %w", err)
	}
	physical := monitorBounds{
		X: int(info.Monitor.Left), Y: int(info.Monitor.Top), Width: int(info.Monitor.Right - info.Monitor.Left), Height: int(info.Monitor.Bottom - info.Monitor.Top),
		WorkX: int(info.Work.Left), WorkY: int(info.Work.Top), WorkWidth: int(info.Work.Right - info.Work.Left), WorkHeight: int(info.Work.Bottom - info.Work.Top),
	}
	initialPhysicalWindow = initialWindowForMonitor(physical)
	dpiX, dpiY := uint32(96), uint32(96)
	getDPI := windows.NewLazySystemDLL("shcore.dll").NewProc("GetDpiForMonitor")
	if getDPI.Find() == nil {
		var x, y uint32
		status, _, _ := getDPI.Call(monitor, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y)))
		if int32(status) >= 0 && x > 0 && y > 0 {
			dpiX, dpiY = x, y
		}
	}
	logical := func(value int, dpi uint32) int { return (value*96 + int(dpi)/2) / int(dpi) }
	initialLogicalWindow = initialWindowForMonitor(monitorBounds{
		X: logical(physical.X, dpiX), Y: logical(physical.Y, dpiY), Width: logical(physical.Width, dpiX), Height: logical(physical.Height, dpiY),
		WorkX: logical(physical.WorkX, dpiX), WorkY: logical(physical.WorkY, dpiY), WorkWidth: logical(physical.WorkWidth, dpiX), WorkHeight: logical(physical.WorkHeight, dpiY),
	})
	return initialLogicalWindow, nil
}

func finalizeInitialPlatformWindow(ctx context.Context, logical initialWindowBounds) {
	if handle := platformWindowHandle(); handle != 0 {
		b := initialPhysicalWindow
		width := (logical.Width*b.Width + initialLogicalWindow.Width/2) / initialLogicalWindow.Width
		height := (logical.Height*b.Height + initialLogicalWindow.Height/2) / initialLogicalWindow.Height
		b.X += (b.Width - width) / 2
		b.Y += (b.Height - height) / 2
		b.Width, b.Height = width, height
		// Direct Win32 positioning avoids Wails' relative-to-current-monitor SetPos
		// conversion. Moving between monitors can trigger WM_DPICHANGED, so apply
		// the final physical bounds again after that synchronous transition.
		for range 2 {
			ok, _, err := windowUser32.NewProc("SetWindowPos").Call(handle, 0, uintptr(b.X), uintptr(b.Y), uintptr(b.Width), uintptr(b.Height), 0x0014)
			if ok == 0 {
				log.Printf("DengShell initial window placement: %v", err)
				break
			}
		}
	}
	runtime.WindowShow(ctx)
	// A legacy updater may launch this process with SW_HIDE. Its first show
	// call can therefore be ignored; explicitly restore before the tray watcher
	// interprets a startup minimisation as a user request. Saved maximisation is
	// applied by the caller after this initial restoration.
	runtime.WindowUnminimise(ctx)
	platformRaiseWindow()
}

func platformRuntimeDescription() string {
	version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err != nil || version == "" {
		return "WebView2 (native frontend ready)"
	}
	return "WebView2 " + version
}
