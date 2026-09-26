package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func readPlainConfigForTest(t *testing.T, s *Store) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.dir, EncryptedConfigName))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptConfig(s.key, data)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func TestPortableEncryptedFreshRestartAndBackup(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{EncryptedConfigName, ConfigKeyName} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatalf("unsafe mode: %s %v", name, info.Mode())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); !os.IsNotExist(err) {
		t.Fatal("fresh plaintext config exists")
	}
	p, err := s.Save(Profile{Name: "private-host-name", Host: "private.example", User: "root", Secret: "VERY-PRIVATE-SSH-PASSWORD"}, false)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, EncryptedConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte(p.Host)) || bytes.Contains(before, []byte(p.Name)) || bytes.Contains(before, []byte("VERY-PRIVATE")) {
		t.Fatal("configuration not encrypted")
	}
	value := s.List().Appearance
	value.Theme, value.OnboardingCompleted = "dark", true
	value.Layout["dengshell.workspace"] = json.RawMessage(`{"tab":"commands","follow":false}`)
	if _, err := s.SaveAppearance(value); err != nil {
		t.Fatal(err)
	}
	backup, _ := os.ReadFile(filepath.Join(dir, EncryptedConfigName+".bak"))
	if !bytes.Equal(backup, before) {
		t.Fatal("backup is not byte-exact previous authenticated file")
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(p.ID)
	if err != nil || got.Secret != "VERY-PRIVATE-SSH-PASSWORD" {
		t.Fatal("credential lost across restart", err)
	}
	if !reflect.DeepEqual(reopened.List().Appearance, s.List().Appearance) {
		t.Fatal("preferences lost on reopen")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".dengshell-write-") || strings.HasSuffix(entry.Name(), ".json") {
			t.Fatal("plaintext/temporary output retained", entry.Name())
		}
	}
}

func TestLegacyMigrationBackupIsEncryptedExactAndRestorable(t *testing.T) {
	for _, tc := range []struct{ name, prefix, original string }{
		{"legacy-schema", "config.pre-v2", "{\n  \"servers\": [],\n  \"notes\": \"PRIVATE-LEGACY-PASSWORD\"\n}\n"},
		{"current-schema", "config.pre-encryption", "{\n  \"schemaVersion\": 2,\n  \"servers\": [],\n  \"notes\": \"PRIVATE-LEGACY-PASSWORD\"\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tc.original), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			backups, err := filepath.Glob(filepath.Join(dir, tc.prefix+".*.enc"))
			if err != nil || len(backups) != 1 {
				t.Fatal("encrypted migration backup missing", backups, err)
			}
			backup, err := os.ReadFile(backups[0])
			if err != nil {
				t.Fatal(err)
			}
			plain, err := decryptConfig(s.key, backup)
			if err != nil || string(plain) != tc.original {
				t.Fatal("legacy whitespace or content changed in backup", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".json") {
					t.Fatal("migration left a plaintext file", entry.Name())
				}
				data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(data, []byte("PRIVATE-LEGACY-PASSWORD")) {
					t.Fatal("migration wrote plaintext secret", entry.Name())
				}
			}
			// A migration backup can be restored as the primary with the same
			// sidecar key. Old inner schemas upgrade normally after decryption.
			restoreDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(restoreDir, ConfigKeyName), s.key, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(restoreDir, EncryptedConfigName), backup, 0600); err != nil {
				t.Fatal(err)
			}
			restored, err := OpenStore(restoreDir)
			if err != nil {
				t.Fatal("encrypted migration backup could not restore", err)
			}
			if restored.config.SchemaVersion != ConfigSchemaVersion || string(restored.config.Extra["notes"]) != `"PRIVATE-LEGACY-PASSWORD"` {
				t.Fatal("backup restore lost original data")
			}
		})
	}
}

func TestEncryptedConfigCorruptionAndKeyLossNeverOverwrite(t *testing.T) {
	for _, scenario := range []string{"ciphertext", "algorithm", "future-envelope", "wrong-key", "missing-key", "missing-config", "truncated", "trailing", "future-schema"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Save(Profile{Name: "kept", Host: "localhost", User: "root", Secret: "unchanged"}, false); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, EncryptedConfigName)
			data, _ := os.ReadFile(path)
			var envelope configEnvelope
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "ciphertext":
				envelope.Payload[len(envelope.Payload)-1] ^= 1
				data, _ = json.Marshal(envelope)
			case "algorithm":
				envelope.Algorithm = "AES-128-GCM"
				data, _ = json.Marshal(envelope)
			case "future-envelope":
				envelope.Version = 999
				data, _ = json.Marshal(envelope)
			case "wrong-key":
				if err := os.WriteFile(filepath.Join(dir, ConfigKeyName), bytes.Repeat([]byte{7}, 32), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-key":
				if err := os.Remove(filepath.Join(dir, ConfigKeyName)); err != nil {
					t.Fatal(err)
				}
			case "missing-config":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "truncated":
				data = data[:len(data)/2]
			case "trailing":
				data = append(data, []byte(`{}`)...)
			case "future-schema":
				data, err = encryptConfig(s.key, []byte(`{"schemaVersion":999}`))
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "missing-config" {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			backupBefore, _ := os.ReadFile(path + ".bak")
			if _, err := OpenStore(dir); err == nil {
				t.Fatal("damaged config was reopened")
			}
			if scenario != "future-schema" { // A valid authenticated document changed by another writer is also protected by its byte digest.
				if _, err := s.SaveGroup(ServerGroup{Name: "should-not-save"}); err == nil {
					t.Fatal("open store overwrote altered state")
				}
			} else if _, err := s.SaveGroup(ServerGroup{Name: "should-not-save"}); err == nil {
				t.Fatal("newer version overwritten")
			}
			after, readErr := os.ReadFile(path)
			if scenario == "missing-config" {
				if !os.IsNotExist(readErr) {
					t.Fatal("deleted config silently recreated")
				}
			} else if !bytes.Equal(after, data) {
				t.Fatal("damaged file overwritten")
			}
			backupAfter, _ := os.ReadFile(path + ".bak")
			if !bytes.Equal(backupBefore, backupAfter) {
				t.Fatal("last good backup overwritten")
			}
		})
	}
}

func TestConcurrentStoresRejectStaleWrite(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveGroup(ServerGroup{Name: "first writer"}); err != nil {
		t.Fatal(err)
	}
	before := second.List()
	if _, err := second.SaveGroup(ServerGroup{Name: "stale second writer"}); err == nil {
		t.Fatal("stale writer overwrote current config")
	}
	if !reflect.DeepEqual(second.List(), before) {
		t.Fatal("stale write changed in-memory state")
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.List().GroupNodes) != 2 {
		t.Fatal("winning write lost")
	}
}

func TestPortableBackupFailureKeepsCurrentDiskAndMemory(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	beforeDisk, err := os.ReadFile(filepath.Join(s.dir, EncryptedConfigName))
	if err != nil {
		t.Fatal(err)
	}
	beforeMemory := s.List().Appearance
	// A failed backup replacement must abort before the primary is replaced.
	if err := os.Mkdir(filepath.Join(s.dir, EncryptedConfigName+".bak"), 0700); err != nil {
		t.Fatal(err)
	}
	next := beforeMemory
	next.Theme = "dark"
	if _, err := s.SaveAppearance(next); err == nil {
		t.Fatal("save ignored backup failure")
	}
	afterDisk, err := os.ReadFile(filepath.Join(s.dir, EncryptedConfigName))
	if err != nil || !bytes.Equal(beforeDisk, afterDisk) {
		t.Fatal("failed backup changed primary", err)
	}
	if !reflect.DeepEqual(s.List().Appearance, beforeMemory) {
		t.Fatal("failed backup changed appearance in memory")
	}
}

func TestConcurrentPortableCreationAndWriteHaveOneWinner(t *testing.T) {
	dir := t.TempDir()
	var stores [2]*Store
	var failures [2]error
	var workers sync.WaitGroup
	for i := range stores {
		workers.Add(1)
		go func(i int) { defer workers.Done(); stores[i], failures[i] = OpenStore(dir) }(i)
	}
	workers.Wait()
	for _, err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(stores[0].key, stores[1].key) {
		t.Fatal("simultaneous startup replaced encryption key")
	}
	start := make(chan struct{})
	for i := range stores {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, failures[i] = stores[i].SaveGroup(ServerGroup{Name: "one commit"})
		}(i)
	}
	close(start)
	workers.Wait()
	winners := 0
	for _, err := range failures {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent writers committed %d times, expected one", winners)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.List().GroupNodes) != 2 {
		t.Fatal("concurrent commit corrupt or duplicated")
	}
}

func TestFreshPortableStoreDoesNotSearchLegacyUserConfiguration(t *testing.T) {
	oldBase := t.TempDir()
	oldDir := filepath.Join(oldBase, "cloudshell")
	if err := os.Mkdir(oldDir, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"servers":[{"id":"old","host":"old.example","user":"root","name":"OLD PROFILE","secret":"OLD PASSWORD"}]}`)
	if err := os.WriteFile(filepath.Join(oldDir, "config.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", oldBase)
	t.Setenv("APPDATA", oldBase)
	fresh, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.List().Servers) != 0 || fresh.List().Appearance.OnboardingCompleted {
		t.Fatal("fresh folder inherited old configuration")
	}
	untouched, err := os.ReadFile(filepath.Join(oldDir, "config.json"))
	if err != nil || !bytes.Equal(untouched, original) {
		t.Fatal("legacy user directory modified", err)
	}
}

func TestPortableFolderMoveRetainsImportedResourcesAndAllPreferences(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "original")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	fontBytes, err := os.ReadFile(filepath.Join("..", "..", "mobile", "assets", "web", "assets", "fonts", "jetbrains-mono.woff2"))
	if err != nil {
		t.Fatal(err)
	}
	font, err := s.ImportAsset("font", "own.woff2", "Portable font", fontBytes)
	if err != nil {
		t.Fatal(err)
	}
	backgroundBytes := sampleBackground(t)
	background, err := s.ImportAsset("background", "own.png", "Portable image", backgroundBytes)
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.SaveKey(KeyInput{Name: "portable private key", Generate: true, Passphrase: "key-password"})
	if err != nil {
		t.Fatal(err)
	}
	keyBytes, err := s.KeyData(key.ID)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := s.SaveProxy(ManagedProxy{Name: "proxy", Proxy: ProxyConfig{Type: "socks5", Host: "localhost", Port: 1080, Password: "proxy-password"}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "server", Host: "localhost", User: "root", Auth: "key", KeyID: key.ID, Secret: "key-password", ProxyID: proxy.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	value := s.List().Appearance
	value.FontID, value.BackgroundID, value.Theme = font.ID, background.ID, "dark"
	value.OnboardingCompleted = true
	value.Layout["dengshell.history."+p.ID] = json.RawMessage(`["echo remembered"]`)
	if _, err := s.SaveAppearance(value); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWindowState(1280, 800, true); err != nil {
		t.Fatal(err)
	}
	want := s.List()
	destination := filepath.Join(t.TempDir(), "moved-with-upgraded-program")
	if err := os.Rename(dir, destination); err != nil {
		t.Fatal(err)
	}
	// Program replacement leaves the complete portable data folder intact.
	if err := os.WriteFile(filepath.Join(destination, "DengShell.exe"), []byte("replacement program fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(destination)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(reopened.List())
	if !bytes.Equal(wantJSON, gotJSON) {
		t.Fatal("configuration changed after portable folder move")
	}
	for id, wantBytes := range map[string][]byte{font.ID: fontBytes, background.ID: backgroundBytes} {
		data, err := os.ReadFile(filepath.Join(destination, "assets", id))
		if err != nil || !bytes.Equal(data, wantBytes) {
			t.Fatal("imported bytes not preserved", id, err)
		}
	}
	gotKey, err := reopened.KeyData(key.ID)
	if err != nil || !bytes.Equal(gotKey, keyBytes) {
		t.Fatal("private key lost after move", err)
	}
	profile, err := reopened.Get(p.ID)
	if err != nil || profile.Secret != "key-password" {
		t.Fatal("profile credentials lost", err)
	}
	if _, err := reopened.SaveGroup(ServerGroup{Name: "write after move"}); err != nil {
		t.Fatal(err)
	}
}

func TestAppearancePortableLayoutAndNativeWindowIsolation(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stale := s.List().Appearance
	if stale.Theme != "system" || stale.OnboardingCompleted || len(stale.Layout) != 0 {
		t.Fatal("fresh defaults incorrect")
	}
	if err := s.SaveWindowState(1400, 900, true); err != nil {
		t.Fatal(err)
	}
	stale.Theme = "dark"
	stale.OnboardingCompleted = true
	stale.Layout["dengshell.workspace"] = json.RawMessage(`{"pane":"commands"}`)
	stale.Layout["cloudshell.layout"] = json.RawMessage(`{"sidebar":260}`)
	if _, err := s.SaveAppearance(stale); err != nil {
		t.Fatal(err)
	}
	got := s.List().Appearance
	if got.WindowWidth != 1400 || got.WindowHeight != 900 || !got.WindowMaximised || got.Theme != "dark" || !got.OnboardingCompleted {
		t.Fatal("stale frontend reverted native state")
	}
	got.Layout["dengshell.workspace"][0] = 'X'
	if !json.Valid(s.List().Appearance.Layout["dengshell.workspace"]) {
		t.Fatal("API layout bytes alias internal config")
	}
	for _, key := range []string{"unapproved", "dengshell.history.", "dengshell.history../key"} {
		invalid := s.List().Appearance
		invalid.Layout[key] = json.RawMessage(`true`)
		if _, err := s.SaveAppearance(invalid); err == nil {
			t.Fatal("invalid layout key accepted", key)
		}
	}
	oversize := s.List().Appearance
	oversize.Layout["dengshell.workspace"] = json.RawMessage(`"` + strings.Repeat("x", 1<<20) + `"`)
	if _, err := s.SaveAppearance(oversize); err == nil {
		t.Fatal("oversized layout accepted")
	}
	if err := s.SaveWindowState(0, 0, false); err == nil {
		t.Fatal("minimized zero dimensions persisted")
	}
}
