package updateproxy

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func systemProxy(req *http.Request) (*url.URL, error) {
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP") + ":" + os.Getenv("DESKTOP_SESSION"))
	if strings.Contains(desktop, "kde") || strings.Contains(desktop, "plasma") || os.Getenv("KDE_FULL_SESSION") == "true" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, errors.New("无法读取 KDE 代理配置目录")
		}
		f, err := os.Open(filepath.Join(dir, "kioslaverc"))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, errors.New("无法读取 KDE 系统代理配置")
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, (128<<10)+1))
		if err != nil || len(data) > 128<<10 {
			return nil, errors.New("无法读取 KDE 系统代理配置")
		}
		return kdeProxy(req.URL, string(data), os.Getenv)
	}
	if _, err := exec.LookPath("gsettings"); err != nil {
		return nil, nil
	}
	data, err := proxyCommand(req.Context(), "gsettings", "list-recursively", "org.gnome.system.proxy")
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		// Non-GNOME desktops and minimal installations may have gsettings but
		// no GNOME proxy schema. No desktop proxy is available in that case.
		return nil, nil
	}
	return gnomeProxy(req.URL, string(data))
}

var gvariantStrings = regexp.MustCompile(`'((?:\\.|[^'\\])*)'`)

func variantStrings(value string) []string {
	var result []string
	for _, match := range gvariantStrings.FindAllStringSubmatch(value, -1) {
		result = append(result, strings.NewReplacer(`\'`, `'`, `\\`, `\`, `\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(match[1]))
	}
	return result
}
func gnomeProxy(target *url.URL, data string) (*url.URL, error) {
	values := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
		if len(parts) == 3 {
			values[parts[0]+"."+parts[1]] = strings.TrimSpace(parts[2])
		}
	}
	get := func(key string) string {
		v := values["org.gnome.system.proxy."+key]
		if s := variantStrings(v); len(s) > 0 {
			return s[0]
		}
		return v
	}
	mode := get("mode")
	if mode == "" || mode == "none" {
		return nil, nil
	}
	if bypassed(target, strings.Join(variantStrings(values["org.gnome.system.proxy.ignore-hosts"]), ";")) {
		return nil, nil
	}
	if mode == "auto" {
		return nil, errPAC
	}
	if mode != "manual" {
		return nil, errors.New("未知的 GNOME 系统代理模式")
	}
	protocol := target.Scheme
	if get("use-same-proxy") == "true" || get(protocol+".host") == "" || get(protocol+".port") == "0" {
		protocol = "http"
	}
	address, err := hostProxy("http", get(protocol+".host"), get(protocol+".port"))
	if err != nil {
		return nil, err
	}
	if address == "" {
		address, err = hostProxy("socks5h", get("socks.host"), get("socks.port"))
		if err != nil {
			return nil, err
		}
	}
	p, err := parseProxy(address, "http")
	if err == nil && p != nil && p.Scheme == "http" && protocol == "http" && get("http.use-authentication") == "true" {
		p.User = url.UserPassword(get("http.authentication-user"), get("http.authentication-password"))
	}
	return p, err
}

func kdeProxy(target *url.URL, data string, getenv func(string) string) (*url.URL, error) {
	values := map[string]string{}
	inGroup := false
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inGroup = line == "[Proxy Settings]"
			continue
		}
		if !inGroup || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	mode := values["ProxyType"]
	if mode == "" || mode == "0" {
		return nil, nil
	}
	bypass := bypassed(target, values["NoProxyFor"])
	if values["ReversedException"] == "true" {
		bypass = !bypass
	}
	if bypass {
		return nil, nil
	}
	if mode == "2" || mode == "3" {
		return nil, errPAC
	}
	if mode != "1" && mode != "4" {
		return nil, errors.New("未知的 KDE 系统代理模式")
	}
	value, scheme := values[target.Scheme+"Proxy"], "http"
	if value == "" {
		value, scheme = values["socksProxy"], "socks5h"
	}
	if mode == "4" {
		value = getenv(value)
	}
	if strings.HasPrefix(value, "socks://") {
		value = "socks5h://" + strings.TrimPrefix(value, "socks://")
	}
	// KIO saves a proxy as "http://host port" as well as URL-form addresses.
	if fields := strings.Fields(value); len(fields) == 2 {
		u, err := parseProxy(fields[0], scheme)
		if err != nil {
			return nil, err
		}
		u.Host = net.JoinHostPort(u.Hostname(), fields[1])
		value = u.String()
	}
	return parseProxy(value, scheme)
}
