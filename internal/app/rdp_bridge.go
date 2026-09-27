package app

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The embedded engine gets a one-session, authenticated loopback SOCKS endpoint.
// Only its original RDP destination is allowed. The real proxy credentials stay
// in Go, and domain names are passed unchanged to the configured upstream proxy.
type rdpBridge struct {
	listener  net.Listener
	token     string
	target    string
	ctx       context.Context
	cancel    context.CancelFunc
	proxy     ProxyConfig
	wg        sync.WaitGroup
	slots     chan struct{}
	mu        sync.Mutex
	lastError string
}

func newRDPBridge(ctx context.Context, target string, proxy ProxyConfig) (*rdpBridge, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	b := &rdpBridge{listener: l, token: randomID(), target: target, ctx: ctx, cancel: cancel, proxy: proxy, slots: make(chan struct{}, 8)}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		stop := context.AfterFunc(ctx, func() { _ = l.Close() })
		defer stop()
		for {
			conn, e := l.Accept()
			if e != nil {
				return
			}
			select {
			case b.slots <- struct{}{}:
				b.wg.Add(1)
				go func() { defer b.wg.Done(); defer func() { <-b.slots }(); b.serve(conn) }()
			default:
				_ = conn.Close()
			}
		}
	}()
	return b, nil
}

func (b *rdpBridge) close()          { b.cancel(); _ = b.listener.Close(); b.wg.Wait() }
func (b *rdpBridge) failure() string { b.mu.Lock(); defer b.mu.Unlock(); return b.lastError }
func readRDPString(r io.Reader) (string, error) {
	var n [1]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return "", err
	}
	b := make([]byte, int(n[0]))
	_, err := io.ReadFull(r, b)
	return string(b), err
}
func (b *rdpBridge) serve(c net.Conn) {
	defer c.Close()
	stop := context.AfterFunc(b.ctx, func() { _ = c.Close() })
	defer stop()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	var h [4]byte
	if _, err := io.ReadFull(c, h[:2]); err != nil || h[0] != 5 || h[1] == 0 {
		return
	}
	methods := make([]byte, int(h[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	found := false
	for _, m := range methods {
		if m == 2 {
			found = true
		}
	}
	if !found {
		_, _ = c.Write([]byte{5, 255})
		return
	}
	if _, err := c.Write([]byte{5, 2}); err != nil {
		return
	}
	if _, err := io.ReadFull(c, h[:1]); err != nil || h[0] != 1 {
		return
	}
	u, err := readRDPString(c)
	if err != nil {
		return
	}
	p, err := readRDPString(c)
	if err != nil {
		return
	}
	if subtle.ConstantTimeCompare([]byte(u), []byte("dengshell")) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(b.token)) != 1 {
		_, _ = c.Write([]byte{1, 1})
		return
	}
	if _, err := c.Write([]byte{1, 0}); err != nil {
		return
	}
	if _, err := io.ReadFull(c, h[:]); err != nil || h[0] != 5 || h[1] != 1 || h[2] != 0 {
		return
	}
	var host string
	switch h[3] {
	case 1, 4:
		n := 4
		if h[3] == 4 {
			n = 16
		}
		ip := make([]byte, n)
		if _, err := io.ReadFull(c, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 3:
		host, err = readRDPString(c)
		if err != nil {
			return
		}
	default:
		return
	}
	if _, err := io.ReadFull(c, h[:2]); err != nil {
		return
	}
	port := int(binary.BigEndian.Uint16(h[:2]))
	target := net.JoinHostPort(host, strconv.Itoa(port))
	allowedHost, allowedPort, _ := net.SplitHostPort(b.target)
	hostOK := strings.EqualFold(host, allowedHost)
	if ip := net.ParseIP(host); ip != nil {
		hostOK = ip.Equal(net.ParseIP(allowedHost))
	}
	if !hostOK || strconv.Itoa(port) != allowedPort {
		_, _ = c.Write([]byte{5, 2, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	ctx, cancel := context.WithTimeout(b.ctx, 15*time.Second)
	remote, err := dialSSH(ctx, target, b.proxy)
	cancel()
	if err != nil {
		b.mu.Lock()
		if b.proxy.Type == "" || b.proxy.Type == "direct" {
			b.lastError = "无法连接远程桌面地址，请检查地址、端口和服务器防火墙"
		} else {
			b.lastError = "代理未能连接目标 RDP 端口，请检查代理、认证和端口访问权限"
		}
		b.mu.Unlock()
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	stopRemote := context.AfterFunc(b.ctx, func() { _ = remote.Close() })
	defer stopRemote()
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	done := make(chan struct{})
	go func() { _, _ = io.Copy(remote, c); _ = remote.Close(); _ = c.Close(); close(done) }()
	_, _ = io.Copy(c, remote)
	_ = c.Close()
	_ = remote.Close()
	<-done
}

func rdpSafeLine(value string) error {
	if strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("RDP 配置不能包含换行或空字符")
	}
	return nil
}
