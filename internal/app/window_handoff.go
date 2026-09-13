package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"
)

// Runtime handoffs are never written into the portable configuration.
type WindowHandoffRequest struct {
	SessionID   string          `json:"sessionId"`
	Nonce       string          `json:"nonce"`
	Terminal    string          `json:"terminal"`
	Cols        int             `json:"cols"`
	Rows        int             `json:"rows"`
	CWD         string          `json:"cwd"`
	Follow      bool            `json:"follow"`
	ClientState json.RawMessage `json:"clientState"`
}
type WindowHandoffInfo struct {
	SessionID string `json:"sessionId"`
	Nonce     string `json:"nonce"`
}

type WindowHandoff struct {
	WindowHandoffRequest
	Session          *Session  `json:"session"`
	CreatedAt        time.Time `json:"createdAt"`
	SnapshotReleased bool      `json:"-"`
	Cancelled        bool      `json:"-"`
	SourceViewID     string    `json:"-"`
	TargetViewID     string    `json:"-"`
	completion       *terminalHandoff
}

var windowNoncePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{24,96}$`)

func (a *App) ReserveWindowHandoff(input WindowHandoffRequest) (WindowHandoff, error) {
	if !windowNoncePattern.MatchString(input.Nonce) || len(input.Terminal) > 24<<20 || len(input.ClientState) > 2<<20 || input.Cols < 2 || input.Cols > 1000 || input.Rows < 2 || input.Rows > 500 {
		return WindowHandoff{}, errors.New("独立窗口交接数据无效或终端内容过大")
	}
	if len(input.ClientState) > 0 && !json.Valid(input.ClientState) {
		return WindowHandoff{}, errors.New("窗口状态数据无效")
	}
	if _, err := remotePath(input.CWD); err != nil {
		return WindowHandoff{}, err
	}
	s, err := a.session(input.SessionID)
	if err != nil {
		return WindowHandoff{}, err
	}
	completion := s.readyTerminalHandoff(input.Nonce)
	if completion == nil {
		return WindowHandoff{}, errors.New("终端尚未准备好拆分，请重试")
	}
	value := WindowHandoff{WindowHandoffRequest: input, Session: s, CreatedAt: time.Now(), completion: completion}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.windowHandoffs == nil {
		a.windowHandoffs = map[string]WindowHandoff{}
	}
	if _, exists := a.windowHandoffs[input.Nonce]; exists {
		return WindowHandoff{}, errors.New("此窗口交接已存在")
	}
	a.windowHandoffs[input.Nonce] = value
	nonce := input.Nonce
	time.AfterFunc(90*time.Second, func() { a.ForgetWindowHandoff(nonce) })
	return value, nil
}
func (a *App) WindowHandoff(nonce string) (WindowHandoff, error) {
	a.mu.Lock()
	value, ok := a.windowHandoffs[nonce]
	a.mu.Unlock()
	if !ok {
		return WindowHandoff{}, errors.New("窗口交接已结束或过期，请返回原窗口重试")
	}
	if _, err := a.session(value.SessionID); err != nil {
		return WindowHandoff{}, err
	}
	return value, nil
}

// Cancel is serialized by the PTY relay. Once ownership has changed, the caller
// must keep the new window alive instead of trying to revive the old socket.
func (a *App) CancelWindowHandoff(nonce string) (bool, error) {
	// Keep cancellation idempotent even if the source later disconnects. This
	// lightweight record remains until normal TTL/owner cleanup, without data.
	a.mu.Lock()
	value, ok := a.windowHandoffs[nonce]
	a.mu.Unlock()
	if !ok {
		return false, errors.New("窗口交接已结束或过期，请返回原窗口重试")
	}
	if value.Cancelled {
		return false, nil
	}
	finish := func() (bool, bool, error) {
		done, err := value.completion.result()
		if !done {
			return false, false, err
		}
		if err == nil {
			a.ReleaseWindowSnapshot(nonce)
			return true, true, nil
		}
		if errors.Is(err, errTerminalHandoffCancelled) {
			a.markWindowHandoffCancelled(nonce)
			return true, false, nil
		}
		return true, false, err
	}
	if done, attached, err := finish(); done || err != nil {
		return attached, err
	}
	err := a.ResumeTerminalHandoff(value.SessionID, nonce)
	if err == nil {
		a.markWindowHandoffCancelled(nonce)
		return false, nil
	}
	// The actor serializes cancel versus attachment. A timeout, child restore
	// failure or another caller may already have completed the same cancellation.
	if done, attached, resultErr := finish(); done {
		return attached, resultErr
	}
	return false, err
}

func (a *App) markWindowHandoffCancelled(nonce string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if value, ok := a.windowHandoffs[nonce]; ok {
		value.Terminal, value.ClientState = "", nil
		value.Cancelled, value.SnapshotReleased = true, true
		a.windowHandoffs[nonce] = value
	}
}

// Once the new socket owns the PTY, it has already restored the snapshot. Keep
// only the small cancellation/status record rather than a second scrollback copy.
func (a *App) ReleaseWindowSnapshot(nonce string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if value, ok := a.windowHandoffs[nonce]; ok {
		value.Terminal = ""
		value.ClientState = nil
		value.SnapshotReleased = true
		a.windowHandoffs[nonce] = value
	}
}
func (a *App) ForgetWindowHandoff(nonce string) {
	a.mu.Lock()
	delete(a.windowHandoffs, nonce)
	a.mu.Unlock()
}
func (a *App) SetWindowLauncher(launch func(context.Context, WindowHandoffRequest) error) {
	a.mu.Lock()
	a.windowLauncher = launch
	a.mu.Unlock()
}
func (a *App) registerWindowHandoffHTTP(mux *http.ServeMux) {
	read := func(w http.ResponseWriter, r *http.Request) (WindowHandoffRequest, bool) {
		var input WindowHandoffRequest
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, 400, errors.New("独立窗口快照无法读取"))
			return input, false
		}
		return input, true
	}
	mux.HandleFunc("POST /api/windows/handoff", func(w http.ResponseWriter, r *http.Request) {
		input, ok := read(w, r)
		if !ok {
			return
		}
		value, err := a.ReserveWindowHandoff(input)
		respond(w, WindowHandoffInfo{SessionID: value.SessionID, Nonce: value.Nonce}, err)
	})
	mux.HandleFunc("POST /api/windows/launch", func(w http.ResponseWriter, r *http.Request) {
		input, ok := read(w, r)
		if !ok {
			return
		}
		a.mu.Lock()
		launch := a.windowLauncher
		a.mu.Unlock()
		if launch == nil {
			writeError(w, 400, errors.New("当前运行方式没有原生窗口启动器"))
			return
		}
		respond(w, map[string]bool{"ok": true}, launch(r.Context(), input))
	})
	mux.HandleFunc("GET /api/windows/handoff/{nonce}", func(w http.ResponseWriter, r *http.Request) {
		value, err := a.WindowHandoff(r.PathValue("nonce"))
		if err == nil && value.TargetViewID != "" && r.URL.Query().Get("viewId") != value.TargetViewID {
			writeError(w, 403, errors.New("此 SSH 标签只能由指定的目标窗口接收"))
			return
		}
		if err == nil {
			if done, resultErr := value.completion.result(); done {
				err = resultErr
				if err == nil {
					err = errors.New("此会话已经由独立窗口接手，请在该窗口继续操作")
				}
			} else if value.SnapshotReleased {
				err = errors.New("此窗口快照已释放，请返回原窗口重试")
			}
		}
		respond(w, value, err)
	})
	mux.HandleFunc("GET /api/windows/handoff/{nonce}/check", func(w http.ResponseWriter, r *http.Request) {
		value, err := a.WindowHandoff(r.PathValue("nonce"))
		if err == nil && !a.TerminalHandoffReady(value.SessionID, value.Nonce) {
			err = errors.New("终端交接已结束，请返回原窗口重试")
		}
		respond(w, WindowHandoffInfo{SessionID: value.SessionID, Nonce: value.Nonce}, err)
	})
	mux.HandleFunc("GET /api/windows/handoff/{nonce}/status", func(w http.ResponseWriter, r *http.Request) {
		value, err := a.WindowHandoff(r.PathValue("nonce"))
		if err != nil {
			writeError(w, 400, err)
			return
		}
		done, resultErr := value.completion.result()
		err = resultErr
		if !done && err == nil {
			writeJSON(w, map[string]bool{"attached": false})
			return
		}
		if err == nil {
			a.ReleaseWindowSnapshot(value.Nonce)
		}
		respond(w, map[string]bool{"attached": err == nil}, err)
	})
	mux.HandleFunc("POST /api/windows/handoff/{nonce}/cancel", func(w http.ResponseWriter, r *http.Request) {
		attached, err := a.CancelWindowHandoff(r.PathValue("nonce"))
		respond(w, map[string]bool{"attached": attached, "ok": err == nil}, err)
	})
	// Local dialogs are owned by the child window. Actual file/config operations
	// still run in the single primary backend, preventing concurrent store writers.
	mux.HandleFunc("POST /api/windows/native", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Method, SessionID, Remote, Local, Kind, Name string }
		if !decode(w, r, &input) {
			return
		}
		var value any = map[string]bool{"ok": true}
		var err error
		switch input.Method {
		case "import-asset":
			value, err = a.ImportLocalAsset(input.Kind, input.Local, input.Name)
		case "download":
			err = a.DownloadTo(input.SessionID, input.Remote, input.Local)
		case "archive":
			err = a.DownloadArchiveTo(input.SessionID, input.Remote, input.Local)
		case "external-file":
			value, err = a.PrepareExternalFile(input.SessionID, input.Remote)
		default:
			err = errors.New("未知独立窗口本地操作")
		}
		respond(w, value, err)
	})
	mux.HandleFunc("GET /api/windows/alive", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"ok":true}`) })
}
