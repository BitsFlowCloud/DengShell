package app

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCommandGroupsRetainEmptyGroupsAndCommandMoves(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"  空分组 🧰  ", "部署"} {
		if _, err = s.SaveCommandGroup(name); err != nil {
			t.Fatal(err)
		}
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List().CommandGroups; !slices.Equal(got, []string{"空分组 🧰", "部署"}) || len(s.List().Commands) != 0 {
		t.Fatalf("empty groups not retained: %#v", got)
	}
	cmd, err := s.SaveCommand(QuickCommand{Name: "显示目录", Group: "空分组 🧰", Body: "pwd", Color: "blue"})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Group = "部署"
	if _, err = s.SaveCommand(cmd); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteCommand(cmd.ID); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.List().CommandGroups, []string{"空分组 🧰", "部署"}) || len(s.List().Commands) != 0 {
		t.Fatal("moving/deleting the last command deleted its group")
	}
	// The public view must not expose a mutable reference to the saved collection.
	public := s.List()
	public.CommandGroups[0] = "mutated"
	if s.List().CommandGroups[0] != "空分组 🧰" {
		t.Fatal("public group list aliases store memory")
	}
}

func TestCommandGroupsMigrateEncryptedLegacyCommandsOnce(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.config.CommandGroups = nil
	s.config.Commands = []QuickCommand{{ID: "one", Name: "A", Group: " 运维 ", Body: "pwd", Color: "blue"}, {ID: "two", Name: "B", Group: "运维", Body: "id", Color: "teal"}, {ID: "three", Name: "C", Body: "ls", Color: "rose"}}
	err = s.writeLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List(); !slices.Equal(got.CommandGroups, []string{"运维", "常用命令"}) || got.Commands[0].Group != "运维" || got.Commands[2].Group != "常用命令" {
		t.Fatalf("legacy migration incorrect: %+v", got)
	}
	path := filepath.Join(dir, EncryptedConfigName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptConfig(s.key, data)
	if err != nil {
		t.Fatal(err)
	}
	var disk Config
	if err = json.Unmarshal(plain, &disk); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(disk.CommandGroups, s.List().CommandGroups) {
		t.Fatal("migration was not written to encrypted config")
	}
	if _, err = OpenStore(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, data) {
		t.Fatal("opening an already migrated config rewrote it")
	}
}

func TestCommandGroupValidationAndWriteRollback(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "   ", "line\nbreak", "tab\there", strings.Repeat("组", 101)} {
		if _, err = s.SaveCommandGroup(name); err == nil {
			t.Fatalf("invalid group accepted: %q", name)
		}
	}
	name := strings.Repeat("组", 100)
	if _, err = s.SaveCommandGroup(name); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveCommandGroup(" " + name + " "); err == nil {
		t.Fatal("duplicate accepted")
	}
	if len(s.List().CommandGroups) != 1 {
		t.Fatal("invalid/duplicate group mutated collection")
	}
	// A changed unlock key simulates an actual persistent-save failure.
	if err = os.WriteFile(filepath.Join(s.dir, ConfigKeyName), bytes.Repeat([]byte{1}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveCommandGroup("不应出现"); err == nil {
		t.Fatal("write failure was ignored")
	}
	if _, err = s.SaveCommand(QuickCommand{Name: "不应保存", Group: "新组", Body: "pwd", Color: "blue"}); err == nil {
		t.Fatal("command write failure was ignored")
	}
	if len(s.List().Commands) != 0 || !slices.Equal(s.List().CommandGroups, []string{name}) {
		t.Fatal("failed save leaked group or command into live config")
	}
}

func TestCommandGroupsHTTPContract(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	handler := a.Handler(fstest.MapFS{"index.html": {Data: []byte("fixture")}})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://wails.localhost"+path, strings.NewReader(body))
		req.Header.Set("X-CloudShell-Token", a.Token())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	created := request("POST", "/api/command-groups", `{"name":" 空组 "}`)
	if created.Code != 200 || !strings.Contains(created.Body.String(), `"name":"空组"`) {
		t.Fatalf("create group: %d %s", created.Code, created.Body)
	}
	for _, bad := range []string{`{"name":"空组"}`, `{"name":""}`} {
		if response := request("POST", "/api/command-groups", bad); response.Code == 200 {
			t.Fatal("invalid request accepted")
		}
	}
	response := request("GET", "/api/config", "")
	var config Config
	if err = json.Unmarshal(response.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(config.CommandGroups, []string{"空组"}) || len(config.Commands) != 0 {
		t.Fatalf("empty group missing from API: %s", response.Body)
	}
}
