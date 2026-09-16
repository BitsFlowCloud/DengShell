//go:build desktop

package main

import (
	"bytes"
	"cloudshell/internal/app"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

type desktopBackend interface {
	RequireUnlocked() error
	URL() string
	Token() string
	ConfigDirectory() string
	Appearance() app.Appearance
	SaveWindowState(int, int, bool) error
	Handler(fs.FS) http.Handler
	ImportLocalAsset(string, string, string) (app.ManagedAsset, error)
	DownloadTo(string, string, string) error
	DownloadArchiveTo(string, string, string) error
	PrepareExternalFile(string, string) (string, error)
	PreparedUpdate(string) (app.UpdateDownload, error)
}

// This wrapper belongs only to Wails' in-process asset server. The public
// loopback listener always uses App.Handler directly and requires credentials.
func nativeAssetHandler(application desktopBackend, assets fs.FS) http.Handler {
	localHandler := application.Handler(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		r.Host = strings.TrimPrefix(application.URL(), "http://")
		r.Header.Del("Origin")
		if r.URL.Path == "/boot.js" {
			r.Header.Set("X-CloudShell-Token", application.Token())
		}
		localHandler.ServeHTTP(w, r)
	})
}

type detachedLaunch struct {
	Base      string `json:"base"`
	Token     string `json:"token"`
	ConfigDir string `json:"configDir"`
	Nonce     string `json:"nonce"`
	SessionID string `json:"sessionId"`
}
type remoteDesktopBackend struct {
	launch     detachedLaunch
	client     *http.Client
	mu         sync.Mutex
	appearance app.Appearance
}

func (r *remoteDesktopBackend) URL() string             { return r.launch.Base }
func (r *remoteDesktopBackend) Token() string           { return r.launch.Token }
func (r *remoteDesktopBackend) ConfigDirectory() string { return r.launch.ConfigDir }
func (r *remoteDesktopBackend) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.URL()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CloudShell-Token", r.Token())
	response, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接主窗口服务：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		var value struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value)
		if value.Error == "" {
			value.Error = "独立窗口操作失败"
		}
		return errors.New(value.Error)
	}
	if output == nil {
		_, err = io.Copy(io.Discard, response.Body)
		return err
	}
	return json.NewDecoder(io.LimitReader(response.Body, 40<<20)).Decode(output)
}
func (r *remoteDesktopBackend) RequireUnlocked() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return r.request(ctx, "GET", "/api/security-lock/access", nil, nil)
}
func (r *remoteDesktopBackend) Appearance() app.Appearance {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var value struct {
		Appearance app.Appearance `json:"appearance"`
	}
	if r.request(ctx, "GET", "/api/config", nil, &value) == nil {
		r.mu.Lock()
		r.appearance = value.Appearance
		r.mu.Unlock()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.appearance
}
func (r *remoteDesktopBackend) SaveWindowState(int, int, bool) error { return nil }
func (r *remoteDesktopBackend) PreparedUpdate(string) (app.UpdateDownload, error) {
	return app.UpdateDownload{}, errors.New("请在主窗口中执行更新，并先关闭独立窗口")
}
func (r *remoteDesktopBackend) native(method, session, remote, local, kind, name string, output any) error {
	return r.request(context.Background(), "POST", "/api/windows/native", map[string]string{"Method": method, "SessionID": session, "Remote": remote, "Local": local, "Kind": kind, "Name": name}, output)
}
func (r *remoteDesktopBackend) ImportLocalAsset(kind, path, name string) (app.ManagedAsset, error) {
	var value app.ManagedAsset
	err := r.native("import-asset", "", "", path, kind, name, &value)
	return value, err
}
func (r *remoteDesktopBackend) DownloadTo(s, p, l string) error {
	return r.native("download", s, p, l, "", "", nil)
}
func (r *remoteDesktopBackend) DownloadArchiveTo(s, p, l string) error {
	return r.native("archive", s, p, l, "", "", nil)
}
func (r *remoteDesktopBackend) PrepareExternalFile(s, p string) (string, error) {
	var value string
	err := r.native("external-file", s, p, "", "", "", &value)
	return value, err
}
func (r *remoteDesktopBackend) Handler(_ fs.FS) http.Handler {
	target, _ := url.Parse(r.URL())
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = r.client.Transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "主窗口服务已关闭", http.StatusServiceUnavailable)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		q = q.Clone(q.Context())
		q.Host = target.Host
		if q.URL.Path == "/boot.js" {
			ctx, cancel := context.WithTimeout(q.Context(), 5*time.Second)
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, "GET", r.URL()+"/boot.js", nil)
			request.Header.Set("X-CloudShell-Token", r.Token())
			response, err := r.client.Do(request)
			if err != nil {
				http.Error(w, "主窗口服务已关闭", 503)
				return
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				http.Error(w, "主窗口启动凭据已失效", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = io.Copy(w, io.LimitReader(response.Body, 4<<20))
			nonce, _ := json.Marshal(r.launch.Nonce)
			fmt.Fprintf(w, "\nwindow.CLOUDSHELL.detachedNonce=%s;window.CLOUDSHELL.startupAnimation=false;", nonce)
			return
		}
		proxy.ServeHTTP(w, q)
	})
}
