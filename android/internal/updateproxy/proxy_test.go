package updateproxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnvironmentPrecedenceAndBypass(t *testing.T) {
	for _, tc := range []struct {
		name         string
		env          map[string]string
		target, want string
		system       bool
	}{
		{"https overrides all and system", map[string]string{"HTTPS_PROXY": "127.0.0.1:50001", "ALL_PROXY": "socks5h://127.0.0.1:50003"}, "https://ds.free-vps.org/up.exe", "http://127.0.0.1:50001", false},
		{"all proxy", map[string]string{"ALL_PROXY": "socks5h://127.0.0.1:50003"}, "https://ds.free-vps.org/up.exe", "socks5h://127.0.0.1:50003", false},
		{"lowercase", map[string]string{"https_proxy": "http://proxy:80"}, "https://ds.free-vps.org/up.exe", "http://proxy:80", false},
		{"no proxy", map[string]string{"NO_PROXY": ".free-vps.org"}, "https://ds.free-vps.org/up.exe", "", false},
		{"no proxy star", map[string]string{"HTTPS_PROXY": "proxy:80", "NO_PROXY": "*"}, "https://ds.free-vps.org/up.exe", "", false},
		{"loopback", nil, "http://127.0.0.1:12345/api", "", false},
		{"ipv6 loopback", nil, "http://[::1]:12345/api", "", false},
		{"system", nil, "https://ds.free-vps.org/up.exe", "http://system:7890", true},
		{"http proxy is not https override", map[string]string{"HTTP_PROXY": "http-only:80"}, "https://ds.free-vps.org/up.exe", "http://system:7890", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", tc.target, nil)
			called := false
			p, err := resolve(r, func(k string) string { return tc.env[k] }, func(*http.Request) (*url.URL, error) { called = true; return url.Parse("http://system:7890") })
			got := ""
			if p != nil {
				got = p.String()
			}
			if err != nil || got != tc.want || called != tc.system {
				t.Fatalf("got %q err=%v system=%v", got, err, called)
			}
		})
	}
}

func TestInvalidProxyNeverLeaksCredentialsOrFallsBack(t *testing.T) {
	for _, value := range []string{"http://alice:secret@proxy:99999", "ftp://alice:secret@proxy", "http://alice:secret@proxy/path", "http://alice:secret@proxy:bad", "http:///missing", "http://alice:secret@proxy?secret=yes"} {
		r, _ := http.NewRequest("GET", "https://ds.free-vps.org/up.exe", nil)
		_, err := resolve(r, func(k string) string {
			if k == "HTTPS_PROXY" {
				return value
			}
			return ""
		}, func(*http.Request) (*url.URL, error) { t.Fatal("invalid proxy fell back"); return nil, nil })
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", "https://ds.free-vps.org/up.exe", nil)
	if _, err := resolve(r, func(string) string { return "" }, func(*http.Request) (*url.URL, error) { t.Fatal("cancel ignored"); return nil, nil }); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestWindowsProtocolMappingAndExclusions(t *testing.T) {
	target, _ := url.Parse("https://ds.free-vps.org/up.exe")
	for _, tc := range []struct{ servers, bypass, want string }{
		{"127.0.0.1:7890", "", "http://127.0.0.1:7890"},
		{"http=127.0.0.1:50001;https=127.0.0.1:50003", "", "http://127.0.0.1:50003"},
		{"http=127.0.0.1:50001", "", ""},
		{"socks=[::1]:1080", "", "socks5h://[::1]:1080"},
		{"127.0.0.1:7890", "*.free-vps.org", ""},
		{"127.0.0.1:7890", "<local>;*.example.com", "http://127.0.0.1:7890"},
		{"", "", ""},
	} {
		p, err := windowsProxy(target, tc.servers, tc.bypass)
		got := ""
		if p != nil {
			got = p.String()
		}
		if err != nil || got != tc.want {
			t.Fatalf("%q => %q %v", tc.servers, got, err)
		}
	}
}

// The origin uses a deliberately unresolvable hostname. Only the CONNECT proxy
// can reach it, proving HTTPS bytes use the proxy rather than merely selecting
// a URL in a unit test. A dead configured proxy must not fall back to direct.
func TestHTTPSDownloadThroughProxyAndSettingChanges(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "signed-update-bytes") }))
	defer origin.Close()
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "update.invalid:443" {
			t.Errorf("unexpected proxy request %s %s", r.Method, r.Host)
			w.WriteHeader(400)
			return
		}
		up, err := net.Dial("tcp", origin.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			w.WriteHeader(502)
			return
		}
		down, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			up.Close()
			t.Error(err)
			return
		}
		calls.Add(1)
		buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buf.Flush()
		go func() {
			defer down.Close()
			defer up.Close()
			done := make(chan struct{})
			go func() { io.Copy(up, buf); close(done) }()
			io.Copy(down, up)
			down.Close()
			<-done
		}()
	}))
	defer proxy.Close()
	var address atomic.Value
	address.Store(proxy.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Isolated test certificate only.
	transport.Proxy = func(r *http.Request) (*url.URL, error) {
		return resolve(r, func(k string) string {
			if k == "HTTPS_PROXY" {
				return address.Load().(string)
			}
			return ""
		}, func(*http.Request) (*url.URL, error) { return nil, nil })
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	r, err := client.Get("https://update.invalid/package")
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil || string(b) != "signed-update-bytes" || calls.Load() != 1 {
		t.Fatalf("proxy download failed: %q %v", b, err)
	}
	// Proxy selection is re-evaluated even while another route has an idle connection.
	address.Store("http://127.0.0.1:1")
	if r, err = client.Get("https://update.invalid/package"); err == nil {
		r.Body.Close()
		t.Fatal("reused obsolete proxy route")
	}
}
