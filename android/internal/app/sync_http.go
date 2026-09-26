package app

import (
	"cloudshell/internal/syncserver"
	"cloudshell/internal/syncvault"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) startSyncHostLocked(settings syncHostSettings) string {
	s := &a.syncState
	if s.host != nil {
		if settings.IP != s.hostSettings.IP || settings.Port != s.hostSettings.Port {
			return "本机同步服务已在其他地址或端口运行，请先停止服务再更改"
		}
		return ""
	}
	if net.ParseIP(settings.IP) == nil || settings.Port < 1024 || settings.Port > 65535 {
		return "本机同步 IP 或端口无效"
	}
	h, e := syncserver.Open(filepath.Join(a.store.dir, "sync-service"), settings.IP, settings.Port)
	if e != nil {
		return e.Error()
	}
	if e = h.Start(settings.IP, settings.Port); e != nil {
		h.Close()
		return "无法开启本机同步服务，请检查 IP 是否属于本机、端口是否被占用"
	}
	s.host = h
	s.hostSettings = settings
	return ""
}
func localSyncAddresses() []string {
	out := []string{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ip, _, e := net.ParseCIDR(a.String())
		if e == nil && ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
			out = append(out, ip.String())
		}
	}
	out = append(out, "127.0.0.1")
	return out
}
func (a *App) newLocalSync(ctx context.Context, in struct {
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Recovery string `json:"recovery"`
	Secrets  bool   `json:"secrets"`
}) (string, error) {
	ctx, finish, e := a.beginSyncOperation(ctx)
	if e != nil {
		return "", e
	}
	defer finish()
	s := &a.syncState
	if _, e := os.Stat(filepath.Join(a.store.dir, syncConfigName)); !os.IsNotExist(e) {
		return "", errors.New("请先断开当前同步位置")
	}
	if len([]rune(in.Name)) < 1 || len([]rune(in.Name)) > 80 {
		return "", errors.New("请填写设备名称（最多 80 字）")
	}
	allowed := false
	for _, ip := range localSyncAddresses() {
		if ip == in.IP {
			allowed = true
		}
	}
	if !allowed {
		return "", errors.New("请选择本机局域网地址")
	}
	key, e := syncvault.PassKey(in.Password, syncvault.Random())
	clear(key)
	if e != nil {
		return "", e
	}
	s.hostError = a.startSyncHostLocked(syncHostSettings{in.IP, in.Port, true})
	if s.hostError != "" {
		return "", errors.New(s.hostError)
	}
	if e = atomicConfigFile(filepath.Join(a.store.dir, "sync-service.json"), syncRaw(s.hostSettings)); e != nil {
		return "", e
	}
	c := s.host.Info()
	var m syncvault.Metadata
	var master []byte
	restore := c.Bootstrap == ""
	if restore {
		m, e = s.host.LocalMetadata()
		if e == nil {
			master, e = m.Unlock(in.Password, in.Recovery)
		}
	} else {
		m, master, e = syncvault.NewMetadataFor(c.Vault, in.Password, in.Secrets)
	}
	if e != nil {
		return "", e
	}
	device, token := syncvault.ID(), syncvault.Encode(syncvault.Random())
	enrollment := &syncEnrollment{Join: syncserver.Join{ID: device, Name: in.Name, Token: token}, Bootstrap: c.Bootstrap, RestoreOwner: restore}
	c.Bootstrap = ""
	p := &syncProfile{Provider: "local", Name: in.Name, Device: device, Connection: c, Token: token, Owner: true, Metadata: m, Master: master, Enrollment: enrollment}
	if e = a.useSyncProfileLocked(p, in.Password); e != nil {
		clear(master)
		return "", e
	}
	if e = a.enrollSyncLocked(ctx); e != nil {
		s.lastError = e.Error()
		return "", e
	}
	return syncvault.Encode(master), nil
}
func (a *App) joinLocalSync(ctx context.Context, code, name, password, recovery string) (string, error) {
	ctx, finish, e := a.beginSyncOperation(ctx)
	if e != nil {
		return "", e
	}
	defer finish()
	s := &a.syncState
	if _, e := os.Stat(filepath.Join(a.store.dir, syncConfigName)); !os.IsNotExist(e) {
		return "", errors.New("请先断开当前同步位置")
	}
	var invite syncserver.Invited
	if e := syncvault.Unpack(strings.TrimSpace(code), &invite); e != nil {
		return "", e
	}
	if len([]rune(name)) < 1 || len([]rune(name)) > 80 {
		return "", errors.New("请填写设备名称")
	}
	client, e := syncserver.NewClient(invite.Connection, invite.Bootstrap)
	if e != nil {
		return "", e
	}
	defer client.Close()
	id, token := syncvault.ID(), syncvault.Encode(syncvault.Random())
	// Validate the local protection password before consuming a one-use invite.
	k, e := syncvault.PassKey(password, syncvault.Random())
	clear(k)
	if e != nil {
		return "", e
	}
	var m syncvault.Metadata
	var master []byte
	owner := invite.Bootstrap != ""
	if owner {
		m, master, e = syncvault.NewMetadataFor(invite.Vault, password, false)

	} else {
		if invite.Expires < time.Now().Unix() {
			return "", errors.New("邀请已过期，请在原设备生成新邀请")
		}
		// Decrypt the metadata carried by the invitation before consuming it.
		m = invite.Metadata
		if m.Vault != invite.Vault {
			return "", errors.New("邀请空间身份不匹配")
		}
		master, e = m.Unlock(password, recovery)
	}
	if e != nil {
		clear(master)
		return "", e
	}
	c := invite.Connection
	c.Bootstrap = ""
	p := &syncProfile{Provider: "local", Name: name, Device: id, Connection: c, Token: token, Owner: owner, Metadata: m, Master: master, Enrollment: &syncEnrollment{Join: syncserver.Join{ID: id, Name: name, Token: token, Invite: invite.Invite}, Bootstrap: invite.Bootstrap}}
	if e = a.useSyncProfileLocked(p, password); e != nil {
		clear(master)
		return "", e
	}
	if e = a.enrollSyncLocked(ctx); e != nil {
		s.lastError = e.Error()
		return "", e
	}
	return syncvault.Encode(master), nil
}
func (a *App) registerSyncHTTP(mux *http.ServeMux) {
	a.registerSyncHistoryHTTP(mux)
	mux.HandleFunc("GET /api/sync/status", func(w http.ResponseWriter, r *http.Request) { a.respondSync(w, a.syncStatus(), nil) })
	mux.HandleFunc("GET /api/sync/addresses", func(w http.ResponseWriter, r *http.Request) {
		name, _ := os.Hostname()
		writeJSON(w, map[string]any{"addresses": localSyncAddresses(), "name": name})
	})
	mux.HandleFunc("POST /api/sync/local/create", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IP       string `json:"ip"`
			Port     int    `json:"port"`
			Name     string `json:"name"`
			Password string `json:"password"`
			Recovery string `json:"recovery"`
			Secrets  bool   `json:"secrets"`
		}
		if !decode(w, r, &in) {
			return
		}
		code, e := a.newLocalSync(r.Context(), in)
		a.respondSync(w, map[string]string{"recovery": code}, e)
	})
	mux.HandleFunc("POST /api/sync/local/join", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Code, Name, Password, Recovery string }
		if !decode(w, r, &in) {
			return
		}
		code, e := a.joinLocalSync(r.Context(), in.Code, in.Name, in.Password, in.Recovery)
		a.respondSync(w, map[string]string{"recovery": code}, e)
	})
	mux.HandleFunc("POST /api/sync/unlock", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Password string }
		if !decode(w, r, &in) {
			return
		}
		a.respondSync(w, map[string]bool{"ok": true}, a.unlockSyncContext(r.Context(), in.Password))
	})
	mux.HandleFunc("POST /api/sync/pause", func(w http.ResponseWriter, r *http.Request) {
		a.cancelActiveSync()
		_, finish, ok := a.lockSyncHTTP(w, r)
		if !ok {
			return
		}
		defer finish()
		a.pauseSyncLocked()
		a.respondSync(w, map[string]bool{"ok": true}, nil)
	})
	mux.HandleFunc("POST /api/sync/disconnect", func(w http.ResponseWriter, r *http.Request) {
		a.respondSync(w, map[string]bool{"ok": true}, a.disconnectSync())
	})
	mux.HandleFunc("POST /api/sync/run", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Resolutions map[string]string `json:"resolutions"`
		}
		if !decode(w, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		a.respondSync(w, map[string]bool{"ok": true}, a.synchronize(ctx, in.Resolutions))
	})
	mux.HandleFunc("POST /api/sync/invite", func(w http.ResponseWriter, r *http.Request) {
		r, finish, ok := a.lockSyncHTTP(w, r)
		if !ok {
			return
		}
		defer finish()
		s := &a.syncState
		c, ok := s.backend.(*syncserver.Client)
		if !ok || s.profile == nil || !s.profile.Owner {
			writeError(w, 403, errors.New("仅自建同步的管理设备可以生成邀请"))
			return
		}
		var invite syncserver.Invited
		e := c.Request(r.Context(), "POST", "/v1/invite", struct{}{}, &invite)
		code := ""
		if e == nil {
			code, e = syncvault.Pack(invite)
		}
		a.respondSync(w, map[string]any{"code": code, "expires": invite.Expires}, e)
	})
	mux.HandleFunc("GET /api/sync/devices", func(w http.ResponseWriter, r *http.Request) {
		r, finish, ok := a.lockSyncHTTP(w, r)
		if !ok {
			return
		}
		defer finish()
		s := &a.syncState
		c, ok := s.backend.(*syncserver.Client)
		if !ok {
			writeError(w, 400, errors.New("请先解锁本机 / 自建同步空间"))
			return
		}
		var devices []syncvault.Device
		e := c.Request(r.Context(), "GET", "/v1/devices", nil, &devices)
		a.respondSync(w, devices, e)
	})
	mux.HandleFunc("POST /api/sync/revoke", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		if !decode(w, r, &in) {
			return
		}
		r, finish, ok := a.lockSyncHTTP(w, r)
		if !ok {
			return
		}
		defer finish()
		s := &a.syncState
		c, ok := s.backend.(*syncserver.Client)
		if !ok || s.profile == nil || !s.profile.Owner {
			writeError(w, 403, errors.New("当前设备不能管理自建同步授权"))
			return
		}
		e := c.Request(r.Context(), "POST", "/v1/revoke", map[string]string{"id": in.ID}, nil)
		a.respondSync(w, map[string]bool{"ok": true}, e)
	})
	mux.HandleFunc("POST /api/sync/recovery", func(w http.ResponseWriter, r *http.Request) {
		r, finish, ok := a.lockSyncHTTP(w, r)
		if !ok {
			return
		}
		defer finish()
		s := &a.syncState
		if s.profile == nil {
			writeError(w, 400, errors.New("请先解锁同步空间"))
			return
		}
		a.respondSync(w, map[string]string{"recovery": syncvault.Encode(s.profile.Master)}, nil)
	})
	mux.HandleFunc("POST /api/sync/host/stop", func(w http.ResponseWriter, r *http.Request) {
		a.cancelActiveSync()
		r, finish, ok := a.lockSyncHTTP(w, r)
		if !ok {
			return
		}
		defer finish()
		s := &a.syncState
		if s.host != nil {
			s.host.Close()
			s.host = nil
		}
		s.hostSettings.Enabled = false
		e := atomicConfigFile(filepath.Join(a.store.dir, "sync-service.json"), syncRaw(s.hostSettings))
		a.respondSync(w, map[string]bool{"ok": true}, e)
	})
	mux.HandleFunc("POST /api/sync/host/start", func(w http.ResponseWriter, r *http.Request) {
		r, finish, ok := a.lockSyncHTTP(w, r)
		if !ok {
			return
		}
		defer finish()
		s := &a.syncState
		s.hostSettings.Enabled = true
		s.hostError = a.startSyncHostLocked(s.hostSettings)
		var e error
		if s.hostError != "" {
			e = errors.New(s.hostError)
		} else {
			e = atomicConfigFile(filepath.Join(a.store.dir, "sync-service.json"), syncRaw(s.hostSettings))
		}
		a.respondSync(w, map[string]bool{"ok": true}, e)
	})
}
