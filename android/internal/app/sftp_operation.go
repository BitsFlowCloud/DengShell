package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
)

const sftpIdleTimeout = 30 * time.Second

// SFTP v3 has no request cancellation. Closing a File only sends another
// request, and cannot interrupt a pending Write/Stat/Close. Give a responsive
// operation time to clean up; otherwise close only the isolated SFTP bridge.
// This also bounds cleanup and preserves the global upload worker limit.
type sftpOperation struct {
	ctx               context.Context
	cancel            context.CancelCauseFunc
	done              chan struct{}
	exited            chan struct{}
	progress          chan struct{}
	stopSession       func() bool
	forced            atomic.Bool
	fileChannelClosed atomic.Bool
	closeOnce         sync.Once
}

type sftpOperationContextKey struct{}

func startSFTPOperation(parent context.Context, s *Session, idle time.Duration) *sftpOperation {
	ctx, cancel := context.WithCancelCause(parent)
	op := &sftpOperation{ctx: ctx, cancel: cancel, done: make(chan struct{}), exited: make(chan struct{}), progress: make(chan struct{}, 1)}
	op.ctx = context.WithValue(ctx, sftpOperationContextKey{}, op)
	if s.ctx != nil {
		op.stopSession = context.AfterFunc(s.ctx, func() { cancel(context.Canceled) })
	}
	go func() {
		defer close(op.exited)
		timer := time.NewTimer(idle)
		defer timer.Stop()
		for {
			select {
			case <-op.done:
				return
			case <-op.progress:
				timer.Reset(idle)
			case <-timer.C:
				cancel(context.DeadlineExceeded)
			case <-ctx.Done():
				s.noteOperationCancellation("sftp", context.Cause(ctx))
				grace := time.NewTimer(s.cleanupGracePeriod())
				defer grace.Stop()
				select {
				case <-op.done:
					return
				case <-grace.C:
					op.forced.Store(true)
					if b := s.fileBridge.Load(); b != nil {
						op.fileChannelClosed.Store(true)
						s.noteOperationCancellation("sftp-channel-closed", context.Cause(ctx))
						b.close()
					} else {
						// Standalone transports used by callers/tests have no SSH
						// bridge. Closing them cannot affect a separate terminal.
						s.forceCloseDiagnostic("DS-240", "sftp-cleanup-timeout")
					}
					return
				}
			}
		}
	}()
	return op
}

func (op *sftpOperation) touch() {
	select {
	case op.progress <- struct{}{}:
	default:
	}
}

func touchSFTPOperation(ctx context.Context) {
	if op, ok := ctx.Value(sftpOperationContextKey{}).(*sftpOperation); ok {
		op.touch()
	}
}

func (op *sftpOperation) close() {
	op.closeOnce.Do(func() {
		close(op.done)
		if op.stopSession != nil {
			op.stopSession()
		}
		<-op.exited
		op.cancel(nil)
	})
}

type sftpInterruptedError struct {
	cause             error
	disconnected      bool
	fileChannelClosed bool
}

func (e *sftpInterruptedError) Error() string {
	message := "文件操作已取消"
	if errors.Is(e.cause, context.DeadlineExceeded) {
		message = "文件操作超时，服务器未及时响应"
	}
	if e.disconnected {
		message += "；服务器未响应，已断开此 SSH 连接，请重新连接。未完成的私有临时文件可能保留在远端"
	}
	if e.fileChannelClosed {
		message += "；文件通道已中断，SSH 终端保持连接。重新连接后可恢复文件管理；未完成的临时文件可能保留在远端"
	}
	return message
}
func (e *sftpInterruptedError) Unwrap() error { return e.cause }
func (e *sftpInterruptedError) Code() string  { return "sftp_interrupted" }
func (op *sftpOperation) err(err error) error {
	if cause := context.Cause(op.ctx); cause != nil {
		return &sftpInterruptedError{cause: cause, disconnected: op.forced.Load() && !op.fileChannelClosed.Load(), fileChannelClosed: op.fileChannelClosed.Load()}
	}
	return err
}
func (op *sftpOperation) finish(err *error) {
	*err = op.err(*err)
	op.close()
}

type sftpProgressReader struct {
	reader io.Reader
	op     *sftpOperation
}

type sftpProgressReadSeeker struct {
	io.ReadSeeker
	op *sftpOperation
}

func (r sftpProgressReadSeeker) Read(p []byte) (int, error) {
	return sftpProgressReader{r.ReadSeeker, r.op}.Read(p)
}

func (r sftpProgressReader) Read(p []byte) (int, error) {
	// Observe each acknowledged SFTP packet even when io.ReadAll supplies a
	// large buffer, rather than timing out a healthy multi-packet read.
	if len(p) > uploadChunkSize {
		p = p[:uploadChunkSize]
	}
	n, err := r.reader.Read(p)
	if n > 0 {
		r.op.touch()
	}
	return n, err
}

type sftpProgressWriter struct {
	io.Writer
	op *sftpOperation
}

func (w sftpProgressWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n > 0 {
		w.op.touch()
	}
	return n, err
}

// A random private directory is required because pkg/sftp's OpenFile creates
// with the server's default mode. Chmod alone would leave a window in which
// another user could open the empty file and retain access to subsequent data.
func privateRemoteTemporary(c *sftp.Client, target, purpose string) (*sftp.File, string, func(), error) {
	dir := path.Join(path.Dir(target), ".dengshell-"+purpose+"-"+randomID())
	if err := c.Mkdir(dir); err != nil {
		return nil, "", nil, err
	}
	cleanup := func() { _ = c.Remove(path.Join(dir, "contents.part")); _ = c.RemoveDirectory(dir) }
	if err := c.Chmod(dir, 0700); err != nil {
		cleanup()
		return nil, "", nil, err
	}
	temporary := path.Join(dir, "contents.part")
	f, err := c.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		cleanup()
		return nil, "", nil, err
	}
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		cleanup()
		return nil, "", nil, err
	}
	return f, temporary, cleanup, nil
}

func preserveRemoteMetadata(c *sftp.Client, temporary string, original os.FileInfo) error {
	if original == nil {
		return nil
	} // Newly uploaded files remain private (0600).
	if stat, ok := original.Sys().(*sftp.FileStat); ok {
		info, err := c.Stat(temporary)
		if err != nil {
			return err
		}
		newStat, ok := info.Sys().(*sftp.FileStat)
		if !ok {
			return errors.New("无法确认临时文件的所有者/用户组，已保留原文件")
		}
		if newStat.UID != stat.UID || newStat.GID != stat.GID {
			if err = c.Chown(temporary, int(stat.UID), int(stat.GID)); err != nil {
				return errors.New("无法保留原文件的所有者/用户组，已保留原文件")
			}
		}
	}
	// chown can clear setuid/setgid; restore the complete mode afterwards.
	return c.Chmod(temporary, original.Mode())
}

type remoteWriteKey struct {
	session  *Session
	identity string
	target   string
}
type remoteWriteLock struct {
	token chan struct{}
	refs  int
}

var remoteWrites = struct {
	sync.Mutex
	entries map[remoteWriteKey]*remoteWriteLock
}{entries: make(map[remoteWriteKey]*remoteWriteLock)}

// Serialize the same target, including aliases resolved before acquiring this
// lock. Waiting never owns a global mutex and honors request cancellation.
func lockRemoteWrite(ctx context.Context, s *Session, target string) (func(), error) {
	key := remoteWriteKey{session: s, target: target}
	if s.fileWriteIdentity != "" {
		key.session = nil
		key.identity = s.fileWriteIdentity
	}
	remoteWrites.Lock()
	entry := remoteWrites.entries[key]
	if entry == nil {
		entry = &remoteWriteLock{token: make(chan struct{}, 1)}
		remoteWrites.entries[key] = entry
	}
	entry.refs++
	remoteWrites.Unlock()
	drop := func() {
		remoteWrites.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(remoteWrites.entries, key)
		}
		remoteWrites.Unlock()
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case entry.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-entry.token
			drop()
			return nil, err
		}
		return func() { <-entry.token; drop() }, nil
	}
}
