package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

func TestUploadPreservesMetadataAndKeepsNewFilesPrivate(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0700, 0640 | os.ModeSetgid} {
		t.Run(mode.String(), func(t *testing.T) {
			a, s, root, _ := uploadSFTPFixture(t, 0)
			target := filepath.Join(root, "target.txt")
			if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, mode); err != nil {
				t.Fatal(err)
			}
			before, err := s.files.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			ctx, task, err := a.beginTransfer(context.Background(), s, "", target, 3)
			if err != nil {
				t.Fatal(err)
			}
			defer task.cancel()
			if err = a.copyUpload(ctx, s, task, bytes.NewBufferString("new"), true); err != nil {
				t.Fatal(err)
			}
			after, err := s.files.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			oldStat, newStat := before.Sys().(*sftp.FileStat), after.Sys().(*sftp.FileStat)
			if after.Mode() != before.Mode() || oldStat.UID != newStat.UID || oldStat.GID != newStat.GID {
				t.Fatalf("metadata changed: before=%+v after=%+v", oldStat, newStat)
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != "new" {
				t.Fatalf("replacement bytes: %q %v", data, err)
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 1 {
				t.Fatalf("temporary directory leaked: %v", entries)
			}
		})
	}
	a, s, root, _ := uploadSFTPFixture(t, 0)
	target := filepath.Join(root, "new.txt")
	ctx, task, err := a.beginTransfer(context.Background(), s, "", target, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer task.cancel()
	if err = a.copyUpload(ctx, s, task, bytes.NewBufferString("new"), false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("new file not private: %v %v", info, err)
	}
}

func waitUploadPending(t *testing.T, link *uploadTestLink, count int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		link.mu.Lock()
		pending := link.pending
		link.mu.Unlock()
		if pending >= count {
			return
		}
		select {
		case <-deadline:
			t.Fatal("SFTP writes did not start")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestUploadContentsArePrivateBeforeAcknowledgement(t *testing.T) {
	a, s, root, link := uploadSFTPFixture(t, 0)
	gate := make(chan struct{})
	link.mu.Lock()
	link.ackGate = gate
	link.mu.Unlock()
	target := filepath.Join(root, "private.conf")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, task, err := a.beginTransfer(context.Background(), s, "", target, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer task.cancel()
	done := make(chan error, 1)
	go func() { done <- a.copyUpload(ctx, s, task, bytes.NewBufferString("new"), true) }()
	defer func() {
		close(gate)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	waitUploadPending(t, link, 1)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, entry := range entries {
		if entry.Name() == "private.conf" {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			t.Fatalf("staging directory is not private: %v %v", info, err)
		}
		temporary := filepath.Join(root, entry.Name(), "contents.part")
		info, err = os.Stat(temporary)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("staging file is not private: %v %v", info, err)
		}
		data, err := os.ReadFile(temporary)
		if err != nil || string(data) != "new" {
			t.Fatalf("pending content: %q %v", data, err)
		}
		seen = true
	}
	if !seen {
		t.Fatal("no staging directory")
	}
	if data, _ := os.ReadFile(target); string(data) != "old" {
		t.Fatal("unacknowledged upload replaced target")
	}
}

func TestCancelledStalledUploadsReleaseAllGlobalSlots(t *testing.T) {
	a, s, root, link := uploadSFTPFixture(t, 0)
	gate := make(chan struct{})
	defer close(gate)
	link.mu.Lock()
	link.ackGate = gate
	link.mu.Unlock()
	var cancels []context.CancelFunc
	finished := make(chan error, 4)
	for i := range 4 {
		ctx, task, err := a.beginTransfer(context.Background(), s, "", filepath.Join(root, string(rune('a'+i))), 1)
		if err != nil {
			t.Fatal(err)
		}
		cancels = append(cancels, task.cancel)
		go func() {
			release, err := a.acquireUploadSlot(ctx, task)
			if err == nil {
				err = a.copyUpload(ctx, s, task, bytes.NewBufferString("x"), false)
				release()
			}
			a.finishTransfer(task, err)
			finished <- err
		}()
	}
	waitUploadPending(t, link, 4)
	for _, cancel := range cancels {
		cancel()
	}
	for range 4 {
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cancelled upload still owns its worker slot")
		}
	}
	if len(uploadSlots) != 0 {
		t.Fatal("upload slots leaked")
	}
	// A different session can immediately use the shared capacity.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := a.acquireUploadSlot(ctx, &Transfer{})
	if err != nil {
		t.Fatal(err)
	}
	release()
}

type stalledFileLister struct {
	entered chan struct{}
	gate    chan struct{}
}

func (l *stalledFileLister) Filelist(_ *sftp.Request) (sftp.ListerAt, error) {
	l.entered <- struct{}{}
	<-l.gate
	return nil, os.ErrNotExist
}

func stalledSFTPSession(t *testing.T) (*Session, <-chan struct{}) {
	t.Helper()
	cc, sc := net.Pipe()
	lister := &stalledFileLister{make(chan struct{}, 1), make(chan struct{})}
	server := sftp.NewRequestServer(sc, sftp.Handlers{FileList: lister})
	go server.Serve()
	client, err := sftp.NewClientPipe(cc, cc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(lister.gate); cc.Close(); client.Close(); server.Close(); sc.Close() })
	return &Session{ID: "unresponsive-sftp", ctx: context.Background(), files: client, cleanupGrace: 250 * time.Millisecond}, lister.entered
}

func TestCancelledStalledSaveCannotBlockAnotherSession(t *testing.T) {
	a, good, root := fileToolFixture(t)
	target := filepath.Join(root, "unchanged.txt")
	if err := os.WriteFile(target, []byte("same"), 0600); err != nil {
		t.Fatal(err)
	}
	bad, entered := stalledSFTPSession(t)
	a.sessions[bad.ID] = bad
	body, _ := json.Marshal(textDocument{Path: "/blocked", Text: "new", Encoding: "utf-8", SHA256: "dummy"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("POST", "/", bytes.NewReader(body)).WithContext(ctx)
	request.SetPathValue("id", bad.ID)
	first := make(chan struct{})
	badResponse := httptest.NewRecorder()
	go func() { a.saveTextHTTP(badResponse, request); close(first) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Lstat did not start")
	}
	// Healthy save completes even while the faulty request has not been cancelled.
	body, _ = json.Marshal(textDocument{Path: target, Text: "same", Encoding: "utf-8", SHA256: digestText([]byte("same"))})
	request2 := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	request2.SetPathValue("id", good.ID)
	response := httptest.NewRecorder()
	second := make(chan struct{})
	go func() { a.saveTextHTTP(response, request2); close(second) }()
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("healthy session blocked behind another server")
	}
	if response.Code != 200 {
		t.Fatalf("healthy save: %d %s", response.Code, response.Body.String())
	}
	cancel()
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled Lstat did not release request")
	}
	if badResponse.Code == 200 || !bytes.Contains(badResponse.Body.Bytes(), []byte("sftp_interrupted")) {
		t.Fatalf("missing interruption response: %s", badResponse.Body.String())
	}
}

func TestSFTPIdleDeadlineInterruptsLstatWithoutPeerReply(t *testing.T) {
	s, entered := stalledSFTPSession(t)
	s.diagnosticLog = &sshDiagnosticLog{}
	op := startSFTPOperation(context.Background(), s, 25*time.Millisecond)
	defer op.close()
	done := make(chan error, 1)
	go func() { _, err := s.files.Lstat("/blocked"); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Lstat did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(op.err(err), context.DeadlineExceeded) || !op.forced.Load() {
			t.Fatalf("deadline did not force transport shutdown: %v", op.err(err))
		}
		s.diagnosticLog.mu.Lock()
		diagnostic := s.diagnosticLog.records[s.ID]
		s.diagnosticLog.mu.Unlock()
		if diagnostic.Code != "DS-240" {
			t.Fatalf("SFTP diagnostic: %+v", diagnostic)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle timeout did not interrupt SFTP")
	}
}

func TestRemoteWriteLockIsCancellableAndSerializesSameTarget(t *testing.T) {
	s := &Session{}
	release, err := lockRemoteWrite(context.Background(), s, "/same")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := lockRemoteWrite(ctx, s, "/same"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("same target was not serialized: %v", err)
	}
	other, err := lockRemoteWrite(context.Background(), s, "/different")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	remoteWrites.Lock()
	remaining := len(remoteWrites.entries)
	remoteWrites.Unlock()
	if remaining != 0 {
		t.Fatalf("unused write locks retained: %d", remaining)
	}
}

func TestConcurrentTextSaveAcrossSameServerSessionsDetectsConflict(t *testing.T) {
	a, first, root := fileToolFixture(t)
	first.fileWriteIdentity = "isolated-server:22\x00test-host-key"
	second := &Session{ID: "second-editor", ctx: first.ctx, files: first.files, fileWriteIdentity: first.fileWriteIdentity}
	a.sessions[second.ID] = second
	target := filepath.Join(root, "shared.txt")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	results := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for i, session := range []*Session{first, second} {
		body, _ := json.Marshal(textDocument{Path: target, Text: fmt.Sprintf("edit-%d", i), Encoding: "utf-8", SHA256: digestText([]byte("old"))})
		request := httptest.NewRequest("POST", "/", bytes.NewReader(body))
		request.SetPathValue("id", session.ID)
		go func() {
			<-start
			response := httptest.NewRecorder()
			a.saveTextHTTP(response, request)
			results <- response
		}()
	}
	close(start)
	var codes []int
	for range 2 {
		select {
		case response := <-results:
			codes = append(codes, response.Code)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent saves did not complete")
		}
	}
	if !((codes[0] == 200 && codes[1] == 409) || (codes[0] == 409 && codes[1] == 200)) {
		t.Fatalf("same-server concurrent write lost conflict protection: %v", codes)
	}
	data, err := os.ReadFile(target)
	if err != nil || (string(data) != "edit-0" && string(data) != "edit-1") {
		t.Fatalf("unexpected saved bytes: %q %v", data, err)
	}
}

func TestCancelledUploadInterruptsUnacknowledgedClose(t *testing.T) {
	a, s, root, link := uploadSFTPFixture(t, 0)
	gate := make(chan struct{})
	defer close(gate)
	link.mu.Lock()
	link.closeGate = gate
	link.mu.Unlock()
	target := filepath.Join(root, "original")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, task, err := a.beginTransfer(context.Background(), s, "", target, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer task.cancel()
	done := make(chan error, 1)
	go func() { done <- a.copyUpload(ctx, s, task, bytes.NewBufferString("new"), true) }()
	deadline := time.After(3 * time.Second)
	for {
		link.mu.Lock()
		pending := len(link.closes)
		link.mu.Unlock()
		if pending > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("CLOSE did not start")
		case <-time.After(time.Millisecond):
		}
	}
	task.cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CLOSE still requires a peer reply after cancellation")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Fatalf("cancelled CLOSE committed target: %q %v", data, err)
	}
}

func TestRealHTTPSlowUploadBodyCancellationReleasesSlotAndKeepsSSH(t *testing.T) {
	a, s, root, _ := uploadSFTPFixture(t, 0)
	target := filepath.Join(root, "preserve.txt")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", s.ID)
		a.uploadHTTP(w, r)
		close(finished)
	})
	mux.HandleFunc("POST /cancel", func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", "slow-body")
		a.cancelTransfer(w, r)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Send one full chunk, then leave Content-Length deliberately incomplete.
	_, err = fmt.Fprintf(conn, "POST /upload?path=%s&overwrite=1&task=slow-body HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n\r\n", url.QueryEscape(target), strings.TrimPrefix(server.URL, "http://"), uploadChunkSize*32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write(bytes.Repeat([]byte("x"), uploadChunkSize)); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		a.mu.Lock()
		task := a.transfers["slow-body"]
		progressed := task != nil && task.Done >= uploadChunkSize
		a.mu.Unlock()
		if progressed {
			break
		}
		select {
		case <-deadline:
			t.Fatal("first remote chunk was not acknowledged")
		case <-time.After(time.Millisecond):
		}
	}
	response, err := http.Post(server.URL+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("real HTTP body Read did not unblock on cancellation")
	}
	if len(uploadSlots) != 0 {
		t.Fatal("slow HTTP body retained upload capacity")
	}
	if _, err = s.files.Stat(target); err != nil {
		t.Fatalf("responsive SSH/SFTP was unnecessarily closed: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "original" {
		t.Fatalf("slow cancelled upload replaced target: %q %v", data, err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("responsive cancellation did not clean up: %v", entries)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	result, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(result.Body)
	result.Body.Close()
	if err != nil || result.StatusCode == 200 || !bytes.Contains(payload, []byte("sftp_interrupted")) {
		t.Fatalf("cancel result: %d %s %v", result.StatusCode, payload, err)
	}
}
