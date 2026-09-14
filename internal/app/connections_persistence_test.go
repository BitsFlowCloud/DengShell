package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestConfigSchemaMigrationPreservesCredentialsAndLiteralGroups(t *testing.T) {
	dir := t.TempDir()
	original := []byte(`{
 "servers":[{"id":"legacy","name":"遗留服务器","host":"127.0.0.1","port":22,"user":"root","group":"prod/api","auth":"password","secret":"ssh-secret","proxy":{"type":"socks5","host":"127.0.0.1","port":1080,"user":"test","password":"inline-proxy-secret"}}],
 "groups":["prod/api","备份"],
 "hostKeys":{"host:22":"SHA256:kept"},
 "commands":[{"id":"command","name":"命令","command":"printf kept","group":"default","color":"blue"}],
 "keys":[{"id":"managed-key","name":"保留密钥","publicKey":"public-only"}],
 "proxies":[{"id":"proxy","name":"保留代理","proxy":{"type":"http","host":"localhost","port":3128,"password":"proxy-secret"}}],
 "assets":[{"id":"asset","kind":"font","name":"字体","size":100}],
 "appearance":{"fontId":"builtin:jetbrains-mono","backgroundId":"builtin:moonlake","backgroundOpacity":0,"uiScale":1.25,"terminalFontSize":16,"startupAnimation":false},
 "userNotes":{"untouched":"metadata"}
}`)
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "keys"), 0700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "keys", "managed-key")
	if err := os.WriteFile(keyPath, []byte("private material stays in its file"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.config.SchemaVersion != ConfigSchemaVersion || len(s.config.GroupNodes) != 2 || s.config.GroupNodes[0].Name != "prod/api" || s.config.GroupNodes[0].ParentID != "" {
		t.Fatalf("bad migration: %+v", s.config.GroupNodes)
	}
	profile, err := s.Get("legacy")
	if err != nil || profile.Secret != "ssh-secret" || profile.Proxy.Password != "inline-proxy-secret" || profile.GroupID != s.config.GroupNodes[0].ID {
		t.Fatalf("lost credentials/group: %+v %v", profile, err)
	}
	if len(s.config.Commands) != 1 || len(s.config.Keys) != 1 || len(s.config.Proxies) != 1 || len(s.config.Assets) != 1 || s.config.HostKeys["host:22"] != "SHA256:kept" {
		t.Fatal("lost ancillary settings")
	}
	if s.config.Appearance.BackgroundOpacity != 0 || s.config.Appearance.UIScale != 1.25 || s.config.Appearance.StartupAnimation {
		t.Fatal("explicit appearance changed")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "config.pre-v2.*.enc"))
	if len(files) != 1 {
		t.Fatalf("migration backup count: %v", files)
	}
	backup, _ := os.ReadFile(files[0])
	backupPlain, err := decryptConfig(s.key, backup)
	if err != nil || !bytes.Equal(backupPlain, original) {
		t.Fatal("encrypted backup did not preserve byte-exact original", err)
	}
	if bytes.Contains(backup, []byte("ssh-secret")) || bytes.Contains(backup, []byte("proxy-secret")) {
		t.Fatal("migration backup exposed plaintext credentials")
	}
	info, _ := os.Stat(files[0])
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("backup exposed credentials")
	}
	raw := readPlainConfigForTest(t, s)
	if !bytes.Contains(raw, []byte(`"userNotes"`)) || !bytes.Contains(raw, []byte("proxy-secret")) {
		t.Fatal("unknown metadata or credential lost in upgraded file")
	}
	public, _ := json.Marshal(s.List())
	for _, secret := range []string{"ssh-secret", "proxy-secret", "inline-proxy-secret", "private material", "userNotes"} {
		if bytes.Contains(public, []byte(secret)) {
			t.Fatal("ordinary API exposed private config", secret)
		}
	}
	beforeIDs := s.List().GroupNodes
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeIDs, reopened.List().GroupNodes) {
		t.Fatal("group IDs changed on reopening")
	}
	files, _ = filepath.Glob(filepath.Join(dir, "config.pre-v2.*.enc"))
	if len(files) != 1 {
		t.Fatal("migration repeated")
	}
	unchanged, _ := os.ReadFile(keyPath)
	if string(unchanged) != "private material stays in its file" {
		t.Fatal("migration changed key file")
	}
}
func TestConfigFutureVersionAndCorruptTreeLeaveOriginalUntouched(t *testing.T) {
	for _, input := range []string{`{"schemaVersion":999,"servers":[],"unknownSecret":"kept"}`, `{"schemaVersion":2,"groupNodes":[{"id":"a","name":"A","parentId":"b"},{"id":"b","name":"B","parentId":"a"}]}`, `{"schemaVersion":2,"groupNodes":[{"id":"a","name":"A","parentId":"missing"}]}`} {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenStore(dir); err == nil {
			t.Fatalf("invalid config accepted: %s", input)
		}
		current, _ := os.ReadFile(path)
		if string(current) != input {
			t.Fatal("invalid config overwritten")
		}
		backups, _ := filepath.Glob(filepath.Join(dir, "config.pre-v2.*.enc"))
		if len(backups) != 0 {
			t.Fatal("invalid document migrated")
		}
	}
}
func TestHierarchicalGroupsPreserveIdentityAndPreventCycles(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.SaveGroup(ServerGroup{Name: "生产", Emoji: "🇨🇳"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.SaveGroup(ServerGroup{Name: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.SaveGroup(ServerGroup{Name: "API", ParentID: a.ID, Emoji: "🧑🏽‍💻"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.SaveGroup(ServerGroup{Name: "API", ParentID: b.ID})
	if err != nil || other.ID == child.ID {
		t.Fatal("same name in distinct parents rejected")
	}
	if _, err := s.SaveGroup(ServerGroup{Name: "API", ParentID: a.ID}); err == nil {
		t.Fatal("duplicate siblings accepted")
	}
	p, err := s.Save(Profile{Name: "Nested", Host: "localhost", User: "root", GroupID: child.ID, Secret: "kept"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Group != "生产 / API" {
		t.Fatal(p.Group)
	}
	a.ParentID = child.ID
	if _, err := s.SaveGroup(a); err == nil {
		t.Fatal("parent moved into descendant")
	}
	a.ParentID = ""
	a.Name = "生产新名"
	if _, err := s.SaveGroup(a); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Get(p.ID)
	if p.Group != "生产新名 / API" || p.GroupID != child.ID || p.Secret != "kept" {
		t.Fatalf("rename broke profile: %+v", p)
	}
	if err := s.DeleteGroup(a.ID); err == nil {
		t.Fatal("parent with child deleted")
	}
	if err := s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	child.Name = "API归档"
	child.ParentID = b.ID
	if _, err := s.SaveGroup(child); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(child.ID); err == nil || !strings.Contains(err.Error(), "回收站") {
		t.Fatal("trashed group reference lost", err)
	}
	restored, err := s.Restore(p.ID)
	if err != nil || restored.GroupID != child.ID || restored.Group != "测试 / API归档" {
		t.Fatalf("restore group: %+v %v", restored, err)
	}
	if restored.Secret != "" || !restored.HasSecret {
		t.Fatal("restore response leaked/lost credential metadata")
	}
	p, _ = s.Get(p.ID)
	if p.Secret != "kept" {
		t.Fatal("restore lost secret")
	}
}
func TestGroupHierarchyHasNoFixedDepthLimit(t *testing.T) {
	groups := make([]ServerGroup, 1200)
	parent := ""
	for i := range groups {
		id := randomID()
		groups[i] = ServerGroup{ID: id, Name: "层", ParentID: parent}
		parent = id
	}
	if err := validateGroupGraph(groups); err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(legacyGroupPath(parent, groupIndex(groups)), "层"); count != len(groups) {
		t.Fatalf("deep path truncated: %d", count)
	}
	dir := t.TempDir()
	config := Config{SchemaVersion: 2, GroupNodes: groups, Servers: []Profile{{ID: "deep", Name: "deep", Host: "localhost", User: "root", Auth: "agent", GroupID: parent}}, Appearance: defaultAppearance()}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Get("deep")
	p.Name = "renamed at level1200"
	if _, err := s.Save(p, false); err != nil {
		t.Fatal("long display path rejected despite stable group ID", err)
	}
	groups[0].ParentID = groups[len(groups)-1].ID
	if err := validateGroupGraph(groups); err == nil {
		t.Fatal("deep cycle accepted")
	}
}
func TestTrashHistoryAndCredentialReferenceRetention(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.SaveKey(KeyInput{Name: "key", Generate: true})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := s.SaveProxy(ManagedProxy{Name: "proxy", Proxy: ProxyConfig{Type: "socks5", Host: "localhost", Port: 1080, Password: "proxy-password"}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "server", Host: "localhost", User: "root", Auth: "key", KeyID: key.ID, ProxyID: proxy.ID, Secret: "key-passphrase"}, false)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 9, 14, 1, 2, 3, 0, time.UTC)
	last := first.Add(time.Minute)
	for _, at := range []time.Time{first, last} {
		if err := s.MarkConnected(p.ID, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Purge(p.ID); err == nil {
		t.Fatal("purged an active server")
	}
	if err := s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(p.ID); err == nil {
		t.Fatal("trash can still connect")
	}
	if _, err := s.Save(p, false); err == nil {
		t.Fatal("editing silently restored trash")
	}
	if err := s.MarkConnected(p.ID, time.Now()); err == nil {
		t.Fatal("trash received connection success")
	}
	if err := s.DeleteKey(key.ID); err == nil {
		t.Fatal("trash key removed")
	}
	if err := s.DeleteProxy(proxy.ID); err == nil {
		t.Fatal("trash proxy removed")
	}
	s, err = OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list.Servers) != 0 || len(list.Trash) != 1 || len(list.ConnectionHistory) != 1 || list.ConnectionHistory[0].Count != 2 || !list.ConnectionHistory[0].LastConnectedAt.Equal(last) {
		t.Fatalf("lifecycle did not persist: %+v", list)
	}
	data, _ := json.Marshal(list)
	if bytes.Contains(data, []byte("key-passphrase")) || bytes.Contains(data, []byte("proxy-password")) {
		t.Fatal("trash/history exposed secrets")
	}
	listedTime := list.Trash[0].DeletedAt
	*listedTime = time.Time{}
	if s.List().Trash[0].DeletedAt.IsZero() {
		t.Fatal("public snapshot aliases private timestamp")
	}
	if _, err := s.Restore(p.ID); err != nil {
		t.Fatal(err)
	}
	private, _ := s.Get(p.ID)
	if private.Secret != "key-passphrase" || private.KeyID != key.ID || private.ProxyID != proxy.ID {
		t.Fatal("restore lost credentials/references")
	}
	if err := s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.List().Trash) != 0 || len(s.List().ConnectionHistory) != 0 {
		t.Fatal("permanent deletion retained profile/history")
	}
	if err := s.DeleteKey(key.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProxy(proxy.ID); err != nil {
		t.Fatal(err)
	}
}
func TestConnectionMutationsRollbackWhenConfigWriteFails(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "server", Host: "localhost", User: "root", Secret: "secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	group := s.List().GroupNodes[0]
	path := filepath.Join(s.dir, EncryptedConfigName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	assertRollback := func(name string, operation func() error) {
		t.Helper()
		before, _ := json.Marshal(s.config)
		if err := operation(); err == nil {
			t.Fatalf("%s write unexpectedly succeeded", name)
		}
		after, _ := json.Marshal(s.config)
		if !bytes.Equal(before, after) {
			t.Fatalf("%s mutated state despite write failure", name)
		}
	}
	assertRollback("trash", func() error { return s.Delete(p.ID) })
	assertRollback("history", func() error { return s.MarkConnected(p.ID, time.Now()) })
	assertRollback("group create", func() error { _, err := s.SaveGroup(ServerGroup{Name: "new"}); return err })
	assertRollback("group rename", func() error { group.Name = "renamed"; _, err := s.SaveGroup(group); return err })
	assertRollback("profile with new group", func() error {
		_, err := s.Save(Profile{Name: "new", Host: "localhost", User: "root", Group: "newgroup"}, false)
		return err
	})
	// Stage a trashed record in memory to exercise both restore and purge rollback.
	at := time.Now()
	s.config.Servers[0].DeletedAt = &at
	assertRollback("restore", func() error { _, err := s.Restore(p.ID); return err })
	assertRollback("purge", func() error { return s.Purge(p.ID) })
}
func TestFailedConnectionDoesNotEnterHistory(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := a.store.Save(Profile{Name: "failure", Host: "127.0.0.1", Port: 1, User: "test", Auth: "password"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Connect(context.Background(), p.ID, ""); err == nil {
		t.Fatal("missing credential connection unexpectedly succeeded")
	}
	if len(a.store.List().ConnectionHistory) != 0 {
		t.Fatal("failed attempt persisted as success")
	}
}

func TestConnectionHistoryAndTrashLocalSSHIntegration(t *testing.T) {
	key := os.Getenv("CLOUDSHELL_TEST_KEY")
	if key == "" {
		t.Skip("requires dedicated localhost:19225 sshd")
	}
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := a.store.Save(Profile{Name: "history fixture", Host: "127.0.0.1", Port: 19225, User: "bitsflow", Auth: "key", KeyPath: key}, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := connectLocalSSHFixture(t, a, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := connectLocalSSHFixture(t, a, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	list := a.store.List()
	if len(list.ConnectionHistory) != 1 || list.ConnectionHistory[0].Count != 2 || time.Since(list.ConnectionHistory[0].LastConnectedAt) > 5*time.Second {
		t.Fatalf("successes not recorded: %+v", list.ConnectionHistory)
	}
	if err := a.trashProfile(p.ID); err != nil {
		t.Fatal(err)
	}
	if first.ctx.Err() == nil || second.ctx.Err() == nil {
		t.Fatal("trash did not close active transports")
	}
	if _, err := a.session(first.ID); err == nil {
		t.Fatal("deleted session still registered")
	}
	if _, err := connectLocalSSHFixture(t, a, p.ID, ""); err == nil {
		t.Fatal("trashed profile reconnected")
	}
	if got := a.store.List().ConnectionHistory[0].Count; got != 2 {
		t.Fatal("failed trash reconnect entered history", got)
	}
	if _, err := a.store.Restore(p.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := connectLocalSSHFixture(t, a, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	a.disconnect(restored.ID)
	reopened, err := OpenStore(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.List().ConnectionHistory[0].Count; got != 3 {
		t.Fatal("successful history did not persist", got)
	}
	t.Log("Two successful SSH/SFTP connections recorded; soft deletion closed both; restore/reconnect persisted third success")
}
