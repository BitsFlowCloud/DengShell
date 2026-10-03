package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const aiSettingsFile = "dengshell.ai.enc"
const aiSettingsAAD = "DengShell/ai/v1"
const aiSettingsLimit = 256 << 10

// AI credentials deliberately live outside Config: connection export, sync and
// browser bootstrap must never include them. Enabling control is session-only
// frontend state, not a persisted setting that silently re-enables on restart.
type aiProvider struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Format          string  `json:"format"`
	BaseURL         string  `json:"baseURL"`
	Model           string  `json:"model"`
	ReasoningEffort string  `json:"reasoningEffort,omitempty"`
	APIKey          string  `json:"apiKey,omitempty"`
	HasKey          bool    `json:"hasKey"`
	ClearKey        bool    `json:"clearKey,omitempty"`
	Proxy           aiProxy `json:"proxy"`
}

type aiSettings struct {
	Provider  string       `json:"provider"`
	Providers []aiProvider `json:"providers"`
	Timeout   int          `json:"timeout"`
}

type aiOperation struct{ cancel context.CancelFunc }
type aiState struct {
	mu        sync.Mutex
	requests  map[string]*aiOperation
	cancelled map[string]time.Time
	closed    bool
}

func aiDefaultSettings() aiSettings {
	return aiSettings{Providers: []aiProvider{}, Timeout: 120}
}

func aiPublicSettings(s aiSettings) aiSettings {
	s.Providers = append([]aiProvider{}, s.Providers...)
	for i := range s.Providers {
		s.Providers[i].HasKey = s.Providers[i].APIKey != ""
		s.Providers[i].APIKey = ""
		s.Providers[i].ClearKey = false
		s.Providers[i].Proxy = aiPublicProxy(s.Providers[i].Proxy)
	}
	return s
}

func (a *App) loadAISettings() (aiSettings, error) {
	data, err := readConfigFile(filepath.Join(a.store.dir, aiSettingsFile), aiSettingsLimit)
	if os.IsNotExist(err) {
		return aiDefaultSettings(), nil
	}
	if err != nil {
		return aiSettings{}, errors.New("无法读取 AI 设置，原文件未修改")
	}
	aead, err := configCipher(a.store.key)
	if err != nil {
		return aiSettings{}, err
	}
	plain, err := aead.Open(nil, nil, data, []byte(aiSettingsAAD))
	if err != nil {
		return aiSettings{}, errors.New("AI 设置完整性校验失败，请恢复配置备份；原文件未修改")
	}
	var s aiSettings
	if err := json.Unmarshal(plain, &s); err != nil {
		return aiSettings{}, errors.New("AI 设置格式无效，原文件未修改")
	}
	if err := aiValidateSettings(&s); err != nil {
		return aiSettings{}, errors.New("AI 设置内容无效，原文件未修改")
	}
	return s, nil
}

func aiNormalizeProvider(p *aiProvider) error {
	p.ID, p.Name = strings.TrimSpace(p.ID), strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	p.Model, p.APIKey = strings.TrimSpace(p.Model), strings.TrimSpace(p.APIKey)
	p.ReasoningEffort = strings.TrimSpace(p.ReasoningEffort)
	if p.ID == "" || len(p.ID) > 128 || len(p.Name) > 256 || len(p.BaseURL) > 4096 || len(p.Model) > 1024 || len(p.APIKey) > 8192 || len(p.ReasoningEffort) > 32 {
		return errors.New("AI 服务设置不完整或超过长度限制")
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	switch p.Format {
	case "openai", "anthropic", "gemini":
	default:
		return errors.New("不支持的 AI 接口格式")
	}
	if p.BaseURL != "" {
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("AI 接口地址应为 HTTP 或 HTTPS 地址，不能包含账号、查询参数或片段")
		}
	}
	if strings.ContainsAny(p.APIKey, "\r\n") {
		return errors.New("API Key 不能包含换行")
	}
	return aiNormalizeProxy(&p.Proxy)
}

func aiValidateSettings(s *aiSettings) error {
	if len(s.Providers) > 24 {
		return errors.New("最多可保存 24 个 AI 服务")
	}
	if len(s.Providers) == 0 {
		s.Provider, s.Providers = "", []aiProvider{}
	}
	if s.Timeout == 0 {
		s.Timeout = 120
	}
	if s.Timeout < 10 || s.Timeout > 600 {
		return errors.New("AI 请求超时应为 10 至 600 秒")
	}
	seen, found := map[string]bool{}, false
	for i := range s.Providers {
		p := &s.Providers[i]
		if err := aiNormalizeProvider(p); err != nil {
			return err
		}
		if seen[p.ID] {
			return errors.New("AI 服务标识重复")
		}
		seen[p.ID] = true
		found = found || p.ID == s.Provider
	}
	if len(s.Providers) > 0 && !found {
		return errors.New("请选择有效的 AI 服务")
	}
	return nil
}

func aiMergeSecrets(p aiProvider, old aiSettings) aiProvider {
	if p.ClearKey {
		p.APIKey = ""
	} else if p.APIKey == "" {
		for _, saved := range old.Providers {
			// Editing an endpoint must never silently send the previous service's
			// credential to a different host or a different gateway path.
			if saved.ID == p.ID && saved.BaseURL == p.BaseURL && saved.Format == p.Format {
				p.APIKey = saved.APIKey
			}
		}
	}
	p.HasKey, p.ClearKey = false, false
	if p.Proxy.ClearPassword {
		p.Proxy.Password = ""
	} else if p.Proxy.Password == "" && (p.Proxy.Type == "http" || p.Proxy.Type == "socks5") {
		for _, saved := range old.Providers {
			if saved.ID == p.ID && aiSameProxy(p.Proxy, saved.Proxy) {
				p.Proxy.Password = saved.Proxy.Password
			}
		}
	}
	p.Proxy.HasPassword, p.Proxy.ClearPassword = false, false
	return p
}

func (a *App) saveAISettings(s aiSettings) (aiSettings, error) {
	if err := a.RequireUnlocked(); err != nil {
		return aiSettings{}, err
	}
	// Secret merging must not mutate the caller's redacted public settings.
	s.Providers = append([]aiProvider(nil), s.Providers...)
	if err := aiValidateSettings(&s); err != nil {
		return aiSettings{}, err
	}
	// Match security-policy writes' lock order (security state, then directory).
	// Holding the state lock through the atomic replacement also prevents a
	// concurrent lock event from allowing a settings write after locking.
	a.securityLock.mu.Lock()
	defer a.securityLock.mu.Unlock()
	if a.securityLock.status().Locked {
		a.cancelAIRequests()
		return aiSettings{}, lockedError{}
	}
	unlock, err := lockConfigDirectory(a.store.dir)
	if err != nil {
		return aiSettings{}, err
	}
	defer unlock()
	key, err := readConfigFile(filepath.Join(a.store.dir, ConfigKeyName), 32)
	if err != nil || !bytes.Equal(key, a.store.key) {
		return aiSettings{}, errors.New("配置密钥已改变或丢失，已拒绝保存 AI 设置，请重新启动")
	}
	old, err := a.loadAISettings()
	if err != nil {
		return aiSettings{}, err
	}
	for i := range s.Providers {
		s.Providers[i] = aiMergeSecrets(s.Providers[i], old)
	}
	plain, err := json.Marshal(s)
	if err != nil {
		return aiSettings{}, err
	}
	aead, err := configCipher(key)
	if err != nil {
		return aiSettings{}, err
	}
	data := aead.Seal(nil, nil, plain, []byte(aiSettingsAAD))
	if len(data) > aiSettingsLimit {
		return aiSettings{}, errors.New("AI 设置超过大小限制")
	}
	if err := atomicConfigFile(filepath.Join(a.store.dir, aiSettingsFile), data); err != nil {
		return aiSettings{}, errors.New("无法保存 AI 设置")
	}
	a.cancelAIRequests()
	return aiPublicSettings(s), nil
}

func (a *App) beginAIRequest(parent context.Context, id string, timeout int) (context.Context, func(), error) {
	if id == "" || len(id) > 128 {
		return nil, nil, errors.New("AI 请求标识无效")
	}
	if err := a.RequireUnlocked(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	stop := func() bool { return false }
	if a.ctx != nil {
		stop = context.AfterFunc(a.ctx, cancel)
	}
	op := &aiOperation{cancel: cancel}
	s := &a.ai
	s.mu.Lock()
	if s.requests == nil {
		s.requests = map[string]*aiOperation{}
	}
	if s.cancelled == nil {
		s.cancelled = map[string]time.Time{}
	}
	for k, at := range s.cancelled {
		if time.Since(at) > 10*time.Minute {
			delete(s.cancelled, k)
		}
	}
	_, wasCancelled := s.cancelled[id]
	if s.closed || s.requests[id] != nil || len(s.requests) >= 4 || wasCancelled {
		s.mu.Unlock()
		stop()
		cancel()
		if wasCancelled {
			return nil, nil, errors.New("AI 请求已取消")
		}
		return nil, nil, errors.New("AI 请求正在进行或服务已关闭，请稍后再试")
	}
	s.requests[id] = op
	s.mu.Unlock()
	finish := func() {
		stop()
		cancel()
		s.mu.Lock()
		if s.requests[id] == op {
			delete(s.requests, id)
		}
		s.mu.Unlock()
	}
	// Close the registration race with the lock callback.
	if err := a.RequireUnlocked(); err != nil {
		finish()
		return nil, nil, err
	}
	if a.ctx != nil && a.ctx.Err() != nil {
		finish()
		return nil, nil, errors.New("AI 服务正在关闭")
	}
	return ctx, finish, nil
}

func (a *App) cancelAIRequest(id string) {
	s := &a.ai
	s.mu.Lock()
	defer s.mu.Unlock()
	if op := s.requests[id]; op != nil {
		op.cancel()
	}
	if s.cancelled == nil {
		s.cancelled = map[string]time.Time{}
	}
	// A cancel can reach the backend before the long-running Wails bridge call.
	// Tombstones make that ordering deterministic without unbounded storage.
	if len(s.cancelled) >= 256 {
		var oldest string
		var at time.Time
		for k, t := range s.cancelled {
			if oldest == "" || t.Before(at) {
				oldest, at = k, t
			}
		}
		delete(s.cancelled, oldest)
	}
	s.cancelled[id] = time.Now()
}

func (a *App) cancelAIRequests() {
	s := &a.ai
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range s.requests {
		op.cancel()
	}
}

func (a *App) closeAI() {
	s := &a.ai
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, op := range s.requests {
		op.cancel()
	}
}

func (a *App) respondAI(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if locked := a.RequireUnlocked(); locked != nil {
		writeError(w, http.StatusLocked, locked)
		return
	}
	respond(w, value, err)
}

func (a *App) registerAIHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/settings", func(w http.ResponseWriter, r *http.Request) {
		s, err := a.loadAISettings()
		a.respondAI(w, aiPublicSettings(s), err)
	})
	mux.HandleFunc("POST /api/ai/settings", func(w http.ResponseWriter, r *http.Request) {
		var s aiSettings
		if !decode(w, r, &s) {
			return
		}
		next, err := a.saveAISettings(s)
		a.respondAI(w, next, err)
	})
	mux.HandleFunc("POST /api/ai/cancel", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			RequestID string `json:"requestId"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.RequestID == "" || len(input.RequestID) > 128 {
			writeError(w, 400, errors.New("AI 请求标识无效"))
			return
		}
		a.cancelAIRequest(input.RequestID)
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/ai/models", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Provider  aiProvider `json:"provider"`
			RequestID string     `json:"requestId"`
		}
		if !decode(w, r, &input) {
			return
		}
		if err := aiNormalizeProvider(&input.Provider); err != nil {
			a.respondAI(w, nil, err)
			return
		}
		s, err := a.loadAISettings()
		if err != nil {
			a.respondAI(w, nil, err)
			return
		}
		p := aiMergeSecrets(input.Provider, s)
		if input.RequestID == "" {
			input.RequestID = "models-" + randomID()
		}
		ctx, finish, err := a.beginAIRequest(r.Context(), input.RequestID, min(s.Timeout, 60))
		if err != nil {
			a.respondAI(w, nil, err)
			return
		}
		defer finish()
		client, err := aiHTTPClient(p)
		if err != nil {
			a.respondAI(w, nil, err)
			return
		}
		defer client.CloseIdleConnections()
		models, err := aiListModels(ctx, client, p)
		a.respondAI(w, map[string]any{"models": models}, err)
	})
	mux.HandleFunc("POST /api/ai/chat", func(w http.ResponseWriter, r *http.Request) {
		var input aiChatRequest
		if !decode(w, r, &input) {
			return
		}
		if err := aiValidateChat(input); err != nil {
			a.respondAI(w, nil, err)
			return
		}
		s, err := a.loadAISettings()
		if err != nil {
			a.respondAI(w, nil, err)
			return
		}
		if input.Provider == "" {
			input.Provider = s.Provider
		}
		var p aiProvider
		for _, candidate := range s.Providers {
			if candidate.ID == input.Provider {
				p = candidate
				break
			}
		}
		if p.ID == "" {
			a.respondAI(w, nil, errors.New("AI 服务不存在，请重新选择"))
			return
		}
		ctx, finish, err := a.beginAIRequest(r.Context(), input.RequestID, s.Timeout)
		if err != nil {
			a.respondAI(w, nil, err)
			return
		}
		defer finish()
		client, err := aiHTTPClient(p)
		if err != nil {
			a.respondAI(w, nil, err)
			return
		}
		defer client.CloseIdleConnections()
		result, err := aiChat(ctx, client, p, input)
		a.respondAI(w, result, err)
	})
}
