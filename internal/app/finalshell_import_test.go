package app

import (
	"bytes"
	"context"
	"crypto/des"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

const finalShellTestPassword = "DengShell-QA-123!"
const finalShellTestCipher = "AQIDBAUGBwhSo9sQPy5eFkuGZiawt4poDYA6D2O3v3Y="

func finalShellFixture(id string, auth int) map[string]any {
	return map[string]any{"id": id, "name": "QA " + id, "host": "test.invalid", "port": 22, "user_name": "fixture", "authentication_type": auth, "conection_type": 100, "password": finalShellTestCipher, "terminal_encoding": "UTF-8", "proxy_id": "0"}
}
func writeFinalShellFixture(t *testing.T, directory, name string, value map[string]any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(directory, name+"_connect_config.json")
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestFinalShellPasswordReferenceVectors(t *testing.T) {
	// Independently generated with the user-provided reference implementation:
	// signed Java bytes, signed nextLong low word, 127 discards and block padding.
	for _, test := range []struct{ encoded, plain string }{
		{finalShellTestCipher, finalShellTestPassword},
		{"//6AgQjIBgvm59KvCX6kcV9BMrqUIZugyUGPDWTploFMGL5nsWmTlw==", "简体繁體 Paßwörd 🔑"},
		{"fwECAwQFBgdn/leI7eV6W2BjAAFLHxlS", "eight888"},
		{"gH9+fXx7ennnkpiMwHHB+A==", ""}, {"", ""},
	} {
		got, err := decodeFinalShellPassword(test.encoded)
		if err != nil || got != test.plain {
			t.Fatal("reference vector mismatch")
		}
	}
	for _, input := range []string{"not a cipher", "AAAA", base64.StdEncoding.EncodeToString(make([]byte, 17)), strings.Repeat("A", 65540)} {
		if _, err := decodeFinalShellPassword(input); err == nil {
			t.Fatal("invalid password encoding accepted")
		}
	}
	head := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	key, _ := finalShellDESKey(head)
	block, _ := des.NewCipher(key[:])
	for _, plain := range [][]byte{{1, 2, 3, 4, 5, 6, 7, 0}, {1, 2, 3, 4, 5, 6, 7, 2}, {255, 254, 253, 252, 251, 250, 249, 1}} {
		ciphertext := make([]byte, 8)
		block.Encrypt(ciphertext, plain)
		if _, err := decodeFinalShellPassword(base64.StdEncoding.EncodeToString(append(append([]byte{}, head...), ciphertext...))); err == nil {
			t.Fatal("invalid padding or UTF-8 accepted")
		}
	}
}

func TestFinalShellWindowsBOMAndUnknownCommands(t *testing.T) {
	value := finalShellFixture("bom", 1)
	value["command"] = "never execute exported commands"
	value["secret_key"] = "never treat arbitrary exported data as a private key"
	data, _ := json.Marshal(value)
	p, _, err := parseFinalShellProfile(append([]byte{0xef, 0xbb, 0xbf}, data...))
	if err != nil || p.Secret != finalShellTestPassword || p.KeyPath != "" || p.KeyID != "" {
		t.Fatal("BOM or irrelevant exported fields changed credential interpretation")
	}
}
func TestFinalShellImportAtomicIdempotentAndPrivate(t *testing.T) {
	directory := t.TempDir()
	writeFinalShellFixture(t, directory, "password", finalShellFixture("password", 1))
	writeFinalShellFixture(t, directory, "nested/key", finalShellFixture("key", 2))
	app, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	before := app.store.List()
	result, err := app.store.importFinalShell(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 2 || result.NeedsKey != 1 || result.NeedsPassword != 0 || result.Failed != 0 {
		t.Fatalf("unexpected import counts: %+v", result)
	}
	var password, key Profile
	for _, p := range app.store.config.Servers {
		if p.Auth == "password" {
			password = p
		} else {
			key = p
		}
	}
	if password.Secret != finalShellTestPassword || key.Secret != "" || key.KeyID != "" || key.KeyPath != "" || key.Auth != "key" {
		t.Fatal("credentials or key draft were misinterpreted")
	}
	if key.Group != "FinalShell 导入 / nested" {
		t.Fatal("nested directory grouping lost")
	}
	if !reflect.DeepEqual(before.Appearance, app.store.List().Appearance) || !reflect.DeepEqual(before.Keys, app.store.List().Keys) {
		t.Fatal("unrelated data changed")
	}
	for _, value := range []any{result, app.store.List()} {
		data, _ := json.Marshal(value)
		if bytes.Contains(data, []byte(finalShellTestPassword)) || bytes.Contains(data, []byte(finalShellTestCipher)) || bytes.Contains(data, []byte(password.FinalShellID)) {
			t.Fatal("sensitive import data reached public API")
		}
	}
	disk, _ := os.ReadFile(filepath.Join(app.store.dir, EncryptedConfigName))
	if bytes.Contains(disk, []byte(finalShellTestPassword)) {
		t.Fatal("password persisted without encryption")
	}
	_, err = app.Connect(context.Background(), key.ID, "")
	var authError *AuthenticationError
	if !errors.As(err, &authError) || authError.Code() != "ssh_key_missing" {
		t.Fatal("missing private key did not fail before network access")
	}
	password.Name = "Edited by user"
	password.Secret = "updated-local-password"
	saved, err := app.store.Save(password, false)
	if err != nil {
		t.Fatal(err)
	}
	if saved.FinalShellID != "" {
		t.Fatal("source identity exposed")
	}
	again, err := app.store.importFinalShell(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if again.Imported != 0 || again.Skipped != 2 || again.NeedsKey != 1 {
		t.Fatal("repeated import created duplicates")
	}
	p, _ := app.store.Get(password.ID)
	if p.Secret != "updated-local-password" || p.Name != "Edited by user" || p.FinalShellID == "" {
		t.Fatal("import overwrote user edits or lost source identity")
	}
	// Filling in a private-key path converts the imported draft into a normal profile.
	key.KeyPath = "/test-only/private-key"
	if _, err = app.store.Save(key, false); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(app.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if p, err = reopened.Get(key.ID); err != nil || p.KeyPath != key.KeyPath || p.FinalShellID == "" {
		t.Fatal("completed key profile did not persist")
	}
}
func TestFinalShellImportPartialFailuresAndBoundary(t *testing.T) {
	directory := t.TempDir()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	good := finalShellFixture("good", 1)
	writeFinalShellFixture(t, directory, "good", good)
	brokenPassword := finalShellFixture("no-password", 1)
	brokenPassword["password"] = "plain text is not a FinalShell cipher"
	writeFinalShellFixture(t, directory, "no-password", brokenPassword)
	for _, test := range []struct {
		name, field string
		value       any
	}{{"proxy", "proxy_id", "external-id"}, {"invalid-auth", "authentication_type", 9}, {"bad-port", "port", 65536}, {"not-ssh", "conection_type", 101}, {"deleted", "delete_time", 123}, {"legacy-encoding", "terminal_encoding", "GBK"}} {
		value := finalShellFixture(test.name, 1)
		value[test.field] = test.value
		writeFinalShellFixture(t, directory, test.name, value)
	}
	if err = os.WriteFile(filepath.Join(directory, "bad_connect_config.json"), []byte(`{"password":"DO-NOT-ECHO-THIS-INPUT",`), 0600); err != nil {
		t.Fatal(err)
	}
	oversize := filepath.Join(directory, "large_connect_config.json")
	f, _ := os.Create(oversize)
	if err = f.Truncate(finalShellMaxFile + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err = os.WriteFile(filepath.Join(directory, "password.py"), []byte("must never execute"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.importFinalShell(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 3 || result.Failed != 7 || result.NeedsPassword != 1 || result.NeedsProxy != 1 {
		t.Fatalf("bad result counts: %+v", result)
	}
	encoded, _ := json.Marshal(result)
	if bytes.Contains(encoded, []byte("DO-NOT-ECHO")) || bytes.Contains(encoded, []byte("plain text is")) {
		t.Fatal("raw input leaked in error")
	}
	for _, p := range s.config.Servers {
		if p.Name == "QA no-password" && p.Secret != "" {
			t.Fatal("undecodable password saved as plaintext")
		}
	}
	missing, err := s.importFinalShell(context.Background(), filepath.Join(directory, "missing"))
	if err != nil || missing.Found {
		t.Fatal("missing directory was not reported")
	}
}
func TestFinalShellImportRejectsSymlinksAndRollsBack(t *testing.T) {
	directory := t.TempDir()
	outside := t.TempDir()
	writeFinalShellFixture(t, outside, "secret", finalShellFixture("outside", 1))
	if err := os.Symlink(filepath.Join(outside, "secret_connect_config.json"), filepath.Join(directory, "link_connect_config.json")); err != nil {
		t.Skip("symlink unavailable")
	}
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.importFinalShell(context.Background(), directory)
	if err != nil || result.Imported != 0 || result.Failed != 1 {
		t.Fatal("symlink followed")
	}
	writeFinalShellFixture(t, directory, "good", finalShellFixture("good", 1))
	before := s.List()
	key := filepath.Join(s.dir, ConfigKeyName)
	if err = os.Rename(key, key+".qa-backup"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.importFinalShell(context.Background(), directory); err == nil {
		t.Fatal("save succeeded with missing config key")
	}
	if !reflect.DeepEqual(before, s.List()) {
		t.Fatal("failed import partially changed memory")
	}
	if err = os.Rename(key+".qa-backup", key); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.importFinalShell(ctx, directory); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled scan was committed")
	}
	if !reflect.DeepEqual(before, s.List()) {
		t.Fatal("cancelled import changed data")
	}
}
func TestFinalShellConcurrentImportsAndHTTPAuthentication(t *testing.T) {
	directory := t.TempDir()
	writeFinalShellFixture(t, directory, "good", finalShellFixture("good", 1))
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := a.store.importFinalShell(context.Background(), directory)
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(a.store.List().Servers) != 1 {
		t.Fatal("concurrent imports created duplicates")
	}
	handler := a.Handler(fstest.MapFS{"index.html": {Data: []byte("test")}})
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/api/imports/finalshell", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("import endpoint allows unauthenticated access")
		}
	}
	r := httptest.NewRequest("GET", "/api/imports/finalshell", nil)
	r.Header.Set("X-CloudShell-Token", a.Token())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), finalShellFolder) {
		t.Fatal("fixed import directory unavailable")
	}
}
func TestFinalShellUserProvidedSamples(t *testing.T) {
	directory := os.Getenv("DENGSHELL_FINALSHELL_QA_DIRECTORY")
	if directory == "" {
		t.Skip("user-provided samples are tested only with an explicit private QA directory")
	}
	var expected map[string]string
	if json.Unmarshal([]byte(os.Getenv("DENGSHELL_FINALSHELL_QA_DIGESTS")), &expected) != nil || len(expected) == 0 {
		t.Fatal("missing independent reference comparison")
	}
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.importFinalShell(context.Background(), directory)
	if err != nil {
		t.Fatal("sample import failed")
	}
	if result.Imported != 2 || result.Failed != 0 || result.NeedsKey != 1 || result.NeedsPassword != 0 {
		t.Fatal("unexpected sample import outcome")
	}
	for _, item := range result.Items {
		p, err := s.Get(item.ProfileID)
		if err != nil {
			t.Fatal("sample profile missing")
		}
		if p.Auth == "key" {
			if p.Secret != "" || !item.NeedsKey {
				t.Fatal("key reference treated as credential")
			}
			continue
		}
		digest := sha256.Sum256([]byte(p.Secret))
		if hex.EncodeToString(digest[:]) != expected[item.File] {
			t.Fatal("password differs from independent decoder")
		}
	}
}
