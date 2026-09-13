package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"github.com/pkg/sftp"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestTextEncodingsRoundTripWithoutNewlineOrBOMChanges(t *testing.T) {
	text := "中文 繁體 ABC\t123\r\nsecond\nthird\r  trailing  \r\n"
	for _, name := range []string{"utf-8", "utf-8-bom", "utf-16le", "utf-16le-bom", "utf-16be-bom", "gb18030", "gbk", "big5"} {
		t.Run(name, func(t *testing.T) {
			encoded, e := encodeText(text, name)
			if e != nil {
				t.Fatal(e)
			}
			actual, actualName, e := decodeText(encoded, name)
			if e != nil || actual != text || actualName != name {
				t.Fatal("roundtrip", e, actual)
			}
			again, e := encodeText(actual, actualName)
			if e != nil || !bytes.Equal(encoded, again) {
				t.Fatal("changed bytes")
			}
		})
	}
	if _, _, e := decodeText([]byte{0xff, 0x81}, "auto"); e == nil {
		t.Fatal("guessed ambiguous encoding")
	}
	if _, _, e := decodeText([]byte{'a', 0, 'b'}, "auto"); e == nil {
		t.Fatal("accepted binary")
	}
	if _, e := encodeText("emoji 🚀", "gbk"); e == nil {
		t.Fatal("silently lost characters")
	}
}
func fileToolFixture(t *testing.T) (*App, *Session, string) {
	t.Helper()
	root := t.TempDir()
	cc, sc := net.Pipe()
	server, e := sftp.NewServer(sc)
	if e != nil {
		t.Fatal(e)
	}
	go server.Serve()
	client, e := sftp.NewClientPipe(cc, cc)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { client.Close(); server.Close(); sc.Close() })
	s := &Session{ID: "file-tools", Home: root, ctx: context.Background(), files: client}
	return &App{sessions: map[string]*Session{s.ID: s}}, s, root
}
func TestRemoteTextSaveConflictAndExactBytes(t *testing.T) {
	a, s, root := fileToolFixture(t)
	target := filepath.Join(root, "中文.txt")
	original, _ := encodeText("甲\r\n乙\n", "utf-16le-bom")
	os.WriteFile(target, original, 0640)
	get := httptest.NewRequest("GET", "/file-content?path="+url.QueryEscape(target), nil)
	get.SetPathValue("id", s.ID)
	out := httptest.NewRecorder()
	a.readTextHTTP(out, get)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	var doc textDocument
	json.Unmarshal(out.Body.Bytes(), &doc)
	doc.Text += " 粘贴\r\n\t保留  "
	save := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(doc)
		r := httptest.NewRequest("POST", "/file-content", bytes.NewReader(body))
		r.SetPathValue("id", s.ID)
		w := httptest.NewRecorder()
		a.saveTextHTTP(w, r)
		return w
	}
	if out = save(); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	actual, _ := os.ReadFile(target)
	expected, _ := encodeText(doc.Text, doc.Encoding)
	if !bytes.Equal(actual, expected) {
		t.Fatal("save changed bytes")
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0640 {
		t.Fatal("permissions changed")
	}
	if out = save(); out.Code != 409 {
		t.Fatalf("lost update not prevented: %d %s", out.Code, out.Body.String())
	}
}
func TestArchiveAndRecursiveDeleteDoNotFollowSymlinks(t *testing.T) {
	_, s, root := fileToolFixture(t)
	folder := filepath.Join(root, "package")
	os.Mkdir(folder, 0700)
	os.WriteFile(filepath.Join(folder, "中文.txt"), []byte("original\r\n"), 0600)
	outside := filepath.Join(root, "keep.txt")
	os.WriteFile(outside, []byte("keep"), 0600)
	if e := os.Symlink(outside, filepath.Join(folder, "link")); e != nil {
		t.Skip(e)
	}
	var archive bytes.Buffer
	if e := archiveRemote(context.Background(), s.files, folder, &archive); e != nil {
		t.Fatal(e)
	}
	gz, e := gzip.NewReader(&archive)
	if e != nil {
		t.Fatal(e)
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if h.Name == "package/link" {
			found = h.Typeflag == tar.TypeSymlink && h.Linkname == outside
		}
		if h.Name == "package/中文.txt" {
			b, _ := io.ReadAll(tr)
			if string(b) != "original\r\n" {
				t.Fatal("archive data changed")
			}
		}
	}
	if !found {
		t.Fatal("symlink not preserved")
	}
	if e = removeRemoteTree(context.Background(), s.files, folder, 0); e != nil {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(outside); e != nil || string(b) != "keep" {
		t.Fatal("followed delete symlink")
	}
	if e = removeRemoteTree(context.Background(), s.files, "/", 0); e == nil {
		t.Fatal("allowed root deletion")
	}
}
