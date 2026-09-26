package app

import (
	"bytes"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func lockTestApp(t *testing.T) *App {
	t.Helper()
	a, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(a.Close)
	return a
}
func enableTestPassword(t *testing.T, a *App, idle int) {
	t.Helper()
	grant, e := a.AuthorizeLockSettings(LockProof{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.SaveLockSettings(LockSettingsInput{Grant: grant, Enabled: true, PasswordEnabled: true, Password: "1234", PasswordHint: "fixture hint", IdleSeconds: idle})
	if e != nil {
		t.Fatal(e)
	}
}
func advanceLockClock(a *App, d time.Duration) {
	s := &a.securityLock
	s.mu.Lock()
	now := s.clock().Add(d)
	s.now = func() time.Time { return now }
	s.mu.Unlock()
}
func TestSecurityLockPasswordPersistenceAndRetry(t *testing.T) {
	a := lockTestApp(t)
	enableTestPassword(t, a, 0)
	data, e := os.ReadFile(filepath.Join(a.store.dir, securityLockFile))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(data, []byte("1234")) || bytes.Contains(data, []byte("fixture hint")) {
		t.Fatal("plaintext lock configuration persisted")
	}
	public, _ := json.Marshal(a.store.List())
	if bytes.Contains(public, []byte("passwordHash")) || bytes.Contains(public, []byte("fixture hint")) {
		t.Fatal("lock credentials escaped into exported config")
	}
	if _, e = a.LockNow(); e != nil {
		t.Fatal(e)
	}
	if a.RequireUnlocked() == nil {
		t.Fatal("locked access accepted")
	}
	for i := 0; i < 5; i++ {
		if _, e = a.Unlock(LockProof{"password", "wrong"}); e == nil {
			t.Fatal("bad password unlocked")
		}
	}
	if a.SecurityLockStatus().RetryAfterSeconds < 28 {
		t.Fatal("missing retry backoff")
	}
	if _, e = a.Unlock(LockProof{"password", "1234"}); e == nil {
		t.Fatal("cooldown bypassed")
	}
	reopened, e := New(a.store.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if !reopened.SecurityLockStatus().Locked || reopened.SecurityLockStatus().RetryAfterSeconds == 0 {
		t.Fatal("restart bypassed lock or retry delay")
	}
	advanceLockClock(reopened, time.Minute)
	status, e := reopened.Unlock(LockProof{"password", "1234"})
	if e != nil || status.Locked || status.RetryAfterSeconds != 0 {
		t.Fatal(status, e)
	}
	if !a.SecurityLockStatus().Locked {
		t.Fatal("test app instances unexpectedly share unlocked memory")
	}
}
func TestSecurityLockAuthorizationAndIdle(t *testing.T) {
	a := lockTestApp(t)
	advanceLockClock(a, 0)
	token, e := a.AuthorizeLockSettings(LockProof{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.SaveLockSettings(LockSettingsInput{Grant: token, Enabled: true, PasswordEnabled: true, Password: "123"})
	if e == nil {
		t.Fatal("short password accepted")
	}
	_, e = a.SaveLockSettings(LockSettingsInput{Grant: token, Enabled: true})
	if e == nil {
		t.Fatal("lock with no unlock method enabled")
	}
	_, e = a.SaveLockSettings(LockSettingsInput{Grant: token, Enabled: true, PasswordEnabled: true, Password: "1234", IdleSeconds: 2})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.SaveLockSettings(LockSettingsInput{Grant: token}); e == nil {
		t.Fatal("reused settings grant disabled security")
	}
	if _, e = a.AuthorizeLockSettings(LockProof{"password", "wrong"}); e == nil {
		t.Fatal("settings authentication bypassed")
	}
	token, e = a.AuthorizeLockSettings(LockProof{"password", "1234"})
	if e != nil {
		t.Fatal(e)
	}
	advanceLockClock(a, time.Second)
	if a.SecurityLockStatus().Locked {
		t.Fatal("locked too early")
	}
	a.SecurityLockStatus()
	advanceLockClock(a, 1100*time.Millisecond)
	if !a.SecurityLockStatus().Locked {
		t.Fatal("status polling extended idle timer")
	}
	if !a.LockActivity().Locked {
		t.Fatal("activity unlocked app")
	}
	if _, e = a.SaveLockSettings(LockSettingsInput{Grant: token}); e == nil {
		t.Fatal("pre-lock settings grant bypassed lock")
	}
	if _, e = a.Unlock(LockProof{"password", "1234"}); e != nil {
		t.Fatal(e)
	}
	if _, e = a.SaveLockSettings(LockSettingsInput{Grant: token}); e == nil {
		t.Fatal("stale grant survived relock")
	}
	token, e = a.AuthorizeLockSettings(LockProof{"password", "1234"})
	if e != nil {
		t.Fatal(e)
	}
	status, e := a.SaveLockSettings(LockSettingsInput{Grant: token, Enabled: false})
	if e != nil || status.Enabled || status.PasswordConfigured {
		t.Fatal(status, e)
	}
}
func TestSecurityLockRFC6238AndTOTPBinding(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, v := range []struct {
		at   int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		if got := lockTOTP(secret, v.at/30); got != v.want {
			t.Fatalf("RFC 6238 %d got %s want %s", v.at, got, v.want)
		}
	}
	now := time.Unix(1234567890, 0)
	code := lockTOTP(secret, now.Unix()/30)
	if _, ok := matchLockTOTP(secret, code, now, now.Unix()/30); ok {
		t.Fatal("TOTP replay accepted")
	}
	if _, ok := matchLockTOTP(secret, code, now.Add(61*time.Second), -1); ok {
		t.Fatal("expired TOTP accepted")
	}
	a := lockTestApp(t)
	grant, e := a.AuthorizeLockSettings(LockProof{})
	if e != nil {
		t.Fatal(e)
	}
	enrollment, e := a.EnrollLockTOTP(grant)
	if e != nil {
		t.Fatal(e)
	}
	key, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
	if e != nil || len(key) != 20 {
		t.Fatal(e)
	}
	uri, e := url.Parse(enrollment.URI)
	if e != nil || uri.Scheme != "otpauth" || uri.Host != "totp" || uri.Query().Get("issuer") != "DengShell" || uri.Query().Get("secret") != enrollment.Secret {
		t.Fatal("invalid authenticator URI")
	}
	pngBytes, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(enrollment.QR, "data:image/png;base64,"))
	if e != nil {
		t.Fatal(e)
	}
	qr, e := png.Decode(bytes.NewReader(pngBytes))
	if e != nil || qr.Bounds().Dx() != 280 {
		t.Fatal("invalid local QR image", e)
	}
	input := LockSettingsInput{Grant: grant, Enabled: true, TwoFactorEnabled: true, Code: "bad"}
	if _, e = a.SaveLockSettings(input); e == nil || a.SecurityLockStatus().Enabled {
		t.Fatal("unconfirmed TOTP enabled lock")
	}
	a.securityLock.mu.Lock()
	a.securityLock.now = func() time.Time { return now }
	a.securityLock.grants[grant] = lockGrant{expires: now.Add(time.Minute), revision: a.securityLock.revision, secret: key}
	a.securityLock.mu.Unlock()
	input.Code = lockTOTP(key, now.Unix()/30)
	if _, e = a.SaveLockSettings(input); e != nil {
		t.Fatal(e)
	}
	a.LockNow()
	if _, e = a.Unlock(LockProof{"totp", input.Code}); e == nil {
		t.Fatal("binding code replayed to unlock")
	}
	advanceLockClock(a, 30*time.Second)
	next := lockTOTP(key, now.Unix()/30+1)
	if _, e = a.Unlock(LockProof{"totp", next}); e != nil {
		t.Fatal(e)
	}
	a.LockNow()
	if _, e = a.Unlock(LockProof{"totp", next}); e == nil {
		t.Fatal("same TOTP unlocked twice")
	}
	reopened, e := New(a.store.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	reopened.securityLock.mu.Lock()
	reopened.securityLock.now = func() time.Time { return now.Add(30 * time.Second) }
	reopened.securityLock.mu.Unlock()
	if _, e = reopened.Unlock(LockProof{"totp", next}); e == nil {
		t.Fatal("restart erased TOTP replay record")
	}
}
func TestSecurityLockHTTPAndTamperFailure(t *testing.T) {
	a := lockTestApp(t)
	enableTestPassword(t, a, 0)
	a.LockNow()
	handler := a.Handler(os.DirFS("../../mobile/assets/web"))
	request := func(method, path string, body string, token bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token {
			r.Header.Set("X-CloudShell-Token", a.Token())
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, v := range []struct{ method, path string }{{"GET", "/api/config"}, {"POST", "/api/sessions"}, {"GET", "/api/sessions/fixture/processes"}, {"POST", "/api/profiles"}, {"GET", "/api/config/export"}, {"GET", "/api/security-lock/access"}} {
		if w := request(v.method, v.path, "{}", true); w.Code != http.StatusLocked || !strings.Contains(w.Body.String(), "DENGSHELL_LOCKED") {
			t.Fatalf("%s %s: %d %s", v.method, v.path, w.Code, w.Body)
		}
	}
	if w := request("GET", "/api/security-lock/status", "", false); w.Code != 403 {
		t.Fatal("lock endpoint accepted missing capability")
	}
	if w := request("GET", "/api/security-lock/status", "", true); w.Code != 200 || strings.Contains(w.Body.String(), "passwordHash") {
		t.Fatal(w.Code, w.Body)
	}
	if w := request("POST", "/api/security-lock/activity", "{}", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"locked":true`) {
		t.Fatal("activity bypassed locked state")
	}
	if w := request("POST", "/api/security-lock/unlock", `{"method":"password","value":"1234"}`, true); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := request("GET", "/api/config", "", true); w.Code != 200 {
		t.Fatal("unlock did not restore access", w.Body)
	}
	path := filepath.Join(a.store.dir, securityLockFile)
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	data[len(data)-1] ^= 1
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	if reopened, e := New(a.store.dir); e == nil {
		reopened.Close()
		t.Fatal("corrupt lock silently disabled")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, data) {
		t.Fatal("corrupt lock overwritten")
	}
}
func TestSecurityLockPasswordOrTOTPAndExpiredGrant(t *testing.T) {
	a := lockTestApp(t)
	enableTestPassword(t, a, 0)
	grant, e := a.AuthorizeLockSettings(LockProof{"password", "1234"})
	if e != nil {
		t.Fatal(e)
	}
	advanceLockClock(a, 6*time.Minute)
	if _, e = a.EnrollLockTOTP(grant); e == nil {
		t.Fatal("expired settings grant usable")
	}
	grant, e = a.AuthorizeLockSettings(LockProof{"password", "1234"})
	if e != nil {
		t.Fatal(e)
	}
	enrollment, e := a.EnrollLockTOTP(grant)
	if e != nil {
		t.Fatal(e)
	}
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
	a.securityLock.mu.Lock()
	now := a.securityLock.clock()
	a.securityLock.mu.Unlock()
	_, e = a.SaveLockSettings(LockSettingsInput{Grant: grant, Enabled: true, PasswordEnabled: true, TwoFactorEnabled: true, Code: lockTOTP(key, now.Unix()/30)})
	if e != nil {
		t.Fatal(e)
	}
	a.LockNow()
	if _, e = a.Unlock(LockProof{"password", "1234"}); e != nil {
		t.Fatal("password alternative failed", e)
	}
	a.LockNow()
	advanceLockClock(a, 30*time.Second)
	if _, e = a.Unlock(LockProof{"totp", lockTOTP(key, now.Unix()/30+1)}); e != nil {
		t.Fatal("TOTP alternative failed", e)
	}
}
