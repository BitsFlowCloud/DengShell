//go:build desktop

package main

import (
	"cloudshell/internal/app"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNativeAndDetachedBootstrapUsePrivateAuthorization(t *testing.T) {
	a, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	assets := fstest.MapFS{"index.html": {Data: []byte("fixture")}}
	if err := a.Start("127.0.0.1:0", assets); err != nil {
		t.Fatal(err)
	}
	request := func(handler http.Handler) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://wails.localhost/boot.js", nil)
		req.Header.Set("Origin", "null")
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		return r
	}
	primary := request(nativeAssetHandler(a, assets))
	if primary.Code != 200 || !strings.Contains(primary.Body.String(), a.Token()) {
		t.Fatal("native boot missing trusted token", primary.Code)
	}
	remote := &remoteDesktopBackend{launch: detachedLaunch{Base: a.URL(), Token: a.Token(), ConfigDir: a.ConfigDirectory(), Nonce: "fixture-detached-bootstrap-00001"}, client: &http.Client{Transport: &http.Transport{Proxy: nil}}}
	defer remote.client.CloseIdleConnections()
	child := request(nativeAssetHandler(remote, assets))
	if child.Code != 200 || !strings.Contains(child.Body.String(), a.Token()) || !strings.Contains(child.Body.String(), remote.launch.Nonce) {
		t.Fatal("detached bootstrap lost credentials or handoff nonce", child.Code)
	}
	remote.launch.Token = "invalid-stale-token"
	stale := request(nativeAssetHandler(remote, assets))
	if stale.Code != 403 || strings.Contains(stale.Body.String(), "window.CLOUDSHELL") {
		t.Fatal("failed parent boot was served as executable success", stale.Code)
	}
	response, err := http.Get(a.URL() + "/boot.js")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 403 || strings.Contains(string(data), a.Token()) {
		t.Fatal("native authorization leaked to public listener")
	}
}
