package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func archiveEntryNames(t *testing.T, data []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	return names
}
func TestSelectionArchiveExactFilesAndAtomicFailure(t *testing.T) {
	a, s, root := fileToolFixture(t)
	first := filepath.Join(root, "a.txt")
	second := filepath.Join(root, "中文.txt")
	folder := filepath.Join(root, "folder")
	unselected := filepath.Join(root, "keep.txt")
	for _, p := range []string{first, second, unselected} {
		if err := os.WriteFile(p, []byte(p), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "nested.txt"), []byte("nested"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(unselected, link); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := archiveRemoteSelection(context.Background(), s.files, []string{first, folder, second, link, first}, &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"a.txt", "folder", "folder/nested.txt", "中文.txt", "link"}
	if got := archiveEntryNames(t, out.Bytes()); !reflect.DeepEqual(got, want) {
		t.Fatalf("archive entries %v != %v", got, want)
	}
	target := filepath.Join(t.TempDir(), "download.tar.gz")
	if err := a.DownloadSelectionArchiveTo(s.ID, []string{first, second}, target); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := archiveEntryNames(t, before); !reflect.DeepEqual(got, []string{"a.txt", "中文.txt"}) {
		t.Fatal(got)
	}
	if err := a.DownloadSelectionArchiveTo(s.ID, []string{first, filepath.Join(root, "missing")}, target); err == nil {
		t.Fatal("accepted incomplete archive")
	}
	after, _ := os.ReadFile(target)
	if !bytes.Equal(before, after) {
		t.Fatal("failure replaced a previous download")
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".dengshell-archive-*"))
	if len(leftovers) != 0 {
		t.Fatal(leftovers)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := archiveRemoteSelection(ctx, s.files, []string{first, second}, io.Discard); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestSelectionArchiveRejectsAmbiguousPaths(t *testing.T) {
	for _, paths := range [][]string{nil, {"/tmp/a", "/srv/a"}, {"/", "/tmp/a"}, {"relative"}, {"/a\x00b"}} {
		if _, err := archiveTargets(paths); err == nil {
			t.Fatalf("accepted %q", paths)
		}
	}
	if _, err := archiveTargets(make([]string, 10001)); err == nil {
		t.Fatal("unbounded selection")
	}
}
func TestSelectionArchiveHTTP(t *testing.T) {
	_, s, root := fileToolFixture(t)
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.sessions[s.ID] = s
	defer delete(a.sessions, s.ID)
	for _, name := range []string{"one", "two", "keep"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	query := url.Values{"path": {filepath.Join(root, "one"), filepath.Join(root, "two")}}
	req := httptest.NewRequest("GET", "/archive?"+query.Encode(), nil)
	req.SetPathValue("id", s.ID)
	response := httptest.NewRecorder()
	a.archiveHTTP(response, req)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if got := archiveEntryNames(t, response.Body.Bytes()); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatal(got)
	}
	if response.Header().Get("Content-Disposition") != "attachment; filename=DengShell-files.tar.gz" {
		t.Fatal(response.Header())
	}
}
