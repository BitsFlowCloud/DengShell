package updateproxy

import (
	"net/http"
	"net/url"
	"strings"
)

func systemProxy(req *http.Request) (*url.URL, error) {
	data, err := proxyCommand(req.Context(), "/usr/sbin/scutil", "--proxy")
	if err != nil {
		return nil, err
	}
	return darwinProxy(req.URL, string(data))
}

func darwinProxy(target *url.URL, data string) (*url.URL, error) {
	values := map[string]string{}
	var bypass []string
	inExceptions := false
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ExceptionsList :") {
			inExceptions = true
			continue
		}
		if line == "}" {
			inExceptions = false
			continue
		}
		key, value, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		if inExceptions {
			bypass = append(bypass, value)
		} else {
			values[key] = value
		}
	}
	if values["ExcludeSimpleHostnames"] == "1" {
		bypass = append(bypass, "<local>")
	}
	if bypassed(target, strings.Join(bypass, ";")) {
		return nil, nil
	}
	if values["ProxyAutoConfigEnable"] == "1" || values["ProxyAutoDiscoveryEnable"] == "1" {
		return nil, errPAC
	}
	key, scheme := strings.ToUpper(target.Scheme), "http"
	if values[key+"Enable"] != "1" {
		key, scheme = "SOCKS", "socks5h"
	}
	if values[key+"Enable"] != "1" {
		return nil, nil
	}
	address, err := hostProxy(scheme, values[key+"Proxy"], values[key+"Port"])
	if err != nil {
		return nil, err
	}
	return parseProxy(address, scheme)
}
