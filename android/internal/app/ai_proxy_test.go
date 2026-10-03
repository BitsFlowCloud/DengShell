package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func aiFixtureProxy(t *testing.T, endpoint, kind, user, password string) aiProxy {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return aiProxy{Type: kind, Host: u.Hostname(), Port: port, User: user, Password: password}
}

func aiFixtureHTTP(t *testing.T, mux *http.ServeMux, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", path, bytes.NewReader(body)))
	return w
}

func TestAIEmptyAndIndependentConfigurationSecrets(t *testing.T) {
	a := lockTestApp(t)
	s, err := a.loadAISettings()
	if err != nil || s.Provider != "" || len(s.Providers) != 0 || s.Timeout != 120 {
		t.Fatal("unexpected initial settings", err)
	}
	raw, _ := json.Marshal(aiPublicSettings(s))
	if string(raw) != `{"provider":"","providers":[],"timeout":120}` {
		t.Fatalf("unexpected public defaults: %s", raw)
	}
	if _, err := a.saveAISettings(s); err != nil {
		t.Fatal("empty settings cannot be saved", err)
	}
	s = aiTestSettings("https://shared.invalid/v1")
	s.Providers[0].Proxy = aiProxy{Type: "http", Host: "proxy.invalid", Port: 8080, User: "fixture-user", Password: "password-one"}
	second := s.Providers[0]
	second.ID = "second"
	second.APIKey = "token-two"
	second.Proxy.Password = "password-two"
	s.Providers = append(s.Providers, second)
	public, err := a.saveAISettings(s)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(public)
	for _, secret := range []string{s.Providers[0].APIKey, "token-two", "password-one", "password-two", `"password":`, `"apiKey":`} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("public settings contain secret")
		}
	}
	for _, p := range public.Providers {
		if !p.HasKey || !p.Proxy.HasPassword {
			t.Fatal("missing secret metadata")
		}
	}
	if _, err := a.saveAISettings(public); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.loadAISettings()
	if loaded.Providers[0].APIKey != s.Providers[0].APIKey || loaded.Providers[1].APIKey != "token-two" || loaded.Providers[0].Proxy.Password != "password-one" || loaded.Providers[1].Proxy.Password != "password-two" {
		t.Fatal("same-endpoint configurations did not retain independent secrets")
	}
	disk, _ := os.ReadFile(filepath.Join(a.store.dir, aiSettingsFile))
	ordinary, _ := json.Marshal(a.store.List())
	for _, secret := range []string{s.Providers[0].APIKey, "token-two", "password-one", "password-two", "proxy.invalid"} {
		if bytes.Contains(disk, []byte(secret)) || bytes.Contains(ordinary, []byte(secret)) {
			t.Fatal("AI secret persisted outside encrypted AI settings")
		}
	}
	public.Providers[0].ClearKey, public.Providers[0].Proxy.ClearPassword = true, true
	public, err = a.saveAISettings(public)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.loadAISettings()
	if loaded.Providers[0].APIKey != "" || loaded.Providers[0].Proxy.Password != "" || loaded.Providers[1].APIKey != "token-two" || loaded.Providers[1].Proxy.Password != "password-two" {
		t.Fatal("clear affected another configuration")
	}
	// API and proxy credential retention have independent endpoint boundaries.
	public.Providers[1].BaseURL = "https://changed.invalid/v1"
	public, err = a.saveAISettings(public)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.loadAISettings()
	if loaded.Providers[1].APIKey != "" || loaded.Providers[1].Proxy.Password != "password-two" {
		t.Fatal("API endpoint change crossed credential boundary")
	}
	public.Providers[1].Proxy.User = "different-user"
	public, err = a.saveAISettings(public)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.loadAISettings()
	if loaded.Providers[1].Proxy.Password != "" {
		t.Fatal("proxy password followed a different identity")
	}
	for _, mutate := range []func(*aiProxy){func(p *aiProxy) { p.Host = "other.invalid" }, func(p *aiProxy) { p.Port++ }, func(p *aiProxy) { p.Type = "socks5" }} {
		old := aiSettings{Providers: []aiProvider{{ID: "fixture", Proxy: aiProxy{Type: "http", Host: "old.invalid", Port: 80, User: "user", Password: "do-not-inherit"}}}}
		p := old.Providers[0]
		p.Proxy.Password = ""
		mutate(&p.Proxy)
		if aiMergeSecrets(p, old).Proxy.Password != "" {
			t.Fatal("password followed changed proxy endpoint")
		}
	}
	if _, err := a.saveAISettings(aiSettings{Timeout: 120}); err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.loadAISettings()
	if len(loaded.Providers) != 0 || loaded.Provider != "" {
		t.Fatal("deleting last configuration failed")
	}
	blank := aiTestSettings("https://shared.invalid/v1")
	blank.Providers[0].APIKey = ""
	if _, err := a.saveAISettings(blank); err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.loadAISettings()
	if loaded.Providers[0].APIKey != "" {
		t.Fatal("deleted token resurrected")
	}
}

func TestAILegacySettingsPreservedAndProxyValidation(t *testing.T) {
	a := lockTestApp(t)
	legacy := []byte(`{"provider":"kimi","providers":[{"id":"kimi","name":"My existing config","format":"openai","baseURL":"https://legacy.invalid/v1","apiKey":"legacy-fixture-token"}],"timeout":120}`)
	cipher, _ := configCipher(a.store.key)
	if err := atomicConfigFile(filepath.Join(a.store.dir, aiSettingsFile), cipher.Seal(nil, nil, legacy, []byte(aiSettingsAAD))); err != nil {
		t.Fatal(err)
	}
	s, err := a.loadAISettings()
	if err != nil || len(s.Providers) != 1 || s.Providers[0].ID != "kimi" || s.Providers[0].APIKey != "legacy-fixture-token" || s.Providers[0].Proxy.Type != "system" {
		t.Fatal("existing user configuration lost", err)
	}
	for _, kind := range []string{"system", "direct"} {
		p := aiProxy{Type: kind, Host: "old", Port: 80, User: "old-user", Password: "old-password", HasPassword: true, ClearPassword: true}
		if err := aiNormalizeProxy(&p); err != nil || p != (aiProxy{Type: kind}) {
			t.Fatal("inactive proxy secrets retained")
		}
	}
	for _, p := range []aiProxy{{Type: "ssh"}, {Type: "http", Host: "http://bad.invalid", Port: 80}, {Type: "http", Host: "good.invalid", Port: 65536}, {Type: "http", Host: "bad.invalid", Port: 80, User: "bad:user"}, {Type: "socks5", Host: "good.invalid", Port: 80, Password: strings.Repeat("x", 256)}, {Type: "http", Host: "good.invalid", Port: 80, Password: "bad\nsecret"}} {
		if aiNormalizeProxy(&p) == nil {
			t.Fatal("invalid proxy accepted")
		}
	}
	p := aiProxy{Type: "http", Host: "[::1]", Port: 80}
	if err := aiNormalizeProxy(&p); err != nil || p.Host != "::1" {
		t.Fatal("IPv6 proxy rejected")
	}
	limits := aiSettings{Provider: "p0", Timeout: 120}
	for i := 0; i < 24; i++ {
		limits.Providers = append(limits.Providers, aiProvider{ID: fmt.Sprintf("p%d", i), Format: "openai"})
	}
	if err := aiValidateSettings(&limits); err != nil {
		t.Fatal("24 configurations rejected", err)
	}
	limits.Providers = append(limits.Providers, aiProvider{ID: "p24", Format: "openai"})
	if aiValidateSettings(&limits) == nil {
		t.Fatal("more than 24 configurations accepted")
	}
}

func TestAIHTTPProxyDraftAndChatAuthentication(t *testing.T) {
	a := lockTestApp(t)
	var savedCalls, draftCalls atomic.Int32
	proxy := func(user, password, token string, calls *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
			if r.Header.Get("Proxy-Authorization") != want || r.Header.Get("Authorization") != "Bearer "+token || r.URL.Host != "ai-fixture.invalid" {
				t.Error("wrong isolated proxy route or credentials")
			}
			if strings.HasSuffix(r.URL.Path, "/models") {
				io.WriteString(w, `{"data":[{"id":"proxy-model"}]}`)
			} else {
				io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"via-proxy"}}]}`)
			}
		}))
	}
	saved := proxy("saved-user", "saved-password", "ai-fixture-secret-123", &savedCalls)
	defer saved.Close()
	draft := proxy("draft-user", "draft-password", "draft-token", &draftCalls)
	defer draft.Close()
	s := aiTestSettings("http://ai-fixture.invalid/v1")
	s.Providers[0].Proxy = aiFixtureProxy(t, saved.URL, "http", "saved-user", "saved-password")
	public, err := a.saveAISettings(s)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.registerAIHTTP(mux)
	d := public.Providers[0]
	d.APIKey = "draft-token"
	d.Proxy = aiFixtureProxy(t, draft.URL, "http", "draft-user", "draft-password")
	w := aiFixtureHTTP(t, mux, "/api/ai/models", map[string]any{"provider": d})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "proxy-model") {
		t.Fatal("draft route failed", w.Code, w.Body.String())
	}
	w = aiFixtureHTTP(t, mux, "/api/ai/models", map[string]any{"provider": public.Providers[0]})
	if w.Code != 200 {
		t.Fatal("saved model proxy credentials not merged", w.Body.String())
	}
	w = aiFixtureHTTP(t, mux, "/api/ai/chat", aiTestInput())
	if w.Code != 200 || !strings.Contains(w.Body.String(), "via-proxy") || savedCalls.Load() != 2 || draftCalls.Load() != 1 {
		t.Fatal("saved chat route failed", w.Body.String())
	}
	loaded, _ := a.loadAISettings()
	if loaded.Providers[0].APIKey != "ai-fixture-secret-123" || loaded.Providers[0].Proxy.Password != "saved-password" {
		t.Fatal("draft discovery altered saved secrets")
	}
}

func TestAIProxyDirectSystemAndNoFallback(t *testing.T) {
	var systemCalls, originCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		systemCalls.Add(1)
		io.WriteString(w, `{"data":[{"id":"system"}]}`)
	}))
	defer proxy.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		io.WriteString(w, `{"data":[{"id":"direct"}]}`)
	}))
	defer origin.Close()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTP_PROXY", proxy.URL)
	for _, kind := range []string{"system", "direct"} {
		p := aiTestSettings("http://ai-fixture.invalid/v1").Providers[0]
		p.Proxy.Type = kind
		client, err := aiHTTPClient(p)
		if err != nil {
			t.Fatal(err)
		}
		if kind == "direct" {
			client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address == "ai-fixture.invalid:80" {
					address = origin.Listener.Addr().String()
				}
				return (&net.Dialer{}).DialContext(ctx, network, address)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		models, err := aiListModels(ctx, client, p)
		cancel()
		client.CloseIdleConnections()
		if err != nil || len(models) != 1 || models[0] != kind {
			t.Fatal("unexpected route", kind, err)
		}
	}
	if originCalls.Load() != 1 || systemCalls.Load() != 1 || os.Getenv("HTTP_PROXY") != proxy.URL {
		t.Fatal("direct consulted system proxy or environment changed")
	}
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := dead.URL
	dead.Close()
	p := aiTestSettings(origin.URL).Providers[0]
	p.Proxy = aiFixtureProxy(t, address, "http", "user", "sensitive-proxy-password")
	client, err := aiHTTPClient(p)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = aiListModels(ctx, client, p)
	if err == nil || strings.Contains(err.Error(), p.Proxy.Password) || originCalls.Load() != 1 || systemCalls.Load() != 1 {
		t.Fatal("broken explicit proxy fell back or leaked credentials")
	}
}

func TestAIProxyErrorsAndCancellation(t *testing.T) {
	p := aiTestSettings("http://ai-fixture.invalid").Providers[0]
	p.Proxy = aiProxy{Type: "http", Host: "unused", Port: 80, User: "user", Password: "pass/word+secret"}
	encoded := base64.StdEncoding.EncodeToString([]byte(p.Proxy.User + ":" + p.Proxy.Password))
	body, _ := json.Marshal(map[string]string{"message": p.APIKey + " " + p.Proxy.Password + " " + url.QueryEscape(p.Proxy.Password) + " " + encoded})
	for _, code := range []int{400, 407} {
		err := aiAPIError(code, body, aiProviderSecrets(p)...)
		for _, secret := range []string{p.APIKey, p.Proxy.Password, url.QueryEscape(p.Proxy.Password), encoded} {
			if strings.Contains(err.Error(), secret) {
				t.Fatal("error leaked credentials")
			}
		}
	}
	for _, action := range []string{"cancel", "lock"} {
		t.Run(action, func(t *testing.T) {
			a := lockTestApp(t)
			started, stopped := make(chan struct{}), make(chan struct{})
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				close(started)
				<-r.Context().Done()
				close(stopped)
			}))
			defer proxy.Close()
			defer proxy.CloseClientConnections()
			s := aiTestSettings("http://ai-fixture.invalid/v1")
			s.Providers[0].Proxy = aiFixtureProxy(t, proxy.URL, "http", "user", "password")
			if _, err := a.saveAISettings(s); err != nil {
				t.Fatal(err)
			}
			if action == "lock" {
				enableTestPassword(t, a, 0)
			}
			mux := http.NewServeMux()
			a.registerAIHTTP(mux)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- aiFixtureHTTP(t, mux, "/api/ai/chat", aiTestInput()) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("proxy not reached")
			}
			if action == "lock" {
				if _, err := a.LockNow(); err != nil {
					t.Fatal(err)
				}
			} else {
				a.cancelAIRequest("test-request")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("proxy request not cancelled")
			}
			select {
			case w := <-done:
				if w.Code == 200 {
					t.Fatal("cancelled request succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled handler blocked")
			}
		})
	}
}

func TestAISystemProxyErrorBodiesCannotExposeCredentials(t *testing.T) {
	for _, code := range []int{400, 502} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			const password = "system-fixture/pass+secret"
			encoded := base64.StdEncoding.EncodeToString([]byte("system-user:" + password))
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "Basic "+encoded {
					t.Error("system proxy authentication missing")
				}
				w.WriteHeader(code)
				json.NewEncoder(w).Encode(map[string]string{"message": "remote-error-marker " + password + " " + url.QueryEscape(password) + " " + encoded})
			}))
			defer proxy.Close()
			for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
				t.Setenv(key, "")
			}
			u, _ := url.Parse(proxy.URL)
			u.User = url.UserPassword("system-user", password)
			t.Setenv("HTTP_PROXY", u.String())
			p := aiTestSettings("http://ai-fixture.invalid/v1").Providers[0]
			client, err := aiHTTPClient(p)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			_, err = aiListModels(context.Background(), client, p)
			if err == nil || !strings.Contains(err.Error(), "HTTP "+strconv.Itoa(code)) {
				t.Fatal("missing safe HTTP error")
			}
			for _, secret := range []string{password, url.QueryEscape(password), encoded, "remote-error-marker"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("system proxy response exposed credentials or arbitrary body")
				}
			}
		})
	}
}

func TestAIHTTPProxyCONNECTAuthentication(t *testing.T) {
	var connected atomic.Bool
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ai-fixture-secret-123" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("origin credential headers incorrect")
		}
		io.WriteString(w, `{"data":[{"id":"connect-model"}]}`)
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("connect-user:connect-password"))
		if r.Method != "CONNECT" || r.Host != "ai-fixture.invalid:443" || r.Header.Get("Proxy-Authorization") != want {
			t.Error("incorrect authenticated CONNECT request")
			w.WriteHeader(407)
			return
		}
		up, err := net.DialTimeout("tcp", origin.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Error("fixture origin unavailable")
			w.WriteHeader(502)
			return
		}
		defer up.Close()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error("fixture hijack failed")
			return
		}
		defer conn.Close()
		connected.Store(true)
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{})
		go func() { io.Copy(up, conn); close(done) }()
		io.Copy(conn, up)
		conn.Close()
		<-done
	}))
	defer proxy.Close()
	p := aiTestSettings("https://ai-fixture.invalid/v1").Providers[0]
	p.Proxy = aiFixtureProxy(t, proxy.URL, "http", "connect-user", "connect-password")
	client, err := aiHTTPClient(p)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	client.Transport.(*http.Transport).TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	// Keep certificate verification against the isolated origin's identity;
	// the unresolved request hostname exists only to prove proxy routing.
	client.Transport.(*http.Transport).TLSClientConfig.ServerName = "127.0.0.1"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	models, err := aiListModels(ctx, client, p)
	if err != nil || len(models) != 1 || models[0] != "connect-model" || !connected.Load() {
		t.Fatal("HTTPS proxy tunnel failed", err)
	}
}

func TestAISOCKS5ProxyAuthenticationAndRemoteDNS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan error, 1)
	go func() {
		finished <- func() error {
			conn, err := listener.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			header := make([]byte, 2)
			if _, err := io.ReadFull(conn, header); err != nil {
				return err
			}
			methods := make([]byte, int(header[1]))
			if _, err := io.ReadFull(conn, methods); err != nil {
				return err
			}
			if !bytes.Contains(methods, []byte{2}) {
				return fmt.Errorf("SOCKS authentication not offered")
			}
			conn.Write([]byte{5, 2})
			if _, err := io.ReadFull(conn, header); err != nil {
				return err
			}
			user := make([]byte, int(header[1]))
			if _, err := io.ReadFull(conn, user); err != nil {
				return err
			}
			length := make([]byte, 1)
			if _, err := io.ReadFull(conn, length); err != nil {
				return err
			}
			password := make([]byte, int(length[0]))
			if _, err := io.ReadFull(conn, password); err != nil {
				return err
			}
			if string(user) != "socks-user" || string(password) != "socks-password" {
				return fmt.Errorf("wrong SOCKS credentials")
			}
			conn.Write([]byte{1, 0})
			request := make([]byte, 5)
			if _, err := io.ReadFull(conn, request); err != nil {
				return err
			}
			if request[0] != 5 || request[1] != 1 || request[3] != 3 {
				return fmt.Errorf("expected SOCKS remote DNS")
			}
			name := make([]byte, int(request[4])+2)
			if _, err := io.ReadFull(conn, name); err != nil {
				return err
			}
			if string(name[:len(name)-2]) != "ai-fixture.invalid" {
				return fmt.Errorf("wrong SOCKS destination")
			}
			conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
			// Serve a single ordinary HTTP response through the SOCKS tunnel.
			r, err := http.ReadRequest(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			if r.Header.Get("Authorization") != "Bearer ai-fixture-secret-123" || r.Header.Get("Proxy-Authorization") != "" {
				return fmt.Errorf("SOCKS tunnel credential headers incorrect")
			}
			body := `{"data":[{"id":"socks-ok"}]}`
			_, err = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
			return err
		}()
	}()
	p := aiTestSettings("http://ai-fixture.invalid/v1").Providers[0]
	p.Proxy = aiFixtureProxy(t, "http://"+listener.Addr().String(), "socks5", "socks-user", "socks-password")
	client, err := aiHTTPClient(p)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	models, err := aiListModels(ctx, client, p)
	if err != nil || len(models) != 1 || models[0] != "socks-ok" {
		t.Fatal("SOCKS route failed", err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}
