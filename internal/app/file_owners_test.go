package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type ownerTestInfo struct {
	os.FileInfo
	uid, gid uint32
}

func (f ownerTestInfo) Sys() any { return &sftp.FileStat{UID: f.uid, GID: f.gid} }

func ownerSSHFixture(t *testing.T, output func(string) string) (*Session, *atomic.Int32) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var calls atomic.Int32
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		server, channels, requests, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			conn.Close()
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for incoming := range channels {
			channel, requests, err := incoming.Accept()
			if err != nil {
				continue
			}
			for request := range requests {
				if request.Type != "exec" {
					request.Reply(false, nil)
					continue
				}
				var command struct{ Value string }
				if ssh.Unmarshal(request.Payload, &command) != nil {
					request.Reply(false, nil)
					break
				}
				calls.Add(1)
				request.Reply(true, nil)
				channel.Write([]byte(output(command.Value)))
				channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				break
			}
			channel.Close()
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second})
	if err != nil {
		cancel()
		listener.Close()
		t.Fatal(err)
	}
	s := &Session{client: client, ctx: ctx, cancel: cancel}
	t.Cleanup(func() {
		s.forceClose()
		listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("SSH fixture did not stop")
		}
	})
	return s, &calls
}

func TestFileOwnersRemoteNamesCacheIsolationAndExpiry(t *testing.T) {
	var revision atomic.Int32
	s, calls := ownerSSHFixture(t, func(command string) string {
		if !strings.Contains(command, "getent passwd 0 9001 4294967295 2>/dev/null") || !strings.Contains(command, "getent group 0 9002 2>/dev/null") {
			t.Errorf("unexpected batched lookup: %s", command)
		}
		return fmt.Sprintf("u:0:remote-root-%d\ng:0:remote-wheel\nu:9001:alice\ng:9002:developers\nu:17:unrequested\nu:4294967295:bad\x1bname\n", revision.Load())
	})
	infos := []os.FileInfo{ownerTestInfo{uid: 0, gid: 0}, ownerTestInfo{uid: 9001, gid: 9002}, ownerTestInfo{uid: ^uint32(0), gid: 0}}
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			names := s.resolveFileOwners(context.Background(), infos)
			if names.owner(infos[0]) != "remote-root-0 / remote-wheel" || names.owner(infos[1]) != "alice / developers" || names.owner(infos[2]) != "未知用户 / remote-wheel" {
				t.Errorf("bad names: %#v", names)
			}
		}()
	}
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent lookup was not shared: %d", calls.Load())
	}
	old := s.resolveFileOwners(context.Background(), infos)
	revision.Store(1)
	s.fileOwners.mu.Lock()
	s.fileOwners.expires = time.Time{}
	s.fileOwners.mu.Unlock()
	if got := s.resolveFileOwners(context.Background(), infos).owner(infos[0]); got != "remote-root-1 / remote-wheel" {
		t.Fatal(got)
	}
	if old.owner(infos[0]) != "remote-root-0 / remote-wheel" {
		t.Fatal("snapshot mutated")
	}
	other := &Session{}
	if got := other.resolveFileOwners(context.Background(), infos).owner(infos[0]); got != "未知用户 / 未知用户组" {
		t.Fatal("names leaked between servers", got)
	}
}

func TestFileOwnersWaitHonorsCancellation(t *testing.T) {
	s := &Session{}
	s.fileOwners.loading = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { s.resolveFileOwners(ctx, []os.FileInfo{ownerTestInfo{uid: 0, gid: 0}}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled waiter blocked")
	}
}

func TestFileListAndPermissionsUseRemoteNamesAndExactTime(t *testing.T) {
	a, s, root := fileToolFixture(t)
	target := filepath.Join(root, "fixture.txt")
	if err := os.WriteFile(target, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000059, 0)
	if err := os.Chtimes(target, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	info, err := s.files.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*sftp.FileStat)
	remote, _ := ownerSSHFixture(t, func(command string) string {
		return fmt.Sprintf("u:%d:remote-alice\ng:%d:remote-staff\n", st.UID, st.GID)
	})
	s.client = remote.client
	request := httptest.NewRequest("GET", "/files?path="+url.QueryEscape(root), nil)
	request.SetPathValue("id", s.ID)
	w := httptest.NewRecorder()
	a.listFiles(w, request)
	var listing struct {
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(listing.Entries) != 1 || listing.Entries[0].Owner != "remote-alice / remote-staff" || listing.Entries[0].ModifiedAt != stamp.UnixMilli() {
		t.Fatal(w.Body.String())
	}
	request = httptest.NewRequest("GET", "/permissions?path="+url.QueryEscape(target), nil)
	request.SetPathValue("id", s.ID)
	w = httptest.NewRecorder()
	a.permissionsHTTP(w, request)
	var permissions map[string]string
	json.Unmarshal(w.Body.Bytes(), &permissions)
	if w.Code != 200 || permissions["owner"] != "remote-alice / remote-staff" {
		t.Fatal(w.Body.String())
	}
}

func TestFileOwnersSFTPDatabaseFallback(t *testing.T) {
	_, s, _ := fileToolFixture(t)
	// The in-process SFTP fixture serves this test host's filesystem. Read-only
	// /etc/passwd here represents a remote database; no user settings are used.
	f, err := s.files.Open("/etc/passwd")
	if err != nil {
		t.Skip("fixture has no passwd database")
	}
	f.Close()
	names := s.resolveFileOwners(context.Background(), []os.FileInfo{ownerTestInfo{uid: 0, gid: 0}})
	if names.users[0] != "root" || names.groups[0] == "" {
		t.Fatalf("SFTP-only fallback failed: %#v", names)
	}
}
