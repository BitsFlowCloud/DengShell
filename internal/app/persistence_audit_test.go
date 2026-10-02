package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSecurityLockRejectsChangedPolicyWhileRunning(t *testing.T) {
	for _, change := range []string{"corrupt", "delete", "key"} {
		t.Run(change, func(t *testing.T) {
			a := lockTestApp(t)
			enableTestPassword(t, a, 0)
			if _, err := a.LockNow(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a.store.dir, securityLockFile)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "corrupt":
				before[len(before)-1] ^= 1
				err = os.WriteFile(path, before, 0600)
			case "delete":
				err = os.Remove(path)
			case "key":
				err = os.WriteFile(filepath.Join(a.store.dir, ConfigKeyName), bytes.Repeat([]byte{0x42}, 32), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			status, err := a.Unlock(LockProof{Method: "password", Value: "1234"})
			if err == nil || !status.Locked {
				t.Fatal("changed policy/key was overwritten and unlock succeeded")
			}
			after, err := os.ReadFile(path)
			if change == "delete" {
				if !os.IsNotExist(err) {
					t.Fatal("deleted policy was silently recreated")
				}
			} else if err != nil || !bytes.Equal(before, after) {
				t.Fatal("policy bytes changed after rejected unlock", err)
			}
		})
	}
}

func TestSecurityLockRejectsStaleInstanceSettings(t *testing.T) {
	a := lockTestApp(t)
	b, err := New(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	staleGrant, err := b.AuthorizeLockSettings(LockProof{})
	if err != nil {
		t.Fatal(err)
	}
	enableTestPassword(t, a, 0)
	before, err := os.ReadFile(filepath.Join(a.store.dir, securityLockFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.SaveLockSettings(LockSettingsInput{Grant: staleGrant, Enabled: false}); err == nil {
		t.Fatal("stale instance disabled a newly enabled password without authorization")
	}
	after, err := os.ReadFile(filepath.Join(a.store.dir, securityLockFile))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("stale settings overwrote the current policy", err)
	}
}

func TestSecurityLockRejectsTOTPReplayAcrossInstances(t *testing.T) {
	a := lockTestApp(t)
	secret := []byte("12345678901234567890")
	now := time.Unix(1234567890, 0)
	policy := securityLockPolicy{Version: 1, Enabled: true, TOTPSecret: secret, LastTOTPStep: -1}
	a.securityLock.mu.Lock()
	err := a.saveLockPolicy(policy)
	a.securityLock.policy = policy
	a.securityLock.locked = true
	a.securityLock.now = func() time.Time { return now }
	a.securityLock.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.securityLock.mu.Lock()
	b.securityLock.now = func() time.Time { return now }
	b.securityLock.mu.Unlock()
	proof := LockProof{Method: "totp", Value: lockTOTP(secret, now.Unix()/30)}
	if _, err := a.Unlock(proof); err != nil {
		t.Fatal(err)
	}
	if status, err := b.Unlock(proof); err == nil || !status.Locked {
		t.Fatal("second instance replayed an already accepted TOTP")
	}
}

func TestSyncAuthenticationChangesDoNotReuseUnrelatedCredentials(t *testing.T) {
	for _, sourceAuth := range []string{"password", "key"} {
		for _, targetAuth := range []string{"password", "key", "agent"} {
			t.Run(sourceAuth+"_to_"+targetAuth, func(t *testing.T) {
				s, err := OpenStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				p, err := s.Save(Profile{Name: "fixture", Host: "fixture.invalid", User: "root", Auth: sourceAuth, Secret: "local-only-credential", KeyPath: "/fixture/local-key"}, false)
				if err != nil {
					t.Fatal(err)
				}
				expected, err := s.syncProjection(false)
				if err != nil {
					t.Fatal(err)
				}
				next := make(map[string]json.RawMessage, len(expected))
				for k, v := range expected {
					next[k] = v
				}
				var remote Profile
				if err := json.Unmarshal(next["server/"+p.ID], &remote); err != nil {
					t.Fatal(err)
				}
				remote.Auth = targetAuth
				remote.Name = "remote edit"
				next["server/"+p.ID] = syncRaw(remote)
				if err := s.applySync(expected, next, false); err != nil {
					t.Fatal(err)
				}
				got, err := s.Get(p.ID)
				if err != nil {
					t.Fatal(err)
				}
				if sourceAuth == targetAuth {
					if got.Secret != "local-only-credential" || got.KeyPath != p.KeyPath {
						t.Fatal("compatible device-local credentials were lost")
					}
				} else if got.Secret != "" || got.KeyPath != "" || got.KeyID != "" {
					t.Fatal("an authentication change reused a different credential type")
				}
			})
		}
	}
}

func TestSyncSharedSecretsRemoveObsoleteLocalKeyPath(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "fixture", Host: "fixture.invalid", User: "root", Auth: "key", Secret: "old-key-passphrase", KeyPath: "/fixture/local-key"}, false)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := s.syncProjection(true)
	if err != nil {
		t.Fatal(err)
	}
	next := make(map[string]json.RawMessage, len(expected))
	for k, v := range expected {
		next[k] = v
	}
	var remote Profile
	if err := json.Unmarshal(next["server/"+p.ID], &remote); err != nil {
		t.Fatal(err)
	}
	remote.Auth, remote.Secret = "password", "new-remote-password"
	next["server/"+p.ID] = syncRaw(remote)
	if err := s.applySync(expected, next, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Auth != "password" || got.Secret != remote.Secret || got.KeyPath != "" || got.KeyID != "" {
		t.Fatal("password-authenticated profile retained its obsolete local private key")
	}
}
