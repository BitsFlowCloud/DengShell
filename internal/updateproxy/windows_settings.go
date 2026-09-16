package updateproxy

import (
	"net/url"
	"strings"
)

// Windows protocol keys describe the destination protocol, not the proxy's
// transport: https=127.0.0.1:7890 is an HTTP CONNECT proxy, not a TLS proxy.
func windowsProxy(target *url.URL, servers, bypass string) (*url.URL, error) {
	if bypassed(target, bypass) {
		return nil, nil
	}
	var fallback, selected, socks string
	for _, item := range strings.FieldsFunc(servers, func(r rune) bool { return r == ';' || r == ' ' || r == '\n' }) {
		key, value, keyed := strings.Cut(item, "=")
		if !keyed {
			if fallback == "" {
				fallback = item
			}
			continue
		}
		if strings.EqualFold(key, target.Scheme) && selected == "" {
			selected = value
		}
		if strings.EqualFold(key, "socks") && socks == "" {
			socks = value
		}
	}
	if selected != "" {
		return parseProxy(selected, "http")
	}
	if fallback != "" {
		return parseProxy(fallback, "http")
	}
	if socks != "" {
		return parseProxy(socks, "socks5h")
	}
	return nil, nil
}
