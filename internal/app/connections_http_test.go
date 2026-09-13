package app

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
)

func TestConnectionManagementHTTPContract(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	handler := a.Handler(fstest.MapFS{"index.html": {Data: []byte("fixture")}})
	request := func(method, path string, input any) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(input)
		req := httptest.NewRequest(method, "http://wails.localhost"+path, bytes.NewReader(body))
		req.Header.Set("X-CloudShell-Token", a.Token())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body)
		}
		return response
	}
	response := request("GET", "/api/config/storage", nil)
	var storage struct {
		ConfigPath    string `json:"configPath"`
		ConfigDir     string `json:"configDir"`
		SchemaVersion int    `json:"schemaVersion"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &storage); err != nil {
		t.Fatal(err)
	}
	if storage.ConfigDir != a.store.dir || storage.ConfigPath != filepath.Join(a.store.dir, EncryptedConfigName) || storage.SchemaVersion != 2 {
		t.Fatalf("incorrect storage metadata: %+v", storage)
	}
	response = request("POST", "/api/group-nodes", ServerGroup{Name: "HTTP parent", Emoji: "🛰️"})
	var group ServerGroup
	if err := json.Unmarshal(response.Body.Bytes(), &group); err != nil {
		t.Fatal(err)
	}
	response = request("POST", "/api/profiles", Profile{Name: "HTTP server", Host: "localhost", User: "root", GroupID: group.ID, Secret: "kept-private"})
	var profile Profile
	if err := json.Unmarshal(response.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.GroupID != group.ID || profile.Secret != "" || !profile.HasSecret {
		t.Fatal("save contract lost grouping/secret metadata")
	}
	request("DELETE", "/api/profiles/"+profile.ID, nil)
	response = request("GET", "/api/config", nil)
	var config Config
	if err := json.Unmarshal(response.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Servers) != 0 || len(config.Trash) != 1 || config.Trash[0].DeletedAt == nil || bytes.Contains(response.Body.Bytes(), []byte("kept-private")) {
		t.Fatal("soft-delete API did not redact or partition profiles")
	}
	response = request("POST", "/api/trash/"+profile.ID+"/restore", nil)
	if bytes.Contains(response.Body.Bytes(), []byte("kept-private")) {
		t.Fatal("restore HTTP response disclosed password")
	}
	request("DELETE", "/api/profiles/"+profile.ID, nil)
	request("DELETE", "/api/trash/"+profile.ID, nil)
	request("DELETE", "/api/group-nodes/"+group.ID, nil)
	appearance := a.store.List().Appearance
	appearance.StartupAnimation = false
	if _, err := a.store.SaveAppearance(appearance); err != nil {
		t.Fatal(err)
	}
	response = request("GET", "/boot.js", nil)
	if !bytes.Contains(response.Body.Bytes(), []byte(`"startupAnimation":false`)) || bytes.Contains(response.Body.Bytes(), []byte("kept-private")) {
		t.Fatal("startup bootstrap does not reflect persisted opt-out")
	}
}
func TestConfigMigrationRefusesToProceedWithoutBackup(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires nonroot POSIX directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := []byte(`{"servers":[],"groups":["保留"]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	if _, err := OpenStore(dir); err == nil {
		t.Fatal("migration continued without writing required backup")
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatal("migration modified original after backup failure", err)
	}
}
