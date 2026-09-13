package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteTextEditingResolvesFileAndParentLinksWithoutReplacingLinks(t *testing.T) {
	a, s, root := fileToolFixture(t)
	actualDir := filepath.Join(root, "actual")
	if err := os.Mkdir(actualDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(actualDir, "中文.txt")
	fileLink := filepath.Join(root, "file-link")
	parentLink := filepath.Join(root, "parent-link")
	if err := os.Symlink("actual/中文.txt", fileLink); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(actualDir, parentLink); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{fileLink, filepath.Join(parentLink, "中文.txt")} {
		t.Run(filepath.Base(filepath.Dir(source))+"-"+filepath.Base(source), func(t *testing.T) {
			original, _ := encodeText("原始\r\n第二行\n", "utf-16le-bom")
			if err := os.WriteFile(target, original, 0640); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("GET", "/file-content?path="+url.QueryEscape(source), nil)
			request.SetPathValue("id", s.ID)
			response := httptest.NewRecorder()
			a.readTextHTTP(response, request)
			if response.Code != 200 {
				t.Fatal(response.Body.String())
			}
			var doc textDocument
			if err := json.Unmarshal(response.Body.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Path != target {
				t.Fatal("GET did not return resolved target", doc.Path)
			}
			doc.Path = source // Exercise POST resolution independently of GET.
			doc.Text += "粘贴\t  保留\r\n"
			body, _ := json.Marshal(doc)
			request = httptest.NewRequest("POST", "/file-content", bytes.NewReader(body))
			request.SetPathValue("id", s.ID)
			response = httptest.NewRecorder()
			a.saveTextHTTP(response, request)
			if response.Code != 200 {
				t.Fatal(response.Code, response.Body.String())
			}
			wanted, _ := encodeText(doc.Text, doc.Encoding)
			got, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(got, wanted) {
				t.Fatal("resolved target was not written exactly", err)
			}
			for link, want := range map[string]string{fileLink: "actual/中文.txt", parentLink: actualDir} {
				got, err := os.Readlink(link)
				if err != nil || got != want {
					t.Fatal("symlink was replaced or modified", link, err)
				}
			}
		})
	}
}

func TestRemoteSymlinkResolutionRelativeTargetsAndLoopLimit(t *testing.T) {
	_, s, root := fileToolFixture(t)
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "real")
	if err := os.WriteFile(target, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	relative := filepath.Join(root, "nested", "relative")
	if err := os.Symlink("../real", relative); err != nil {
		t.Skip(err)
	}
	if got, err := resolveRemoteSymlinks(context.Background(), s.files, relative); err != nil || got != target {
		t.Fatal("relative parent target", got, err)
	}
	next := "real"
	for i := 0; i < 41; i++ {
		name := fmt.Sprintf("chain-%02d", i)
		if err := os.Symlink(next, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
		next = name
	}
	if got, err := resolveRemoteSymlinks(context.Background(), s.files, filepath.Join(root, "chain-39")); err != nil || got != target {
		t.Fatal("valid 40-link chain rejected", got, err)
	}
	if _, err := resolveRemoteSymlinks(context.Background(), s.files, filepath.Join(root, "chain-40")); err == nil || !strings.Contains(err.Error(), "40") {
		t.Fatal("unbounded link chain", err)
	}
	if err := os.Symlink("cycle", filepath.Join(root, "cycle")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRemoteSymlinks(context.Background(), s.files, filepath.Join(root, "cycle")); err == nil || !strings.Contains(err.Error(), "40") {
		t.Fatal("unbounded cycle", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolveRemoteSymlinks(ctx, s.files, target); err != context.Canceled {
		t.Fatal("ignored cancellation", err)
	}
}
