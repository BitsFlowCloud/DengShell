package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type Profile struct {
	FinalShellID string      `json:"finalShellId,omitempty"`
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Host         string      `json:"host"`
	Port         int         `json:"port"`
	User         string      `json:"user"`
	GroupID      string      `json:"groupId"`
	DeletedAt    *time.Time  `json:"deletedAt,omitempty"`
	Group        string      `json:"group"`
	Auth         string      `json:"auth"`
	KeyPath      string      `json:"keyPath,omitempty"`
	KeyID        string      `json:"keyId,omitempty"`
	Secret       string      `json:"secret,omitempty"`
	HasSecret    bool        `json:"hasSecret,omitempty"`
	Proxy        ProxyConfig `json:"proxy"`
	ProxyID      string      `json:"proxyId,omitempty"`
}

type Config struct {
	SchemaVersion     int                        `json:"schemaVersion"`
	GroupNodes        []ServerGroup              `json:"groupNodes"`
	Trash             []Profile                  `json:"trash,omitempty"`
	ConnectionHistory []ConnectionHistory        `json:"connectionHistory"`
	CommandHistory    *GlobalCommandHistory      `json:"commandHistory,omitempty"`
	Extra             map[string]json.RawMessage `json:"-"`
	Servers           []Profile                  `json:"servers"`
	Groups            []string                   `json:"groups"`
	HostKeys          map[string]string          `json:"hostKeys"`
	Commands          []QuickCommand             `json:"commands"`
	CommandGroups     []string                   `json:"commandGroups"`
	Keys              []ManagedKey               `json:"keys"`
	Proxies           []ManagedProxy             `json:"proxies"`
	Assets            []ManagedAsset             `json:"assets"`
	Appearance        Appearance                 `json:"appearance"`
}

type Store struct {
	mu                      sync.Mutex
	dir                     string
	config                  Config
	key                     []byte
	diskDigest              [32]byte
	hasDisk                 bool
	historyState            map[string]*commandHistoryState
	globalHistoryOperations []string
}

func randomID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (s *Store) List() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Config{SchemaVersion: s.config.SchemaVersion, Servers: []Profile{}, Trash: []Profile{}, Groups: append([]string{}, s.config.Groups...), GroupNodes: append([]ServerGroup{}, s.config.GroupNodes...), ConnectionHistory: append([]ConnectionHistory{}, s.config.ConnectionHistory...), Commands: append([]QuickCommand{}, s.config.Commands...), CommandGroups: append([]string{}, s.config.CommandGroups...), Keys: append([]ManagedKey{}, s.config.Keys...), Assets: append([]ManagedAsset{}, s.config.Assets...), Proxies: make([]ManagedProxy, len(s.config.Proxies)), Appearance: cloneAppearance(s.config.Appearance)}
	result.CommandHistory = cloneGlobalCommandHistory(s.config.CommandHistory)
	for i, key := range result.Keys {
		result.Keys[i] = key.public()
	}
	for i, p := range s.config.Proxies {
		p.Proxy = p.Proxy.public()
		result.Proxies[i] = p
	}
	for _, p := range s.config.Servers {
		p = publicProfile(p)
		if p.DeletedAt != nil {
			result.Trash = append(result.Trash, p)
		} else {
			result.Servers = append(result.Servers, p)
		}
	}
	return result
}
func publicProfile(p Profile) Profile {
	p.FinalShellID = ""
	p.HasSecret = p.Secret != ""
	p.Secret = ""
	p.Proxy = p.Proxy.public()
	if p.DeletedAt != nil {
		at := *p.DeletedAt
		p.DeletedAt = &at
	}
	return p
}
func (s *Store) Get(id string) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.config.Servers {
		if p.ID == id && p.DeletedAt == nil {
			return p, nil
		}
	}
	return Profile{}, errors.New("服务器配置不存在")
}
func (s *Store) Save(p Profile, clearSecret bool) (Profile, error) {
	p.FinalShellID = ""
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Name = strings.TrimSpace(p.Name)
	p.Host = strings.Trim(strings.TrimSpace(p.Host), "[]")
	p.User = strings.TrimSpace(p.User)
	p.Group = strings.TrimSpace(p.Group)
	if err := p.Proxy.validate(); err != nil {
		return Profile{}, err
	}
	if p.ProxyID != "" {
		found := false
		for _, proxy := range s.config.Proxies {
			if proxy.ID == p.ProxyID {
				found = true
				break
			}
		}
		if !found {
			return Profile{}, errors.New("所选代理不存在，请重新选择")
		}
		p.Proxy = ProxyConfig{Type: "direct"}
	}
	if p.Name == "" || p.Host == "" || p.User == "" {
		return Profile{}, errors.New("请填写名称、服务器地址和用户名")
	}
	if strings.ContainsAny(p.Host, " /\r\n\t") || len(p.Host) > 253 {
		return Profile{}, errors.New("服务器地址应为主机名或 IP，不包含端口")
	}
	if p.Port == 0 {
		p.Port = 22
	}
	if p.Port < 1 || p.Port > 65535 {
		return Profile{}, errors.New("端口应为 1–65535")
	}
	if p.Auth == "" {
		p.Auth = "password"
	}
	if p.Auth != "password" && p.Auth != "key" && p.Auth != "agent" {
		return Profile{}, errors.New("不支持的认证方式")
	}
	if p.Auth == "key" && p.KeyPath == "" && p.KeyID == "" {
		return Profile{}, errors.New("请选择私钥文件")
	}
	if p.Auth != "key" {
		p.KeyID = ""
		p.KeyPath = ""
	}
	if p.KeyID != "" {
		found := false
		for _, key := range s.config.Keys {
			if key.ID == p.KeyID {
				found = true
				break
			}
		}
		if !found {
			return Profile{}, errors.New("所选密钥不存在")
		}
		p.KeyPath = ""
	}
	if len(p.Name) > 100 {
		return Profile{}, errors.New("名称或分组过长")
	}
	old := cloneConnectionConfig(s.config)
	committed := false
	defer func() {
		if !committed {
			s.config = old
		}
	}()
	index := -1
	for i, entry := range s.config.Servers {
		if entry.ID == p.ID {
			p.FinalShellID = entry.FinalShellID
			if entry.DeletedAt != nil {
				return Profile{}, errors.New("服务器已在回收站，请先恢复")
			}
			if p.GroupID == "" && (p.Group == "" || p.Group == entry.Group) {
				p.GroupID = entry.GroupID
			}
			index = i
			if p.Secret == "" && !clearSecret && p.Auth == entry.Auth && p.KeyID == entry.KeyID && p.KeyPath == entry.KeyPath {
				p.Secret = entry.Secret
			}
			if p.Proxy.Password == "" && !p.Proxy.ClearPassword && p.Proxy.Type == entry.Proxy.Type && p.Proxy.Host == entry.Proxy.Host && p.Proxy.Port == entry.Proxy.Port && p.Proxy.User == entry.Proxy.User {
				p.Proxy.Password = entry.Proxy.Password
			}
			break
		}
	}
	if p.ID == "" {
		p.ID = randomID()
	} else if index < 0 {
		return Profile{}, errors.New("服务器配置不存在，请重新新建")
	}
	if err := s.assignProfileGroupLocked(&p); err != nil {
		return Profile{}, err
	}
	p.DeletedAt = nil
	if clearSecret {
		p.Secret = ""
	}
	p.HasSecret = false
	if p.Proxy.ClearPassword {
		p.Proxy.Password = ""
	}
	p.Proxy.HasPassword = false
	p.Proxy.ClearPassword = false
	if index < 0 {
		s.config.Servers = append(s.config.Servers, p)
	} else {
		s.config.Servers[index] = p
	}
	s.refreshLegacyGroupsLocked()
	if err := s.writeLocked(); err != nil {
		return Profile{}, err
	}
	committed = true
	return publicProfile(p), nil
}

func (s *Store) hostKey(hostname string, _ net.Addr, key ssh.PublicKey) error {
	return s.hostKeyWithApproval(hostname, key, nil)
}

// The approval is scoped to this exact host, presented key and prior trust
// record. A changed key is rejected before SSH authentication sends a secret.
func (s *Store) hostKeyWithApproval(hostname string, key ssh.PublicKey, approval *HostKeyApproval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fingerprint := ssh.FingerprintSHA256(key)
	known, existed := s.config.HostKeys[hostname]
	if existed && known == fingerprint {
		return nil
	}
	if approval == nil || approval.Host != hostname || approval.Fingerprint != fingerprint || approval.PreviousFingerprint != known {
		return &HostKeyError{Host: hostname, Fingerprint: fingerprint, PreviousFingerprint: known, Algorithm: key.Type()}
	}
	s.config.HostKeys[hostname] = fingerprint
	if err := s.writeLocked(); err != nil {
		if existed {
			s.config.HostKeys[hostname] = known
		} else {
			delete(s.config.HostKeys, hostname)
		}
		return err
	}
	return nil
}
func (s *Store) ResetHostKey(p Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := net.JoinHostPort(p.Host, fmt.Sprint(p.Port))
	old, ok := s.config.HostKeys[key]
	delete(s.config.HostKeys, key)
	if err := s.writeLocked(); err != nil {
		if ok {
			s.config.HostKeys[key] = old
		}
		return err
	}
	return nil
}
