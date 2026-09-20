package app

import (
	"cloudshell/internal/syncvault"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

type syncKey struct {
	ManagedKey
	Private []byte `json:"private"`
}

func syncRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func (s *Store) syncProjection(secrets bool) (map[string]json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncProjectionLocked(secrets)
}
func (s *Store) syncProjectionLocked(secrets bool) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for _, p := range s.config.Servers {
		if p.Temporary {
			continue
		}
		p.FinalShellID = ""
		p.Group = "" // Derived display path; group IDs are the synchronized identity.
		p.HasSecret = false
		p.KeyPath = ""
		p.NeedsProxy = p.NeedsProxy || p.ProxyID != "" || (p.Proxy.Type != "" && p.Proxy.Type != "direct")
		p.Proxy = ProxyConfig{}
		p.ProxyID = ""
		if !secrets {
			p.Secret = ""
			p.KeyID = ""
		}
		out["server/"+p.ID] = syncRaw(p)
	}
	for _, g := range s.config.GroupNodes {
		out["group/"+g.ID] = syncRaw(g)
	}
	for _, c := range s.config.Commands {
		out["command/"+c.ID] = syncRaw(c)
	}
	for _, name := range s.config.CommandGroups {
		out["command-group/"+syncvault.Hash([]byte(name))] = syncRaw(name)
	}
	if secrets {
		for _, k := range s.config.Keys {
			b, e := readConfigFile(filepath.Join(s.dir, "keys", k.ID), 2<<20)
			if e != nil {
				return nil, errors.New("无法读取托管私钥，已停止同步")
			}
			k.HasPassphrase = false
			out["key/"+k.ID] = syncRaw(syncKey{k, b})
		}
	}
	return out, nil
}
func projectedEntries(entries map[string]syncvault.Entry) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	for k, e := range entries {
		if !syncvault.EqualJSON(e.Value, []byte("null")) {
			m[k] = e.Value
		}
	}
	return m
}
func sameProjection(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !syncvault.EqualJSON(v, b[k]) {
			return false
		}
	}
	return true
}
func safeSyncID(id string) bool {
	if len(id) < 8 || len(id) > 96 {
		return false
	}
	for _, c := range []byte(id) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// applySync validates the complete prospective configuration before writing.
// Network calls never run under the Store mutex; edits during a sync cause a
// retry, rather than overwriting what the user has just changed.
func (s *Store) applySync(expected, next map[string]json.RawMessage, secrets bool) error {
	return s.prepareSync(expected, next, secrets, true)
}
func (s *Store) prepareSync(expected, next map[string]json.RawMessage, secrets, commit bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, e := s.syncProjectionLocked(secrets)
	if e != nil {
		return e
	}
	if !sameProjection(current, expected) {
		return errors.New("同步期间本机配置已变化，将在下次同步时合并")
	}
	if sameProjection(current, next) {
		return nil
	}
	b, e := json.Marshal(s.config)
	if e != nil {
		return e
	}
	var candidate Config
	if e = json.Unmarshal(b, &candidate); e != nil {
		return e
	}
	oldProfiles := map[string]Profile{}
	for _, p := range candidate.Servers {
		oldProfiles[p.ID] = p
	}
	candidate.Servers = []Profile{}
	candidate.GroupNodes = []ServerGroup{}
	candidate.Commands = []QuickCommand{}
	candidate.CommandGroups = []string{}
	candidate.Trash = nil
	if secrets {
		candidate.Keys = []ManagedKey{}
	}
	keyData := map[string][]byte{}
	keys := make([]string, 0, len(next))
	for k := range next {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		raw := next[k]
		parts := strings.SplitN(k, "/", 2)
		if len(parts) != 2 || !safeSyncID(parts[1]) {
			return errors.New("同步记录编号无效")
		}
		id := parts[1]
		switch parts[0] {
		case "server":
			var p Profile
			if json.Unmarshal(raw, &p) != nil || p.ID != id || p.Temporary || p.Name == "" || len(p.Name) > 100 || p.Host == "" || len(p.Host) > 253 || strings.ContainsAny(p.Host, " /\r\n\t") || p.User == "" || len(p.User) > 256 || p.Port < 1 || p.Port > 65535 || (p.Auth != "password" && p.Auth != "key" && p.Auth != "agent") {
				return fmt.Errorf("同步服务器记录无效：%s", id)
			}
			p.KeyPath = ""
			p.FinalShellID = ""
			p.HasSecret = false
			p.Proxy = ProxyConfig{Type: "direct"}
			p.ProxyID = ""
			if old, ok := oldProfiles[id]; ok {
				p.Proxy = old.Proxy
				p.ProxyID = old.ProxyID
				p.NeedsProxy = old.NeedsProxy
				p.FinalShellID = old.FinalShellID
				if !secrets {
					p.Secret = old.Secret
					p.KeyID = old.KeyID
					p.KeyPath = old.KeyPath
				} else if p.KeyID == "" {
					p.KeyPath = old.KeyPath
				}
			} else if !secrets {
				p.Secret = ""
				p.KeyID = ""
			}
			candidate.Servers = append(candidate.Servers, p)
		case "group":
			var g ServerGroup
			if json.Unmarshal(raw, &g) != nil || g.ID != id {
				return errors.New("同步分组无效")
			}
			candidate.GroupNodes = append(candidate.GroupNodes, g)
		case "command":
			var c QuickCommand
			if json.Unmarshal(raw, &c) != nil || c.ID != id || c.Name == "" || len(c.Name) > 100 || strings.TrimSpace(c.Body) == "" || len(c.Body) > 65536 || validateCommandGroup(c.Group) != nil || strings.ContainsFunc(c.Body, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) || !strings.Contains("|blue|green|amber|purple|rose|teal|", "|"+c.Color+"|") || c.Color == "" {
				return errors.New("同步快捷命令无效")
			}
			candidate.Commands = append(candidate.Commands, c)
		case "command-group":
			var name string
			if json.Unmarshal(raw, &name) != nil || validateCommandGroup(name) != nil || syncvault.Hash([]byte(name)) != id {
				return errors.New("同步命令分组无效")
			}
			candidate.CommandGroups = append(candidate.CommandGroups, name)
		case "key":
			if !secrets {
				return errors.New("当前空间没有启用私钥同步")
			}
			var k syncKey
			if json.Unmarshal(raw, &k) != nil || k.ID != id || len(k.Private) > 2<<20 || len(k.Private) == 0 {
				return errors.New("同步私钥记录无效")
			}
			var signer ssh.Signer
			if k.Encrypted {
				signer, e = ssh.ParsePrivateKeyWithPassphrase(k.Private, []byte(k.Passphrase))
			} else {
				signer, e = ssh.ParsePrivateKey(k.Private)
			}
			if e != nil || k.Name == "" || len(k.Name) > 100 || ssh.FingerprintSHA256(signer.PublicKey()) != k.Fingerprint || strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) != strings.TrimSpace(k.PublicKey) {
				return errors.New("同步私钥校验失败")
			}
			keyData[id] = k.Private
			candidate.Keys = append(candidate.Keys, k.ManagedKey)
		default:
			return errors.New("同步数据包含未知记录类型，请更新客户端")
		}
	}
	if e = validateGroupGraph(candidate.GroupNodes); e != nil {
		return fmt.Errorf("分组合并需要处理：%w", e)
	}
	groups := groupIndex(candidate.GroupNodes)
	keyIDs := map[string]bool{}
	for _, k := range candidate.Keys {
		keyIDs[k.ID] = true
	}
	for _, p := range candidate.Servers {
		if p.GroupID != "" {
			if _, ok := groups[p.GroupID]; !ok {
				return errors.New("连接引用的分组已被另一台设备删除，请先解决分组冲突")
			}
		}
		if p.KeyID != "" && !keyIDs[p.KeyID] {
			return errors.New("连接引用的托管私钥缺失，未覆盖本地配置")
		}
	}
	normalizeCommandGroups(&candidate)
	if len(candidate.Commands) > 1000 || len(candidate.CommandGroups) > 1000 {
		return errors.New("合并后的快捷命令或分组超过 1000 条上限")
	}
	// Ordering is device-local; receiving one record must not reshuffle every
	// existing server, group or quick-command button.
	preserveSyncOrder(s.config.Servers, candidate.Servers, func(p Profile) string { return p.ID })
	preserveSyncOrder(s.config.GroupNodes, candidate.GroupNodes, func(g ServerGroup) string { return g.ID })
	preserveSyncOrder(s.config.Commands, candidate.Commands, func(c QuickCommand) string { return c.ID })
	preserveSyncOrder(s.config.Keys, candidate.Keys, func(k ManagedKey) string { return k.ID })
	preserveSyncOrder(s.config.CommandGroups, candidate.CommandGroups, func(s string) string { return s })
	// Existing private key material is immutable under an ID. Never replace an
	// existing user's key file as a side effect of receiving a remote snapshot.
	created := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, p := range created {
				_ = os.Remove(p)
			}
		}
	}()
	if len(keyData) > 0 && commit {
		if e = os.MkdirAll(filepath.Join(s.dir, "keys"), 0700); e != nil {
			return e
		}
	}
	for id, data := range keyData {
		path := filepath.Join(s.dir, "keys", id)
		old, err := readConfigFile(path, 2<<20)
		if err == nil {
			if !reflect.DeepEqual(old, data) {
				return errors.New("同名托管私钥内容冲突，已拒绝替换")
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		if !commit {
			continue
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		created = append(created, path)
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		ce := f.Close()
		if err == nil {
			err = ce
		}
		if err != nil {
			return err
		}
	}
	if !commit {
		return nil
	}
	// Keep ten encrypted pre-sync configuration backups; existing key files are
	// retained, so a configuration restore never loses its private key material.
	backupDir := filepath.Join(s.dir, "sync-backups")
	if e = os.MkdirAll(backupDir, 0700); e != nil {
		return e
	}
	encrypted, e := encryptConfig(s.key, b)
	if e != nil {
		return e
	}
	if e = atomicConfigFile(filepath.Join(backupDir, time.Now().UTC().Format("20060102T150405.000000000")+".enc"), encrypted); e != nil {
		return e
	}
	old := s.config
	s.config = candidate
	s.refreshLegacyGroupsLocked()
	if e = s.writeLocked(); e != nil {
		s.config = old
		return e
	}
	committed = true
	if files, err := os.ReadDir(backupDir); err == nil && len(files) > 10 {
		for _, f := range files[:len(files)-10] {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".enc") {
				_ = os.Remove(filepath.Join(backupDir, f.Name()))
			}
		}
	}
	return nil
}
func preserveSyncOrder[T any](old, next []T, id func(T) string) {
	index := map[string]int{}
	for i, v := range old {
		index[id(v)] = i + 1
	}
	sort.SliceStable(next, func(i, j int) bool {
		a, b := index[id(next[i])], index[id(next[j])]
		if a == 0 {
			return false
		}
		if b == 0 {
			return true
		}
		return a < b
	})
}
