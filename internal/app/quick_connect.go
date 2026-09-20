package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var quickPingSlots = make(chan struct{}, 4)

type quickProfile struct {
	Profile
	created time.Time
}

// Parse a connection description, never an executable shell command. OpenSSH
// options with local side effects (-o, -F, -L, -R, ProxyCommand...) are rejected.
func parseQuickSSH(command string) (Profile, error) {
	p := Profile{Port: 22, User: "root", Auth: "password", Proxy: ProxyConfig{Type: "direct"}, Temporary: true}
	if len(command) > 2048 || strings.ContainsAny(command, "\r\n\x00") {
		return p, errors.New("请输入一行 SSH 地址或命令")
	}
	args := strings.Fields(command)
	if len(args) > 0 && args[0] == "ssh" {
		args = args[1:]
	}
	target := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-p" || arg == "-l" {
			i++
			if i == len(args) {
				return p, errors.New("-p 后需要端口，-l 后需要用户名")
			}
			if arg == "-p" {
				port, e := strconv.Atoi(args[i])
				if e != nil || port < 1 || port > 65535 {
					return p, errors.New("端口应为 1–65535")
				}
				p.Port = port
			} else {
				p.User = args[i]
			}
		} else if strings.HasPrefix(arg, "-") || target != "" {
			return p, errors.New("支持 ssh [-p 端口] [-l 用户名] 用户@主机；私钥请在下方选择")
		} else {
			target = arg
		}
	}
	if at := strings.LastIndex(target, "@"); at >= 0 {
		p.User = target[:at]
		target = target[at+1:]
	}
	if host, port, err := net.SplitHostPort(target); err == nil {
		p.Host = host
		p.Port, _ = strconv.Atoi(port)
	} else {
		p.Host = strings.Trim(target, "[]")
	}
	if !validQuickHost(p.Host) || p.Port < 1 || p.Port > 65535 || p.User == "" || len(p.User) > 256 || strings.ContainsAny(p.User, "@ /\\\"'`;|&$<>") || strings.ContainsFunc(p.User, unicode.IsControl) {
		return p, errors.New("请输入有效的用户名、主机名/IP 和端口")
	}
	p.Name = p.Host
	if len(p.Name) > 100 {
		p.Name = p.Name[:100]
	}
	return p, nil
}
func validQuickHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func (a *App) connectionProfile(id string) (Profile, error) {
	if p, err := a.store.Get(id); err == nil {
		return p, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.quickProfiles[id]; ok {
		return p.Profile, nil
	}
	return Profile{}, errors.New("临时连接已过期，请重新输入 SSH 地址")
}
func (a *App) configWithQuickProfiles() Config {
	config := a.store.List()
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.quickProfiles {
		config.TemporaryServers = append(config.TemporaryServers, publicProfile(p.Profile))
	}
	return config
}
func (a *App) registerQuickConnectHTTP(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/quick-connect", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Command string `json:"command"`
			Auth    string `json:"auth"`
			KeyID   string `json:"keyId"`
		}
		if !decode(w, r, &input) {
			return
		}
		p, err := parseQuickSSH(input.Command)
		if err != nil {
			respond(w, nil, err)
			return
		}
		switch input.Auth {
		case "", "password":
		case "agent":
			p.Auth = "agent"
		case "key":
			found := false
			for _, key := range a.store.List().Keys {
				if key.ID == input.KeyID {
					found = true
					break
				}
			}
			if !found {
				respond(w, nil, errors.New("请选择密钥管理器中的私钥"))
				return
			}
			p.Auth = "key"
			p.KeyID = input.KeyID
		default:
			respond(w, nil, errors.New("请选择有效的认证方式"))
			return
		}
		a.mu.Lock()
		if a.quickProfiles == nil {
			a.quickProfiles = map[string]quickProfile{}
		}
		for id, old := range a.quickProfiles {
			if time.Since(old.created) < 30*time.Minute {
				continue
			}
			used := false
			for _, session := range a.sessions {
				if session.ProfileID == id {
					used = true
					break
				}
			}
			if !used {
				delete(a.quickProfiles, id)
			}
		}
		if len(a.quickProfiles) >= 256 {
			a.mu.Unlock()
			respond(w, nil, errors.New("临时连接过多，请保存需要保留的连接后重试"))
			return
		}
		p.ID = randomID()
		a.quickProfiles[p.ID] = quickProfile{p, time.Now()}
		a.mu.Unlock()
		respond(w, publicProfile(p), nil)
	})
	mux.HandleFunc("POST /api/quick-connect/{id}/save", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name     string `json:"name"`
			Notes    string `json:"notes"`
			Group    string `json:"group"`
			Secret   string `json:"secret"`
			Remember bool   `json:"remember"`
		}
		if !decode(w, r, &input) {
			return
		}
		a.quickMu.Lock()
		defer a.quickMu.Unlock()
		id := r.PathValue("id")
		if saved, err := a.store.Get(id); err == nil {
			respond(w, publicProfile(saved), nil)
			return
		}
		a.mu.Lock()
		entry, ok := a.quickProfiles[id]
		connected := false
		for _, s := range a.sessions {
			if s.ProfileID == id && s.ctx.Err() == nil {
				connected = true
				break
			}
		}
		a.mu.Unlock()
		if !ok || !connected {
			respond(w, nil, errors.New("临时连接尚未成功或已经断开，请连接后再保存"))
			return
		}
		p := entry.Profile
		p.Temporary = false
		p.Name = input.Name
		p.Notes = input.Notes
		p.Group = input.Group
		if input.Remember {
			p.Secret = input.Secret
		}
		saved, err := a.store.saveProfile(p, false, true)
		if err == nil {
			a.mu.Lock()
			delete(a.quickProfiles, id)
			a.mu.Unlock()
		}
		respond(w, saved, err)
	})
	mux.HandleFunc("POST /api/quick-connect/ping", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Host string `json:"host"`
		}
		if !decode(w, r, &input) {
			return
		}
		host := strings.Trim(strings.TrimSpace(input.Host), "[]")
		if !validQuickHost(host) {
			respond(w, nil, errors.New("ping 后只接受主机名或 IP"))
			return
		}
		select {
		case quickPingSlots <- struct{}{}:
			defer func() { <-quickPingSlots }()
		default:
			respond(w, nil, errors.New("本机探测繁忙，请稍后重试"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		target, err := resolveDiagnosticTarget(ctx, host)
		if err != nil {
			respond(w, nil, err)
			return
		}
		var output strings.Builder
		fmt.Fprintf(&output, "本机 ICMP → %s (%s)\n", host, target)
		received := 0
		for i := 0; i < 3; i++ {
			if err := ctx.Err(); err != nil {
				break
			}
			probe := ProbeICMP(ctx, target, 1500*time.Millisecond)
			if probe.Responded && probe.Status == "reply" {
				received++
				fmt.Fprintf(&output, "%d  %s  %.2f ms\n", i+1, probe.Address, probe.Milliseconds)
			} else {
				fmt.Fprintf(&output, "%d  %s %s\n", i+1, probe.Status, probe.Error)
			}
		}
		fmt.Fprintf(&output, "收到 %d / 3 个响应", received)
		writeJSON(w, map[string]any{"output": output.String(), "ok": received > 0})
	})
}
