// Package syncserver is an isolated ciphertext-only service. It exposes no
// terminal, file-browser, command execution or local DengShell API routes.
package syncserver

import (
	"bytes"
	"cloudshell/internal/syncvault"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Server struct {
	mu              sync.Mutex
	db              *sql.DB
	cert            *certificates
	http            *http.Server
	listener        net.Listener
	Connection      syncvault.Connection
	Bootstrap       string
	limit           chan struct{}
	failures        map[string]attempt
	release         func()
	maxStorageBytes int64
}
type attempt struct {
	at    time.Time
	count int
}
type Join struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Token  string `json:"token"`
	Invite string `json:"invite"`
}
type Setup struct {
	Metadata syncvault.Metadata `json:"metadata"`
	Device   Join               `json:"device"`
}
type Invited struct {
	syncvault.Connection
	Invite   string             `json:"invite"`
	Expires  int64              `json:"expires"`
	Metadata syncvault.Metadata `json:"metadata"`
}

func Open(dir, ip string, port int) (*Server, error) {
	if net.ParseIP(ip) == nil || port < 0 || port > 65535 {
		return nil, errors.New("IP 或端口无效")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	release, e := lockServiceDirectory(dir)
	if e != nil {
		return nil, e
	}
	owned := false
	defer func() {
		if !owned {
			release()
		}
	}()
	for _, name := range []string{"identity.pem", "sync.db"} {
		if st, e := os.Lstat(filepath.Join(dir, name)); e == nil && !st.Mode().IsRegular() {
			return nil, errors.New("同步服务数据路径不是普通文件")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sync.db")); err == nil {
		if _, err = os.Stat(filepath.Join(dir, "identity.pem")); os.IsNotExist(err) {
			return nil, errors.New("同步身份文件缺失，请恢复包含 identity.pem 的完整备份；未重新生成身份")
		}
	}
	c, e := openCertificates(dir, ip)
	if e != nil {
		return nil, e
	}
	absolute, e := filepath.Abs(filepath.Join(dir, "sync.db"))
	if e != nil {
		return nil, e
	}
	uriPath := filepath.ToSlash(absolute)
	if strings.HasPrefix(uriPath, "//") {
		return nil, errors.New("同步数据库需要本地磁盘目录，不能使用 UNC 共享")
	}
	u := sqliteFileURI(uriPath)
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Server{db: db, cert: c, limit: make(chan struct{}, 8), failures: map[string]attempt{}, release: release, maxStorageBytes: 256 << 20}
	ok := false
	defer func() {
		if !ok {
			db.Close()
		}
	}()
	_, e = db.Exec(`CREATE TABLE IF NOT EXISTS settings(k TEXT PRIMARY KEY,v BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS devices(id TEXT PRIMARY KEY,name TEXT NOT NULL,hash TEXT UNIQUE NOT NULL,owner INTEGER NOT NULL,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS invites(hash TEXT PRIMARY KEY,expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS snapshots(hash TEXT PRIMARY KEY,device TEXT NOT NULL,seq INTEGER NOT NULL,created INTEGER NOT NULL,data BLOB NOT NULL,UNIQUE(device,seq));`)
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(filepath.Join(dir, "sync.db"), 0600); e != nil {
		return nil, e
	}
	var vault string
	e = db.QueryRow("SELECT v FROM settings WHERE k='vault'").Scan(&vault)
	if errors.Is(e, sql.ErrNoRows) {
		vault = syncvault.ID()
		s.Bootstrap = syncvault.Encode(syncvault.Random())
		tx, e := db.Begin()
		if e != nil {
			return nil, e
		}
		defer tx.Rollback()
		for k, v := range map[string]string{"vault": vault, "bootstrap": syncvault.Hash([]byte(s.Bootstrap))} {
			if _, e = tx.Exec("INSERT INTO settings(k,v) VALUES(?,?)", k, v); e != nil {
				return nil, e
			}
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
	} else if e != nil {
		return nil, e
	}
	// A stopped, never-initialized service must not lose its only bootstrap.
	var bootstrapHash string
	if s.Bootstrap == "" && db.QueryRow("SELECT v FROM settings WHERE k='bootstrap'").Scan(&bootstrapHash) == nil {
		s.Bootstrap = syncvault.Encode(syncvault.Random())
		if _, e = db.Exec("UPDATE settings SET v=? WHERE k='bootstrap'", syncvault.Hash([]byte(s.Bootstrap))); e != nil {
			return nil, e
		}
	}
	s.Connection = syncvault.Connection{Version: 1, URL: "https://" + net.JoinHostPort(ip, strconv.Itoa(port)), CA: c.PEM, Vault: vault, Bootstrap: s.Bootstrap}
	ok = true
	owned = true
	return s, nil
}
func sqliteFileURI(absoluteSlashPath string) url.URL {
	// SQLite requires /C:/ for Windows drive letters, never a URI authority
	// named "C:". url.URL also escapes literal # and ? in directory names.
	if len(absoluteSlashPath) > 2 && absoluteSlashPath[1] == ':' {
		absoluteSlashPath = "/" + absoluteSlashPath
	}
	return url.URL{Scheme: "file", Path: absoluteSlashPath}
}
func (s *Server) Start(ip string, port int) error {
	l, e := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if e != nil {
		return e
	}
	s.listener = l
	_, p, _ := net.SplitHostPort(l.Addr().String())
	s.Connection.URL = "https://" + net.JoinHostPort(s.cert.ip, p)
	s.http = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 45 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 45 * time.Second, MaxHeaderBytes: 8192, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: s.cert.get}}
	go func() { _ = s.http.Serve(tls.NewListener(l, s.http.TLSConfig)) }()
	return nil
}
func (s *Server) Close() {
	if s.http != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.http.Shutdown(ctx)
		_ = s.http.Close()
	}
	_ = s.db.Close()
	if s.release != nil {
		s.release()
	}
}
func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, msg string) {
	send(w, status, map[string]string{"error": msg})
}
func read(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, (syncvault.MaxBlob*4/3)+65536)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		failure(w, 400, "请求格式无效")
		return false
	}
	return true
}
func (s *Server) authenticate(token string) (syncvault.Device, error) {
	var d syncvault.Device
	if len(token) > 128 {
		return d, errors.New("invalid token")
	}
	e := s.db.QueryRow("SELECT id,name,owner,created FROM devices WHERE hash=?", syncvault.Hash([]byte(token))).Scan(&d.ID, &d.Name, &d.Owner, &d.Created)
	return d, e
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Header.Get("Origin") != "" {
		failure(w, 403, "仅支持 DengShell 同步客户端")
		return
	}
	select {
	case s.limit <- struct{}{}:
		defer func() { <-s.limit }()
	default:
		failure(w, 429, "同步服务繁忙，请稍后重试")
		return
	}
	// Receive bounded request bodies before taking the shared state lock.
	// An incomplete join/upload must not stall every other authorized device.
	var bodyLimit int64
	if r.Method == "POST" {
		switch r.URL.Path {
		case "/v1/setup", "/v1/join", "/v1/revoke":
			bodyLimit = 32 << 10
			_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
		case "/v1/snapshots":
			// Large unauthenticated uploads are rejected by the normal auth path
			// without buffering their body. Authentication is checked again below.
			if _, err := s.authenticate(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); err == nil {
				bodyLimit = (syncvault.MaxBlob * 4 / 3) + 65536
			}
		}
	}
	if bodyLimit > 0 {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, bodyLimit))
		_ = r.Body.Close()
		if err != nil {
			failure(w, 400, "请求格式或大小无效")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	now := time.Now()
	f := s.failures[ip]
	if now.Sub(f.at) > time.Minute {
		f = attempt{at: now}
	}
	if f.count >= 30 {
		failure(w, 429, "请求过于频繁，请稍后重试")
		return
	}
	f.count++
	if len(s.failures) > 1024 {
		s.failures = map[string]attempt{}
	}
	s.failures[ip] = f
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Method == "POST" && r.URL.Path == "/v1/setup" {
		s.setup(w, r, token)
		return
	}
	if r.Method == "POST" && r.URL.Path == "/v1/join" {
		s.join(w, r)
		return
	}
	d, e := s.authenticate(token)
	if e != nil {
		failure(w, 401, "设备未授权或已被撤销")
		return
	}
	delete(s.failures, ip)
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/meta":
		var b []byte
		if s.db.QueryRow("SELECT v FROM settings WHERE k='meta'").Scan(&b) != nil {
			failure(w, 409, "同步空间尚未初始化")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	case r.Method == "GET" && r.URL.Path == "/v1/snapshots":
		s.list(w)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/snapshots/"):
		s.get(w, strings.TrimPrefix(r.URL.Path, "/v1/snapshots/"))
	case r.Method == "POST" && r.URL.Path == "/v1/snapshots":
		s.put(w, r, d)
	case r.Method == "GET" && r.URL.Path == "/v1/devices":
		s.devices(w)
	case r.Method == "POST" && r.URL.Path == "/v1/invite" && d.Owner:
		var count int
		_, _ = s.db.Exec("DELETE FROM invites WHERE expires<?", now.Unix())
		_ = s.db.QueryRow("SELECT count(*) FROM invites").Scan(&count)
		if count >= 8 {
			failure(w, 409, "待使用邀请过多，请等待过期")
			return
		}
		v := syncvault.Encode(syncvault.Random())
		exp := now.Add(10 * time.Minute).Unix()
		if _, e = s.db.Exec("INSERT INTO invites VALUES(?,?)", syncvault.Hash([]byte(v)), exp); e != nil {
			failure(w, 500, "邀请保存失败")
			return
		}
		c := s.Connection
		c.Bootstrap = ""
		var raw []byte
		var meta syncvault.Metadata
		if s.db.QueryRow("SELECT v FROM settings WHERE k='meta'").Scan(&raw) != nil || json.Unmarshal(raw, &meta) != nil {
			failure(w, 500, "空间数据无效")
			return
		}
		send(w, 200, Invited{c, v, exp, meta})
	case r.Method == "POST" && r.URL.Path == "/v1/revoke" && d.Owner:
		var in struct {
			ID string `json:"id"`
		}
		if !read(w, r, &in) {
			return
		}
		if in.ID == d.ID {
			failure(w, 400, "不能移除当前管理设备")
			return
		}
		_, e = s.db.Exec("DELETE FROM devices WHERE id=? AND owner=0", in.ID)
		if e != nil {
			failure(w, 500, "撤销失败")
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	default:
		failure(w, 404, "接口不存在或没有权限")
	}
}
func validJoin(d Join) bool {
	_, e := syncvault.Decode(d.Token)
	return e == nil && syncvault.ValidID(d.ID) && len([]rune(d.Name)) > 0 && len([]rune(d.Name)) <= 80
}
func (s *Server) setup(w http.ResponseWriter, r *http.Request, token string) {
	var in Setup
	if !read(w, r, &in) {
		return
	}
	// A lost response is retried with the same journaled device credential.
	if d, e := s.authenticate(in.Device.Token); e == nil && d.ID == in.Device.ID && d.Owner {
		var b []byte
		if s.db.QueryRow("SELECT v FROM settings WHERE k='meta'").Scan(&b) == nil && syncvault.EqualJSON(b, mustJSON(in.Metadata)) {
			send(w, 200, map[string]bool{"ok": true})
			return
		}
	}
	var expected string
	if s.db.QueryRow("SELECT v FROM settings WHERE k='bootstrap'").Scan(&expected) != nil || subtle.ConstantTimeCompare([]byte(expected), []byte(syncvault.Hash([]byte(token)))) != 1 {
		failure(w, 403, "初始化凭据无效或已使用")
		return
	}
	m := in.Metadata
	_, proofErr := syncvault.Decode(m.Proof)
	if !validJoin(in.Device) || m.Version != 1 || m.Vault != s.Connection.Vault || len(m.Salt) != 32 || len(m.WrappedKey) != 72 || proofErr != nil {
		failure(w, 400, "初始化数据无效")
		return
	}
	b, _ := json.Marshal(m)
	tx, e := s.db.Begin()
	if e != nil {
		failure(w, 500, "初始化失败")
		return
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO settings VALUES('meta',?)", b); e == nil {
		_, e = tx.Exec("INSERT INTO devices VALUES(?,?,?,?,?)", in.Device.ID, in.Device.Name, syncvault.Hash([]byte(in.Device.Token)), 1, time.Now().Unix())
	}
	if e == nil {
		_, e = tx.Exec("DELETE FROM settings WHERE k='bootstrap'")
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		failure(w, 500, "初始化失败")
		return
	}
	s.Bootstrap = ""
	s.Connection.Bootstrap = ""
	send(w, 200, map[string]bool{"ok": true})
}
func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	var in Join
	if !read(w, r, &in) {
		return
	}
	if !validJoin(in) || len(in.Invite) > 128 {
		failure(w, 400, "设备信息无效")
		return
	}
	if d, e := s.authenticate(in.Token); e == nil && d.ID == in.ID {
		send(w, 200, map[string]bool{"ok": true})
		return
	}
	var exp int64
	var n int
	_ = s.db.QueryRow("SELECT count(*) FROM devices").Scan(&n)
	if n >= 32 {
		failure(w, 409, "最多允许 32 台设备")
		return
	}
	h := syncvault.Hash([]byte(in.Invite))
	if s.db.QueryRow("SELECT expires FROM invites WHERE hash=?", h).Scan(&exp) != nil || exp < time.Now().Unix() {
		failure(w, 403, "邀请已过期或已使用")
		return
	}
	tx, e := s.db.Begin()
	if e != nil {
		failure(w, 500, "设备加入失败")
		return
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO devices VALUES(?,?,?,?,?)", in.ID, in.Name, syncvault.Hash([]byte(in.Token)), 0, time.Now().Unix()); e == nil {
		_, e = tx.Exec("DELETE FROM invites WHERE hash=?", h)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		failure(w, 409, "设备加入失败，请生成新邀请")
		return
	}
	send(w, 200, map[string]bool{"ok": true})
}
func (s *Server) list(w http.ResponseWriter) {
	rows, e := s.db.Query("SELECT hash,device,seq,length(data),created FROM snapshots ORDER BY created DESC")
	if e != nil {
		failure(w, 500, "读取快照失败")
		return
	}
	defer rows.Close()
	out := []syncvault.Object{}
	for rows.Next() {
		var o syncvault.Object
		if rows.Scan(&o.Hash, &o.Device, &o.Sequence, &o.Size, &o.Created) != nil {
			failure(w, 500, "读取快照失败")
			return
		}
		o.ID = o.Hash
		out = append(out, o)
	}
	if rows.Err() != nil {
		failure(w, 500, "读取快照失败")
		return
	}
	send(w, 200, out)
}
func (s *Server) get(w http.ResponseWriter, id string) {
	if !syncvault.ValidID(id) {
		failure(w, 400, "快照编号无效")
		return
	}
	var b []byte
	if s.db.QueryRow("SELECT data FROM snapshots WHERE hash=?", id).Scan(&b) != nil {
		failure(w, 404, "快照不存在")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(b)
}
func (s *Server) put(w http.ResponseWriter, r *http.Request, d syncvault.Device) {
	var in struct {
		Object syncvault.Object `json:"object"`
		Data   []byte           `json:"data"`
	}
	if !read(w, r, &in) {
		return
	}
	o := in.Object
	if o.Device != d.ID || o.Sequence == 0 || o.Sequence > 1<<53 || len(in.Data) > syncvault.MaxBlob || len(in.Data) < 40 || syncvault.Hash(in.Data) != o.Hash {
		failure(w, 400, "快照校验失败")
		return
	}
	var previous []byte
	var previousDevice string
	var previousSequence uint64
	if e := s.db.QueryRow("SELECT data,device,seq FROM snapshots WHERE hash=?", o.Hash).Scan(&previous, &previousDevice, &previousSequence); e == nil {
		if previousDevice == o.Device && previousSequence == o.Sequence && bytes.Equal(previous, in.Data) {
			send(w, 200, map[string]bool{"ok": true})
			return
		}
		failure(w, 409, "快照冲突")
		return
	} else if !errors.Is(e, sql.ErrNoRows) {
		failure(w, 500, "读取快照失败")
		return
	}
	var latest uint64
	if e := s.db.QueryRow("SELECT COALESCE(MAX(seq),0) FROM snapshots WHERE device=?", d.ID).Scan(&latest); e != nil {
		failure(w, 500, "读取快照失败")
		return
	}
	if o.Sequence <= latest {
		failure(w, 409, "设备版本冲突，请重新配对此设备")
		return
	}
	e := s.storeSnapshot(o, in.Data)
	if errors.Is(e, errStorageCapacity) {
		failure(w, 507, errStorageCapacity.Error())
		return
	}
	if e != nil {
		failure(w, 500, "保存快照失败")
		return
	}
	send(w, 200, map[string]bool{"ok": true})
}
func (s *Server) devices(w http.ResponseWriter) {
	rows, e := s.db.Query("SELECT id,name,owner,created FROM devices ORDER BY created")
	if e != nil {
		failure(w, 500, "设备列表读取失败")
		return
	}
	defer rows.Close()
	out := []syncvault.Device{}
	for rows.Next() {
		var d syncvault.Device
		if rows.Scan(&d.ID, &d.Name, &d.Owner, &d.Created) != nil {
			failure(w, 500, "设备列表读取失败")
			return
		}
		out = append(out, d)
	}
	if rows.Err() != nil {
		failure(w, 500, "设备列表读取失败")
		return
	}
	send(w, 200, out)
}

func (s *Server) String() string { return fmt.Sprintf("DengShell Sync %s", s.Connection.URL) }
