//go:build desktop && windows

package main

import (
	"runtime"
	"strconv"
	"unsafe"
)

func platformWindowViewIdentity() (string, bool) {
	h := platformWindowHandle()
	if h == 0 {
		return "", false
	}
	visible, _, _ := windowUser32.NewProc("IsWindowVisible").Call(h)
	iconic, _, _ := windowUser32.NewProc("IsIconic").Call(h)
	return strconv.FormatUint(uint64(h), 16), visible != 0 && iconic == 0
}
func platformWindowPointerTargets() ([]string, bool, string) {
	// Make pointer and hit-test coordinates physical on this thread, even when
	// the webview bridge dispatched from a differently scaled monitor/thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	setDPI := windowUser32.NewProc("SetThreadDpiAwarenessContext")
	if setDPI.Find() == nil {
		previous, _, _ := setDPI.Call(^uintptr(3))
		if previous != 0 {
			defer setDPI.Call(previous)
		}
	} // PER_MONITOR_AWARE_V2 = -4
	var point struct{ X, Y int32 }
	ok, _, _ := windowUser32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	if ok == 0 {
		return nil, true, ""
	}
	packed := uint64(uint32(point.X)) | uint64(uint32(point.Y))<<32
	child, _, _ := windowUser32.NewProc("WindowFromPoint").Call(uintptr(packed))
	if child == 0 {
		return nil, true, ""
	}
	root, _, _ := windowUser32.NewProc("GetAncestor").Call(child, 2) // GA_ROOT, including WebView child windows.
	if root == 0 {
		root = child
	}
	return []string{strconv.FormatUint(uint64(root), 16)}, true, ""
}
