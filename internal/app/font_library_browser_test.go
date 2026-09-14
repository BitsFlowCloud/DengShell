package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in browser fixture: an isolated Store and local, reviewed website files.
// Nothing reads the real profile directory or makes a public network request.
func TestFontLibraryBrowserFixture(t *testing.T) {
	root := os.Getenv("DENG_FONT_QA_DIR")
	if root == "" {
		t.Skip("set DENG_FONT_QA_DIR for the isolated browser fixture")
	}
	a, err := New(filepath.Join(root, "browser-data"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.fontLibrary.client = &http.Client{Transport: fontTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "ds.free-vps.org" || !strings.HasPrefix(r.URL.Path, "/fonts/") {
			return nil, os.ErrPermission
		}
		if _, err := os.Stat(filepath.Join(root, "offline")); err == nil {
			return nil, os.ErrNotExist
		}
		if _, err := os.Stat(filepath.Join(root, "slow")); err == nil && strings.Contains(r.URL.Path, "/files/") {
			select {
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		file, err := os.Open(filepath.Join(root, "website", r.URL.Path))
		if err != nil {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header), Request: r}, nil
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: file, ContentLength: info.Size(), Header: make(http.Header), Request: r}, nil
	})}
	handler := a.Handler(os.DirFS(filepath.Join("..", "..", "web")))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/updates/check" {
			writeJSON(w, map[string]string{"status": "none"})
			return
		}
		handler.ServeHTTP(w, r)
	}))
	a.baseURL = "http://" + server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	data, _ := json.Marshal(map[string]string{"url": a.BrowserURL(), "token": a.Token()})
	if err = os.WriteFile(filepath.Join(root, "browser-fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(root, "stop-browser")); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("browser fixture timed out")
}
