package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
)

func TestDirectoryNavigatorListsRealFoldersAndDirectorySymlinks(t *testing.T) {
	requirePOSIXFilesystemFixture(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "实际目录"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"linked-dir": "实际目录", "linked-file": "notes.txt", "broken-link": "missing"} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlink test unavailable: %v", err)
		}
	}
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	server, err := sftp.NewServer(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go server.Serve()
	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	a := &App{sessions: map[string]*Session{"tree-test": {ID: "tree-test", Home: root, ctx: context.Background(), files: client}}}
	list := func(query string) map[string]Entry {
		t.Helper()
		request := httptest.NewRequest("GET", "/api/sessions/tree-test/files?path="+url.QueryEscape(filepath.ToSlash(root))+query, nil)
		request.SetPathValue("id", "tree-test")
		response := httptest.NewRecorder()
		a.listFiles(response, request)
		if response.Code != 200 {
			t.Fatalf("list status %d: %s", response.Code, response.Body.String())
		}
		var result struct {
			Entries []Entry `json:"entries"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		entries := map[string]Entry{}
		for _, entry := range result.Entries {
			entries[entry.Name] = entry
		}
		return entries
	}
	directories := list("&directories=1")
	if len(directories) != 2 || directories["实际目录"].Kind != "folder" || directories["linked-dir"].Kind != "folder" || !directories["linked-dir"].Link {
		t.Fatalf("unexpected directory navigator entries: %#v", directories)
	}
	all := list("")
	if len(all) != 5 || all["notes.txt"].Kind != "file" || !all["linked-file"].Link || !all["broken-link"].Link {
		t.Fatalf("normal file listing changed: %#v", all)
	}
}
