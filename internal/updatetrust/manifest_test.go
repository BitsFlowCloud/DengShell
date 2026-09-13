package updatetrust

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignedManifestAuthentication(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1789300000, 0)
	d, err := Sign(Descriptor{SchemaVersion: 2, Build: 20260913022, Product: "DengShell", Platform: "windows-amd64", Version: "v0.01", Notes: "中文 <更新>\n说明", SHA256: strings.Repeat("a", 64), ExecutableSHA256: strings.Repeat("a", 64), Size: 42}, private, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]ed25519.PublicKey{KeyID(public): public}
	if err = Verify(d, keys, now); err != nil {
		t.Fatal(err)
	}
	// JSON whitespace and escaped Unicode are transport details, not signed data.
	b, _ := json.MarshalIndent(d, "", "  ")
	var roundTrip Descriptor
	if err = json.Unmarshal(b, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if err = Verify(roundTrip, keys, now); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Descriptor){
		"schema":           func(d *Descriptor) { d.SchemaVersion++ },
		"build":            func(d *Descriptor) { d.Build++ },
		"product":          func(d *Descriptor) { d.Product = "Other" },
		"platform":         func(d *Descriptor) { d.Platform = "linux-amd64" },
		"version":          func(d *Descriptor) { d.Version = "v9.0" },
		"notes":            func(d *Descriptor) { d.Notes = "安装伪造更新" },
		"hash":             func(d *Descriptor) { d.SHA256 = strings.Repeat("b", 64) },
		"size":             func(d *Descriptor) { d.Size++ },
		"executable":       func(d *Descriptor) { d.ExecutableSHA256 = strings.Repeat("b", 64) },
		"key":              func(d *Descriptor) { d.SigningKeyID = strings.Repeat("0", 64) },
		"issued":           func(d *Descriptor) { d.IssuedAt-- },
		"expires":          func(d *Descriptor) { d.ExpiresAt++ },
		"unsigned":         func(d *Descriptor) { d.Signature = "" },
		"invalid encoding": func(d *Descriptor) { d.Signature = "!!!" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := d
			mutate(&bad)
			if Verify(bad, keys, now) == nil {
				t.Fatal("tampered metadata accepted")
			}
		})
	}
	if Verify(d, keys, now.Add(24*time.Hour)) == nil {
		t.Fatal("expired signature accepted")
	}
	if Verify(d, keys, now.Add(-6*time.Minute)) == nil {
		t.Fatal("future signature accepted")
	}
	if Verify(d, nil, now) == nil {
		t.Fatal("missing trust anchor accepted")
	}
	if _, err = Sign(d, private, now, MaxLifetime+time.Second); err == nil {
		t.Fatal("unbounded validity accepted")
	}
	if _, err = Sign(d, nil, now, time.Hour); err == nil {
		t.Fatal("missing private key accepted")
	}
	// Even a valid signature cannot override the verifier's maximum lifetime.
	tooLong := d
	tooLong.ExpiresAt = now.Add(MaxLifetime + time.Hour).Unix()
	// Sign() enforces the same limit, so extending its fields also invalidates it.
	if Verify(tooLong, keys, now) == nil {
		t.Fatal("overlong validity accepted")
	}
}

func TestTrustRotationRequiresExplicitClientKeyChange(t *testing.T) {
	oldPublic, oldPrivate, _ := ed25519.GenerateKey(rand.Reader)
	newPublic, newPrivate, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	old, _ := Sign(Descriptor{}, oldPrivate, now, time.Hour)
	newer, _ := Sign(Descriptor{}, newPrivate, now, time.Hour)
	oldKeys := map[string]ed25519.PublicKey{KeyID(oldPublic): oldPublic}
	newKeys := map[string]ed25519.PublicKey{KeyID(newPublic): newPublic}
	if Verify(newer, oldKeys, now) == nil {
		t.Fatal("website introduced an untrusted key")
	}
	if Verify(old, newKeys, now) == nil {
		t.Fatal("removed key remains trusted in new client")
	}
	if err := Verify(newer, newKeys, now); err != nil {
		t.Fatal(err)
	}
	if len(PublisherKeys()) != 1 {
		t.Fatal("compiled publisher key is malformed")
	}
}
