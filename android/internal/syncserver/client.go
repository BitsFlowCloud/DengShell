package syncserver

import (
	"bytes"
	"cloudshell/internal/syncvault"
	"cloudshell/internal/updateproxy"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	Connection syncvault.Connection
	Token      string
	HTTP       *http.Client
}

func NewClient(c syncvault.Connection, token string) (*Client, error) {
	u, e := url.Parse(c.URL)
	if e != nil || u.Scheme != "https" || net.ParseIP(u.Hostname()) == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || c.Version != 1 || !syncvault.ValidID(c.Vault) {
		return nil, errors.New("同步连接资料必须包含 HTTPS IP 地址和有效身份")
	}
	roots := x509.NewCertPool()
	if len(c.CA) > 16384 || !roots.AppendCertsFromPEM([]byte(c.CA)) {
		return nil, errors.New("同步服务证书无效")
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = updateproxy.Proxy
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}
	t.TLSHandshakeTimeout = 10 * time.Second
	t.ResponseHeaderTimeout = 15 * time.Second
	return &Client{Connection: c, Token: token, HTTP: &http.Client{Transport: t, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("同步服务不允许重定向") }}}, nil
}
func (c *Client) Close() { c.HTTP.CloseIdleConnections() }
func (c *Client) Request(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	var e error
	if input != nil {
		body, e = json.Marshal(input)
		if e != nil {
			return e
		}
	}
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.Connection.URL, "/")+path, bytes.NewReader(body))
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Content-Type", "application/json")
	resp, e := c.HTTP.Do(r)
	if e != nil {
		return errors.New("无法安全连接同步服务，请检查地址、证书、网络或代理")
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, syncvault.MaxBlob*2+1))
	if e != nil || len(b) > syncvault.MaxBlob*2 {
		return errors.New("同步服务响应无效")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var m struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &m)
		if len(m.Error) > 300 {
			m.Error = ""
		}
		if m.Error == "" {
			m.Error = fmt.Sprintf("同步请求失败 (%d)", resp.StatusCode)
		}
		return errors.New(m.Error)
	}
	if raw, ok := output.(*[]byte); ok {
		*raw = b
		return nil
	}
	if output != nil {
		return json.Unmarshal(b, output)
	}
	return nil
}
func (c *Client) Metadata(ctx context.Context) (syncvault.Metadata, error) {
	var m syncvault.Metadata
	e := c.Request(ctx, "GET", "/v1/meta", nil, &m)
	return m, e
}
func (c *Client) Create(context.Context, syncvault.Metadata) error {
	return errors.New("自建同步空间需要使用初始化流程")
}
func (c *Client) List(ctx context.Context) ([]syncvault.Object, error) {
	var o []syncvault.Object
	e := c.Request(ctx, "GET", "/v1/snapshots", nil, &o)
	return o, e
}
func (c *Client) Read(ctx context.Context, o syncvault.Object) ([]byte, error) {
	if !syncvault.ValidID(o.ID) {
		return nil, errors.New("快照编号无效")
	}
	var b []byte
	e := c.Request(ctx, "GET", "/v1/snapshots/"+o.ID, nil, &b)
	return b, e
}
func (c *Client) Write(ctx context.Context, o syncvault.Object, b []byte) error {
	return c.Request(ctx, "POST", "/v1/snapshots", map[string]any{"object": o, "data": b}, nil)
}
