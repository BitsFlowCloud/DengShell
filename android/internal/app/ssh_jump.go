package app

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
)

type sshJumpHostError struct {
	profileID string
	name      string
	cause     error
}

func (e *sshJumpHostError) Error() string {
	return fmt.Sprintf("SSH 跳板机「%s」：%v", e.name, e.cause)
}

func (e *sshJumpHostError) Unwrap() error { return e.cause }

type sshJumpConn struct {
	net.Conn
	jump *ssh.Client
}

// This connection owns the jump transport. Closing it also interrupts a
// target handshake or channel close stalled at the jump server.
func (c *sshJumpConn) Close() error { return c.jump.Close() }

func validSSHJumpHost(target, jump Profile) bool {
	return jump.ID != "" && target.ID != jump.ID && target.Protocol != "rdp" &&
		jump.Protocol != "rdp" && jump.JumpHostID == "" &&
		(!strings.EqualFold(target.Host, jump.Host) || target.Port != jump.Port)
}
