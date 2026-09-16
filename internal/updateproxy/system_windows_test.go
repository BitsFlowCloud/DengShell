package updateproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This integration test changes only the disposable CI user's Internet Options
// and restores them. Never run it automatically on a developer/user desktop.
func TestWindowsSystemProxyNative(t *testing.T) {
	if os.Getenv("DENGSHELL_TEST_SYSTEM_PROXY") != "1" || os.Getenv("CI") != "true" {
		t.Skip("requires a disposable Windows CI user")
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"} {
		t.Setenv(key, "")
	}
	var previous ieProxyConfig
	ok, _, err := getIEProxy.Call(uintptr(unsafe.Pointer(&previous)))
	if ok == 0 {
		t.Fatalf("read original settings: %v", err)
	}
	defer freeProxyString(previous.Proxy)
	defer freeProxyString(previous.Bypass)
	defer freeProxyString(previous.AutoConfigURL)
	flags := uintptr(1)
	if previous.Proxy != nil {
		flags |= 2
	}
	if previous.AutoConfigURL != nil {
		flags |= 4
	}
	if previous.AutoDetect != 0 {
		flags |= 8
	}
	setOption := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	// On amd64 the union requires eight-byte alignment.
	type option64 struct {
		Key   uint32
		_     uint32
		Value uintptr
	}
	type optionList struct {
		Size         uint32
		Connection   *uint16
		Count, Error uint32
		Options      *option64
	}
	set := func(proxy, bypass, pac string, flags uintptr) {
		p, _ := windows.UTF16PtrFromString(proxy)
		b, _ := windows.UTF16PtrFromString(bypass)
		a, _ := windows.UTF16PtrFromString(pac)
		options := []option64{{Key: 1, Value: flags}, {Key: 2, Value: uintptr(unsafe.Pointer(p))}, {Key: 3, Value: uintptr(unsafe.Pointer(b))}, {Key: 4, Value: uintptr(unsafe.Pointer(a))}}
		list := optionList{Count: uint32(len(options)), Options: &options[0]}
		list.Size = uint32(unsafe.Sizeof(list))
		r, _, e := setOption.Call(0, 75, uintptr(unsafe.Pointer(&list)), uintptr(list.Size))
		runtime.KeepAlive(p)
		runtime.KeepAlive(b)
		runtime.KeepAlive(a)
		if r == 0 {
			t.Fatalf("set isolated proxy: %v", e)
		}
		setOption.Call(0, 39, 0, 0)
		setOption.Call(0, 37, 0, 0)
	}
	defer func() {
		set(windows.UTF16PtrToString(previous.Proxy), windows.UTF16PtrToString(previous.Bypass), windows.UTF16PtrToString(previous.AutoConfigURL), flags)
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "update.invalid" {
			t.Errorf("unexpected destination: %s", r.URL)
		}
		io.WriteString(w, "native-system-proxy")
	}))
	defer server.Close()
	set(strings.TrimPrefix(server.URL, "http://"), "<local>", "", 3)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = Proxy
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	res, err := client.Get("http://update.invalid/up.exe")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || string(body) != "native-system-proxy" {
		t.Fatalf("not through system proxy: %q %v", body, err)
	}
	r, _ := http.NewRequest("GET", "https://ds.free-vps.org/up.exe", nil)
	p, err := Proxy(r)
	if err != nil || p == nil || p.String() != server.URL {
		t.Fatalf("HTTPS system proxy: %v %v", p, err)
	}
	set("https=127.0.0.1:50003;http=127.0.0.1:50001", "", "", 3)
	p, err = Proxy(r)
	if err != nil || p == nil || p.String() != "http://127.0.0.1:50003" {
		t.Fatalf("changed system proxy not read: %v %v", p, err)
	}
	set("127.0.0.1:7890", "*.free-vps.org", "", 3)
	p, err = Proxy(r)
	if err != nil || p != nil {
		t.Fatalf("bypass ignored: %v %v", p, err)
	}
	set("", "", "", 1)
	p, err = Proxy(r)
	if err != nil || p != nil {
		t.Fatalf("disabled proxy ignored: %v %v", p, err)
	}
}
