package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const windowViewTTL = 15 * time.Second

var windowViewIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,96}$`)

// Views are ephemeral renderers of the same backend, never saved to user data.
// NativeHandle is supplied by the desktop bridge; browser views leave it empty.
type WindowViewUpdate struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	SessionIDs   []string `json:"sessionIds"`
	Visible      bool     `json:"visible"`
	NativeHandle string   `json:"nativeHandle,omitempty"`
}
type WindowViewInfo struct {
	WindowViewUpdate
	UpdatedAt time.Time `json:"updatedAt"`
}
type WindowMergeRequest struct {
	SourceID string               `json:"sourceId"`
	TargetID string               `json:"targetId"`
	Handoff  WindowHandoffRequest `json:"handoff"`
}

func (a *App) RegisterWindowView(input WindowViewUpdate) (WindowViewInfo, error) {
	if !windowViewIDPattern.MatchString(input.ID) || len(input.Title) > 240 || len(input.SessionIDs) > 256 || len(input.NativeHandle) > 32 {
		return WindowViewInfo{}, errors.New("窗口注册信息无效")
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(input.SessionIDs))
	for _, id := range input.SessionIDs {
		if len(id) > 128 || id == "" {
			return WindowViewInfo{}, errors.New("窗口会话信息无效")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	input.SessionIDs = ids
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		input.Title = "DengShell"
	}
	value := WindowViewInfo{WindowViewUpdate: input, UpdatedAt: time.Now()}
	a.mu.Lock()
	if a.windowViews == nil {
		a.windowViews = map[string]WindowViewInfo{}
	}
	// A browser heartbeat cannot accidentally erase the native identity.
	if old, ok := a.windowViews[input.ID]; ok && value.NativeHandle == "" {
		value.NativeHandle = old.NativeHandle
	}
	a.windowViews[input.ID] = value
	a.mu.Unlock()
	return value, nil
}
func (a *App) WindowViews() []WindowViewInfo {
	now := time.Now()
	a.mu.Lock()
	values := make([]WindowViewInfo, 0, len(a.windowViews))
	expired := []string{}
	for id, value := range a.windowViews {
		if now.Sub(value.UpdatedAt) > windowViewTTL {
			delete(a.windowViews, id)
			expired = append(expired, id)
			continue
		}
		value.SessionIDs = append([]string{}, value.SessionIDs...)
		values = append(values, value)
	}
	a.mu.Unlock()
	for _, id := range expired {
		a.cancelIncomingWindowHandoffs(id)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Title != values[j].Title {
			return values[i].Title < values[j].Title
		}
		return values[i].ID < values[j].ID
	})
	return values
}
func (a *App) ForgetWindowView(id string) {
	a.mu.Lock()
	delete(a.windowViews, id)
	a.mu.Unlock()
	a.cancelIncomingWindowHandoffs(id)
}
func (a *App) cancelIncomingWindowHandoffs(id string) {
	a.mu.Lock()
	nonces := []string{}
	for nonce, h := range a.windowHandoffs {
		if h.TargetViewID == id {
			if done, _ := h.completion.result(); !done {
				nonces = append(nonces, nonce)
			}
		}
	}
	a.mu.Unlock()
	for _, nonce := range nonces {
		go func(n string) { _, _ = a.CancelWindowHandoff(n) }(nonce)
	}
}
func containsWindowSession(v WindowViewInfo, id string) bool {
	for _, sid := range v.SessionIDs {
		if sid == id {
			return true
		}
	}
	return false
}
func (a *App) mergeViewsValidLocked(input WindowMergeRequest) error {
	if input.SourceID == input.TargetID {
		return errors.New("请选择另一个 DengShell 窗口")
	}
	source, sourceOK := a.windowViews[input.SourceID]
	target, targetOK := a.windowViews[input.TargetID]
	now := time.Now()
	if !sourceOK || now.Sub(source.UpdatedAt) > windowViewTTL {
		return errors.New("原窗口已离线，请重试")
	}
	if !targetOK || now.Sub(target.UpdatedAt) > windowViewTTL || !target.Visible {
		return errors.New("目标窗口已关闭或隐藏，请选择可见窗口")
	}
	if !containsWindowSession(source, input.Handoff.SessionID) {
		return errors.New("此 SSH 标签已不在原窗口")
	}
	if containsWindowSession(target, input.Handoff.SessionID) {
		return errors.New("目标窗口已包含此 SSH 标签")
	}
	return nil
}
func (a *App) MergeWindow(input WindowMergeRequest) (WindowHandoffInfo, error) {
	a.mu.Lock()
	err := a.mergeViewsValidLocked(input)
	a.mu.Unlock()
	if err != nil {
		return WindowHandoffInfo{}, err
	}
	value, err := a.ReserveWindowHandoff(input.Handoff)
	if err != nil {
		return WindowHandoffInfo{}, err
	}
	a.mu.Lock()
	err = a.mergeViewsValidLocked(input)
	if err == nil {
		value.SourceViewID = input.SourceID
		value.TargetViewID = input.TargetID
		a.windowHandoffs[value.Nonce] = value
	}
	a.mu.Unlock()
	if err != nil {
		_, _ = a.CancelWindowHandoff(value.Nonce)
		return WindowHandoffInfo{}, err
	}
	return WindowHandoffInfo{SessionID: value.SessionID, Nonce: value.Nonce}, nil
}
func (a *App) IncomingWindowHandoffs(id string) ([]WindowHandoffInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	view, ok := a.windowViews[id]
	if !ok || time.Since(view.UpdatedAt) > windowViewTTL {
		return nil, errors.New("窗口未注册或已离线")
	}
	values := []WindowHandoffInfo{}
	for _, h := range a.windowHandoffs {
		if h.TargetViewID != id || h.Cancelled || h.SnapshotReleased {
			continue
		}
		if done, _ := h.completion.result(); done {
			continue
		}
		values = append(values, WindowHandoffInfo{SessionID: h.SessionID, Nonce: h.Nonce})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Nonce < values[j].Nonce })
	return values, nil
}
func (a *App) registerWindowViewsHTTP(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/windows/views", func(w http.ResponseWriter, r *http.Request) {
		var input WindowViewUpdate
		if !decode(w, r, &input) {
			return
		}
		value, err := a.RegisterWindowView(input)
		respond(w, value, err)
	})
	mux.HandleFunc("GET /api/windows/views", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.WindowViews()) })
	mux.HandleFunc("DELETE /api/windows/views/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.ForgetWindowView(r.PathValue("id"))
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/windows/views/{id}/incoming", func(w http.ResponseWriter, r *http.Request) {
		value, err := a.IncomingWindowHandoffs(r.PathValue("id"))
		respond(w, value, err)
	})
	mux.HandleFunc("POST /api/windows/merge", func(w http.ResponseWriter, r *http.Request) {
		var input WindowMergeRequest
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, 400, errors.New("窗口合并数据无法读取"))
			return
		}
		value, err := a.MergeWindow(input)
		respond(w, value, err)
	})
}
