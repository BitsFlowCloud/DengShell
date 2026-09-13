package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
)

type relayTestClient struct {
	ws    *websocket.Conn
	data  bytes.Buffer
	ready []byte
}

func newRelayFixture(t *testing.T) (*App, *Session, string) {
	t.Helper()
	key := os.Getenv("CLOUDSHELL_TEST_KEY")
	if key == "" {
		t.Skip("requires isolated localhost:19225 SSH fixture and CLOUDSHELL_TEST_KEY")
	}
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler(fstest.MapFS{}))
	a.baseURL = server.URL
	t.Cleanup(func() { a.Close(); server.Close() })
	p, err := a.store.Save(Profile{Name: "terminal handoff fixture", Host: "127.0.0.1", Port: 19225, User: "bitsflow", Auth: "key", KeyPath: key}, false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := connectLocalSSHFixture(t, a, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return a, s, "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/" + s.ID + "/terminal?token=" + url.QueryEscape(a.Token())
}
func dialRelayTest(t *testing.T, address string) *relayTestClient {
	t.Helper()
	ws, response, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		t.Fatalf("dial terminal: %v response=%v", err, response)
	}
	t.Cleanup(func() { ws.Close() })
	c := &relayTestClient{ws: ws}
	c.until(t, func(m relayMessage) bool { return m.Type == "ready" })
	return c
}
func (c *relayTestClient) send(t *testing.T, m relayMessage) {
	t.Helper()
	c.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := c.ws.WriteJSON(m); err != nil {
		t.Fatal(err)
	}
}
func (c *relayTestClient) until(t *testing.T, predicate func(relayMessage) bool) {
	t.Helper()
	for {
		c.ws.SetReadDeadline(time.Now().Add(8 * time.Second))
		kind, data, err := c.ws.ReadMessage()
		if err != nil {
			t.Fatalf("terminal read: %v; bytes=%d", err, c.data.Len())
		}
		var m relayMessage
		if kind == websocket.BinaryMessage {
			c.data.Write(data)
			c.send(t, relayMessage{Type: "ack"})
		} else {
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			if m.Type == "ready" {
				c.ready = append([]byte(nil), data...)
			}
		}
		if predicate(m) {
			return
		}
	}
}
func (c *relayTestClient) command(t *testing.T, command, marker string) {
	t.Helper()
	c.send(t, relayMessage{Type: "input", Data: command + "\r"})
	c.until(t, func(relayMessage) bool { return strings.Contains(c.data.String(), marker) })
}
func relayWaitClosed(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("terminal closure leaked the SSH session")
	}
}
func relayRejectDial(t *testing.T, address string) {
	t.Helper()
	ws, res, err := websocket.DefaultDialer.Dial(address, nil)
	if ws != nil {
		ws.Close()
	}
	if err == nil || res == nil || res.StatusCode != 409 {
		t.Fatalf("wrong/used handoff credential was not rejected: %v %v", err, res)
	}
}

func TestTerminalRelayHandoffKeepsPTYAndAllOutput(t *testing.T) {
	a, s, address := newRelayFixture(t)
	old := dialRelayTest(t, address)
	old.command(t, "stty -echo; printf '\\nPID:%s\\n' \"$$\"", "\r\nPID:")
	// Read through the complete PID line before asking the same shell to stream.
	if !regexp.MustCompile(`PID:[0-9]+\r?\n`).Match(old.data.Bytes()) {
		old.until(t, func(relayMessage) bool { return regexp.MustCompile(`PID:[0-9]+\r?\n`).Match(old.data.Bytes()) })
	}
	pid := regexp.MustCompile(`PID:([0-9]+)`).FindSubmatch(old.data.Bytes())[1]
	old.data.Reset()
	payload := strings.Repeat("abcdefghij", 100)
	old.send(t, relayMessage{Type: "input", Data: "i=0; while [ \"$i\" -lt 3000 ]; do printf 'ROW:%04d:%s\\n' \"$i\" '" + payload + "'; i=$((i+1)); done; printf 'STREAM_DONE\\n'\r"})
	old.until(t, func(relayMessage) bool { return old.data.Len() > 0 })
	nonce := "relay-stream-handoff-0001"
	old.send(t, relayMessage{Type: "handoff-begin", Nonce: nonce})
	old.until(t, func(m relayMessage) bool { return m.Type == "handoff-ready" && m.Nonce == nonce })
	if !a.TerminalHandoffReady(s.ID, nonce) {
		t.Fatal("ready barrier not registered")
	}
	relayRejectDial(t, address+"&handoff=wrong-nonce-00000000")
	if strings.Contains(old.data.String(), "STREAM_DONE") {
		t.Fatal("test did not pause during live output")
	}
	next := dialRelayTest(t, address+"&handoff="+nonce)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.WaitTerminalHandoff(ctx, s.ID, nonce); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(old.ready, next.ready) {
		t.Fatal("handoff recreated or changed shell integration")
	}
	relayRejectDial(t, address+"&handoff="+nonce)
	next.until(t, func(relayMessage) bool { return strings.Contains(next.data.String(), "STREAM_DONE\r\n") })
	all := old.data.String() + next.data.String()
	matches := regexp.MustCompile(`ROW:([0-9]{4}):([^\r\n]*)`).FindAllStringSubmatch(all, -1)
	if len(matches) != 3000 {
		t.Fatalf("output lost/duplicated: rows=%d bytes=%d", len(matches), len(all))
	}
	for n, row := range matches {
		if row[1] != fmt.Sprintf("%04d", n) || row[2] != payload {
			t.Fatalf("output order/bytes changed at %d", n)
		}
	}
	next.data.Reset()
	next.command(t, "printf '\\nAFTER_PID:%s\\n' \"$$\"", "\r\nAFTER_PID:")
	if !strings.Contains(next.data.String(), "AFTER_PID:"+string(pid)+"\r\n") {
		t.Fatalf("shell PID changed after handoff: %q", next.data.String())
	}
	old.ws.Close()
	if _, err := a.session(s.ID); err != nil {
		t.Fatal("closing migrated source disconnected the shell")
	}
	next.ws.Close()
	relayWaitClosed(t, s)
}

func TestTerminalRelayCancelAndTimeoutRestoreSource(t *testing.T) {
	for _, mode := range []string{"api", "websocket", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			a, s, address := newRelayFixture(t)
			c := dialRelayTest(t, address)
			s.mu.Lock()
			r := s.terminalRelay
			s.mu.Unlock()
			r.mu.Lock()
			r.timeout = 120 * time.Millisecond
			r.mu.Unlock()
			nonce := "relay-cancel-" + mode + "-00000000"
			c.send(t, relayMessage{Type: "handoff-begin", Nonce: nonce})
			c.until(t, func(m relayMessage) bool { return m.Type == "handoff-ready" })
			if mode == "api" {
				if err := a.ResumeTerminalHandoff(s.ID, nonce); err != nil {
					t.Fatal(err)
				}
			} else if mode == "websocket" {
				c.send(t, relayMessage{Type: "handoff-cancel", Nonce: nonce})
			}
			c.until(t, func(m relayMessage) bool { return m.Type == "handoff-cancelled" })
			if a.TerminalHandoffReady(s.ID, nonce) {
				t.Fatal("cancelled handoff stayed ready")
			}
			if err := a.WaitTerminalHandoff(context.Background(), s.ID, nonce); err == nil {
				t.Fatal("cancelled handoff reported success")
			}
			c.data.Reset()
			c.command(t, "printf '\\nRESUMED_OK\\n'", "\r\nRESUMED_OK\r\n")
			c.ws.Close()
			relayWaitClosed(t, s)
		})
	}
}

func TestTerminalRelayAbandonedHandoffAndBackpressureClose(t *testing.T) {
	for _, mode := range []string{"abandoned", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			a, s, address := newRelayFixture(t)
			c := dialRelayTest(t, address)
			s.mu.Lock()
			r := s.terminalRelay
			s.mu.Unlock()
			r.mu.Lock()
			r.timeout = 180 * time.Millisecond
			r.mu.Unlock()
			c.send(t, relayMessage{Type: "input", Data: "stty -echo; yes RELAY_BOUNDED_OUTPUT\r"})
			c.send(t, relayMessage{Type: "handoff-begin", Nonce: "relay-backpressure-000000"})
			c.until(t, func(m relayMessage) bool { return m.Type == "handoff-ready" })
			time.Sleep(40 * time.Millisecond)
			if len(r.output) > terminalRelayFrames {
				t.Fatal("output queue exceeded its bound")
			}
			if mode == "abandoned" {
				c.ws.Close()
			} else {
				a.disconnect(s.ID)
			}
			relayWaitClosed(t, s)
		})
	}
}

func TestTerminalRelayWaitsForCompleteParserBoundary(t *testing.T) {
	for _, tc := range []struct{ name, start, end, complete string }{
		{"utf8", `\344`, `\270\255`, "中"},
		{"csi", `\033[38;2;`, `120;240;160m`, "\x1b[38;2;120;240;160m"},
		{"osc", `\033]0;`, `test-title\007`, "\x1b]0;test-title\x07"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, s, address := newRelayFixture(t)
			old := dialRelayTest(t, address)
			old.command(t, "stty -echo; printf '\\nBOUNDARY_SETUP\\n'", "\r\nBOUNDARY_SETUP\r\n")
			old.data.Reset()
			old.send(t, relayMessage{Type: "input", Data: "printf 'SPLIT_START" + tc.start + "'; sleep 0.3; printf '" + tc.end + "COMPLETE\\n'\r"})
			old.until(t, func(relayMessage) bool { return strings.Contains(old.data.String(), "SPLIT_START") })
			if strings.Contains(old.data.String(), "COMPLETE") {
				t.Fatal("fixture failed to split output")
			}
			nonce := "relay-boundary-" + tc.name + "-000000"
			old.send(t, relayMessage{Type: "handoff-begin", Nonce: nonce})
			old.until(t, func(m relayMessage) bool { return m.Type == "handoff-ready" && m.Nonce == nonce })
			if !strings.Contains(old.data.String(), "SPLIT_START"+tc.complete) {
				t.Fatalf("handoff cut a parser sequence: %q", old.data.String())
			}
			next := dialRelayTest(t, address+"&handoff="+nonce)
			if err := a.WaitTerminalHandoff(context.Background(), s.ID, nonce); err != nil {
				t.Fatal(err)
			}
			next.command(t, "printf '\\nCHILD_OK\\n'", "\r\nCHILD_OK\r\n")
		})
	}
}

func TestTerminalRelayIncompleteBoundaryFailsAndRecovers(t *testing.T) {
	for _, cancelPending := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel-%t", cancelPending), func(t *testing.T) {
			a, s, address := newRelayFixture(t)
			c := dialRelayTest(t, address)
			c.command(t, "stty -echo; printf '\\nBOUNDARY_SETUP\\n'", "\r\nBOUNDARY_SETUP\r\n")
			c.data.Reset()
			delay := "3.4"
			if cancelPending {
				delay = "0.3"
			}
			c.send(t, relayMessage{Type: "input", Data: "printf '\\033]0;UNFINISHED'; sleep " + delay + "; printf '\\007BOUNDARY_END\\n'\r"})
			c.until(t, func(relayMessage) bool { return strings.Contains(c.data.String(), "UNFINISHED") })
			nonce := "relay-incomplete-00000000"
			c.send(t, relayMessage{Type: "handoff-begin", Nonce: nonce})
			if cancelPending {
				c.send(t, relayMessage{Type: "handoff-cancel", Nonce: nonce})
			}
			c.until(t, func(m relayMessage) bool {
				if m.Type == "handoff-ready" {
					t.Fatal("unfinished OSC was incorrectly ready")
				}
				return m.Type == "handoff-error" || m.Type == "handoff-cancelled"
			})
			if a.TerminalHandoffReady(s.ID, nonce) {
				t.Fatal("failed handoff remains attachable")
			}
			if err := a.WaitTerminalHandoff(context.Background(), s.ID, nonce); err == nil {
				t.Fatal("failed boundary handoff reported success")
			}
			c.command(t, "printf '\\nRECOVERED_BOUNDARY\\n'", "\r\nRECOVERED_BOUNDARY\r\n")
		})
	}
}
