package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestWorkspacePersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	command, err := s.SaveCommand(QuickCommand{Name: "容器列表", Group: "运维", Body: "printf 'hello'\r\nprintf 'world'", Color: "teal"})
	if err != nil {
		t.Fatal(err)
	}
	command.Name = "已改名"
	command.Color = "rose"
	if _, err = s.SaveCommand(command); err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "代理服务器", Host: "target.invalid", Port: 22, User: "root", Secret: "ssh-secret", Proxy: ProxyConfig{Type: "socks5", Host: "localhost", Port: 1080, User: "proxy-user", Password: "proxy-secret"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(s.List())
	if bytes.Contains(public, []byte("proxy-secret")) || bytes.Contains(public, []byte("ssh-secret")) || !p.Proxy.HasPassword {
		t.Fatal("credentials exposed or password indicator lost")
	}
	p.Name = "已编辑"
	if _, err = s.Save(p, false); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := reopened.Get(p.ID)
	if stored.Proxy.Password != "proxy-secret" || stored.Secret != "ssh-secret" {
		t.Fatal("editing erased saved credentials")
	}
	if got := reopened.List().Commands; len(got) != 1 || got[0].Name != "已改名" || got[0].Color != "rose" || got[0].Body != "printf 'hello'\nprintf 'world'" {
		t.Fatalf("commands not persisted: %#v", got)
	}
	p.Proxy.Host = "new-proxy.invalid"
	if _, err = reopened.Save(p, false); err != nil {
		t.Fatal(err)
	}
	stored, _ = reopened.Get(p.ID)
	if stored.Proxy.Password != "" {
		t.Fatal("old proxy password retained for a different endpoint")
	}
	if err = reopened.DeleteCommand(command.ID); err != nil {
		t.Fatal(err)
	}
	reopened, _ = OpenStore(dir)
	if len(reopened.List().Commands) != 0 {
		t.Fatal("command deletion not persisted")
	}
	if _, err = reopened.SaveCommand(QuickCommand{ID: command.ID, Name: "resurrect", Body: "id", Color: "blue"}); err == nil {
		t.Fatal("stale edit recreated deleted command")
	}
}

func TestManagedKeys(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	key, err := s.SaveKey(KeyInput{Name: "generated", Generate: true, Passphrase: "key-password"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.KeyData(key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ssh.ParsePrivateKey(data); err == nil {
		t.Fatal("generated private key not encrypted")
	}
	signer, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte("key-password"))
	if err != nil {
		t.Fatal(err)
	}
	if !key.Encrypted || ssh.FingerprintSHA256(signer.PublicKey()) != key.Fingerprint {
		t.Fatal("incorrect public key metadata")
	}
	original := filepath.Join(t.TempDir(), "original-key")
	if err = os.WriteFile(original, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveKey(KeyInput{Name: "wrong", SourcePath: original, Passphrase: "incorrect"}); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	imported, err := s.SaveKey(KeyInput{Name: "imported", SourcePath: original, Passphrase: "key-password"})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Fingerprint != key.Fingerprint {
		t.Fatal("import changed key")
	}
	p, err := s.Save(Profile{Name: "key server", Host: "localhost", User: "root", Auth: "key", KeyID: imported.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteKey(imported.ID); err == nil {
		t.Fatal("deleted a key referenced by a server")
	}
	if _, err = s.SaveKey(KeyInput{ID: imported.ID, Name: "renamed"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.List().Keys; len(got) != 2 || got[1].Name != "renamed" {
		t.Fatal("key metadata did not persist")
	}
	listed, _ := json.Marshal(reopened.List())
	if bytes.Contains(listed, []byte("PRIVATE KEY")) || bytes.Contains(listed, []byte("key-password")) {
		t.Fatal("private material exposed in listing")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(s.dir, "keys", imported.ID))
		if info.Mode().Perm() != 0600 {
			t.Fatal("private key permissions")
		}
	}
	if err = reopened.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if err = reopened.DeleteKey(imported.ID); err == nil {
		t.Fatal("deleted a key still referenced by the trash")
	}
	if err = reopened.Purge(p.ID); err != nil {
		t.Fatal(err)
	}
	if err = reopened.DeleteKey(imported.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.dir, "keys", imported.ID)); !os.IsNotExist(err) {
		t.Fatal("managed copy left behind")
	}
	originalData, err := os.ReadFile(original)
	if err != nil || !bytes.Equal(originalData, data) {
		t.Fatal("original key was modified")
	}
}
