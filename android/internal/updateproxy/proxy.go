// Package updateproxy selects a proxy for update traffic without changing SSH
// connections, process environment, or the application's default HTTP transport.
package updateproxy

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

// Proxy re-reads the environment and desktop settings for each request. In
// particular, a proxy enabled after the startup probe is used by the download.
func Proxy(req *http.Request) (*url.URL, error) {
	return resolve(req, os.Getenv, systemProxy)
}

func resolve(req *http.Request, getenv func(string) string, system func(*http.Request) (*url.URL, error)) (*url.URL, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	get := func(key string) string {
		if value := getenv(key); value != "" {
			return value
		}
		return getenv(strings.ToLower(key))
	}
	// Use Go's established NO_PROXY semantics even when the proxy comes from
	// desktop settings. Loopback also always bypasses the update proxy.
	cfg := httpproxy.Config{HTTPProxy: "http://proxy.invalid", HTTPSProxy: "http://proxy.invalid", NoProxy: get("NO_PROXY")}
	p, err := cfg.ProxyFunc()(req.URL)
	if err != nil || p == nil {
		return nil, err
	}
	key := "HTTP_PROXY"
	if req.URL.Scheme == "https" {
		key = "HTTPS_PROXY"
	}
	address := get(key)
	if address == "" {
		address = get("ALL_PROXY")
	}
	if address != "" {
		return parseProxy(address, "http")
	}
	return system(req)
}

func parseProxy(address, defaultScheme string) (*url.URL, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, nil
	}
	if !strings.Contains(address, "://") {
		address = defaultScheme + "://" + address
	}
	p, err := url.Parse(address)
	// Never include the original URL in errors: it may contain credentials.
	if err != nil || p.Hostname() == "" || p.Opaque != "" || (p.Path != "" && p.Path != "/") || p.RawQuery != "" || p.Fragment != "" {
		return nil, errors.New("更新代理地址无效，请检查系统代理或 HTTPS_PROXY / ALL_PROXY")
	}
	p.Scheme = strings.ToLower(p.Scheme)
	switch p.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("更新代理仅支持 HTTP、HTTPS 和 SOCKS5")
	}
	if port := p.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("更新代理端口无效")
		}
	}
	p.Path = ""
	return p, nil
}

func bypassed(target *url.URL, list string) bool {
	host := strings.ToLower(target.Hostname())
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	for _, item := range strings.FieldsFunc(strings.ToLower(list), func(r rune) bool { return r == ';' || r == ',' || r == ' ' || r == '\n' }) {
		if item == "<local>" {
			if !strings.Contains(host, ".") && net.ParseIP(host) == nil {
				return true
			}
			continue
		}
		if match, _ := path.Match(item, host); match {
			return true
		}
		if _, network, err := net.ParseCIDR(item); err == nil && network.Contains(net.ParseIP(host)) {
			return true
		}
	}
	return false
}

func hostProxy(scheme, host, port string) (string, error) {
	if host == "" || port == "" || port == "0" {
		return "", nil
	}
	p, err := parseProxy(scheme+"://"+net.JoinHostPort(strings.Trim(host, "[]"), port), scheme)
	if err != nil {
		return "", err
	}
	return p.String(), nil
}

var errPAC = errors.New("当前系统的自动代理脚本无法解析，请设置 HTTPS_PROXY / ALL_PROXY，或在系统中配置 HTTP / SOCKS5 代理")
