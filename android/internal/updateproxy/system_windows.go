package updateproxy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var winhttp = windows.NewLazySystemDLL("winhttp.dll")
var getIEProxy = winhttp.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
var winOpen = winhttp.NewProc("WinHttpOpen")
var winClose = winhttp.NewProc("WinHttpCloseHandle")
var getURLProxy = winhttp.NewProc("WinHttpGetProxyForUrl")
var setTimeouts = winhttp.NewProc("WinHttpSetTimeouts")
var globalFree = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")

type ieProxyConfig struct {
	AutoDetect                   int32
	AutoConfigURL, Proxy, Bypass *uint16
}
type autoProxyOptions struct {
	Flags, DetectFlags uint32
	ConfigURL          *uint16
	Reserved           uintptr
	ReservedFlags      uint32
	AutoLogon          int32
}
type winProxyInfo struct {
	AccessType    uint32
	Proxy, Bypass *uint16
}

func freeProxyString(p *uint16) {
	if p != nil {
		globalFree.Call(uintptr(unsafe.Pointer(p)))
	}
}

func systemProxy(req *http.Request) (*url.URL, error) {
	var settings ieProxyConfig
	ok, _, e := getIEProxy.Call(uintptr(unsafe.Pointer(&settings)))
	if ok == 0 {
		if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) {
			return nil, nil
		}
		return nil, errors.New("无法读取 Windows 系统代理设置")
	}
	defer freeProxyString(settings.AutoConfigURL)
	defer freeProxyString(settings.Proxy)
	defer freeProxyString(settings.Bypass)
	servers, bypass := windows.UTF16PtrToString(settings.Proxy), windows.UTF16PtrToString(settings.Bypass)
	pac := windows.UTF16PtrToString(settings.AutoConfigURL)
	if bypassed(req.URL, bypass) {
		return nil, nil
	}
	// Manual proxies (including FlClash) need no network discovery. Only use
	// WPAD without a manual proxy; an explicitly configured PAC takes priority.
	if pac != "" || (servers == "" && settings.AutoDetect != 0) {
		proxy, err := automaticWindowsProxy(req, pac, settings.AutoDetect != 0)
		if err == nil {
			return proxy, nil
		}
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		if servers == "" && pac != "" {
			return nil, errPAC
		}
		// WPAD is commonly enabled on networks without any proxy. Fall back
		// to the user's manual settings, or direct if none are configured.
	}
	return windowsProxy(req.URL, servers, bypass)
}

// WinHTTP's synchronous PAC resolver does not accept a context. Bound callers
// and concurrent native calls so a stalled WPAD service cannot block startup or
// accumulate unbounded goroutines. The worker owns and frees all native memory.
var pacSlots = make(chan struct{}, 2)

func automaticWindowsProxy(req *http.Request, pac string, detect bool) (*url.URL, error) {
	ctx, cancel := context.WithTimeout(req.Context(), time.Second)
	defer cancel()
	select {
	case pacSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	type result struct {
		proxy *url.URL
		err   error
	}
	done := make(chan result, 1)
	target := *req.URL
	go func() {
		defer func() { <-pacSlots }()
		p, err := nativeAutomaticProxy(&target, pac, detect)
		done <- result{p, err}
	}()
	select {
	case r := <-done:
		return r.proxy, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func nativeAutomaticProxy(target *url.URL, pac string, detect bool) (*url.URL, error) {
	agent, _ := windows.UTF16PtrFromString("DengShell update")
	h, _, _ := winOpen.Call(uintptr(unsafe.Pointer(agent)), 1, 0, 0, 0)
	if h == 0 {
		return nil, errPAC
	}
	defer winClose.Call(h)
	setTimeouts.Call(h, 1000, 1000, 1000, 1000)
	var options autoProxyOptions
	if pac != "" {
		var err error
		options.ConfigURL, err = windows.UTF16PtrFromString(pac)
		if err != nil {
			return nil, errPAC
		}
		options.Flags = 2 // WINHTTP_AUTOPROXY_CONFIG_URL
	} else if detect {
		options.Flags, options.DetectFlags = 1, 3 // DHCP and DNS WPAD
	}
	address, err := windows.UTF16PtrFromString(target.String())
	if err != nil {
		return nil, errPAC
	}
	var info winProxyInfo
	ok, _, _ := getURLProxy.Call(h, uintptr(unsafe.Pointer(address)), uintptr(unsafe.Pointer(&options)), uintptr(unsafe.Pointer(&info)))
	defer freeProxyString(info.Proxy)
	defer freeProxyString(info.Bypass)
	if ok == 0 {
		return nil, errPAC
	}
	if info.AccessType == 1 {
		return nil, nil
	}
	if info.AccessType != 3 {
		return nil, errPAC
	}
	return windowsProxy(target, windows.UTF16PtrToString(info.Proxy), windows.UTF16PtrToString(info.Bypass))
}
