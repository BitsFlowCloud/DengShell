package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestManagedKeyPassphrasePersistsAndIsRedacted(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const passphrase = "  shared-key-passphrase-机密  "
	generated, err := s.SaveKey(KeyInput{Name: "generated", Generate: true, Passphrase: passphrase})
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.KeyData(generated.ID)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := s.SaveKey(KeyInput{Name: "imported", PrivateKey: string(data), Passphrase: passphrase})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []ManagedKey{generated, imported} {
		if key.Passphrase != "" || !key.HasPassphrase {
			t.Fatal("save response exposed or lost passphrase metadata")
		}
	}
	renamed, err := s.SaveKey(KeyInput{ID: imported.ID, Name: "renamed"})
	if err != nil || !renamed.HasPassphrase || renamed.Passphrase != "" {
		t.Fatal("rename lost or exposed passphrase", err)
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range reopened.List().Keys {
		stored, secret, err := reopened.keyMaterial(key.ID)
		if err != nil || secret != passphrase {
			t.Fatal("restart lost exact passphrase", err)
		}
		if _, err := ssh.ParsePrivateKeyWithPassphrase(stored, []byte(secret)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stored, data) {
			t.Fatal("saved key file was modified")
		}
	}
	public, err := json.Marshal(reopened.List())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(public, []byte(passphrase)) || bytes.Contains(public, []byte(`"passphrase"`)) || bytes.Contains(public, []byte("PRIVATE KEY")) {
		t.Fatal("configuration API leaked private material")
	}
	encrypted, err := os.ReadFile(filepath.Join(s.dir, EncryptedConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(passphrase)) {
		t.Fatal("passphrase saved as plaintext")
	}
	plain, err := decryptConfig(reopened.key, encrypted)
	if err != nil || !bytes.Contains(plain, []byte(passphrase)) {
		t.Fatal("passphrase not inside authenticated encrypted configuration", err)
	}
}

func TestManagedKeyLegacyPassphraseEditValidationAndRollback(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.SaveKey(KeyInput{Name: "legacy", Generate: true, Passphrase: "correct"})
	if err != nil {
		t.Fatal(err)
	}
	// Model an r16 key: the encrypted file exists but no passphrase was retained.
	s.config.Keys[0].Passphrase = ""
	if err := s.writeLocked(); err != nil {
		t.Fatal(err)
	}
	before, err := s.KeyData(key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveKey(KeyInput{ID: key.ID, Name: "wrong", Passphrase: "incorrect"}); err == nil {
		t.Fatal("accepted incorrect passphrase")
	}
	if s.List().Keys[0].Name != "legacy" || s.List().Keys[0].HasPassphrase {
		t.Fatal("failed validation changed key")
	}
	updated, err := s.SaveKey(KeyInput{ID: key.ID, Name: "updated", Passphrase: "correct"})
	if err != nil || !updated.HasPassphrase || updated.ID != key.ID {
		t.Fatal("legacy edit failed", err)
	}
	after, err := s.KeyData(key.ID)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("editing passphrase changed private file", err)
	}
	// A concurrent disk change must prevent commit and roll back the in-memory secret.
	s.config.Keys[0].Passphrase = ""
	if err := s.writeLocked(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, EncryptedConfigName), []byte("externally changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveKey(KeyInput{ID: key.ID, Name: "not committed", Passphrase: "correct"}); err == nil {
		t.Fatal("ignored disk conflict")
	}
	if s.List().Keys[0].HasPassphrase || s.List().Keys[0].Name != "updated" {
		t.Fatal("failed write retained uncommitted metadata")
	}
}

func TestManagedKeySharedPassphraseConnectsAfterRestart(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	a, host, port := authenticationFixture(t, &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if (meta.User() == "server-a" || meta.User() == "server-b") && bytes.Equal(key.Marshal(), signer.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, errors.New("fixture rejected key")
	}})
	block, err := ssh.MarshalPrivateKeyWithPassphrase(private, "fixture", []byte("shared-secret"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := a.store.SaveKey(KeyInput{Name: "shared", PrivateKey: string(pem.EncodeToMemory(block)), Passphrase: "shared-secret"})
	if err != nil {
		t.Fatal(err)
	}
	var profiles []Profile
	for _, user := range []string{"server-a", "server-b"} {
		secret := ""
		if user == "server-b" {
			secret = "obsolete-profile-passphrase"
		}
		profile, err := a.store.Save(Profile{Name: user, Host: host, Port: port, User: user, Auth: "key", KeyID: key.ID, Secret: secret}, false)
		if err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, profile)
	}
	checkConnections := func(app *App) {
		for _, profile := range profiles {
			for _, supplied := range []string{"", "obsolete-window-cache"} {
				session, err := app.Connect(context.Background(), profile.ID, supplied)
				if err != nil {
					t.Fatal("managed passphrase did not take priority for shared profile", err)
				}
				app.disconnect(session.ID)
			}
		}
	}
	checkConnections(a)
	a.Close()
	reopened, err := New(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	checkConnections(reopened)
}

func TestManagedKeyHTTPDoesNotReturnSavedPassphrase(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	response := httptest.NewRecorder()
	a.saveKeyHTTP(response, httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(`{"name":"http key","generate":true,"passphrase":"http-private-secret"}`)))
	if response.Code != http.StatusOK {
		t.Fatal("save request failed", response.Code)
	}
	if strings.Contains(response.Body.String(), "http-private-secret") || strings.Contains(response.Body.String(), `"passphrase"`) {
		t.Fatal("HTTP save response exposed passphrase")
	}
	var key ManagedKey
	if err := json.Unmarshal(response.Body.Bytes(), &key); err != nil || !key.HasPassphrase {
		t.Fatal("HTTP response lost saved indication", err)
	}
}
