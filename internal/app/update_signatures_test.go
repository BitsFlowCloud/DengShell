package app

import (
	"cloudshell/internal/updatetrust"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebsiteControlCannotForgeUpdate(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	for _, platform := range []string{"windows-amd64", "linux-amd64"} {
		t.Run(platform, func(t *testing.T) {
			d := UpdateDescriptor{SchemaVersion: 2, Build: ApplicationBuild + 1, Product: "DengShell", Platform: platform, Version: ApplicationVersion, SHA256: strings.Repeat("a", 64), ExecutableSHA256: strings.Repeat("a", 64), Size: 42}
			var wire atomic.Value
			wire.Store(d)
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(wire.Load().(UpdateDescriptor))
			}))
			defer s.Close()
			check := func() UpdateStatus {
				return checkUpdate(context.Background(), s.Client(), s.URL, platform, strings.Repeat("b", 64), UpdateReceipt{})
			}
			if got := check(); got.Status != "none" || got.Reason != "invalid-signature" {
				t.Fatalf("unsigned accepted: %+v", got)
			}
			d, _ = updatetrust.Sign(d, private, now, time.Hour)
			wire.Store(d)
			if got := check(); got.Status != "none" || got.Reason != "invalid-signature" {
				t.Fatalf("attacker key accepted: %+v", got)
			}
			trusted := map[string]ed25519.PublicKey{updatetrust.KeyID(public): public}
			verify := func() UpdateStatus {
				return checkUpdateTrusted(context.Background(), s.Client(), s.URL, platform, strings.Repeat("b", 64), UpdateReceipt{}, trusted, now)
			}
			if got := verify(); got.Status != "available" {
				t.Fatalf("trusted signature rejected: %+v", got)
			}
			d.Build++
			d.SHA256 = strings.Repeat("c", 64)
			d.ExecutableSHA256 = d.SHA256
			wire.Store(d)
			if got := verify(); got.Status != "none" || got.Reason != "invalid-signature" {
				t.Fatalf("website changed hash/build: %+v", got)
			}
		})
	}
}

func TestExpiredOfferCannotStartDownload(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.updateCheck.once.Do(func() { a.updateCheck.done = make(chan struct{}); close(a.updateCheck.done) })
	a.updateCheck.result = UpdateStatus{Status: "available", Package: &UpdatePackage{Build: ApplicationBuild + 1, Version: ApplicationVersion, SHA256: strings.Repeat("a", 64), expiresAt: time.Now().Add(-time.Second).Unix()}}
	if _, err = a.StartUpdateDownload(strings.Repeat("a", 64)); err == nil {
		t.Fatal("expired startup offer downloaded")
	}
}
