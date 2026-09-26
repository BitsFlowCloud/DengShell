package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func finalShellTestKey(t *testing.T, passphrase string) []byte {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(private, "import fixture")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(private, "import fixture", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block)
}

func writeFinalShellTestKey(t *testing.T, directory, filename string, data []byte) {
	t.Helper()
	p := filepath.Join(directory, "key", filename)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeFinalShellKeyConnection(t *testing.T, directory, name, ref string) {
	t.Helper()
	v := finalShellFixture(name, 2)
	v["secret_key_id"] = ref
	writeFinalShellFixture(t, directory, name, v)
}

func TestFinalShellKeysShareAndPersist(t *testing.T) {
	dir := t.TempDir()
	first, second := finalShellTestKey(t, ""), finalShellTestKey(t, "")
	writeFinalShellTestKey(t, dir, "shared.pem", first)
	writeFinalShellTestKey(t, dir, "alias", first)
	record, _ := json.Marshal(map[string]string{"id": "metadata-id", "name": "JSON key", "private_key": string(second)})
	writeFinalShellTestKey(t, dir, "original-key.json", record)
	for name, ref := range map[string]string{"one": "shared", "two": "shared", "three": "alias", "four": "metadata-id", "missing": "unknown"} {
		writeFinalShellKeyConnection(t, dir, name, ref)
	}
	writeFinalShellFixture(t, dir, "password", finalShellFixture("password", 1))
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.importFinalShell(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 6 || result.KeysImported != 2 || result.KeysSkipped != 1 || result.KeysAssociated != 4 || result.NeedsKey != 1 {
		t.Fatalf("unexpected counts: %+v", result)
	}
	ids := map[string]string{}
	for _, p := range s.config.Servers {
		ids[p.Name] = p.KeyID
		if p.Auth == "key" && p.Secret != "" {
			t.Fatal("account password used as key passphrase")
		}
	}
	if ids["QA one"] == "" || ids["QA one"] != ids["QA two"] || ids["QA one"] != ids["QA three"] || ids["QA one"] == ids["QA four"] {
		t.Fatal("key references were not matched uniquely")
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	beforeRestart, _ := json.Marshal(s.List())
	afterRestart, _ := json.Marshal(reopened.List())
	if !bytes.Equal(beforeRestart, afterRestart) {
		t.Fatal("restart changed imported data")
	}
	for _, value := range []any{result, reopened.List()} {
		data, _ := json.Marshal(value)
		if bytes.Contains(data, []byte("PRIVATE KEY")) || bytes.Contains(data, []byte(finalShellTestPassword)) {
			t.Fatal("private material exposed in API")
		}
	}
	again, err := s.importFinalShell(context.Background(), dir)
	if err != nil || again.Imported != 0 || again.Updated != 0 || again.KeysImported != 0 || again.Skipped != 6 || len(s.config.Keys) != 2 {
		t.Fatal("reimport duplicated records", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "key", "shared.pem"))
	if !bytes.Equal(got, first) {
		t.Fatal("source changed")
	}
}

func TestFinalShellKeysReimportOnlyFillsMissing(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"missing", "manual", "deleted"} {
		writeFinalShellKeyConnection(t, dir, name, "shared")
	}
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.importFinalShell(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range s.config.Servers {
		switch p.Name {
		case "QA missing":
			p.Name = "My edited name"
			p.Secret = "preserve local fallback"
		case "QA manual":
			p.KeyPath = "/user/selected/key"
		case "QA deleted":
			if err = s.Delete(p.ID); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err = s.Save(p, false); err != nil {
			t.Fatal(err)
		}
	}
	writeFinalShellTestKey(t, dir, "shared", finalShellTestKey(t, ""))
	result, err := s.importFinalShell(context.Background(), dir)
	if err != nil || result.Imported != 0 || result.Updated != 1 || result.Skipped != 2 || result.KeysAssociated != 1 {
		t.Fatalf("unexpected reimport: %+v, %v", result, err)
	}
	for _, p := range s.config.Servers {
		switch p.Name {
		case "My edited name":
			if p.KeyID == "" || p.Secret != "preserve local fallback" {
				t.Fatal("missing association not filled or user edits lost")
			}
		case "QA manual":
			if p.KeyID != "" || p.KeyPath != "/user/selected/key" {
				t.Fatal("manual key overwritten")
			}
		case "QA deleted":
			if p.KeyID != "" || p.DeletedAt == nil {
				t.Fatal("deleted connection restored or changed")
			}
		}
	}
}

func TestFinalShellKeysEncryptedImportAndMissingAtConnect(t *testing.T) {
	dir := t.TempDir()
	const passphrase = "test-only-secret"
	writeFinalShellTestKey(t, dir, "locked", finalShellTestKey(t, passphrase))
	writeFinalShellKeyConnection(t, dir, "one", "locked")
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	r, err := a.store.importFinalShell(context.Background(), dir)
	if err != nil || r.NeedsKey != 0 || r.KeysImported != 1 {
		t.Fatal("encrypted key not imported", err)
	}
	p := a.store.config.Servers[0]
	assertAuth := func(want string) {
		t.Helper()
		_, err := a.Connect(context.Background(), p.ID, "")
		var e *AuthenticationError
		if !errors.As(err, &e) || e.Code() != want {
			t.Fatalf("want %s before network, got %v", want, err)
		}
	}
	assertAuth("ssh_key_passphrase_required")
	key := a.store.List().Keys[0]
	if !key.Encrypted || key.HasPassphrase {
		t.Fatal("invented encrypted key password")
	}
	saved, err := a.store.SaveKey(KeyInput{ID: key.ID, Name: key.Name, Passphrase: passphrase})
	if err != nil || !saved.HasPassphrase || saved.Fingerprint == "" {
		t.Fatal("saved passphrase or fingerprint missing", err)
	}
	data, secret, err := a.store.keyMaterial(key.ID)
	if err != nil || secret != passphrase {
		t.Fatal("shared key passphrase unavailable", err)
	}
	if _, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(secret)); err != nil {
		t.Fatal(err)
	}
	clear(data)
	if err = os.Remove(filepath.Join(a.store.dir, "keys", key.ID)); err != nil {
		t.Fatal(err)
	}
	assertAuth("ssh_key_missing")
	a.store.config.Keys = nil
	assertAuth("ssh_key_missing")
}

func TestFinalShellKeyMetadataPasswordsAndInvalidInput(t *testing.T) {
	data := finalShellTestKey(t, finalShellTestPassword)
	for _, password := range []string{finalShellTestPassword, finalShellTestCipher, "wrong-password"} {
		record, _ := json.Marshal(map[string]string{"id": "key-id", "privateKey": string(data), "password": password})
		candidate, err := parseFinalShellKey(record, "key/test.json")
		if err != nil {
			t.Fatal(err)
		}
		want := finalShellTestPassword
		if password == "wrong-password" {
			want = ""
		}
		if candidate.key.Passphrase != want || !candidate.key.Encrypted {
			t.Fatal("key-specific password incorrectly interpreted")
		}
		clear(candidate.data)
	}
	for _, input := range [][]byte{[]byte(`{"privateKey":"/outside/key","password":"do-not-echo"}`), []byte("invalid-private-content"), []byte(`{"path":"/outside/key"}`)} {
		if _, err := parseFinalShellKey(input, "key/test.json"); err == nil {
			t.Fatal("invalid key or file reference accepted")
		}
	}
}

func TestFinalShellKeysAmbiguousReferenceIsNotGuessed(t *testing.T) {
	dir := t.TempDir()
	writeFinalShellTestKey(t, dir, "first/same.pem", finalShellTestKey(t, ""))
	writeFinalShellTestKey(t, dir, "second/same.pem", finalShellTestKey(t, ""))
	writeFinalShellKeyConnection(t, dir, "ambiguous", "same")
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.importFinalShell(context.Background(), dir)
	if err != nil || r.KeysImported != 2 || r.NeedsKey != 1 || r.KeysAssociated != 0 || s.config.Servers[0].KeyID != "" {
		t.Fatal("ambiguous reference guessed", err)
	}
}

func TestFinalShellKeysBoundaryRollbackAndConcurrentImport(t *testing.T) {
	dir := t.TempDir()
	writeFinalShellTestKey(t, dir, "good", finalShellTestKey(t, ""))
	writeFinalShellTestKey(t, dir, "public.pub", []byte("not a private key"))
	writeFinalShellTestKey(t, dir, "bad.json", []byte(`{"path":"outside"}`))
	writeFinalShellTestKey(t, dir, "oversized", make([]byte, (2<<20)+1))
	wantFailures := 2
	if err := os.Symlink(filepath.Join(dir, "key", "good"), filepath.Join(dir, "key", "link")); err == nil {
		wantFailures++
	} else {
		t.Log("symlink check unavailable on this host; continuing rollback and import checks")
	}
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before := s.List()
	configKey := filepath.Join(s.dir, ConfigKeyName)
	if err := os.Rename(configKey, configKey+".backup"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.importFinalShell(context.Background(), dir); err == nil {
		t.Fatal("write failure was ignored")
	}
	if !reflect.DeepEqual(before, s.List()) {
		t.Fatal("failed import changed config")
	}
	files, err := os.ReadDir(filepath.Join(s.dir, "keys"))
	if err != nil || len(files) != 0 {
		t.Fatal("failed import left private files", err)
	}
	if err := os.Rename(configKey+".backup", configKey); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.importFinalShell(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled import accepted")
	}
	r, err := s.importFinalShell(context.Background(), dir)
	if err != nil || r.Imported != 0 || r.KeysImported != 1 || r.KeysFailed != wantFailures {
		t.Fatalf("key-only import failed: %+v, %v", r, err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if _, err := s.importFinalShell(context.Background(), dir); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(s.config.Keys) != 1 {
		t.Fatal("concurrent import duplicated private keys")
	}
}
