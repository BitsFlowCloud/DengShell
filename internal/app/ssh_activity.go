package app

import (
	"net"
	"sync/atomic"
	"time"
)

var sshActivityEpoch = time.Now()

// Observe inbound transport traffic, including terminal and SFTP packets.
// Successful local writes alone are not proof of delivery to the peer.
type sshActivityConn struct {
	net.Conn
	receivedAt atomic.Int64
}

func newSSHActivityConn(conn net.Conn) *sshActivityConn {
	c := &sshActivityConn{Conn: conn}
	c.receivedAt.Store(time.Since(sshActivityEpoch).Nanoseconds())
	return c
}

func (c *sshActivityConn) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	if n > 0 {
		c.receivedAt.Store(time.Since(sshActivityEpoch).Nanoseconds())
	}
	return n, err
}

func (c *sshActivityConn) idleFor() time.Duration {
	return time.Duration(time.Since(sshActivityEpoch).Nanoseconds() - c.receivedAt.Load())
}
