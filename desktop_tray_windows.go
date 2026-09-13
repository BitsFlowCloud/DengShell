//go:build desktop && windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

type trayWindowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type trayMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          struct{ X, Y int32 }
	Private        uint32
}
type trayNotifyData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	BalloonIcon         uintptr
}

var trayReady atomic.Bool
var trayEvents atomic.Int32
var trayWindow atomic.Uintptr
var trayCallback = windows.NewCallback(trayWndProc)
var trayNotify trayNotifyData
var trayCreatedMessage uint32

func trayString(value string) *uint16 { s, _ := windows.UTF16PtrFromString(value); return s }
func trayAdd() {
	if trayNotify.Window == 0 {
		return
	}
	ok, _, _ := windowShell32.NewProc("Shell_NotifyIconW").Call(0, uintptr(unsafe.Pointer(&trayNotify)))
	trayReady.Store(ok != 0)
	if ok != 0 {
		trayNotify.Version = 4
		windowShell32.NewProc("Shell_NotifyIconW").Call(4, uintptr(unsafe.Pointer(&trayNotify)))
	}
}
func trayWndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	if msg == trayCreatedMessage && trayCreatedMessage != 0 {
		trayAdd()
		return 0
	}
	switch msg {
	case 0x8001:
		event := uint32(lparam) & 0xffff
		if event == 0x0202 || event == 0x0203 || event == 0x0400 || event == 0x0401 {
			trayEvents.Or(1)
		}
		if event == 0x0205 || event == 0x007b {
			menu, _, _ := windowUser32.NewProc("CreatePopupMenu").Call()
			if menu == 0 {
				return 0
			}
			defer windowUser32.NewProc("DestroyMenu").Call(menu)
			windowUser32.NewProc("AppendMenuW").Call(menu, 0, 1, uintptr(unsafe.Pointer(trayString("打开 DengShell"))))
			windowUser32.NewProc("AppendMenuW").Call(menu, 0, 2, uintptr(unsafe.Pointer(trayString("退出 DengShell"))))
			var point struct{ X, Y int32 }
			windowUser32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
			windowUser32.NewProc("SetForegroundWindow").Call(hwnd)
			selected, _, _ := windowUser32.NewProc("TrackPopupMenu").Call(menu, 0x0100|0x0002, uintptr(point.X), uintptr(point.Y), 0, hwnd, 0)
			windowUser32.NewProc("PostMessageW").Call(hwnd, 0, 0, 0)
			if selected == 1 {
				trayEvents.Or(1)
			}
			if selected == 2 {
				trayEvents.Or(2)
			}
		}
		return 0
	case 0x0010:
		windowShell32.NewProc("Shell_NotifyIconW").Call(2, uintptr(unsafe.Pointer(&trayNotify)))
		trayReady.Store(false)
		windowUser32.NewProc("DestroyWindow").Call(hwnd)
		return 0
	case 0x0002:
		windowUser32.NewProc("PostQuitMessage").Call(0)
		return 0
	}
	result, _, _ := windowUser32.NewProc("DefWindowProcW").Call(hwnd, uintptr(msg), wparam, lparam)
	return result
}
func platformInitializeTray(_ string) {
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		instance, _, _ := windowKernel32.NewProc("GetModuleHandleW").Call(0)
		class := trayString("DengShellTrayWindow")
		wc := trayWindowClass{Proc: trayCallback, Instance: instance, Name: class}
		wc.Size = uint32(unsafe.Sizeof(wc))
		registered, _, _ := windowUser32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc)))
		if registered == 0 {
			return
		}
		hwnd, _, _ := windowUser32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(trayString("DengShell tray"))), 0, 0, 0, 0, 0, 0, 0, instance, 0)
		if hwnd == 0 {
			return
		}
		trayWindow.Store(hwnd)
		created, _, _ := windowUser32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(trayString("TaskbarCreated"))))
		trayCreatedMessage = uint32(created)
		icon, _, _ := windowUser32.NewProc("LoadImageW").Call(instance, 2, 1, 32, 32, 0x8000)
		trayNotify = trayNotifyData{Window: hwnd, ID: uint32(os.Getpid()), Flags: 1 | 2 | 4, Callback: 0x8001, Icon: icon}
		trayNotify.Size = uint32(unsafe.Sizeof(trayNotify))
		tip, _ := windows.UTF16FromString("DengShell · 双击打开")
		copy(trayNotify.Tip[:], tip)
		trayAdd()
		var msg trayMessage
		for {
			ok, _, _ := windowUser32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(ok) <= 0 {
				break
			}
			windowUser32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
			windowUser32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
		}
		trayWindow.Store(0)
		trayReady.Store(false)
	}()
}
func platformTrayAvailable() bool { return trayReady.Load() }
func platformTrayEvents() int     { return int(trayEvents.Swap(0)) }
func platformCloseTray() {
	if h := trayWindow.Load(); h != 0 {
		windowUser32.NewProc("PostMessageW").Call(h, 0x0010, 0, 0)
	}
}
func platformRaiseWindow() {
	if h := platformWindowHandle(); h != 0 {
		windowUser32.NewProc("ShowWindow").Call(h, 9)
		windowUser32.NewProc("SetForegroundWindow").Call(h)
		windowUser32.NewProc("BringWindowToTop").Call(h)
	}
}
