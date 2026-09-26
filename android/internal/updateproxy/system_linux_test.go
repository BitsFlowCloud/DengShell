package updateproxy

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGNOMEProxySettings(t *testing.T) {
	target, _ := url.Parse("https://ds.free-vps.org/up.deb")
	base := `org.gnome.system.proxy mode 'manual'
org.gnome.system.proxy use-same-proxy false
org.gnome.system.proxy ignore-hosts ['localhost', '127.0.0.0/8', '::1']
org.gnome.system.proxy.http host '127.0.0.1'
org.gnome.system.proxy.http port 7890
org.gnome.system.proxy.https host '127.0.0.1'
org.gnome.system.proxy.https port 7891
org.gnome.system.proxy.socks host '127.0.0.1'
org.gnome.system.proxy.socks port 1080
`
	for _, tc := range []struct {
		name, data, want string
		fail             bool
	}{
		{"HTTPS", base, "http://127.0.0.1:7891", false},
		{"same HTTP", strings.Replace(base, "use-same-proxy false", "use-same-proxy true", 1), "http://127.0.0.1:7890", false},
		{"HTTP fallback", strings.Replace(base, "https port 7891", "https port 0", 1), "http://127.0.0.1:7890", false},
		{"SOCKS fallback", strings.ReplaceAll(strings.Replace(base, "https port 7891", "https port 0", 1), "http port 7890", "http port 0"), "socks5h://127.0.0.1:1080", false},
		{"disabled", strings.Replace(base, "'manual'", "'none'", 1), "", false},
		{"PAC explicit error", strings.Replace(base, "'manual'", "'auto'", 1), "", true},
		{"bypass", strings.Replace(base, "'localhost'", "'*.free-vps.org'", 1), "", false},
		{"credentials", strings.Replace(base, "use-same-proxy false", "use-same-proxy true", 1) + "org.gnome.system.proxy.http use-authentication true\norg.gnome.system.proxy.http authentication-user 'alice'\norg.gnome.system.proxy.http authentication-password 'a b'\n", "http://alice:a%20b@127.0.0.1:7890", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := gnomeProxy(target, tc.data)
			got := ""
			if p != nil {
				got = p.String()
			}
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("%q %v", got, err)
			}
		})
	}
}

func TestKDEProxySettings(t *testing.T) {
	target, _ := url.Parse("https://ds.free-vps.org/up.deb")
	for _, tc := range []struct {
		data, want string
		fail       bool
	}{
		{"ProxyType=1\nhttpsProxy=http://127.0.0.1 7890", "http://127.0.0.1:7890", false},
		{"ProxyType=1\nhttpsProxy=http://127.0.0.1:7890", "http://127.0.0.1:7890", false},
		{"ProxyType=1\nsocksProxy=socks://127.0.0.1:1080", "socks5h://127.0.0.1:1080", false},
		{"ProxyType=1\nsocksProxy=127.0.0.1:1080", "socks5h://127.0.0.1:1080", false},
		{"ProxyType=0\nhttpsProxy=http://127.0.0.1:7890", "", false},
		{"ProxyType=1\nhttpsProxy=http://proxy:80\nNoProxyFor=*.free-vps.org", "", false},
		{"ProxyType=1\nhttpsProxy=http://proxy:80\nNoProxyFor=*.free-vps.org\nReversedException=true", "http://proxy:80", false},
		{"ProxyType=4\nhttpsProxy=CUSTOM_PROXY", "http://custom:7890", false},
		{"ProxyType=2\nProxy Config Script=http://pac/file", "", true},
	} {
		p, err := kdeProxy(target, "[Proxy Settings]\n"+tc.data, func(k string) string {
			if k == "CUSTOM_PROXY" {
				return "http://custom:7890"
			}
			return ""
		})
		got := ""
		if p != nil {
			got = p.String()
		}
		if (err != nil) != tc.fail || got != tc.want {
			t.Fatalf("%s => %q %v", tc.data, got, err)
		}
	}
}

func TestLinuxReadsDesktopChangesWithoutRestart(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "")
	}
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	r, _ := http.NewRequest("GET", "https://ds.free-vps.org/up.deb", nil)
	for _, port := range []string{"50001", "50003"} {
		if err := os.WriteFile(filepath.Join(dir, "kioslaverc"), []byte("[Proxy Settings]\nProxyType=1\nhttpsProxy=http://127.0.0.1:"+port), 0600); err != nil {
			t.Fatal(err)
		}
		p, err := Proxy(r)
		if err != nil || p == nil || p.Port() != port {
			t.Fatalf("settings not refreshed: %v %v", p, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "kioslaverc"), []byte("[Proxy Settings]\nProxyType=0"), 0600); err != nil {
		t.Fatal(err)
	}
	if p, err := Proxy(r); p != nil || err != nil {
		t.Fatalf("disabled proxy still used: %v %v", p, err)
	}
}
