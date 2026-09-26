//go:build !windows

package app

import (
	"context"
	"errors"
	"net"
	"os"
	"time"
)

func dialSSHAgent(ctx context.Context) (net.Conn, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, errors.New("SSH_AUTH_SOCK 未设置，请先启动 SSH Agent 并添加密钥")
	}
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
}
