package app

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBootstrapRequiresCapabilityAndRejectsOpaqueOrigins(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Start("127.0.0.1:0", fstest.MapFS{}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path, origin, host, token string
		want                            int
	}{
		{"unauthenticated bootstrap", "/boot.js", "", "", "", 403},
		{"bootstrap query token forbidden", "/boot.js?token=" + a.Token(), "", "", "", 403},
		{"authorized bootstrap", "/boot.js", "", "", a.Token(), 200},
		{"same origin bootstrap", "/boot.js", a.URL(), "", a.Token(), 200},
		{"opaque bootstrap", "/boot.js", "null", "", "", 403},
		{"opaque authenticated bootstrap", "/boot.js", "null", "", a.Token(), 403},
		{"foreign bootstrap", "/boot.js", "https://example.invalid", "", a.Token(), 403},
		{"forged native host", "/boot.js", "", "wails.localhost", a.Token(), 403},
		{"opaque authenticated API", "/api/config", "null", "", a.Token(), 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, _ := http.NewRequest("GET", a.URL()+test.path, nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-CloudShell-Token", test.token)
			if test.host != "" {
				request.Host = test.host
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			data, _ := io.ReadAll(response.Body)
			if response.StatusCode != test.want {
				t.Fatalf("status %d, expected %d", response.StatusCode, test.want)
			}
			if test.want != 200 && strings.Contains(string(data), a.Token()) {
				t.Fatal("rejected bootstrap exposed capability")
			}
			if test.want == 200 && (!strings.Contains(string(data), a.Token()) || response.Header.Get("Referrer-Policy") != "no-referrer") {
				t.Fatal("authorized bootstrap unavailable or referrer policy missing")
			}
		})
	}
	if !strings.HasSuffix(a.BrowserURL(), "/#token="+a.Token()) {
		t.Fatal("launch capability not confined to fragment")
	}
}
