package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
)

const securityLockFile = "dengshell.security-lock.enc"
const securityLockAAD = "DengShell/security-lock/v1"

type lockedError struct{}

func (lockedError) Error() string { return "软件已被锁定，请先解锁" }
func (lockedError) Code() string  { return "DENGSHELL_LOCKED" }

// Separate from exported/imported connection documents. Only a password hash
// is persisted; TOTP seeds and retry state use an authenticated local envelope.
type securityLockPolicy struct {
	Version      int    `json:"version"`
	Enabled      bool   `json:"enabled"`
	IdleSeconds  int    `json:"idleSeconds"`
	PasswordHash []byte `json:"passwordHash,omitempty"`
	PasswordHint string `json:"passwordHint,omitempty"`
	TOTPSecret   []byte `json:"totpSecret,omitempty"`
	LastTOTPStep int64  `json:"lastTOTPStep"`
	Failures     int    `json:"failures"`
	RetryAt      int64  `json:"retryAt"`
}
type lockGrant struct {
	expires  time.Time
	revision uint64
	secret   []byte
}
type securityLockState struct {
	mu           sync.Mutex
	policy       securityLockPolicy
	locked       bool
	lastActivity time.Time
	revision     uint64
	grants       map[string]lockGrant
	now          func() time.Time // deterministic clock only in isolated tests
}
type SecurityLockStatus struct {
	Enabled                   bool   `json:"enabled"`
	Locked                    bool   `json:"locked"`
	PasswordConfigured        bool   `json:"passwordConfigured"`
	TwoFactorConfigured       bool   `json:"twoFactorConfigured"`
	PasswordHint              string `json:"passwordHint"`
	IdleSeconds               int    `json:"idleSeconds"`
	IdleRemainingMilliseconds int64  `json:"idleRemainingMilliseconds"`
	RetryAfterSeconds         int64  `json:"retryAfterSeconds"`
	Revision                  uint64 `json:"revision"`
}
type LockProof struct {
	Method string `json:"method"`
	Value  string `json:"value"`
}
type LockSettingsInput struct {
	Grant            string `json:"grant"`
	Enabled          bool   `json:"enabled"`
	IdleSeconds      int    `json:"idleSeconds"`
	PasswordEnabled  bool   `json:"passwordEnabled"`
	Password         string `json:"password"`
	PasswordHint     string `json:"passwordHint"`
	TwoFactorEnabled bool   `json:"twoFactorEnabled"`
	Code             string `json:"code"`
}

func (s *securityLockState) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
func (s *securityLockState) lockDue(now time.Time) {
	if s.policy.Enabled && !s.locked && s.policy.IdleSeconds > 0 && (now.Sub(s.lastActivity) >= time.Duration(s.policy.IdleSeconds)*time.Second || now.UnixMilli()-s.lastActivity.UnixMilli() >= int64(s.policy.IdleSeconds)*1000) {
		s.lock()
	}
}
func (s *securityLockState) lock() {
	if s.policy.Enabled && !s.locked {
		s.locked = true
		s.revision++
		s.grants = nil
	}
}
func (s *securityLockState) status() SecurityLockStatus {
	now := s.clock()
	s.lockDue(now)
	remaining := int64(0)
	if s.policy.Enabled && !s.locked && s.policy.IdleSeconds > 0 {
		remaining = max(0, int64(s.policy.IdleSeconds)*1000-(now.UnixMilli()-s.lastActivity.UnixMilli()))
	}
	return SecurityLockStatus{Enabled: s.policy.Enabled, Locked: s.locked, PasswordConfigured: len(s.policy.PasswordHash) > 0, TwoFactorConfigured: len(s.policy.TOTPSecret) > 0, PasswordHint: s.policy.PasswordHint, IdleSeconds: s.policy.IdleSeconds, IdleRemainingMilliseconds: remaining, RetryAfterSeconds: max(0, s.policy.RetryAt-now.Unix()), Revision: s.revision}
}
func (a *App) initializeSecurityLock() error {
	s := &a.securityLock
	s.policy = securityLockPolicy{Version: 1, LastTOTPStep: -1}
	s.lastActivity = time.Now()
	s.revision = 1
	data, err := readConfigFile(filepath.Join(a.store.dir, securityLockFile), 16384)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		aead, e := configCipher(a.store.key)
		if e != nil {
			return e
		}
		plain, e := aead.Open(nil, nil, data, []byte(securityLockAAD))
		if e != nil {
			return errors.New("安全锁定配置完整性校验失败，请恢复备份；原文件未修改")
		}
		if e = json.Unmarshal(plain, &s.policy); e != nil {
			return errors.New("安全锁定配置无法解析，原文件未修改")
		}
		p := s.policy
		if p.Version != 1 || p.IdleSeconds < 0 || p.IdleSeconds > 604800 || p.Enabled && len(p.PasswordHash) == 0 && len(p.TOTPSecret) == 0 || len(p.TOTPSecret) != 0 && len(p.TOTPSecret) != 20 {
			return errors.New("安全锁定配置无效，原文件未修改")
		}
		if len(p.PasswordHash) > 0 {
			if _, e = bcrypt.Cost(p.PasswordHash); e != nil {
				return errors.New("安全锁定密码记录无效，原文件未修改")
			}
		}
	}
	s.locked = s.policy.Enabled
	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-tick.C:
				s.mu.Lock()
				s.lockDue(s.clock())
				s.mu.Unlock()
			}
		}
	}()
	return nil
}
func (a *App) saveLockPolicy(p securityLockPolicy) error {
	plain, err := json.Marshal(p)
	if err != nil {
		return err
	}
	aead, err := configCipher(a.store.key)
	if err != nil {
		return err
	}
	data := aead.Seal(nil, nil, plain, []byte(securityLockAAD))
	return atomicConfigFile(filepath.Join(a.store.dir, securityLockFile), data)
}
func (a *App) SecurityLockStatus() SecurityLockStatus {
	s := &a.securityLock
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status()
}
func (a *App) RequireUnlocked() error {
	if a.SecurityLockStatus().Locked {
		return lockedError{}
	}
	return nil
}
func (a *App) LockNow() (SecurityLockStatus, error) {
	s := &a.securityLock
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.policy.Enabled {
		return s.status(), errors.New("请先在设置中启用安全锁定")
	}
	s.lock()
	return s.status(), nil
}
func (a *App) LockActivity() SecurityLockStatus {
	s := &a.securityLock
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lockDue(s.clock())
	if !s.locked {
		s.lastActivity = s.clock()
	}
	return s.status()
}

// RFC 6238 / RFC 4226: SHA-1, six digits, a 30-second moving factor. The
// accepted step is persisted before unlocking, preventing replay after restart.
func lockTOTP(secret []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}
func matchLockTOTP(secret []byte, code string, now time.Time, last int64) (int64, bool) {
	if len(code) != 6 || len(secret) != 20 {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	step := now.Unix() / 30
	found := int64(-1)
	for _, delta := range []int64{0, -1, 1} {
		candidate := step + delta
		if candidate >= 0 && candidate > last && subtle.ConstantTimeCompare([]byte(lockTOTP(secret, candidate)), []byte(code)) == 1 {
			found = max(found, candidate)
		}
	}
	return found, found >= 0
}
func (a *App) lockFailure(message string) error {
	s := &a.securityLock
	p := s.policy
	p.Failures = min(20, p.Failures+1)
	if p.Failures >= 5 {
		p.RetryAt = s.clock().Unix() + int64(min(300, 30*(1<<min(4, p.Failures-5))))
	}
	if err := a.saveLockPolicy(p); err != nil {
		return fmt.Errorf("无法保存验证状态，已拒绝操作：%w", err)
	}
	s.policy = p
	return errors.New(message)
}
func (s *securityLockState) retryAllowed() error {
	if wait := s.policy.RetryAt - s.clock().Unix(); wait > 0 {
		return fmt.Errorf("尝试次数过多，请在 %d 秒后重试", wait)
	}
	return nil
}
func (a *App) verifyLockProof(proof LockProof) error {
	s := &a.securityLock
	if err := s.retryAllowed(); err != nil {
		return err
	}
	p := s.policy
	ok := false
	switch proof.Method {
	case "password":
		if len(p.PasswordHash) > 0 && len(proof.Value) <= 72 {
			ok = bcrypt.CompareHashAndPassword(p.PasswordHash, []byte(proof.Value)) == nil
		}
	case "totp":
		var step int64
		step, ok = matchLockTOTP(p.TOTPSecret, proof.Value, s.clock(), p.LastTOTPStep)
		if ok {
			p.LastTOTPStep = step
		}
	}
	if !ok {
		return a.lockFailure("密码或验证码不正确；已使用的验证码需等待下一组")
	}
	p.Failures, p.RetryAt = 0, 0
	if err := a.saveLockPolicy(p); err != nil {
		return fmt.Errorf("无法保存验证状态，已拒绝操作：%w", err)
	}
	s.policy = p
	return nil
}
func (a *App) Unlock(proof LockProof) (SecurityLockStatus, error) {
	s := &a.securityLock
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lockDue(s.clock())
	if !s.locked {
		return s.status(), nil
	}
	if err := a.verifyLockProof(proof); err != nil {
		return s.status(), err
	}
	s.locked = false
	s.lastActivity = s.clock()
	s.revision++
	return s.status(), nil
}
func (a *App) AuthorizeLockSettings(proof LockProof) (string, error) {
	s := &a.securityLock
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lockDue(s.clock())
	if s.locked {
		return "", lockedError{}
	}
	if s.policy.Enabled {
		if err := a.verifyLockProof(proof); err != nil {
			return "", err
		}
	}
	if s.grants == nil {
		s.grants = map[string]lockGrant{}
	}
	for token, g := range s.grants {
		if !s.clock().Before(g.expires) {
			delete(s.grants, token)
		}
	}
	if len(s.grants) >= 8 {
		return "", errors.New("正在编辑安全设置的窗口过多，请稍后重试")
	}
	token := randomID()
	s.grants[token] = lockGrant{expires: s.clock().Add(5 * time.Minute), revision: s.revision}
	s.lastActivity = s.clock()
	return token, nil
}
func (s *securityLockState) grant(token string) (lockGrant, error) {
	s.lockDue(s.clock())
	if s.locked {
		return lockGrant{}, lockedError{}
	}
	g, ok := s.grants[token]
	if !ok || g.revision != s.revision || !s.clock().Before(g.expires) {
		return lockGrant{}, errors.New("安全设置验证已过期，请关闭后重新打开设置")
	}
	return g, nil
}

type LockEnrollment struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
	QR     string `json:"qr"`
}

func (a *App) EnrollLockTOTP(token string) (LockEnrollment, error) {
	s := &a.securityLock
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.grant(token)
	if err != nil {
		return LockEnrollment{}, err
	}
	secret := make([]byte, 20)
	if _, err = rand.Read(secret); err != nil {
		return LockEnrollment{}, err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	hostname, _ := os.Hostname()
	hostname = strings.Map(func(r rune) rune {
		if r < 32 || r == ':' {
			return '-'
		}
		return r
	}, hostname)
	if len(hostname) > 64 {
		hostname = "Desktop"
	}
	if hostname == "" {
		hostname = "Desktop"
	}
	uri := url.URL{Scheme: "otpauth", Host: "totp", Path: "/DengShell:" + hostname}
	uri.RawQuery = url.Values{"secret": {encoded}, "issuer": {"DengShell"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}.Encode()
	png, err := qrcode.Encode(uri.String(), qrcode.Medium, 280)
	if err != nil {
		return LockEnrollment{}, err
	}
	g.secret = secret
	s.grants[token] = g
	return LockEnrollment{Secret: encoded, URI: uri.String(), QR: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)}, nil
}
func (a *App) SaveLockSettings(input LockSettingsInput) (SecurityLockStatus, error) {
	s := &a.securityLock
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.grant(input.Grant)
	if err != nil {
		return s.status(), err
	}
	if input.IdleSeconds < 0 || input.IdleSeconds > 604800 {
		return s.status(), errors.New("自动锁定时间需为 1 至 604800 秒，0 表示仅手动锁定")
	}
	if utf8.RuneCountInString(input.PasswordHint) > 120 {
		return s.status(), errors.New("密码提醒最多 120 个字")
	}
	p := s.policy
	p.Enabled = input.Enabled
	p.IdleSeconds = input.IdleSeconds
	if !input.Enabled {
		p = securityLockPolicy{Version: 1, LastTOTPStep: -1}
	} else {
		if !input.PasswordEnabled && !input.TwoFactorEnabled {
			return s.status(), errors.New("至少配置一种解锁方式")
		}
		if input.PasswordEnabled {
			if input.Password != "" {
				if utf8.RuneCountInString(input.Password) < 4 || len(input.Password) > 72 {
					return s.status(), errors.New("密码至少 4 位，最多 72 字节")
				}
				p.PasswordHash, err = bcrypt.GenerateFromPassword([]byte(input.Password), 12)
				if err != nil {
					return s.status(), err
				}
			}
			if len(p.PasswordHash) == 0 {
				return s.status(), errors.New("请先设置解锁密码")
			}
			p.PasswordHint = strings.TrimSpace(input.PasswordHint)
		} else {
			p.PasswordHash = nil
			p.PasswordHint = ""
		}
		if input.TwoFactorEnabled {
			if len(g.secret) > 0 {
				if err = s.retryAllowed(); err != nil {
					return s.status(), err
				}
				step, ok := matchLockTOTP(g.secret, input.Code, s.clock(), -1)
				if !ok {
					return s.status(), a.lockFailure("绑定验证码不正确，请检查验证器和系统时间")
				}
				p.TOTPSecret = append([]byte{}, g.secret...)
				p.LastTOTPStep = step
			}
			if len(p.TOTPSecret) == 0 {
				return s.status(), errors.New("请先扫码绑定，并填写当前验证码")
			}
		} else {
			p.TOTPSecret = nil
			p.LastTOTPStep = -1
		}
	}
	p.Failures, p.RetryAt = 0, 0
	if err = a.saveLockPolicy(p); err != nil {
		return s.status(), err
	}
	s.policy = p
	s.revision++
	s.grants = nil
	s.lastActivity = s.clock()
	return s.status(), nil
}
func (a *App) registerSecurityLockHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/security-lock/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.SecurityLockStatus()) })
	mux.HandleFunc("GET /api/security-lock/access", func(w http.ResponseWriter, r *http.Request) {
		if err := a.RequireUnlocked(); err != nil {
			writeError(w, 423, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/security-lock/lock", func(w http.ResponseWriter, r *http.Request) { v, e := a.LockNow(); respond(w, v, e) })
	mux.HandleFunc("POST /api/security-lock/activity", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.LockActivity()) })
	mux.HandleFunc("POST /api/security-lock/unlock", func(w http.ResponseWriter, r *http.Request) {
		var p LockProof
		if !decode(w, r, &p) {
			return
		}
		v, e := a.Unlock(p)
		respond(w, v, e)
	})
	mux.HandleFunc("POST /api/security-lock/authorize", func(w http.ResponseWriter, r *http.Request) {
		var p LockProof
		if !decode(w, r, &p) {
			return
		}
		v, e := a.AuthorizeLockSettings(p)
		respond(w, map[string]string{"grant": v}, e)
	})
	mux.HandleFunc("POST /api/security-lock/enroll", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Grant string `json:"grant"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, e := a.EnrollLockTOTP(p.Grant)
		respond(w, v, e)
	})
	mux.HandleFunc("POST /api/security-lock/settings", func(w http.ResponseWriter, r *http.Request) {
		var p LockSettingsInput
		if !decode(w, r, &p) {
			return
		}
		v, e := a.SaveLockSettings(p)
		respond(w, v, e)
	})
}

// Keep only the lock controls and a content-free parent liveness probe reachable
// while locked. Existing SSH streams remain connected; their input is gated in
// the relay as well. Polling/keepalives do not count as human activity.
func lockControlPath(path string) bool {
	return strings.HasPrefix(path, "/api/security-lock/") || path == "/api/windows/alive" || strings.HasPrefix(path, "/api/windows/handoff/") && strings.HasSuffix(path, "/cancel")
}
