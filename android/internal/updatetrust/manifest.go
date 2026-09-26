// Package updatetrust signs update metadata independently of the download host.
package updatetrust

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const MaxLifetime = 180 * 24 * time.Hour

type Descriptor struct {
	SchemaVersion    int    `json:"schemaVersion"`
	Build            uint64 `json:"build"`
	Product          string `json:"product"`
	Platform         string `json:"platform"`
	Version          string `json:"version"`
	Notes            string `json:"notes"`
	SHA256           string `json:"sha256"`
	Size             int64  `json:"size"`
	ExecutableSHA256 string `json:"executableSHA256"`
	SigningKeyID     string `json:"signingKeyID"`
	IssuedAt         int64  `json:"issuedAt"`
	ExpiresAt        int64  `json:"expiresAt"`
	Signature        string `json:"signature"`
}

func KeyID(public ed25519.PublicKey) string {
	h := sha256.Sum256(public)
	return hex.EncodeToString(h[:])
}

// The wire JSON may be pretty-printed. Both sides sign Go's compact JSON of
// this fixed-order struct with an empty signature, prefixed by a protocol domain.
// Every interpreted field, including key identity and validity, is covered.
func payload(d Descriptor) []byte {
	d.Signature = ""
	b, _ := json.Marshal(d)
	return append([]byte("DengShell update manifest Ed25519 v1\x00"), b...)
}

func Sign(d Descriptor, private ed25519.PrivateKey, now time.Time, lifetime time.Duration) (Descriptor, error) {
	if len(private) != ed25519.PrivateKeySize || lifetime <= 0 || lifetime > MaxLifetime {
		return Descriptor{}, errors.New("invalid signing key or metadata lifetime")
	}
	d.SigningKeyID = KeyID(private.Public().(ed25519.PublicKey))
	d.IssuedAt = now.Unix()
	d.ExpiresAt = now.Add(lifetime).Unix()
	d.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload(d)))
	return d, nil
}

func Verify(d Descriptor, trusted map[string]ed25519.PublicKey, now time.Time) error {
	public, ok := trusted[d.SigningKeyID]
	if !ok || len(public) != ed25519.PublicKeySize || KeyID(public) != d.SigningKeyID {
		return errors.New("untrusted update signing key")
	}
	// Bound issuedAt before subtraction so attacker-controlled integers cannot
	// overflow the lifetime comparison. Allow five minutes of clock skew.
	if d.IssuedAt <= 0 || d.IssuedAt > now.Add(5*time.Minute).Unix() || d.ExpiresAt <= now.Unix() || d.ExpiresAt <= d.IssuedAt || d.ExpiresAt-d.IssuedAt > int64(MaxLifetime/time.Second) {
		return errors.New("update signature validity window rejected")
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(d.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(public, payload(d), sig) {
		return errors.New("update signature verification failed")
	}
	return nil
}
