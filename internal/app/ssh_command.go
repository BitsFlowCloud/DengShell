package app

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	monitorOutputLimit = 4 << 20
	networkOutputLimit = 1 << 20
	sshCancelGrace     = 250 * time.Millisecond
)

var errRemoteOutputLimit = errors.New("远端监控输出超过大小限制")

type commandSSHSession struct {
	*ssh.Session
	closed <-chan struct{}
}

// Observe the channel request stream that ssh.Session.wait consumes. Close()
// alone only writes a packet; even a session whose exec was never started owns
// a wait goroutine until this stream closes. A lightweight Client adapter lets
// us observe that lifecycle without opening a second channel or SSH transport.
type commandSSHConn struct {
	ssh.Conn
	transport context.Context
	closed    chan struct{}
}

func (c *commandSSHConn) OpenChannel(kind string, payload []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	channel, requests, err := c.Conn.OpenChannel(kind, payload)
	if err != nil {
		return nil, nil, err
	}
	forward := make(chan *ssh.Request)
	go func() {
		defer close(c.closed)
		defer close(forward)
		for {
			select {
			case request, ok := <-requests:
				if !ok {
					return
				}
				select {
				case forward <- request:
				case <-c.transport.Done():
					return
				}
			case <-c.transport.Done():
				return
			}
		}
	}()
	return channel, forward, nil
}

// Keep the slot until the opening worker really exits. Cancellation can race
// with a healthy peer's channel-open confirmation; allow it to arrive and close
// the unused channel before deciding the entire transport is unresponsive.
func (s *Session) openSSHChannel(ctx context.Context, slots chan struct{}) (*commandSSHSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-slots
		return nil, err
	}
	type opened struct {
		channel *commandSSHSession
		err     error
	}
	ready := make(chan opened)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer func() { <-slots }()
		transport := s.ctx
		if transport == nil {
			transport = context.Background()
		}
		adapter := &commandSSHConn{Conn: s.client.Conn, transport: transport, closed: make(chan struct{})}
		client := &ssh.Client{Conn: adapter}
		inner, err := client.NewSession()
		var channel *commandSSHSession
		if err == nil {
			channel = &commandSSHSession{inner, adapter.closed}
		}
		select {
		case ready <- opened{channel, err}:
		case <-ctx.Done():
			if channel != nil {
				_ = channel.Close()
				<-channel.closed
			}
		}
	}()
	select {
	case result := <-ready:
		return result.channel, result.err
	case <-ctx.Done():
		timer := time.NewTimer(sshCancelGrace)
		defer timer.Stop()
		select {
		case <-finished:
		case <-timer.C:
			s.forceClose()
		}
		return nil, ctx.Err()
	}
}

// stdout and stderr share one budget. Cancel as soon as the limit is exceeded:
// returning a writer error alone still leaves ssh.Session.Wait waiting for an
// exit status that an uncooperative server might never send.
type boundedSSHOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	limit  int
	cancel context.CancelCauseFunc
}

func (w *boundedSSHOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := min(len(p), max(0, w.limit-w.buffer.Len()))
	_, _ = w.buffer.Write(p[:n])
	if n != len(p) {
		w.cancel(errRemoteOutputLimit)
		return n, errRemoteOutputLimit
	}
	return n, nil
}

func (w *boundedSSHOutput) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buffer.Bytes())
}

// Closing an SSH channel is a protocol request, not a local interruption of
// Wait/Start. Give a cooperative server time to close just this channel; if it
// ignores that request, terminate the affected SSH transport to release all
// blocked readers/writers. Other server sessions remain independent.
func (s *Session) runSSHChannel(ctx context.Context, channel *commandSSHSession, command string, signal ...ssh.Signal) error {
	done := make(chan error, 1)
	go func() {
		err := ctx.Err()
		if err == nil {
			err = channel.Run("exec " + posixShellCommand(command))
		}
		_ = channel.Close()
		<-channel.closed
		done <- err
	}()
	select {
	case err := <-done:
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return err
	case <-ctx.Done():
	}
	// Close can itself block writing to a stuck transport, so the grace timer
	// must be started independently of it.
	go func() {
		if len(signal) != 0 {
			_ = channel.Signal(signal[0])
		}
		_ = channel.Close()
	}()
	timer := time.NewTimer(sshCancelGrace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		s.forceClose()
	}
	return context.Cause(ctx)
}

func (s *Session) runBoundedChannel(ctx context.Context, channel *commandSSHSession, command string, limit int) ([]byte, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	output := &boundedSSHOutput{limit: limit, cancel: cancel}
	channel.Stdout, channel.Stderr = output, output
	err := s.runSSHChannel(ctx, channel, command)
	return output.Bytes(), err
}

func (s *Session) runBoundedSSH(ctx context.Context, command string, limit int) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if s.ctx != nil {
		stop := context.AfterFunc(s.ctx, cancel)
		defer stop()
	}
	channel, err := openMTRChannel(ctx, s)
	if err != nil {
		return nil, err
	}
	return s.runBoundedChannel(ctx, channel, command, limit)
}
