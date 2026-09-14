package app

import (
	"bytes"
	"context"
	"errors"
	"sync"

	"golang.org/x/crypto/ssh"
)

const (
	monitorOutputLimit = 4 << 20
	networkOutputLimit = 1 << 20
)

var errRemoteOutputLimit = errors.New("远端监控输出超过大小限制")

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

func (s *Session) runBoundedSSH(ctx context.Context, command string, limit int) ([]byte, error) {
	return s.commandCollector.run(ctx, s, "command", command, limit)
}
