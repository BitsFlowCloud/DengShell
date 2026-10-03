package app

import (
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"cloudshell/internal/updateproxy"
)

// AI proxy credentials belong to one AI configuration and its encrypted file.
// They never refer to SSH proxy IDs, global selections or process environment.
type aiProxy struct {
	Type          string `json:"type"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	User          string `json:"user,omitempty"`
	Password      string `json:"password,omitempty"`
	HasPassword   bool   `json:"hasPassword"`
	ClearPassword bool   `json:"clearPassword,omitempty"`
}

func aiNormalizeProxy(p *aiProxy) error {
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	if p.Type == "" {
		p.Type = "system"
	}
	switch p.Type {
	case "system", "direct":
		*p = aiProxy{Type: p.Type}
		return nil
	case "http", "socks5":
	default:
		return errors.New("AI 代理类型应为系统、直连、HTTP 或 SOCKS5")
	}
	p.Host = strings.ToLower(strings.TrimSpace(p.Host))
	if strings.HasPrefix(p.Host, "[") && strings.HasSuffix(p.Host, "]") {
		p.Host = strings.TrimSuffix(strings.TrimPrefix(p.Host, "["), "]")
	}
	if p.Host == "" || len(p.Host) > 253 || strings.ContainsAny(p.Host, "/\\?#@[] \t\r\n\x00") || (strings.Contains(p.Host, ":") && net.ParseIP(p.Host) == nil) {
		return errors.New("AI 代理主机应只包含有效主机名或 IP，不含协议、端口或路径")
	}
	if ip := net.ParseIP(p.Host); ip != nil {
		p.Host = ip.String()
	}
	if p.Port < 1 || p.Port > 65535 {
		return errors.New("AI 代理端口应为 1 至 65535")
	}
	if len(p.User) > 1024 || len(p.Password) > 8192 || strings.ContainsAny(p.User+p.Password, "\x00\r\n") {
		return errors.New("AI 代理认证内容无效或过长")
	}
	if p.Type == "socks5" && (len(p.User) > 255 || len(p.Password) > 255) {
		return errors.New("SOCKS5 代理账号和密码各不能超过 255 字节")
	}
	if p.Type == "http" && strings.Contains(p.User, ":") {
		return errors.New("HTTP 代理账号不能包含冒号")
	}
	return nil
}

func aiPublicProxy(p aiProxy) aiProxy {
	if p.Type == "" {
		p.Type = "system"
	}
	p.HasPassword = p.Password != ""
	p.Password, p.ClearPassword = "", false
	return p
}

func aiSameProxy(a, b aiProxy) bool {
	return a.Type == b.Type && a.Host == b.Host && a.Port == b.Port && a.User == b.User
}

func aiHTTPClient(p aiProvider) (*http.Client, error) {
	if err := aiNormalizeProxy(&p.Proxy); err != nil {
		return nil, err
	}
	// A request owns its transport and closes idle connections on completion.
	// This avoids retaining old credentials or reusing another config's route.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	switch p.Proxy.Type {
	case "system":
		transport.Proxy = updateproxy.Proxy
	case "direct":
		transport.Proxy = nil
	default:
		proxyURL := &url.URL{Scheme: p.Proxy.Type, Host: net.JoinHostPort(p.Proxy.Host, strconv.Itoa(p.Proxy.Port))}
		if p.Proxy.User != "" || p.Proxy.Password != "" {
			proxyURL.User = url.UserPassword(p.Proxy.User, p.Proxy.Password)
		}
		// net/http implements HTTP CONNECT and SOCKS5 remote DNS with request
		// context cancellation. Explicit proxies never bypass or fall back.
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || len(via) == 0 || req.URL.Scheme != via[0].URL.Scheme || !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
			return errors.New("AI 服务重定向到了不同地址，已拒绝转发密钥")
		}
		return nil
	}}, nil
}

func aiProviderSecrets(p aiProvider) []string {
	secrets := []string{p.APIKey, p.Proxy.Password}
	if p.Proxy.Password != "" {
		secrets = append(secrets, base64.StdEncoding.EncodeToString([]byte(p.Proxy.User+":"+p.Proxy.Password)))
	}
	return secrets
}
