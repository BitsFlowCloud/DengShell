package updatetrust

import (
	"crypto/ed25519"
	"encoding/base64"
)

// Public material only. Trust changes require a new trusted client release;
// update JSON cannot add keys. Never embed the corresponding private seed.
const PublisherPublicKey = "KxTdmUTVIfsABg7YjqVsdVTc9VB7uiUSxfiDEua2TnU="

func PublisherKeys() map[string]ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(PublisherPublicKey)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	public := ed25519.PublicKey(b)
	return map[string]ed25519.PublicKey{KeyID(public): public}
}
