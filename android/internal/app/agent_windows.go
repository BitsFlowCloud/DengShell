//go:build windows

package app

import (
	"context"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

func dialSSHAgent(ctx context.Context) (net.Conn, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		socket = `\\.\pipe\openssh-ssh-agent`
	}
	if strings.HasPrefix(strings.ToLower(socket), `\\.\pipe\`) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return winio.DialPipeContext(ctx, socket)
	}
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
}
