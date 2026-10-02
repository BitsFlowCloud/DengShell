package app

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
)

type readTypeInfo struct {
	os.FileInfo
	mode os.FileMode
}

func (f readTypeInfo) Mode() os.FileMode { return f.mode }
func (f readTypeInfo) IsDir() bool       { return f.mode.IsDir() }

type readTypeFixture struct {
	info   os.FileInfo
	opens  atomic.Int32
	opened os.FileMode
}

func (f *readTypeFixture) Fileread(*sftp.Request) (io.ReaderAt, error) {
	f.opens.Add(1)
	return bytes.NewReader([]byte("unexpected data")), nil
}

func (f *readTypeFixture) Filelist(*sftp.Request) (sftp.ListerAt, error) {
	info := f.info
	if f.opens.Load() != 0 && f.opened != 0 {
		info = readTypeInfo{info, f.opened}
	}
	return readTypeList{info}, nil
}

type readTypeList []os.FileInfo

func (l readTypeList) ListAt(entries []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(entries, l[offset:])
	return n, io.EOF
}

func nonRegularReadFixture(t *testing.T, mode, opened os.FileMode) (*App, *Session, *readTypeFixture) {
	t.Helper()
	local := filepath.Join(t.TempDir(), "metadata")
	if err := os.WriteFile(local, []byte("unexpected data"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(local)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &readTypeFixture{info: readTypeInfo{info, mode}, opened: opened}
	cc, sc := net.Pipe()
	server := sftp.NewRequestServer(sc, sftp.Handlers{FileGet: fixture, FileList: fixture})
	go server.Serve()
	client, err := sftp.NewClientPipe(cc, cc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close(); sc.Close() })
	s := &Session{ID: "read-types", Home: "/", ctx: context.Background(), files: client}
	return &App{sessions: map[string]*Session{s.ID: s}}, s, fixture
}

func TestRemoteReadsRejectSpecialFilesBeforeOpening(t *testing.T) {
	for _, mode := range []os.FileMode{os.ModeNamedPipe | 0600, os.ModeDevice | os.ModeCharDevice | 0600, os.ModeDir | 0700} {
		for _, operation := range []string{"download", "native", "text"} {
			t.Run(mode.String()+"/"+operation, func(t *testing.T) {
				a, s, fixture := nonRegularReadFixture(t, mode, 0)
				if operation == "native" {
					local := filepath.Join(t.TempDir(), "existing.txt")
					if err := os.WriteFile(local, []byte("keep"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := a.DownloadTo(s.ID, "/selected", local); err == nil {
						t.Error("non-regular download was reported as successful")
					}
					if data, err := os.ReadFile(local); err != nil || string(data) != "keep" {
						t.Errorf("failed download replaced local file: %q %v", data, err)
					}
				} else {
					r := httptest.NewRequest("GET", "/?path=/selected", nil)
					r.SetPathValue("id", s.ID)
					w := httptest.NewRecorder()
					if operation == "text" {
						a.readTextHTTP(w, r)
					} else {
						a.download(w, r)
					}
					if w.Code != 400 {
						t.Errorf("special file returned %d: %s", w.Code, w.Body.String())
					}
				}
				if fixture.opens.Load() != 0 {
					t.Error("issued OPEN for a non-regular file; a FIFO can block before FSTAT")
				}
			})
		}
	}
}

func TestDownloadRechecksOpenedFileType(t *testing.T) {
	a, s, _ := nonRegularReadFixture(t, 0600, os.ModeNamedPipe|0600)
	local := filepath.Join(t.TempDir(), "output")
	if err := a.DownloadTo(s.ID, "/selected", local); err == nil {
		t.Fatal("accepted file whose type changed after preflight")
	}
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Fatalf("created local output for special file: %v", err)
	}
}

func TestDownloadToPreservesRegularFileAndSymlinkBytes(t *testing.T) {
	a, s, root := fileToolFixture(t)
	remote := filepath.Join(root, "regular")
	data := []byte("download\x00中文\r\n")
	if err := os.WriteFile(remote, data, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(remote, link); err != nil {
		t.Skip(err)
	}
	for _, selected := range []string{remote, link} {
		local := filepath.Join(t.TempDir(), "download")
		if err := a.DownloadTo(s.ID, selected, local); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(local); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("download changed bytes: %q %v", got, err)
		}
	}
}
