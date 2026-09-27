package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

func TestRDPProfilePersistenceAndCredentialIsolation(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "RDP test", Protocol: "rdp", Host: "desktop.invalid", User: "Administrator", Domain: "EXAMPLE", Secret: "private-rdp-password", RDPClipboard: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Port != 3389 || p.Secret != "" || !p.HasSecret || p.Auth != "password" {
		t.Fatalf("incorrect public profile: %+v", p)
	}
	again, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := again.Get(p.ID)
	if err != nil || got.Domain != "EXAMPLE" || !got.RDPClipboard || got.Secret != "private-rdp-password" {
		t.Fatal("RDP configuration did not roundtrip", err)
	}
	got.Protocol = "ssh"
	got.Secret = ""
	got.Auth = "password"
	p, err = again.Save(got, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.HasSecret || p.Protocol != "" || p.Domain != "" || p.RDPClipboard {
		t.Fatal("RDP credential or settings leaked into SSH profile")
	}
	for _, bad := range []Profile{
		{Name: "bad", Protocol: "rdp", Host: "localhost", User: "u", Secret: "x\n/cert:ignore"},
		{Name: "bad", Protocol: "rdp", Host: "localhost", User: "u", Domain: "x\n/cert:ignore"},
		{Name: "bad", Protocol: "rdp", Host: "localhost", User: "u", Auth: "agent"},
		{Name: "bad", Protocol: "unknown", Host: "localhost", User: "u"},
	} {
		if _, err := again.Save(bad, false); err == nil {
			t.Fatal("accepted invalid RDP config")
		}
	}
}

func TestRDPSyncDoesNotReuseSSHCredentials(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "sync fixture", Host: "fixture.invalid", User: "root", Secret: "ssh-only-secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.syncProjection(false)
	if err != nil {
		t.Fatal(err)
	}
	next := make(map[string]json.RawMessage, len(before))
	for k, v := range before {
		next[k] = v
	}
	var remote Profile
	if err = json.Unmarshal(before["server/"+p.ID], &remote); err != nil {
		t.Fatal(err)
	}
	remote.Protocol = "rdp"
	remote.Port = 3389
	remote.Domain = "EXAMPLE"
	remote.RDPClipboard = true
	next["server/"+p.ID] = syncRaw(remote)
	if err = s.applySync(before, next, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(p.ID)
	if err != nil || got.Secret != "" || got.Protocol != "rdp" || got.Domain != "EXAMPLE" || !got.RDPClipboard {
		t.Fatal("RDP sync failed credential isolation", err)
	}
}

func rdpTestDial(b *rdpBridge, token, target string) (net.Conn, error) {
	d, err := proxy.SOCKS5("tcp", b.listener.Addr().String(), &proxy.Auth{User: "dengshell", Password: token}, &net.Dialer{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return d.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
}

func TestRDPBridgeHTTPRemoteDNSAuthAndScope(t *testing.T) {
	seen := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Host + " " + r.Header.Get("Proxy-Authorization")
		c, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(c, rw)
	}))
	defer upstream.Close()
	host, portText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	b, err := newRDPBridge(context.Background(), "proxy-dns-only.invalid:3389", ProxyConfig{Type: "http", Host: host, Port: port, User: "proxy-user", Password: "p:@ secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	for _, tc := range []struct{ token, target string }{{"wrong", b.target}, {b.token, "elsewhere.invalid:3389"}, {b.token, "proxy-dns-only.invalid:22"}} {
		if c, e := rdpTestDial(b, tc.token, tc.target); e == nil {
			c.Close()
			t.Fatal("unauthorized tunnel accepted")
		}
	}
	select {
	case <-seen:
		t.Fatal("unauthorized requests reached upstream")
	default:
	}
	c, err := rdpTestDial(b, b.token, b.target)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	want := b.target + " Basic " + base64.StdEncoding.EncodeToString([]byte("proxy-user:p:@ secret"))
	if got := <-seen; got != want {
		t.Fatalf("proxy request mismatch: %q", got)
	}
	_ = c.SetDeadline(time.Now().Add(time.Second))
	_, _ = c.Write([]byte("rdp-protocol-bytes"))
	buf := make([]byte, 18)
	if _, err = io.ReadFull(c, buf); err != nil || string(buf) != "rdp-protocol-bytes" {
		t.Fatalf("tunnel echo: %q %v", buf, err)
	}
	b.close()
	if _, err = c.Read(buf); err == nil {
		t.Fatal("bridge close did not close the live transport")
	}
}

func TestRDPBridgeSOCKSUpstreamAndFailureNoFallback(t *testing.T) {
	// Another scoped bridge serves as the authenticated upstream SOCKS fixture.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		accepted <- struct{}{}
		_, _ = io.Copy(c, c)
	}()
	outer, err := newRDPBridge(context.Background(), l.Addr().String(), ProxyConfig{Type: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	defer outer.close()
	host, pt, _ := net.SplitHostPort(outer.listener.Addr().String())
	port, _ := strconv.Atoi(pt)
	inner, err := newRDPBridge(context.Background(), l.Addr().String(), ProxyConfig{Type: "socks5", Host: host, Port: port, User: "dengshell", Password: outer.token})
	if err != nil {
		t.Fatal(err)
	}
	defer inner.close()
	c, err := rdpTestDial(inner, inner.token, inner.target)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-accepted
	_ = c.SetDeadline(time.Now().Add(time.Second))
	_, _ = c.Write([]byte("hello"))
	buf := make([]byte, 5)
	if _, err = io.ReadFull(c, buf); err != nil || string(buf) != "hello" {
		t.Fatal("SOCKS upstream failed", err)
	}
	inner.close()
	outer.close()
	failed, err := newRDPBridge(context.Background(), l.Addr().String(), ProxyConfig{Type: "socks5", Host: host, Port: port, User: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	defer failed.close()
	if c, err = rdpTestDial(failed, failed.token, failed.target); err == nil {
		c.Close()
		t.Fatal("failed proxy fell back to direct")
	}
	if !strings.Contains(failed.failure(), "代理") {
		t.Fatal("missing proxy-specific failure")
	}
}

func TestRDPArgumentsUseProxyAndNeverDisableTrust(t *testing.T) {
	p := Profile{Protocol: "rdp", Host: "2001:db8::4", Port: 3390, User: "user name", Name: "test 中文", Domain: "EXAMPLE"}
	args, err := rdpArguments(p, "p :/@ ' 中文", "127.0.0.1:11234", "token")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/v:[2001:db8::4]:3390\n", "/u:user name\n", "/p:p :/@ ' 中文\n", "/d:EXAMPLE\n", "/proxy:socks5://dengshell:token@127.0.0.1:11234\n", "-clipboard\n", "-multitransport\n"} {
		if !strings.Contains(args, want) {
			t.Fatal("missing option", want)
		}
	}
	for _, bad := range []string{"cert:ignore", "cert:tofu", "/drive:", "/sec:rdp"} {
		if strings.Contains(args, bad) {
			t.Fatal("unsafe default", bad)
		}
	}
	if _, err = rdpArguments(p, "p\n/cert:ignore", "127.0.0.1:1", "token"); err == nil {
		t.Fatal("argument injection accepted")
	}
}

func TestRDPHelperProcess(t *testing.T) {
	if os.Getenv("DENGSHELL_RDP_TEST_HELPER") != "1" {
		return
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil || !strings.Contains(string(b), "/p:helper-password\n") {
		os.Exit(2)
	}
	if value := os.Getenv("DENGSHELL_RDP_TEST_EXIT"); value != "" {
		code, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			os.Exit(3)
		}
		if os.Getenv("DENGSHELL_RDP_TEST_READY") == "1" {
			_, _ = os.Stdout.WriteString("DENGSHELL_RDP_READY\n")
		}
		os.Exit(code)
	}
	_, _ = os.Stdout.WriteString("DENGSHELL_RDP_READY\n")
	for {
		time.Sleep(time.Second)
	}
}
func TestRDPSessionIsolationLockAndLifecycle(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.rdp.engine = func() (string, error) { return self, nil }
	a.rdp.command = func(ctx context.Context, engine string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, engine, append([]string{"-test.run=^TestRDPHelperProcess$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "DENGSHELL_RDP_TEST_HELPER=1")
		return cmd
	}
	p, err := a.store.Save(Profile{Name: "same server", Protocol: "rdp", Host: "localhost", User: "Administrator", Secret: "helper-password"}, false)
	if err != nil {
		t.Fatal(err)
	}
	one, err := a.startRDP(p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.startRDP(p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if one.ID == two.ID {
		t.Fatal("same server reused session")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		items := a.rdpSessions()
		if len(items) == 2 && items[0].Status == "connected" && items[1].Status == "connected" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sessions failed to connect", items)
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.rdp.mu.Lock()
	first := a.rdp.sessions[one.ID]
	second := a.rdp.sessions[two.ID]
	a.rdp.mu.Unlock()
	if strings.Contains(strings.Join(first.cmd.Args, " "), "helper-password") {
		t.Fatal("password in process command line")
	}
	data, _ := json.Marshal(a.rdpSessions())
	if strings.Contains(string(data), "helper-password") {
		t.Fatal("password in public status")
	}
	first.stop("closed by test")
	if second.snapshot().Status != "connected" {
		t.Fatal("closing one session affected another")
	}
	a.securityLock.mu.Lock()
	a.securityLock.locked = true
	a.securityLock.mu.Unlock()
	if !a.SecurityLockStatus().Locked {
		t.Fatal("lock did not apply")
	}
	if second.snapshot().Status != "disconnected" {
		t.Fatal("RDP bypassed security lock")
	}
	if _, err = a.startRDP(p.ID, ""); err == nil {
		t.Fatal("RDP started while locked")
	}
	a.closeRDP()
	if first.cmd.ProcessState == nil || second.cmd.ProcessState == nil {
		t.Fatal("engine process not reaped")
	}
}

func TestRDPDisplaySizeMarkers(t *testing.T) {
	for _, tc := range []struct {
		line string
		w, h int
		ok   bool
	}{
		{"DENGSHELL_RDP_SIZE:2000:1250", 2000, 1250, true},
		{"DENGSHELL_RDP_SIZE:2560:1440", 2560, 1440, true},
		{"DENGSHELL_RDP_SIZE:0:0", 0, 0, false},
		{"DENGSHELL_RDP_SIZE:90000:1250", 0, 0, false},
		{"DENGSHELL_RDP_SIZE:2000:1250:unexpected", 0, 0, false},
		{"server-text:2000:1250", 0, 0, false},
		{"DENGSHELL_RDP_SIZE:x:1250", 0, 0, false},
	} {
		w, h, ok := rdpDisplaySize(tc.line)
		if ok != tc.ok || (ok && (w != tc.w || h != tc.h)) {
			t.Errorf("unexpected dimensions from %q", tc.line)
		}
	}
}
