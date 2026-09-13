package app

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

type ProxyConfig struct {
	Type          string `json:"type"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	User          string `json:"user,omitempty"`
	Password      string `json:"password,omitempty"`
	HasPassword   bool   `json:"hasPassword,omitempty"`
	ClearPassword bool   `json:"clearPassword,omitempty"`
}

func (p *ProxyConfig) validate() error {
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	if p.Type == "" || p.Type == "direct" {
		*p = ProxyConfig{Type: "direct"}
		return nil
	}
	if p.Type != "socks5" && p.Type != "http" {
		return errors.New("代理类型应为 SOCKS5 或 HTTP CONNECT")
	}
	p.Host = strings.Trim(strings.TrimSpace(p.Host), "[]")
	if p.Host == "" || len(p.Host) > 253 || strings.ContainsAny(p.Host, " /\r\n\t") || p.Port < 1 || p.Port > 65535 {
		return errors.New("请填写有效的代理地址和端口")
	}
	if len(p.User) > 255 || len(p.Password) > 255 || strings.ContainsAny(p.User, "\r\n:") {
		return errors.New("代理用户名或密码无效／过长")
	}
	return nil
}

func (p ProxyConfig) public() ProxyConfig {
	p.HasPassword = p.Password != ""
	p.Password = ""
	p.ClearPassword = false
	return p
}

func dialSSH(ctx context.Context, target string, config ProxyConfig) (net.Conn, error) {
	dialer := &net.Dialer{KeepAlive: 15 * time.Second}
	if config.Type == "" || config.Type == "direct" {
		return dialer.DialContext(ctx, "tcp", target)
	}
	address := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	if config.Type == "socks5" {
		var auth *proxy.Auth
		if config.User != "" || config.Password != "" {
			auth = &proxy.Auth{User: config.User, Password: config.Password}
		}
		d, err := proxy.SOCKS5("tcp", address, auth, dialer)
		if err != nil {
			return nil, err
		}
		return d.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
	}
	if config.Type != "http" {
		return nil, errors.New("不支持的代理类型")
	}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			conn.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if config.User != "" || config.Password != "" {
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(config.User+":"+config.Password)))
	}
	if err = request.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, fmt.Errorf("读取代理响应：%w", err)
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusProxyAuthRequired {
			return nil, errors.New("代理认证失败，请检查代理用户名和密码")
		}
		return nil, fmt.Errorf("HTTP 代理拒绝连接（状态码 %d）", response.StatusCode)
	}
	if !stop() || ctx.Err() != nil {
		return nil, ctx.Err()
	}
	conn.SetDeadline(time.Time{})
	ready = true
	// CONNECT may arrive in the same read as the SSH banner. Preserve read-ahead.
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
