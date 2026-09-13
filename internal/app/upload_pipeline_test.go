package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// An actual SFTP client/server with a packet-level latency link. Each response
// is delayed independently (not serialized sleeps, which would test bandwidth
// throttling instead of RTT). All files live in t.TempDir().
type uploadTestLink struct {
	mu        sync.Mutex
	writes    map[uint32]bool
	pending   int
	maximum   int
	acked     int
	failNext  bool
	delay     time.Duration
	ackGate   <-chan struct{}
	handles   map[string]bool
	closeGate <-chan struct{}
	closes    map[uint32]bool
}

func (l *uploadTestLink) request(frame []byte) {
	if len(frame) >= 9 && frame[4] == 4 { // SSH_FXP_CLOSE
		l.mu.Lock()
		l.closes[binary.BigEndian.Uint32(frame[5:9])] = true
		l.mu.Unlock()
	}
	if len(frame) >= 9 && frame[4] == 6 { // SSH_FXP_WRITE
		l.mu.Lock()
		l.writes[binary.BigEndian.Uint32(frame[5:9])] = true
		l.pending++
		l.maximum = max(l.maximum, l.pending)
		if len(frame) >= 13 {
			end := 13 + int(binary.BigEndian.Uint32(frame[9:13]))
			if end <= len(frame) {
				l.handles[string(frame[13:end])] = true
			}
		}
		l.mu.Unlock()
	}
}

func (l *uploadTestLink) response(frame []byte) {
	if len(frame) < 13 || frame[4] != 101 { // SSH_FXP_STATUS
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	id := binary.BigEndian.Uint32(frame[5:9])
	delete(l.closes, id)
	if l.writes[id] {
		delete(l.writes, id)
		l.pending--
		l.acked++
		if l.failNext {
			binary.BigEndian.PutUint32(frame[9:13], 4) // SSH_FX_FAILURE
			l.failNext = false
		}
	}
}

func transferFrame(reader io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header)
	if size > 2*1024*1024 {
		return nil, errors.New("unexpected large test packet")
	}
	frame := make([]byte, 4+size)
	copy(frame, header)
	_, err := io.ReadFull(reader, frame[4:])
	return frame, err
}

func uploadSFTPFixture(t *testing.T, delay time.Duration) (*App, *Session, string, *uploadTestLink) {
	t.Helper()
	root := t.TempDir()
	clientEnd, requestEnd := net.Pipe()
	responseEnd, serverEnd := net.Pipe()
	link := &uploadTestLink{writes: make(map[uint32]bool), handles: make(map[string]bool), closes: make(map[uint32]bool), delay: delay}
	ctx, stop := context.WithCancel(context.Background())
	go func() {
		for {
			frame, err := transferFrame(requestEnd)
			if err != nil {
				return
			}
			link.request(frame)
			if _, err = responseEnd.Write(frame); err != nil {
				return
			}
		}
	}()
	type pendingFrame struct {
		data  []byte
		ready time.Time
	}
	responses := make(chan pendingFrame, 128)
	go func() {
		defer close(responses)
		var held sync.WaitGroup
		defer held.Wait()
		for {
			frame, err := transferFrame(responseEnd)
			if err != nil {
				return
			}
			link.mu.Lock()
			gate := link.ackGate
			isWriteACK := len(frame) >= 9 && frame[4] == 101 && link.writes[binary.BigEndian.Uint32(frame[5:9])]
			isCloseACK := len(frame) >= 9 && frame[4] == 101 && link.closes[binary.BigEndian.Uint32(frame[5:9])]
			if isCloseACK {
				gate = link.closeGate
			}
			link.mu.Unlock()
			if gate != nil && (isWriteACK || isCloseACK) {
				held.Go(func() {
					select {
					case <-gate:
					case <-ctx.Done():
						return
					}
					select {
					case responses <- pendingFrame{frame, time.Now().Add(delay)}:
					case <-ctx.Done():
					}
				})
			} else {
				select {
				case responses <- pendingFrame{frame, time.Now().Add(delay)}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	go func() {
		for frame := range responses {
			timer := time.NewTimer(max(0, time.Until(frame.ready)))
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return
			}
			link.response(frame.data)
			if _, err := requestEnd.Write(frame.data); err != nil {
				return
			}
		}
	}()
	server, err := sftp.NewServer(serverEnd)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	client, err := sftp.NewClientPipe(clientEnd, clientEnd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop()
		clientEnd.Close()
		requestEnd.Close()
		responseEnd.Close()
		serverEnd.Close()
		client.Close()
		server.Close()
	})
	s := &Session{ID: "upload-test", Home: root, ctx: ctx, files: client}
	a := &App{sessions: map[string]*Session{s.ID: s}, transfers: make(map[string]*Transfer)}
	return a, s, root, link
}

func TestUploadPipelinesSFTPRequestsAndImprovesDelayedLink(t *testing.T) {
	a, s, root, link := uploadSFTPFixture(t, 12*time.Millisecond)
	data := bytes.Repeat([]byte("中文 upload\r\n\x00binary"), 90000)
	baseline, err := s.files.Create(filepath.Join(root, "sequential.bin"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = io.Copy(io.MultiWriter(baseline, io.Discard), bytes.NewReader(data))
	sequential := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	baseline.Close()
	link.mu.Lock()
	if link.maximum != 1 {
		t.Fatalf("old path unexpectedly parallel: %d", link.maximum)
	}
	link.maximum = 0
	link.mu.Unlock()
	target := filepath.Join(root, "parallel.bin")
	ctx, task, err := a.beginTransfer(context.Background(), s, "", target, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer task.cancel()
	start = time.Now()
	err = a.copyUpload(ctx, s, task, bytes.NewReader(data), false)
	parallel := time.Since(start)
	a.finishTransfer(task, err)
	if err != nil || task.Status != "done" || task.Done != int64(len(data)) {
		t.Fatalf("upload failed: %v %+v", err, task)
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("parallel upload changed bytes", err)
	}
	link.mu.Lock()
	maximum := link.maximum
	link.mu.Unlock()
	if maximum < 2 || maximum > uploadConcurrency {
		t.Fatalf("inflight limit: got %d", maximum)
	}
	// Includes target checks/create/close/rename, deliberately stricter than a
	// write-only benchmark. Broad ratio avoids normal scheduler jitter.
	if parallel*2 >= sequential {
		t.Fatalf("pipeline did not improve delayed link: serial %v, parallel %v", sequential, parallel)
	}
	t.Logf("actual SFTP, 12 ms response delay, %d bytes: sequential=%v parallel=%v speedup=%.2fx maxOutstanding=%d", len(data), sequential, parallel, float64(sequential)/float64(parallel), maximum)
}

type uploadFailReader struct{}

func (uploadFailReader) Read([]byte) (int, error) { return 0, errors.New("test source failed") }

func TestUploadFailureAndCancellationPreserveExistingTarget(t *testing.T) {
	for _, mode := range []string{"read-error", "write-error", "size-mismatch", "cancel", "close-cancel"} {
		t.Run(mode, func(t *testing.T) {
			a, s, root, link := uploadSFTPFixture(t, 4*time.Millisecond)
			target := filepath.Join(root, "keep.txt")
			original := []byte("keep existing target\r\n中文")
			if err := os.WriteFile(target, original, 0600); err != nil {
				t.Fatal(err)
			}
			data := bytes.Repeat([]byte("replacement"), 300000)
			var source io.Reader = bytes.NewReader(data)
			total := int64(len(data))
			if mode == "read-error" {
				source = io.MultiReader(bytes.NewReader(data[:uploadChunkSize]), uploadFailReader{})
			}
			if mode == "size-mismatch" {
				total++
			}
			ctx, task, err := a.beginTransfer(context.Background(), s, "", target, total)
			if err != nil {
				t.Fatal(err)
			}
			defer task.cancel()
			closeGate := make(chan struct{})
			var releaseClose sync.Once
			defer releaseClose.Do(func() { close(closeGate) })
			if mode == "close-cancel" {
				link.mu.Lock()
				link.closeGate = closeGate
				link.mu.Unlock()
			}
			if mode == "write-error" {
				link.mu.Lock()
				link.failNext = true
				link.mu.Unlock()
			}
			if mode == "cancel" || mode == "close-cancel" {
				go func() {
					for {
						link.mu.Lock()
						pending := link.pending
						if mode == "close-cancel" {
							pending = len(link.closes)
						}
						link.mu.Unlock()
						if pending > 0 {
							task.cancel()
							releaseClose.Do(func() { close(closeGate) })
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(time.Millisecond):
						}
					}
				}()
			}
			err = a.copyUpload(ctx, s, task, source, true)
			a.finishTransfer(task, err)
			if err == nil || task.Status == "done" {
				t.Fatal("failed or incomplete file reported complete")
			}
			if (mode == "cancel" || mode == "close-cancel") && task.Status != "cancelled" {
				t.Fatalf("cancel lost: %s %v", task.Status, err)
			}
			actual, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(original, actual) {
				t.Fatal("original replaced by failed upload", err)
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 1 {
				t.Fatalf("temporary file leaked: %v", entries)
			}
		})
	}
}

type uploadAckWriter struct {
	entered chan struct{}
	ack     chan struct{}
	active  atomic.Int32
	maximum atomic.Int32
}

func (w *uploadAckWriter) WriteAt(data []byte, _ int64) (int, error) {
	n := w.active.Add(1)
	for old := w.maximum.Load(); n > old && !w.maximum.CompareAndSwap(old, n); old = w.maximum.Load() {
	}
	w.entered <- struct{}{}
	<-w.ack
	w.active.Add(-1)
	return len(data), nil
}

func TestUploadProgressWaitsForAcknowledgementAndBoundsMemory(t *testing.T) {
	w := &uploadAckWriter{entered: make(chan struct{}, uploadConcurrency*3), ack: make(chan struct{})}
	var progress atomic.Int64
	done := make(chan error, 1)
	go func() {
		_, err := uploadChunks(context.Background(), w, bytes.NewReader(make([]byte, uploadChunkSize*uploadConcurrency*3+7)), func(n int) { progress.Add(int64(n)) })
		done <- err
	}()
	for range uploadConcurrency {
		select {
		case <-w.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("did not pipeline expected requests")
		}
	}
	if progress.Load() != 0 {
		t.Fatal("counted local reads before server acknowledgement")
	}
	select {
	case <-w.entered:
		t.Fatal("exceeded outstanding request bound")
	default:
	}
	close(w.ack)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if progress.Load() != int64(uploadChunkSize*uploadConcurrency*3+7) || w.maximum.Load() > uploadConcurrency {
		t.Fatal("incorrect acknowledged bytes or concurrency")
	}
}

func TestNativeQueuedUploadCancelsBeforeOpeningSource(t *testing.T) {
	a, s, root, _ := uploadSFTPFixture(t, 0)
	for range cap(uploadSlots) {
		uploadSlots <- struct{}{}
	}
	defer func() {
		for range cap(uploadSlots) {
			<-uploadSlots
		}
	}()
	ctx, task, err := a.beginTransfer(context.Background(), s, "", filepath.Join(root, "not-created"), 100)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = "queued"
	done := make(chan struct{})
	go func() { a.runNativeUpload(ctx, s, task, filepath.Join(root, "nonexistent-source"), false); close(done) }()
	task.cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("queued cancellation blocked on upload slot")
	}
	if task.Status != "cancelled" || task.Done != 0 {
		t.Fatal("queued upload started or cancellation lost", task)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("cancelled queued upload modified remote directory")
	}
}

func TestNativeFourFilesUploadWhileFifthQueuesAndCancelledQueueStaysCancelled(t *testing.T) {
	a, s, root, link := uploadSFTPFixture(t, 0)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	link.mu.Lock()
	link.ackGate = gate
	link.mu.Unlock()
	source := filepath.Join(t.TempDir(), "local-source.bin")
	data := bytes.Repeat([]byte("parallel files 中文\x00\r\n"), 10000)
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	var tasks []*Transfer
	var finished []chan struct{}
	start := func() {
		ctx, task, err := a.beginTransfer(context.Background(), s, "", filepath.Join(root, fmt.Sprintf("file-%d.bin", len(tasks)+1)), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		a.mu.Lock()
		task.Status = "queued"
		a.mu.Unlock()
		tasks = append(tasks, task)
		done := make(chan struct{})
		finished = append(finished, done)
		go func() { a.runNativeUpload(ctx, s, task, source, false); close(done) }()
	}
	waitFor := func(description string, predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !predicate() {
			if time.Now().After(deadline) {
				t.Fatal(description)
			}
			time.Sleep(time.Millisecond)
		}
	}
	for range 4 {
		start()
	}
	waitFor("four files did not issue real SFTP writes concurrently", func() bool {
		link.mu.Lock()
		defer link.mu.Unlock()
		return len(link.handles) == 4
	})
	start() // Fifth must wait, then automatically start when a slot is released.
	start() // Sixth is cancelled while still queued.
	a.mu.Lock()
	for i, task := range tasks {
		want := "uploading"
		if i >= 4 {
			want = "queued"
		}
		if task.Status != want {
			a.mu.Unlock()
			t.Fatalf("file %d: got %s, want %s", i+1, task.Status, want)
		}
	}
	a.mu.Unlock()
	if len(uploadSlots) != 4 {
		t.Fatalf("expected exactly four occupied upload slots, got %d", len(uploadSlots))
	}
	tasks[5].cancel()
	select {
	case <-finished[5]:
	case <-time.After(3 * time.Second):
		t.Fatal("sixth queued file could not be cancelled")
	}
	if tasks[5].Status != "cancelled" {
		t.Fatalf("sixth file cancellation lost: %s", tasks[5].Status)
	}
	release.Do(func() { close(gate) })
	for i, done := range finished[:5] {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("file %d did not finish or take a released slot", i+1)
		}
		if tasks[i].Status != "done" {
			t.Fatalf("file %d failed: %+v", i+1, tasks[i])
		}
		actual, err := os.ReadFile(tasks[i].Target)
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatalf("file %d bytes changed: %v", i+1, err)
		}
	}
	if _, err := os.Stat(tasks[5].Target); !os.IsNotExist(err) {
		t.Fatal("cancelled sixth file appeared on server")
	}
	if len(uploadSlots) != 0 {
		t.Fatal("upload slot leaked after completion")
	}
}

func TestHTTPAndNativeUploadsShareFourSlotsAndQueuedHTTPCanCancel(t *testing.T) {
	a, s, root, link := uploadSFTPFixture(t, 0)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	link.mu.Lock()
	link.ackGate = gate
	link.mu.Unlock()
	data := bytes.Repeat([]byte("HTTP plus native\r\n中文"), 10000)
	local := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(local, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, native, err := a.beginTransfer(context.Background(), s, "native", filepath.Join(root, "native.bin"), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	nativeDone := make(chan struct{})
	go func() { a.runNativeUpload(ctx, s, native, local, false); close(nativeDone) }()
	type httpTask struct {
		id, target string
		response   *httptest.ResponseRecorder
		done       chan struct{}
	}
	startHTTP := func(index int) httpTask {
		id := fmt.Sprintf("http-%d", index)
		target := filepath.Join(root, id+".bin")
		request := httptest.NewRequest("POST", "/upload?path="+url.QueryEscape(target)+"&task="+id, bytes.NewReader(data))
		request.SetPathValue("id", s.ID)
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { a.uploadHTTP(response, request); close(done) }()
		return httpTask{id, target, response, done}
	}
	waitFor := func(message string, predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !predicate() {
			if time.Now().After(deadline) {
				t.Fatal(message)
			}
			time.Sleep(time.Millisecond)
		}
	}
	var pending []httpTask
	for i := 1; i <= 3; i++ {
		pending = append(pending, startHTTP(i))
	}
	waitFor("native plus three browser uploads did not fill all four slots", func() bool {
		link.mu.Lock()
		defer link.mu.Unlock()
		return len(link.handles) == 4
	})
	pending = append(pending, startHTTP(4), startHTTP(5))
	waitFor("additional HTTP uploads were not queued", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, id := range []string{"http-4", "http-5"} {
			if task := a.transfers[id]; task == nil || task.Status != "queued" {
				return false
			}
		}
		return true
	})
	if len(uploadSlots) != 4 {
		t.Fatalf("mixed uploads exceeded or bypassed four slots: %d", len(uploadSlots))
	}
	cancelRequest := httptest.NewRequest("DELETE", "/api/transfers/http-5", nil)
	cancelRequest.SetPathValue("id", "http-5")
	cancelResponse := httptest.NewRecorder()
	a.cancelTransfer(cancelResponse, cancelRequest)
	if cancelResponse.Code != 200 {
		t.Fatalf("HTTP cancellation endpoint failed: %s", cancelResponse.Body.String())
	}
	select {
	case <-pending[4].done:
	case <-time.After(3 * time.Second):
		t.Fatal("queued HTTP handler failed to wake after cancellation")
	}
	if pending[4].response.Code != 400 {
		t.Fatalf("cancelled HTTP reported success: %d", pending[4].response.Code)
	}
	a.mu.Lock()
	cancelled := a.transfers["http-5"].Status == "cancelled"
	a.mu.Unlock()
	if !cancelled {
		t.Fatal("cancelled HTTP task not marked cancelled")
	}
	release.Do(func() { close(gate) })
	for _, task := range pending[:4] {
		select {
		case <-task.done:
		case <-time.After(5 * time.Second):
			t.Fatal("HTTP task did not complete/refill", task.id)
		}
		if task.response.Code != 200 {
			t.Fatal(task.id, task.response.Body.String())
		}
		actual, err := os.ReadFile(task.target)
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatal("HTTP upload changed bytes", task.id, err)
		}
	}
	select {
	case <-nativeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("mixed native upload did not complete")
	}
	if native.Status != "done" {
		t.Fatal("mixed native upload failed", native.Error)
	}
	if _, err := os.Stat(pending[4].target); !os.IsNotExist(err) {
		t.Fatal("cancelled queued HTTP created a remote file")
	}
	if len(uploadSlots) != 0 {
		t.Fatal("shared upload slot leaked")
	}
}

func TestUploadDisconnectUnblocksPendingWritesWithoutCommittingTarget(t *testing.T) {
	a, s, root, link := uploadSFTPFixture(t, 0)
	gate := make(chan struct{})
	defer close(gate)
	link.mu.Lock()
	link.ackGate = gate
	link.mu.Unlock()
	target := filepath.Join(root, "existing.bin")
	original := []byte("preserve original on disconnected upload")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "replacement.bin")
	data := bytes.Repeat([]byte("replacement"), 300000)
	if err := os.WriteFile(local, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, task, err := a.beginTransfer(context.Background(), s, "disconnect", target, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.runNativeUpload(ctx, s, task, local, true); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		link.mu.Lock()
		pending := link.pending
		link.mu.Unlock()
		if pending > 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload did not issue pending requests")
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { s.files.Close(); close(closed) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not unblock parallel writes")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("SFTP close did not finish")
	}
	if task.Status != "failed" || task.Done != 0 {
		t.Fatalf("disconnected unacknowledged upload incorrectly completed: %+v", task)
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatal("disconnect committed partial data", err)
	}
	if len(uploadSlots) != 0 {
		t.Fatal("disconnect leaked global upload slot")
	}
	// The lost SFTP transport cannot remove a remote .part file. Cleanup is
	// best effort; the original target is never replaced by that partial file.
}
