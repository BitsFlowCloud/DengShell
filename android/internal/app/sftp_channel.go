package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Local pipes let a failed SFTP subsystem release all pending file operations
// without closing the SSH transport. Each SSH connection owns one bridge;
// failed file operations are never silently replayed on a replacement channel.
type sshSFTPBridge struct {
	local, relay net.Conn
	shell        *ssh.Session
	once         sync.Once
}

func (b *sshSFTPBridge) close() {
	b.once.Do(func() {
		b.local.Close()
		b.relay.Close()
		// Remote CLOSE may block on a stalled link. Only this one bounded
		// cleanup worker remains until the peer or transport finishes.
		go func() { _ = b.shell.Close() }()
	})
}

func (s *Session) openFileClient() (*sftp.Client, error) {
	return s.openFileClientContext(context.Background())
}

func (s *Session) openFileClientContext(ctx context.Context) (*sftp.Client, error) {
	sh, err := s.client.NewSession()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		go sh.Close()
		return nil, err
	}
	in, err := sh.StdinPipe()
	if err != nil {
		sh.Close()
		return nil, err
	}
	out, err := sh.StdoutPipe()
	if err != nil {
		sh.Close()
		return nil, err
	}
	stderr, err := sh.StderrPipe()
	if err != nil {
		sh.Close()
		return nil, err
	}
	local, relay := net.Pipe()
	b := &sshSFTPBridge{local: local, relay: relay, shell: sh}
	s.fileBridge.Store(b)
	stop := context.AfterFunc(ctx, b.close)
	defer stop()
	if err := sh.RequestSubsystem("sftp"); err != nil {
		b.close()
		return nil, err
	}
	go func() { defer b.close(); _, _ = io.Copy(relay, out) }()
	go func() { defer b.close(); _, _ = io.Copy(in, relay) }()
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	client, err := sftp.NewClientPipe(local, local)
	if err != nil {
		b.close()
	}
	return client, err
}

// Only one optional initialization worker belongs to a connection. If a peer
// ignores channel cancellation, it remains owned until the SSH transport ends;
// late results are discarded and never published to the session.
func (s *Session) initializeFileClient(ctx context.Context) (*sftp.Client, string, error) {
	type result struct {
		files *sftp.Client
		home  string
		err   error
	}
	done := make(chan result)
	go func() {
		files, err := s.openFileClientContext(ctx)
		home := "/"
		if err == nil {
			stop := context.AfterFunc(ctx, func() {
				if b := s.fileBridge.Load(); b != nil {
					b.close()
				}
			})
			home, err = files.Getwd()
			stop()
			if err == nil {
				home, err = remotePath(home)
			}
		}
		if err != nil {
			if b := s.fileBridge.Load(); b != nil {
				b.close()
			}
		}
		select {
		case done <- result{files, home, err}:
		case <-ctx.Done():
			if b := s.fileBridge.Load(); b != nil {
				b.close()
			}
		}
	}()
	select {
	case value := <-done:
		if ctx.Err() == nil {
			return value.files, value.home, value.err
		}
	case <-ctx.Done():
	}
	if b := s.fileBridge.Load(); b != nil {
		b.close()
	}
	return nil, "", ctx.Err()
}

var errSFTPUnavailable = errors.New("当前连接的 SFTP 文件服务不可用，SSH 终端仍可正常使用")

func (a *App) fileSession(id string) (*Session, error) {
	s, err := a.session(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 6*time.Second)
	defer cancel()
	if err := s.waitFileInitialization(ctx); err != nil {
		return nil, err
	}
	if files, _ := s.fileClientSnapshot(); files == nil {
		return nil, errSFTPUnavailable
	}
	return s, nil
}

// The public connection fields remain immutable after publication. File metadata
// is published separately so JSON encoding and terminal startup never race with
// the optional SFTP worker. Legacy isolated fixtures have no fileDone channel.
func (s *Session) prepareFileInitialization() {
	s.Home = "/"
	s.SFTPAvailable = false
	s.SFTPPending = true
	s.fileDone = make(chan struct{})
}

func (s *Session) startFileInitialization() {
	if s.fileDone == nil {
		return
	} // Existing isolated fixtures already own their file client.
	s.fileInitOnce.Do(func() {
		go func() {
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			defer cancel()
			files, home, err := s.initializeFileClient(ctx)
			s.fileMu.Lock()
			if s.ctx.Err() != nil {
				err = s.ctx.Err()
				if b := s.fileBridge.Load(); b != nil {
					b.close()
				}
			}
			if err == nil {
				s.files, s.filesHome = files, home
			}
			close(s.fileDone)
			s.fileMu.Unlock()
		}()
	})
}

func (s *Session) fileClientSnapshot() (*sftp.Client, string) {
	s.fileMu.RLock()
	defer s.fileMu.RUnlock()
	home := s.filesHome
	if home == "" {
		home = s.Home
	}
	return s.files, home
}

func (s *Session) waitFileInitialization(ctx context.Context) error {
	if s.fileDone == nil {
		return nil
	}
	select {
	case <-s.fileDone:
		if err := s.ctx.Err(); err != nil {
			return err
		}
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) fileStatus(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := s.waitFileInitialization(ctx); err != nil {
		if s.ctx.Err() != nil || r.Context().Err() != nil {
			writeError(w, 400, err)
			return
		}
		writeJSON(w, map[string]any{"sftpPending": true, "sftpAvailable": false, "home": "/"})
		return
	}
	files, home := s.fileClientSnapshot()
	writeJSON(w, map[string]any{"sftpPending": false, "sftpAvailable": files != nil, "home": home})
}
