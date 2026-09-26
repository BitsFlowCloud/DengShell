package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

func TestStorePersistenceAndSecrets(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "Lab", Host: "localhost", Port: 22, User: "tester", Group: "分组", Secret: "test-secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Secret != "" || !p.HasSecret {
		t.Fatal("secret leaked or lost")
	}
	p.Name = "Renamed"
	if _, err = s.Save(p, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Group("rename", "分组", "新分组"); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := s.Get(p.ID)
	if saved.Secret != "test-secret" || saved.Group != "新分组" {
		t.Fatal("persistence failed")
	}
	data, _ := json.Marshal(s.List())
	if bytes.Contains(data, []byte("test-secret")) {
		t.Fatal("secret in API response")
	}
	info, _ := os.Stat(filepath.Join(dir, EncryptedConfigName))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("config permissions")
	}
	if err = s.Group("delete", "新分组", ""); err == nil {
		t.Fatal("nonempty group deleted")
	}
	if _, err = s.Save(saved, true); err != nil {
		t.Fatal(err)
	}
	saved, _ = s.Get(p.ID)
	if saved.Secret != "" {
		t.Fatal("secret not cleared")
	}
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewSignerFromKey(private)
	if err = s.hostKey("localhost:22", nil, key.PublicKey()); err == nil {
		t.Fatal("unknown host key accepted without confirmation")
	}
	if err = s.hostKeyWithApproval("localhost:22", key.PublicKey(), &HostKeyApproval{Host: "localhost:22", Fingerprint: ssh.FingerprintSHA256(key.PublicKey())}); err != nil {
		t.Fatal(err)
	}
	if err = s.hostKey("localhost:22", nil, key.PublicKey()); err != nil {
		t.Fatal(err)
	}
	_, private, _ = ed25519.GenerateKey(rand.Reader)
	other, _ := ssh.NewSignerFromKey(private)
	var changed *HostKeyError
	if err = s.hostKey("localhost:22", nil, other.PublicKey()); !errors.As(err, &changed) || changed.Code() != "ssh_host_key_changed" {
		t.Fatal("changed host key did not require confirmation", err)
	}
	if err = s.hostKeyWithApproval("localhost:22", other.PublicKey(), &HostKeyApproval{Host: "localhost:22", Fingerprint: changed.Fingerprint, PreviousFingerprint: changed.PreviousFingerprint}); err != nil {
		t.Fatal(err)
	}
	if s.config.HostKeys["localhost:22"] != ssh.FingerprintSHA256(other.PublicKey()) {
		t.Fatal("updated host key was not recorded")
	}
	if err = s.ResetHostKey(saved); err != nil {
		t.Fatal(err)
	}
	if err = s.hostKey("localhost:22", nil, other.PublicKey()); err == nil {
		t.Fatal("reset host key should require fresh confirmation")
	}
}

func TestMonitorParsing(t *testing.T) {
	data := `__CS_OS__
PRETTY_NAME="Test Linux"
__CS_CPUINFO__
processor : 0
model name : Test CPU
__CS_STAT__
cpu 100 20 30 400 50 10 5 15 99 99
__CS_MEM__
MemTotal: 1000 kB
MemAvailable: 400 kB
SwapTotal: 100 kB
SwapFree: 75 kB
__CS_NET__
lo: 999 0 0 0 0 0 0 0 999
eth0: 123 0 0 0 0 0 0 0 456
__CS_BLOCKS__
sda loop0
__CS_DISKIO__
8 0 sda 1 0 10 0 1 0 20 0
8 1 sda1 1 0 10 0 1 0 20 0
__CS_DF__
Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/sda1 1000 200 750 20% /
overlay 1000 200 750 20% /var/lib/docker/overlay2/foo/merged
__CS_PROCESS__
100 1.2 bash
__CS_END__`
	r, err := parseStats([]byte(data), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.cpuTotal != 630 || r.cpuIdle != 450 || r.MemoryUsed != 600*1024 || r.SwapUsed != 25*1024 || r.rx != 123 || r.tx != 456 || r.read != 5120 || r.write != 10240 || len(r.Disks) != 1 {
		t.Fatalf("incorrect metrics: %+v", r)
	}
	if _, err = parseStats([]byte("not linux"), time.Now()); err == nil {
		t.Fatal("missing proc accepted")
	}
	if delta(10, 20) != 0 {
		t.Fatal("counter reset underflow")
	}
}

// Integration uses a dedicated local sshd and scratch directory, never user servers.
func TestLocalSSHIntegration(t *testing.T) {
	key := os.Getenv("CLOUDSHELL_TEST_KEY")
	if key == "" {
		t.Skip("set CLOUDSHELL_TEST_KEY and CLOUDSHELL_TEST_REMOTE for local sshd on port 19225")
	}
	remote := os.Getenv("CLOUDSHELL_TEST_REMOTE")
	if !filepath.IsAbs(remote) || !strings.Contains(remote, "cloudshell-qa") {
		t.Fatal("dedicated scratch directory required")
	}
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Start("127.0.0.1:0", fstest.MapFS{"index.html": {Data: []byte("test")}}); err != nil {
		t.Fatal(err)
	}
	p, err := a.store.Save(Profile{Name: "Test", Host: "127.0.0.1", Port: 19225, User: "bitsflow", Group: "QA", Auth: "key", KeyPath: key}, false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := connectLocalSSHFixture(t, a, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.disconnect(s.ID)
	resp, err := http.Get(a.URL() + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("API lacks authentication")
	}
	ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(a.URL(), "http:", "ws:", 1)+"/api/sessions/"+s.ID+"/terminal?token="+a.Token()+"&cols=100&rows=30", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.TextMessage && bytes.Contains(data, []byte(`"ready"`)) {
			break
		}
	}
	ws.WriteJSON(map[string]any{"type": "resize", "cols": 93, "rows": 27})
	ws.WriteJSON(map[string]string{"type": "input", "data": "stty size; printf 'SSH-%s\\n' 'VERIFIED'\r"})
	var output strings.Builder
	for !strings.Contains(output.String(), "27 93\r\n") || !strings.Contains(output.String(), "SSH-VERIFIED\r\n") {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("terminal: %v; %s", err, output.String())
		}
		if kind == websocket.BinaryMessage {
			output.Write(data)
			ws.WriteJSON(map[string]string{"type": "ack"})
		}
	}
	t.Log("PTY shell input, output and resize passed")
	for i := 0; i < 2; i++ {
		stats, err := s.Stats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if stats.MemoryTotal == 0 || len(stats.Disks) == 0 || i == 1 && !stats.SampleReady {
			t.Fatal("invalid live stats")
		}
	}
	target := filepath.Join(remote, "go-integration.txt")
	defer os.Remove(target)
	upload := func(data string, overwrite bool) error {
		ctx, task, err := a.beginTransfer(context.Background(), s, "", target, int64(len(data)))
		if err != nil {
			return err
		}
		err = a.copyUpload(ctx, s, task, strings.NewReader(data), overwrite)
		a.finishTransfer(task, err)
		return err
	}
	os.Remove(target)
	if err = upload("first 内容", false); err != nil {
		t.Fatal(err)
	}
	if err = upload("new", false); err == nil {
		t.Fatal("collision overwritten")
	}
	if data, _ := os.ReadFile(target); string(data) != "first 内容" {
		t.Fatal("original changed")
	}
	if err = upload("replacement", true); err != nil {
		t.Fatal(err)
	}
	// Cancelling an overwrite must retain the original and clean up the partial file.
	ctx, task, _ := a.beginTransfer(context.Background(), s, "", target, 10000000)
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- a.copyUpload(ctx, s, task, reader, true) }()
	if _, err = writer.Write(bytes.Repeat([]byte("x"), 32768)); err != nil {
		t.Fatal(err)
	}
	task.cancel()
	writer.CloseWithError(context.Canceled)
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("cancel succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel hung")
	}
	a.finishTransfer(task, err)
	if data, _ := os.ReadFile(target); string(data) != "replacement" {
		t.Fatal("cancelled overwrite damaged original")
	}
	parts, _ := filepath.Glob(filepath.Join(remote, ".go-integration.txt.cloudshell-*.part"))
	if len(parts) > 0 {
		t.Fatal("partial file left behind")
	}
	req, _ := http.NewRequest("GET", a.URL()+"/api/sessions/"+s.ID+"/download?path="+url.QueryEscape(target), nil)
	req.Header.Set("X-CloudShell-Token", a.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	download, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(download) != "replacement" {
		t.Fatal("download differs")
	}
	t.Log("live monitoring, SFTP upload, overwrite, cancellation and download passed")
}

func TestNativeOriginRequiresToken(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Start("127.0.0.1:0", fstest.MapFS{}); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"null", "wails://wails"} {
		for _, valid := range []bool{false, true} {
			req, _ := http.NewRequest("GET", a.URL()+"/api/config", nil)
			req.Header.Set("Origin", origin)
			if valid {
				req.Header.Set("X-CloudShell-Token", a.Token())
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			want := 403
			if valid && origin != "null" {
				want = 200
			}
			if res.StatusCode != want {
				t.Fatalf("origin=%s valid=%t: %d", origin, valid, res.StatusCode)
			}
		}
	}
}
