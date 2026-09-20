package app

import (
	"cloudshell/internal/syncserver"
	"cloudshell/internal/syncvault"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const syncConfigName = "dengshell.sync.enc"

type syncPending struct {
	Object syncvault.Object `json:"object"`
	Data   []byte           `json:"data"`
}
type syncEnrollment struct {
	Join         syncserver.Join `json:"join"`
	Bootstrap    string          `json:"bootstrap,omitempty"`
	RestoreOwner bool            `json:"restoreOwner,omitempty"`
}
type syncProfile struct {
	Version    int                         `json:"version"`
	Provider   string                      `json:"provider"`
	Name       string                      `json:"name"`
	Device     string                      `json:"device"`
	Connection syncvault.Connection        `json:"connection"`
	Token      string                      `json:"token"`
	Owner      bool                        `json:"owner"`
	Metadata   syncvault.Metadata          `json:"metadata"`
	Master     []byte                      `json:"master"`
	Snapshot   syncvault.Snapshot          `json:"snapshot"`
	Base       map[string]json.RawMessage  `json:"base"`
	Seen       map[string]syncvault.Object `json:"seen"`
	Pending    *syncPending                `json:"pending,omitempty"`
	Enrollment *syncEnrollment             `json:"enrollment,omitempty"`
}
type syncHostSettings struct {
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Enabled bool   `json:"enabled"`
}
type syncChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type syncConflict struct {
	Key     string       `json:"key"`
	Label   string       `json:"label"`
	Choices []syncChoice `json:"choices"`
}
type syncState struct {
	mu           sync.Mutex
	controlMu    sync.Mutex
	activeCancel context.CancelFunc
	closed       bool
	wg           sync.WaitGroup
	profile      *syncProfile
	key, salt    []byte
	digest       string
	needsSave    bool
	backend      syncvault.Backend
	host         *syncserver.Server
	hostSettings syncHostSettings
	hostError    string
	last         time.Time
	lastError    string
	notice       string
	conflicts    []syncConflict
	changed      uint64
	next         time.Time
	failures     int
	retryAt      time.Time
}

func (a *App) pauseSyncForLock() {
	s := &a.syncState
	s.controlMu.Lock()
	if s.activeCancel != nil {
		s.activeCancel()
	}
	s.controlMu.Unlock()
	if s.mu.TryLock() {
		a.pauseSyncLocked()
		s.mu.Unlock()
	}
}

type syncStatus struct {
	Configured     bool           `json:"configured"`
	Unlocked       bool           `json:"unlocked"`
	Busy           bool           `json:"busy"`
	Provider       string         `json:"provider"`
	Name           string         `json:"name"`
	Device         string         `json:"device"`
	Owner          bool           `json:"owner"`
	Secrets        bool           `json:"secrets"`
	URL            string         `json:"url"`
	Last           string         `json:"last"`
	Error          string         `json:"error"`
	Notice         string         `json:"notice"`
	Conflicts      []syncConflict `json:"conflicts"`
	Changed        uint64         `json:"changed"`
	HostURL        string         `json:"hostUrl"`
	HostError      string         `json:"hostError"`
	HostConfigured bool           `json:"hostConfigured"`
}

func (a *App) initializeSync() {
	s := &a.syncState
	if b, e := readConfigFile(filepath.Join(a.store.dir, "sync-service.json"), 4096); e == nil && json.Unmarshal(b, &s.hostSettings) == nil && s.hostSettings.Enabled {
		s.hostError = a.startSyncHostLocked(s.hostSettings)
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-t.C:
				locked := a.SecurityLockStatus().Locked
				if !s.mu.TryLock() {
					continue
				}
				if locked {
					a.pauseSyncLocked()
					s.mu.Unlock()
					continue
				}
				run := s.profile != nil && len(s.key) > 0 && time.Now().After(s.next) && len(s.conflicts) == 0
				s.mu.Unlock()
				if run {
					ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
					_ = a.synchronize(ctx, nil)
					cancel()
				}
			}
		}
	}()
}
func (a *App) closeSync() {
	s := &a.syncState
	s.controlMu.Lock()
	s.closed = true
	if s.activeCancel != nil {
		s.activeCancel()
	}
	s.controlMu.Unlock()
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	a.pauseSyncLocked()
	if s.host != nil {
		s.host.Close()
		s.host = nil
	}
}
func (a *App) pauseSyncLocked() {
	s := &a.syncState
	if s.backend != nil {
		s.backend.Close()
		s.backend = nil
	}
	if s.profile != nil {
		clear(s.profile.Master)
		s.profile = nil
	}
	clear(s.key)
	s.key = nil
	s.salt = nil
	s.needsSave = false
	s.conflicts = nil
}
func (a *App) syncStatus() syncStatus {
	s := &a.syncState
	_, e := os.Stat(filepath.Join(a.store.dir, syncConfigName))
	out := syncStatus{Configured: e == nil, Conflicts: []syncConflict{}}
	if !s.mu.TryLock() {
		out.Busy = true
		return out
	}
	defer s.mu.Unlock()
	out.HostError = s.hostError
	out.HostConfigured = s.hostSettings.IP != ""
	if s.host != nil {
		out.HostURL = s.host.Info().URL
	}
	out.Error = s.lastError
	out.Notice = s.notice
	out.Changed = s.changed
	out.Conflicts = s.conflicts
	if !s.last.IsZero() {
		out.Last = s.last.Format(time.RFC3339)
	}
	if p := s.profile; p != nil {
		out.Unlocked = true
		out.Provider = p.Provider
		out.Name = p.Name
		out.Device = p.Device
		out.Owner = p.Owner
		out.Secrets = p.Metadata.Secrets
		out.URL = p.Connection.URL
	}
	return out
}
func (a *App) saveSyncLocked() (err error) {
	s := &a.syncState
	defer func() { s.needsSave = err != nil }()
	p := s.profile
	if p == nil || len(s.key) != 32 {
		return errors.New("同步空间未解锁")
	}
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	defer clear(b)
	encrypted, e := syncvault.Protect(s.key, s.salt, b)
	if e != nil {
		return e
	}
	unlock, e := lockConfigDirectory(a.store.dir)
	if e != nil {
		return e
	}
	defer unlock()
	path := filepath.Join(a.store.dir, syncConfigName)
	old, e := readConfigFile(path, syncvault.MaxLocalFile)
	if s.digest != "" {
		if e != nil || syncvault.Hash(old) != s.digest {
			return errors.New("同步配置被其他实例修改，已拒绝覆盖，请重新打开同步空间")
		}
	} else if !os.IsNotExist(e) {
		return errors.New("已有同步配置，请先解锁或断开")
	}
	if len(old) > 0 {
		if e = atomicConfigFile(path+".bak", old); e != nil {
			return e
		}
	}
	if e = atomicConfigFile(path, encrypted); e != nil {
		return e
	}
	s.digest = syncvault.Hash(encrypted)
	return nil
}
func (a *App) useSyncProfileLocked(p *syncProfile, password string) error {
	s := &a.syncState
	if e := a.RequireUnlocked(); e != nil {
		return e
	}
	if p.Provider != "local" {
		return errors.New("仅支持本机 / 自建同步服务")
	}
	if _, e := os.Stat(filepath.Join(a.store.dir, syncConfigName)); !os.IsNotExist(e) {
		return errors.New("请先断开当前同步位置，再连接另一个空间")
	}
	salt := syncvault.Random()
	key, e := syncvault.PassKey(password, salt)
	if e != nil {
		return e
	}
	backend, e := syncserver.NewClient(p.Connection, p.Token)
	if e != nil {
		clear(key)
		return e
	}
	if e = a.RequireUnlocked(); e != nil {
		clear(key)
		backend.Close()
		return e
	}
	p.Version = 1
	p.Base = map[string]json.RawMessage{}
	p.Seen = map[string]syncvault.Object{}
	p.Snapshot = syncvault.Snapshot{Version: 1, Vault: p.Metadata.Vault, Device: p.Device, Clock: syncvault.Clock{}, Entries: map[string]syncvault.Entry{}}
	s.profile = p
	s.key = key
	s.salt = salt
	s.digest = ""
	s.backend = backend
	s.lastError = ""
	s.failures = 0
	s.retryAt = time.Time{}
	s.next = time.Now().Add(2 * time.Second)
	if e = a.saveSyncLocked(); e != nil {
		a.pauseSyncLocked()
		return e
	}
	return nil
}
func (a *App) unlockSync(password string) error {
	return a.unlockSyncContext(context.Background(), password)
}
func (a *App) unlockSyncContext(ctx context.Context, password string) error {
	ctx, finish, e := a.beginSyncOperation(ctx)
	if e != nil {
		return e
	}
	defer finish()
	s := &a.syncState
	if time.Now().Before(s.retryAt) {
		return errors.New("同步口令尝试过于频繁，请稍后重试")
	}
	if s.profile != nil {
		return nil
	}
	b, e := readConfigFile(filepath.Join(a.store.dir, syncConfigName), syncvault.MaxLocalFile)
	if e != nil {
		return errors.New("尚未配置同步位置")
	}
	key, salt, plain, e := syncvault.Unprotect(password, b)
	if e != nil {
		s.failures++
		if s.failures >= 5 {
			s.retryAt = time.Now().Add(30 * time.Second)
		}
		return e
	}
	defer clear(plain)
	var p syncProfile
	if json.Unmarshal(plain, &p) != nil || p.Version != 1 || len(p.Master) != 32 || !syncvault.ValidID(p.Device) || p.Metadata.Version != 1 || p.Metadata.Vault != p.Snapshot.Vault {
		clear(key)
		return errors.New("同步配置损坏，原文件保持不变")
	}
	verified, e := p.Metadata.Unlock("", syncvault.Encode(p.Master))
	clear(verified)
	if e != nil {
		clear(key)
		clear(p.Master)
		return e
	}
	if p.Provider != "local" {
		clear(key)
		clear(p.Master)
		return errors.New("旧同步方式已停用，请断开旧连接后配置本机 / 自建服务；原配置与本机数据会保留")
	}
	backend, e := syncserver.NewClient(p.Connection, p.Token)
	if e != nil {
		clear(key)
		return e
	}
	if p.Seen == nil {
		p.Seen = map[string]syncvault.Object{}
	}
	if p.Base == nil {
		p.Base = map[string]json.RawMessage{}
	}
	if e = a.RequireUnlocked(); e == nil {
		e = ctx.Err()
	}
	if e != nil {
		clear(key)
		clear(p.Master)
		backend.Close()
		return e
	}
	s.profile = &p
	s.backend = backend
	s.key = key
	s.salt = salt
	s.digest = syncvault.Hash(b)
	s.needsSave = false
	s.failures = 0
	s.lastError = ""
	s.next = time.Now()
	return nil
}
func choiceID(e syncvault.Entry) string { return syncvault.Hash(syncRaw(e)) }
func conflictLabel(k string, e syncvault.Entry) string {
	var v struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(e.Value, &v)
	kind := strings.Split(k, "/")[0]
	label := map[string]string{"server": "服务器", "group": "分组", "command": "快捷命令", "key": "托管私钥", "command-group": "命令分组"}[kind]
	if v.Name != "" {
		return label + " · " + v.Name
	}
	_, id, _ := strings.Cut(k, "/")
	return label + " · " + id[:min(8, len(id))]
}
func (a *App) enrollSyncLocked(ctx context.Context) error {
	s := &a.syncState
	p := s.profile
	if p.Enrollment == nil {
		return nil
	}
	c, ok := s.backend.(*syncserver.Client)
	if !ok {
		return errors.New("同步初始化方式无效")
	}
	in := p.Enrollment
	var e error
	if in.RestoreOwner {
		if s.host == nil {
			return errors.New("请先重新启动本机同步服务以恢复管理设备")
		}
		e = s.host.RestoreOwner(in.Join, p.Master)
	} else if in.Bootstrap != "" {
		request := *c
		request.Token = in.Bootstrap
		e = request.Request(ctx, "POST", "/v1/setup", syncserver.Setup{Metadata: p.Metadata, Device: in.Join}, nil)
	} else {
		e = c.Request(ctx, "POST", "/v1/join", in.Join, nil)
	}
	if e != nil {
		return e
	}
	p.Enrollment = nil
	if e = a.saveSyncLocked(); e != nil {
		p.Enrollment = in
	}
	return e
}
func (a *App) synchronize(ctx context.Context, resolutions map[string]string) (err error) {
	ctx, finish, e := a.beginSyncOperation(ctx)
	if e != nil {
		return e
	}
	defer finish()
	s := &a.syncState
	defer func() {
		s.next = time.Now().Add(30 * time.Second)
		if err != nil {
			s.lastError = err.Error()
		} else {
			s.lastError = ""
			s.last = time.Now()
		}
	}()
	p := s.profile
	if p == nil || s.backend == nil {
		return errors.New("请先解锁同步空间")
	}
	// Reject a second local instance before any remote mutation, and avoid
	// rewriting an unchanged encrypted configuration every thirty seconds.
	onDisk, readErr := readConfigFile(filepath.Join(a.store.dir, syncConfigName), syncvault.MaxLocalFile)
	if readErr != nil || syncvault.Hash(onDisk) != s.digest {
		return errors.New("同步配置被其他实例修改，请重新打开同步空间")
	}
	// A previous disk error may leave only an in-memory pending upload or merge
	// baseline. Persist it before performing any further remote operations.
	if s.needsSave {
		if e := a.saveSyncLocked(); e != nil {
			return e
		}
	}
	beforeState := syncvault.Hash(syncRaw(p))
	if e := a.enrollSyncLocked(ctx); e != nil {
		return e
	}
	b := s.backend
	meta, e := b.Metadata(ctx)
	if e != nil {
		return e
	}
	if !syncvault.EqualJSON(syncRaw(meta), syncRaw(p.Metadata)) {
		return errors.New("云端空间身份或加密设置发生变化，已停止同步")
	}
	if p.Pending != nil {
		if e = b.Write(ctx, p.Pending.Object, p.Pending.Data); e != nil {
			return e
		}
		p.Pending = nil
		if e = a.saveSyncLocked(); e != nil {
			return e
		}
	}
	local, e := a.store.syncProjection(p.Metadata.Secrets)
	if e != nil {
		return e
	}
	candidate := syncvault.Clone(p.Snapshot)
	seq := candidate.Clock[p.Device] + 1
	if seq > 1<<53 {
		return errors.New("设备版本达到限制，请重新配对")
	}
	localClock := syncvault.Union(candidate.Clock, syncvault.Clock{p.Device: seq})
	dirty := false
	for k, v := range local {
		if old, ok := p.Base[k]; !ok || !syncvault.EqualJSON(v, old) {
			if saved, ok := candidate.Entries[k]; ok && syncvault.EqualJSON(saved.Value, v) {
				continue // Already published by a resumed pending upload.
			}
			candidate.Entries[k] = syncvault.Entry{Value: v, Clock: localClock}
			dirty = true
		}
	}
	for k := range p.Base {
		if _, ok := local[k]; !ok {
			if saved, ok := candidate.Entries[k]; ok && syncvault.EqualJSON(saved.Value, []byte("null")) {
				continue
			}
			candidate.Entries[k] = syncvault.Entry{Value: json.RawMessage("null"), Clock: localClock}
			dirty = true
		}
	}
	if dirty {
		candidate.Clock = localClock
	}
	objects, e := b.List(ctx)
	if e != nil {
		return e
	}
	heads, e := syncvault.Heads(objects)
	if e != nil {
		return e
	}
	by := map[string]syncvault.Object{}
	for _, o := range heads {
		by[o.Device] = o
	}
	for id, old := range p.Seen {
		now, ok := by[id]
		if !ok || now.Sequence < old.Sequence || (now.Sequence == old.Sequence && now.Hash != old.Hash) {
			return errors.New("云端数据缺失或版本回退，已暂停同步；本机数据保持不变")
		}
	}
	variants := map[string][]syncVariant{}
	for k, entry := range candidate.Entries {
		variants[k] = []syncVariant{{entry, "本机保留的版本"}}
	}
	for _, o := range heads {
		if old, ok := p.Seen[o.Device]; ok && old.Hash == o.Hash {
			continue
		}
		data, e := b.Read(ctx, o)
		if e != nil {
			return e
		}
		remote, e := syncvault.DecodeSnapshot(o, data, p.Master, p.Metadata.Vault)
		if e != nil {
			return e
		}
		// A fresh installation's unused default group is a placeholder, not a
		// user-created second copy of the existing workspace's default group.
		if len(p.Base) == 0 && p.Snapshot.Sequence == 0 && len(local) == 1 {
			for k, v := range local {
				var g ServerGroup
				if strings.HasPrefix(k, "group/") && json.Unmarshal(v, &g) == nil && g.Name == "我的服务器" && g.ParentID == "" && g.Emoji == "" && g.BackgroundColor == "" {
					for rk, rv := range remote.Entries {
						var other ServerGroup
						if rk != k && strings.HasPrefix(rk, "group/") && json.Unmarshal(rv.Value, &other) == nil && other.Name == g.Name && other.ParentID == "" {
							delete(candidate.Entries, k)
							delete(variants, k)
							break
						}
					}
				}
			}
		}
		if o.Device == p.Device && o.Sequence > p.Snapshot.Clock[p.Device] {
			return errors.New("检测到同一设备配置在其他位置写入，请重新配对此设备")
		}
		candidate.Clock = syncvault.Union(candidate.Clock, remote.Clock)
		for k, entry := range remote.Entries {
			variants[k] = addSyncVariant(variants[k], syncVariant{entry, "设备 " + o.Device[:8] + " 的版本"})
		}
	}
	s.conflicts = nil
	for k, frontier := range variants {
		entries := coalesceSyncVariants(frontier)
		if len(entries) == 1 {
			candidate.Entries[k] = entries[0].entry
			continue
		}
		selected := -1
		choices := []syncChoice{}
		seen := map[string]bool{}
		for i, variant := range entries {
			entry := variant.entry
			id := choiceID(entry)
			if id == resolutions[k] {
				selected = i
			}
			if !seen[id] {
				seen[id] = true
				label := variant.label
				if syncvault.EqualJSON(entry.Value, []byte("null")) {
					label += "（删除）"
				}
				choices = append(choices, syncChoice{id, label})
			}
		}
		if selected < 0 {
			s.conflicts = append(s.conflicts, syncConflict{k, conflictLabel(k, entries[0].entry), choices})
			continue
		}
		entry := entries[selected].entry
		entry.Clock = syncvault.Union(candidate.Clock, localClock)
		candidate.Entries[k] = entry
		candidate.Clock = syncvault.Union(candidate.Clock, entry.Clock)
		dirty = true
	}
	if len(s.conflicts) > 0 {
		sort.Slice(s.conflicts, func(i, j int) bool { return s.conflicts[i].Key < s.conflicts[j].Key })
		return errors.New("检测到并发修改，请选择要保留的版本；尚未覆盖本机数据")
	}
	if disambiguateSyncGroups(&candidate, localClock) {
		dirty = true
	}
	// Pull-only merges must obey the same bounds as uploaded snapshots. Two
	// individually valid heads can exceed the protocol/local-journal limits.
	check := candidate
	check.Device = p.Device
	check.Sequence = max(candidate.Clock[p.Device], 1)
	check.Clock = syncvault.Union(candidate.Clock, syncvault.Clock{p.Device: check.Sequence})
	if e = syncvault.Validate(check); e != nil {
		return e
	}
	if len(syncRaw(check)) > syncvault.MaxBlob-64 {
		return errors.New("合并后的同步数据超过快照容量限制，已保留本机数据")
	}
	if e = a.RequireUnlocked(); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	next := projectedEntries(candidate.Entries)
	// Validate the full graph and private keys before publishing any candidate.
	if e = a.store.prepareSync(local, next, p.Metadata.Secrets, false); e != nil {
		return e
	}
	if dirty {
		candidate.Device = p.Device
		candidate.Vault = p.Metadata.Vault
		candidate.Version = 1
		candidate.Sequence = seq
		candidate.Clock = syncvault.Union(candidate.Clock, syncvault.Clock{p.Device: seq})
		o, data, e := syncvault.EncodeSnapshot(candidate, p.Master)
		if e != nil {
			return e
		}
		p.Snapshot = candidate
		p.Pending = &syncPending{o, data}
		if e = a.saveSyncLocked(); e != nil {
			return e
		}
		if e = b.Write(ctx, o, data); e != nil {
			return e
		}
		p.Pending = nil
		p.Seen[p.Device] = o
	}
	if e = a.RequireUnlocked(); e != nil {
		return e
	}
	var applied map[string]json.RawMessage
	if e = a.store.prepareSyncWithBaseline(local, next, p.Metadata.Secrets, true, &applied); e != nil {
		return e
	}
	p.Snapshot = candidate
	for id, o := range by {
		if old := p.Seen[id]; o.Sequence >= old.Sequence {
			p.Seen[id] = o
		}
	}
	p.Base = applied
	if syncvault.Hash(syncRaw(p)) != beforeState {
		if e = a.saveSyncLocked(); e != nil {
			return e
		}
	}
	if !sameProjection(local, applied) {
		s.changed++
	}
	if dirty {
		if history, ok := b.(interface {
			Retain(context.Context, string, int) error
		}); ok {
			if e := history.Retain(ctx, p.Device, 20); e != nil {
				s.notice = "数据已同步，历史清理暂未完成：" + e.Error()
			} else {
				s.notice = ""
			}
		}
	}
	return nil
}

func (a *App) disconnectSync() error {
	a.cancelActiveSync()
	_, finish, e := a.beginSyncOperation(context.Background())
	if e != nil {
		return e
	}
	defer finish()
	s := &a.syncState
	unlock, e := lockConfigDirectory(a.store.dir)
	if e != nil {
		return e
	}
	defer unlock()
	path := filepath.Join(a.store.dir, syncConfigName)
	if _, e := os.Stat(path); e == nil {
		target := filepath.Join(a.store.dir, fmt.Sprintf("dengshell.sync.disconnected-%s.enc", time.Now().UTC().Format("20060102T150405.000000000")))
		if e = os.Rename(path, target); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	a.pauseSyncLocked()
	s.digest = ""
	s.lastError = ""
	return nil
}
