package updateproxy

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSOCKS5UpdateUsesProxyDNS(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "verified-package") }))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	observed := make(chan string, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- func() error {
			conn, err := listener.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			header := make([]byte, 2)
			if _, err = io.ReadFull(conn, header); err != nil {
				return err
			}
			methods := make([]byte, int(header[1]))
			if _, err = io.ReadFull(conn, methods); err != nil {
				return err
			}
			if _, err = conn.Write([]byte{5, 0}); err != nil {
				return err
			}
			request := make([]byte, 5)
			if _, err = io.ReadFull(conn, request); err != nil {
				return err
			}
			if request[0] != 5 || request[1] != 1 || request[3] != 3 {
				observed <- "expected remote DNS hostname"
				return nil
			}
			name := make([]byte, int(request[4])+2)
			if _, err = io.ReadFull(conn, name); err != nil {
				return err
			}
			observed <- string(name[:len(name)-2])
			up, err := net.Dial("tcp", origin.Listener.Addr().String())
			if err != nil {
				return err
			}
			defer up.Close()
			if _, err = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80}); err != nil {
				return err
			}
			done := make(chan struct{})
			go func() { io.Copy(up, conn); close(done) }()
			io.Copy(conn, up)
			conn.Close()
			<-done
			return nil
		}()
	}()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "")
	}
	t.Setenv("ALL_PROXY", "socks5h://"+listener.Addr().String())
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = Proxy
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Isolated TLS fixture only.
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	res, err := client.Get("https://update.invalid/package")
	if err != nil {
		tr.CloseIdleConnections()
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	tr.CloseIdleConnections()
	if err != nil || string(body) != "verified-package" {
		t.Fatalf("SOCKS package: %q %v", body, err)
	}
	if host := <-observed; host != "update.invalid" {
		t.Fatalf("SOCKS remote DNS: %q", host)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}
