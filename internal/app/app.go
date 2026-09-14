package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type App struct {
	uiFontMu         sync.Mutex
	uiFontsAtStartup map[string]bool
	activeUIFontID   string
	fontLibrary      fontLibraryState
	ctx              context.Context
	cancel           context.CancelFunc
	store            *Store
	mu               sync.Mutex
	sessions         map[string]*Session
	transfers        map[string]*Transfer
	diagnostics      map[string]*Diagnostic
	mtrMu            sync.Mutex
	mtrPlans         map[string]*MTRInstallPlan
	mtrInstallations map[string]*MTRInstallation
	token            string
	baseURL          string
	server           *http.Server
	updateCheck      startupUpdate
	windowHandoffs   map[string]WindowHandoff
	windowViews      map[string]WindowViewInfo
	windowLauncher   func(context.Context, WindowHandoffRequest) error
}

func New(configDir string) (*App, error) {
	s, err := OpenStore(configDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{ctx: ctx, cancel: cancel, store: s, sessions: map[string]*Session{}, transfers: map[string]*Transfer{}, token: randomID()}
	a.initializeUIFonts()
	return a, nil
}
func (a *App) URL() string   { return a.baseURL }
func (a *App) Token() string { return a.token }

// BrowserURL carries the bootstrap capability in a fragment, which browsers do
// not send to HTTP servers. bootstrap.js removes it before loading app assets.
func (a *App) BrowserURL() string { return a.baseURL + "/#token=" + url.QueryEscape(a.token) }
func (a *App) Start(address string, assets fs.FS) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if host != "127.0.0.1" && host != "::1" {
		return errors.New("只支持绑定本机回环地址")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	a.baseURL = "http://" + listener.Addr().String()
	a.beginUpdateCheck()
	a.server = &http.Server{Handler: a.Handler(assets), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = a.server.Serve(listener) }()
	return nil
}
func (a *App) Close() {
	a.cancel()
	a.closeDiagnostics()
	a.mu.Lock()
	sessions := []*Session{}
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.mu.Unlock()
	for _, s := range sessions {
		s.Close()
	}
	if a.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = a.server.Shutdown(ctx)
	}
}

func (a *App) allowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == a.baseURL || origin == "http://wails.localhost" || origin == "https://wails.localhost" || origin == "wails://wails.localhost" || origin == "wails://wails"
}
func (a *App) Handler(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boot.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		preferences := a.Appearance()
		uiPreferences := map[string]json.RawMessage{}
		for _, key := range []string{"cloudshell.layout", "dengshell.appearance-palette", "dengshell.server-groups.collapsed", "dengshell.server-manager", "dengshell.workspace"} {
			if value, ok := preferences.Layout[key]; ok {
				uiPreferences[key] = value
			}
		}
		data, _ := json.Marshal(map[string]any{"base": a.baseURL, "token": a.token, "startupAnimation": preferences.StartupAnimation, "theme": preferences.Theme, "uiScale": preferences.UIScale, "uiPreferences": uiPreferences, "uiFontRuntime": a.uiFontRuntime()})
		fmt.Fprintf(w, "window.CLOUDSHELL = %s;", data)
	})
	mux.HandleFunc("GET /api/ui-fonts/runtime", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.uiFontRuntime()) })
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.store.List()) })
	a.registerWindowHandoffHTTP(mux)
	a.registerCommandHistoryHTTP(mux)
	a.registerWindowViewsHTTP(mux)
	a.registerConnectionManagementHTTP(mux)
	a.registerFinalShellImportHTTP(mux)
	a.registerSettingsHTTP(mux)
	a.registerFontLibraryHTTP(mux)
	a.registerDiagnosticsHTTP(mux)
	a.registerMTRInstallationHTTP(mux)
	a.registerUpdateHTTP(mux)
	a.registerFileToolsHTTP(mux)
	a.registerPromptStyleHTTP(mux)
	a.registerUtilitiesHTTP(mux)
	mux.HandleFunc("POST /api/commands", a.saveCommandHTTP)
	mux.HandleFunc("POST /api/command-groups", a.saveCommandGroupHTTP)
	mux.HandleFunc("POST /api/commands/move", a.moveCommandHTTP)
	mux.HandleFunc("POST /api/command-groups/move", a.moveCommandGroupHTTP)
	mux.HandleFunc("POST /api/command-groups/rename", a.renameCommandGroupHTTP)
	mux.HandleFunc("POST /api/keys", a.saveKeyHTTP)
	mux.HandleFunc("DELETE /api/keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, a.store.DeleteKey(r.PathValue("id")))
	})
	mux.HandleFunc("DELETE /api/commands/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, a.store.DeleteCommand(r.PathValue("id")))
	})
	mux.HandleFunc("POST /api/profiles", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Profile
			ClearSecret bool `json:"clearSecret"`
		}
		if !decode(w, r, &input) {
			return
		}
		p, err := a.store.Save(input.Profile, input.ClearSecret)
		respond(w, p, err)
	})
	mux.HandleFunc("DELETE /api/profiles/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, a.trashProfile(r.PathValue("id")))
	})
	mux.HandleFunc("POST /api/groups", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Action, Name, NewName string }
		if !decode(w, r, &input) {
			return
		}
		respond(w, map[string]bool{"ok": true}, a.store.Group(input.Action, input.Name, input.NewName))
	})
	mux.HandleFunc("POST /api/profiles/{id}/reset-key", func(w http.ResponseWriter, r *http.Request) {
		p, err := a.store.Get(r.PathValue("id"))
		if err == nil {
			err = a.store.ResetHostKey(p)
		}
		respond(w, map[string]bool{"ok": true}, err)
	})
	mux.HandleFunc("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ProfileID       string           `json:"profileId"`
			Secret          string           `json:"secret"`
			HostKeyApproval *HostKeyApproval `json:"hostKeyApproval"`
		}
		if !decode(w, r, &input) {
			return
		}
		s, err := a.ConnectWithHostKeyApproval(r.Context(), input.ProfileID, input.Secret, input.HostKeyApproval)
		respond(w, s, err)
	})
	mux.HandleFunc("DELETE /api/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.disconnect(r.PathValue("id"))
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/sessions/{id}/terminal", a.terminal)
	mux.HandleFunc("GET /api/sessions/{id}/files", a.listFiles)
	mux.HandleFunc("POST /api/sessions/{id}/file-action", a.fileAction)
	mux.HandleFunc("GET /api/sessions/{id}/download", a.download)
	mux.HandleFunc("POST /api/sessions/{id}/upload", a.uploadHTTP)
	mux.HandleFunc("POST /api/sessions/{id}/upload-local", a.uploadLocal)
	mux.HandleFunc("GET /api/sessions/{id}/stats", a.statsHTTP)
	mux.HandleFunc("GET /api/sessions/{id}/network", a.networkHTTP)
	mux.HandleFunc("GET /api/sessions/{id}/latency", a.latencyHTTP)
	mux.HandleFunc("POST /api/transfers/{id}/retry", a.retryTransfer)
	mux.HandleFunc("GET /api/transfers", a.listTransfers)
	mux.HandleFunc("DELETE /api/transfers/{id}", a.cancelTransfer)
	mux.Handle("/", http.FileServer(http.FS(assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if parsed, err := url.Parse(a.baseURL); err == nil && a.baseURL != "" && host != parsed.Host {
			writeError(w, 403, errors.New("无效的本地访问地址"))
			return
		}
		if !a.allowedOrigin(r) {
			writeError(w, 403, errors.New("不允许此来源访问"))
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CloudShell-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/boot.js" || strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			token := r.Header.Get("X-CloudShell-Token")
			if token == "" && (strings.HasSuffix(r.URL.Path, "/terminal") || strings.HasSuffix(r.URL.Path, "/download") || strings.HasSuffix(r.URL.Path, "/archive")) {
				token = r.URL.Query().Get("token")
			}
			if token != a.token {
				writeError(w, 403, errors.New("本地会话已更新，请刷新或重启窗口"))
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(value); err != nil {
		writeError(w, 400, errors.New("请求数据无效"))
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	payload := map[string]any{"error": err.Error()}
	var coded interface{ Code() string }
	if errors.As(err, &coded) {
		payload["code"] = coded.Code()
	}
	var hostKey *HostKeyError
	if errors.As(err, &hostKey) {
		payload["hostKey"] = hostKey
	}
	_ = json.NewEncoder(w).Encode(payload)
}
func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, value)
}
