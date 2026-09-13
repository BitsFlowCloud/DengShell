package app

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestHTTPProxyConnect(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(strconv.FormatBool(rejected), func(t *testing.T) {
			seen := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.Method + " " + r.Host + " " + r.Header.Get("Proxy-Authorization")
				if rejected {
					w.WriteHeader(407)
					return
				}
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					return
				}
				defer conn.Close()
				// Headers and SSH data arrive together, exercising preserved read-ahead.
				rw.WriteString("HTTP/1.1 200 Connection established\r\n\r\nSSH-2.0-proxy-test\r\n")
				rw.Flush()
				io.Copy(conn, rw)
			}))
			defer server.Close()
			host, portText, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
			port, _ := strconv.Atoi(portText)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := dialSSH(ctx, "must-resolve-at-proxy.invalid:22", ProxyConfig{Type: "http", Host: host, Port: port, User: "alice", Password: "secret"})
			if rejected {
				if err == nil || !strings.Contains(err.Error(), "认证失败") {
					t.Fatalf("bad auth result: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				reader := bufio.NewReader(conn)
				line, err := reader.ReadString('\n')
				if err != nil || line != "SSH-2.0-proxy-test\r\n" {
					t.Fatalf("lost buffered banner: %q %v", line, err)
				}
				conn.Write([]byte("echo\n"))
				line, err = reader.ReadString('\n')
				if err != nil || line != "echo\n" {
					t.Fatal("tunnel unusable")
				}
			}
			expected := "CONNECT must-resolve-at-proxy.invalid:22 Basic " + base64.StdEncoding.EncodeToString([]byte("alice:secret"))
			if got := <-seen; got != expected {
				t.Fatalf("request mismatch %q", got)
			}
		})
	}
}

func TestProxyCancellation(t *testing.T) {
	for _, kind := range []string{"http", "socks5"} {
		t.Run(kind, func(t *testing.T) {
			listener, _ := net.Listen("tcp", "127.0.0.1:0")
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				io.Copy(io.Discard, conn)
			}()
			host, p, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(p)
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			start := time.Now()
			conn, err := dialSSH(ctx, "test.invalid:22", ProxyConfig{Type: kind, Host: host, Port: port})
			if conn != nil {
				conn.Close()
			}
			if err == nil || time.Since(start) > time.Second {
				t.Fatalf("cancellation failed %v", err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("proxy socket leaked after cancellation")
			}
		})
	}
}

func TestSOCKS5AuthenticationAndRemoteDNS(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(strconv.FormatBool(bad), func(t *testing.T) {
			listener, _ := net.Listen("tcp", "127.0.0.1:0")
			defer listener.Close()
			observed := make(chan string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				read := func(n int) []byte { b := make([]byte, n); io.ReadFull(conn, b); return b }
				header := read(2)
				read(int(header[1]))
				conn.Write([]byte{5, 2})
				auth := read(2)
				user := read(int(auth[1]))
				plen := read(1)
				password := read(int(plen[0]))
				if string(user) != "alice" || string(password) != "secret" {
					conn.Write([]byte{1, 1})
					observed <- "rejected"
					return
				}
				conn.Write([]byte{1, 0})
				request := read(4)
				if request[3] != 3 {
					observed <- "not a domain"
					return
				}
				n := read(1)
				domain := read(int(n[0]))
				read(2)
				observed <- string(domain)
				conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 22})
				io.Copy(conn, conn)
			}()
			host, p, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(p)
			password := "secret"
			if bad {
				password = "wrong"
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := dialSSH(ctx, "remote-dns.invalid:22", ProxyConfig{Type: "socks5", Host: host, Port: port, User: "alice", Password: password})
			if bad {
				if err == nil {
					conn.Close()
					t.Fatal("wrong proxy credentials accepted")
				}
				if <-observed != "rejected" {
					t.Fatal("auth not attempted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(time.Second))
			conn.Write([]byte("ping"))
			b := make([]byte, 4)
			_, err = io.ReadFull(conn, b)
			if err != nil || string(b) != "ping" {
				t.Fatal("SOCKS tunnel failed")
			}
			if <-observed != "remote-dns.invalid" {
				t.Fatal("destination domain was not delegated")
			}
		})
	}
}

func TestSSHLatencyExcludesMonitorExecution(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	config := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(signer)
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()
	done := make(chan struct{})
	root := t.TempDir()
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		server, chans, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer server.Close()
		go func() {
			for request := range requests {
				time.Sleep(20 * time.Millisecond)
				request.Reply(false, nil)
			}
		}()
		for ch := range chans {
			channel, requests, err := ch.Accept()
			if err != nil {
				continue
			}
			go func() {
				defer channel.Close()
				for request := range requests {
					var args struct{ Name string }
					ssh.Unmarshal(request.Payload, &args)
					if request.Type == "subsystem" && args.Name == "sftp" {
						request.Reply(true, nil)
						fs, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(root))
						if err == nil {
							fs.Serve()
							fs.Close()
						}
						return
					}
					if request.Type == "exec" {
						request.Reply(true, nil)
						time.Sleep(300 * time.Millisecond)
						io.WriteString(channel, "__CS_STAT__\ncpu 1 2 3 4 5 6 7 8\n__CS_MEM__\nMemTotal: 1024 kB\nMemAvailable: 512 kB\n__CS_END__\n")
						channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						return
					}
					request.Reply(false, nil)
				}
			}()
		}
	}()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	host, p, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(p)
	profile, err := a.store.Save(Profile{Name: "latency", Host: host, Port: port, User: "tester", Auth: "password", Secret: "password"}, false)
	if err != nil {
		t.Fatal(err)
	}
	trustFixtureHostKey(t, a, listener.Addr().String(), signer.PublicKey())
	session, err := a.Connect(context.Background(), profile.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for !session.Latency().Ready && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	stats, err := session.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !stats.LatencyReady || stats.Latency < 15 || stats.Latency > 200 || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("bad RTT: %.1fms; monitor elapsed %s", stats.Latency, time.Since(start))
	}
	t.Logf("SSH RTT %.1fms, monitoring %.1fms", stats.Latency, float64(time.Since(start).Microseconds())/1000)
	session.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal(errors.New("SSH server did not stop"))
	}
}
