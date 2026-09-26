package app

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

var errMonitorCleaningUp = errors.New("上一次监控采集仍在清理，请稍后重试")

// A collector owns at most one real SSH channel, including channel-open and
// cancelled workers awaiting peer acknowledgement. A stuck collector pauses
// only itself; it cannot close the terminal or consume global diagnostic slots.
type monitorCommandRunner struct {
	mu  sync.Mutex
	job *monitorCommandJob
}

type monitorCommandJob struct {
	done   chan struct{}
	output []byte
	err    error
}

func (r *monitorCommandRunner) run(parent context.Context, s *Session, scope, command string, limit int) ([]byte, error) {
	return r.runWithOutput(parent, s, scope, command, limit, nil)
}

func (r *monitorCommandRunner) runWithOutput(parent context.Context, s *Session, scope, command string, limit int, stream io.Writer) ([]byte, error) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		stop := context.AfterFunc(s.ctx, func() { cancel(context.Canceled) })
		defer stop()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.job != nil {
		select {
		case <-r.job.done:
			r.job = nil
		default:
			r.mu.Unlock()
			return nil, errMonitorCleaningUp
		}
	}
	job := &monitorCommandJob{done: make(chan struct{})}
	r.job = job
	r.mu.Unlock()
	go func() {
		defer close(job.done)
		transport := s.ctx
		if transport == nil {
			transport = context.Background()
		}
		adapter := &commandSSHConn{Conn: s.client.Conn, transport: transport, closed: make(chan struct{})}
		client := &ssh.Client{Conn: adapter}
		channel, err := client.NewSession()
		if err != nil {
			job.err = err
			return
		}
		output := &boundedSSHOutput{limit: limit, cancel: cancel}
		channel.Stdout, channel.Stderr = output, output
		if stream != nil {
			writer := io.MultiWriter(output, stream)
			channel.Stdout, channel.Stderr = writer, writer
		}
		cleanupDone := make(chan struct{})
		stopCleanup := context.AfterFunc(ctx, func() {
			defer close(cleanupDone)
			// Signal only this collector, never send Ctrl-C to the user's PTY.
			_ = channel.Signal(ssh.SIGTERM)
			_ = channel.Close()
		})
		if err = ctx.Err(); err == nil {
			err = channel.Run("exec " + posixShellCommand(command))
		}
		if stopCleanup() {
			_ = channel.Close()
		} else {
			<-cleanupDone
		}
		<-adapter.closed
		job.output, job.err = output.Bytes(), err
	}()
	select {
	case <-job.done:
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return job.output, job.err
	case <-ctx.Done():
		s.noteOperationCancellation("monitor-"+scope, context.Cause(ctx))
		// Allow ordinary local/low-latency cleanup to finish, but never hold an
		// HTTP request for the full transport recovery window. The slot remains
		// owned until the real worker exits, even when the server ignores CLOSE.
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-job.done:
		case <-timer.C:
		}
		return nil, context.Cause(ctx)
	}
}
