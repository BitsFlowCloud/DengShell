package app

import (
	"io"
	"net"
	"sync"

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
	sh, err := s.client.NewSession()
	if err != nil {
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
	if err := sh.RequestSubsystem("sftp"); err != nil {
		sh.Close()
		return nil, err
	}
	local, relay := net.Pipe()
	b := &sshSFTPBridge{local: local, relay: relay, shell: sh}
	s.fileBridge.Store(b)
	go func() { defer b.close(); _, _ = io.Copy(relay, out) }()
	go func() { defer b.close(); _, _ = io.Copy(in, relay) }()
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	client, err := sftp.NewClientPipe(local, local)
	if err != nil {
		b.close()
	}
	return client, err
}
