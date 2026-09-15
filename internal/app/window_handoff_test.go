package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// These validation cases need no SSH daemon. The relay is parked at the same
// ready barrier the real transport reaches; transport movement is covered below.
func reservedWindowFixture(t *testing.T, nonce string) (*App, *Session) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &Session{ID: "handoff-session", ProfileID: "handoff-profile", Home: "/srv/demo", ctx: ctx}
	s.terminalRelay = &terminalRelay{session: s, handoff: &terminalHandoff{nonce: nonce, ready: true, done: make(chan struct{})}}
	a := &App{sessions: map[string]*Session{s.ID: s}}
	return a, s
}
func windowSnapshot(id, nonce string) WindowHandoffRequest {
	return WindowHandoffRequest{SessionID: id, Nonce: nonce, Terminal: "\x1b[32m你好，DengShell\x1b[0m", Cols: 120, Rows: 30, CWD: "/srv/demo", Follow: true, ClientState: json.RawMessage(`{"networkInterface":"eth0","stats":null}`)}
}
func TestWindowHandoffInputLimits(t *testing.T) {
	nonce := "window-validation-nonce-0001"
	a, s := reservedWindowFixture(t, nonce)
	cases := []struct {
		name   string
		mutate func(*WindowHandoffRequest)
	}{
		{"short nonce", func(v *WindowHandoffRequest) { v.Nonce = strings.Repeat("a", 23) }},
		{"long nonce", func(v *WindowHandoffRequest) { v.Nonce = strings.Repeat("a", 97) }},
		{"nonce punctuation", func(v *WindowHandoffRequest) { v.Nonce = "window/invalid/nonce/0001" }},
		{"nonce unicode", func(v *WindowHandoffRequest) { v.Nonce = "window-nonce-含中文-0000001" }},
		{"small columns", func(v *WindowHandoffRequest) { v.Cols = 1 }},
		{"large columns", func(v *WindowHandoffRequest) { v.Cols = 1001 }},
		{"small rows", func(v *WindowHandoffRequest) { v.Rows = 1 }},
		{"large rows", func(v *WindowHandoffRequest) { v.Rows = 501 }},
		{"relative directory", func(v *WindowHandoffRequest) { v.CWD = "srv/demo" }},
		{"nul directory", func(v *WindowHandoffRequest) { v.CWD = "/srv\x00demo" }},
		{"bad client JSON", func(v *WindowHandoffRequest) { v.ClientState = json.RawMessage(`{"unfinished":`) }},
		{"terminal over limit", func(v *WindowHandoffRequest) { v.Terminal = strings.Repeat("x", (24<<20)+1) }},
		{"client state over limit", func(v *WindowHandoffRequest) { v.ClientState = json.RawMessage(`"` + strings.Repeat("x", 2<<20) + `"`) }},
		{"missing session", func(v *WindowHandoffRequest) { v.SessionID = "missing" }},
		{"wrong barrier nonce", func(v *WindowHandoffRequest) { v.Nonce = "another-valid-nonce-0000001" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := windowSnapshot(s.ID, nonce)
			tc.mutate(&input)
			if _, err := a.ReserveWindowHandoff(input); err == nil {
				t.Fatal("invalid handoff accepted")
			}
			if len(a.windowHandoffs) != 0 {
				t.Fatal("invalid handoff was registered")
			}
		})
	}
	for _, bounds := range [][2]int{{2, 2}, {1000, 500}} {
		input := windowSnapshot(s.ID, nonce)
		input.Cols, input.Rows = bounds[0], bounds[1]
		if _, err := a.ReserveWindowHandoff(input); err != nil {
			t.Fatalf("valid dimensions rejected: %v", err)
		}
		a.ForgetWindowHandoff(nonce)
	}
	input := windowSnapshot(s.ID, nonce)
	input.Terminal = strings.Repeat("x", 24<<20)
	if _, err := a.ReserveWindowHandoff(input); err != nil {
		t.Fatalf("exact terminal limit rejected: %v", err)
	}
	a.ForgetWindowHandoff(nonce)
	for _, length := range []int{24, 96} {
		boundaryNonce := strings.Repeat("A", length-2) + "_-"
		boundaryApp, boundarySession := reservedWindowFixture(t, boundaryNonce)
		if _, err := boundaryApp.ReserveWindowHandoff(windowSnapshot(boundarySession.ID, boundaryNonce)); err != nil {
			t.Fatalf("valid nonce boundary %d rejected: %v", length, err)
		}
		boundaryApp.ForgetWindowHandoff(boundaryNonce)
	}
}
func TestWindowHandoffUniqueReservationAndCleanup(t *testing.T) {
	nonce := "window-unique-nonce-0000001"
	a, s := reservedWindowFixture(t, nonce)
	input := windowSnapshot(s.ID, nonce)
	begin := time.Now()
	saved, err := a.ReserveWindowHandoff(input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Session != s || saved.CreatedAt.Before(begin) || saved.Terminal != input.Terminal || saved.CWD != input.CWD || !saved.Follow || !bytes.Equal(saved.ClientState, input.ClientState) {
		t.Fatal("window snapshot did not round-trip")
	}
	second := Session{ID: "another-session", ctx: s.ctx}
	second.terminalRelay = &terminalRelay{session: &second, handoff: &terminalHandoff{nonce: nonce, ready: true, done: make(chan struct{})}}
	a.sessions[second.ID] = &second
	if _, err = a.ReserveWindowHandoff(windowSnapshot(second.ID, nonce)); err == nil {
		t.Fatal("duplicate nonce replaced another session's reservation")
	}
	got, err := a.WindowHandoff(nonce)
	if err != nil || got.SessionID != s.ID {
		t.Fatal("duplicate changed original reservation")
	}
	a.ForgetWindowHandoff(nonce)
	a.ForgetWindowHandoff(nonce)
	if _, err = a.WindowHandoff(nonce); err == nil {
		t.Fatal("forgotten reservation remained readable")
	}
	if _, err = a.ReserveWindowHandoff(input); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	s.ctx = cancelled
	if _, err = a.WindowHandoff(nonce); err == nil {
		t.Fatal("disconnected session remained attachable")
	}
	a.ForgetWindowHandoff(nonce)
}
func TestWindowHandoffHTTPRoutesRequireCurrentToken(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	handler := a.Handler(fstest.MapFS{})
	request := func(method, path, body, token, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://wails.localhost"+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("X-CloudShell-Token", token)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		return r
	}
	routes := [][2]string{{"POST", "/api/windows/handoff"}, {"POST", "/api/windows/launch"}, {"GET", "/api/windows/handoff/window-token-nonce-000001"}, {"GET", "/api/windows/handoff/window-token-nonce-000001/status"}, {"GET", "/api/windows/handoff/window-token-nonce-000001/check"}, {"POST", "/api/windows/handoff/window-token-nonce-000001/cancel"}, {"POST", "/api/windows/native"}, {"GET", "/api/windows/alive"}}
	for _, route := range routes {
		for _, token := range []string{"", "stale-token"} {
			if r := request(route[0], route[1], `{}`, token, ""); r.Code != 403 {
				t.Fatalf("%s unauthorized: %d %s", route[1], r.Code, r.Body)
			}
		}
	}
	if r := request("GET", "/api/windows/alive?token="+a.Token(), "", "", ""); r.Code != 403 {
		t.Fatal("handoff API accepted query-string token")
	}
	if r := request("GET", "/api/windows/alive", "", a.Token(), "https://untrusted.invalid"); r.Code != 403 {
		t.Fatal("cross-origin handoff access accepted")
	}
	if r := request("GET", "/api/windows/alive", "", a.Token(), ""); r.Code != 200 || r.Body.String() != `{"ok":true}` {
		t.Fatal("alive route unavailable")
	}
	if r := request("POST", "/api/windows/launch", `{}`, a.Token(), ""); r.Code != 400 {
		t.Fatal("missing native launcher not reported")
	}
	called := 0
	a.SetWindowLauncher(func(ctx context.Context, input WindowHandoffRequest) error {
		called++
		if input.Nonce != "window-launch-nonce-000001" {
			return errors.New("lost nonce")
		}
		return nil
	})
	if r := request("POST", "/api/windows/launch", `{"nonce":"window-launch-nonce-000001"}`, a.Token(), ""); r.Code != 200 || called != 1 {
		t.Fatalf("launch routing: %d %s", r.Code, r.Body)
	}
	if r := request("POST", "/api/windows/launch", `{`, a.Token(), ""); r.Code != 400 || called != 1 {
		t.Fatal("malformed launch invoked launcher")
	}
	if r := request("POST", "/api/windows/launch", `{"terminal":"`+strings.Repeat("x", 32<<20)+`"}`, a.Token(), ""); r.Code != 400 || called != 1 {
		t.Fatal("oversized HTTP body reached launcher")
	}
	if r := request("POST", "/api/windows/native", `{"method":"unsupported"}`, a.Token(), ""); r.Code != 400 {
		t.Fatal("unknown native operation accepted")
	}
	a.SetWindowLauncher(func(context.Context, WindowHandoffRequest) error { return errors.New("fixture launcher unavailable") })
	if r := request("POST", "/api/windows/launch", `{}`, a.Token(), ""); r.Code != 400 || !strings.Contains(r.Body.String(), "fixture launcher unavailable") {
		t.Fatal("launch failure not returned")
	}
}
func TestWindowHandoffHTTPStatusAndCancellation(t *testing.T) {
	for _, mode := range []string{"cancel", "attach"} {
		t.Run(mode, func(t *testing.T) {
			a, s, address := newRelayFixture(t)
			old := dialRelayTest(t, address)
			nonce := "window-http-" + mode + "-nonce-000001"
			old.send(t, relayMessage{Type: "handoff-begin", Nonce: nonce})
			old.until(t, func(m relayMessage) bool { return m.Type == "handoff-ready" && m.Nonce == nonce })
			handler := a.Handler(fstest.MapFS{})
			request := func(method, path string, body any) *httptest.ResponseRecorder {
				t.Helper()
				data, _ := json.Marshal(body)
				// Use the running fixture's origin, as the native bridge does.
				req := httptest.NewRequest(method, a.URL()+path, bytes.NewReader(data))
				req.Header.Set("X-CloudShell-Token", a.Token())
				r := httptest.NewRecorder()
				handler.ServeHTTP(r, req)
				return r
			}
			if r := request("POST", "/api/windows/handoff", windowSnapshot(s.ID, nonce)); r.Code != 200 || r.Body.Len() > 256 || strings.Contains(r.Body.String(), "terminal") {
				t.Fatalf("reserve route: %d %s", r.Code, r.Body)
			}
			if r := request("GET", "/api/windows/handoff/"+nonce+"/check", nil); r.Code != 200 || r.Body.Len() > 256 || !strings.Contains(r.Body.String(), s.ID) || strings.Contains(r.Body.String(), "terminal") {
				t.Fatalf("lightweight check: %d %s", r.Code, r.Body)
			}
			if r := request("GET", "/api/windows/handoff/"+nonce, nil); r.Code != 200 || !strings.Contains(r.Body.String(), `"sessionId":"`+s.ID+`"`) {
				t.Fatal("snapshot route lost session")
			}
			if r := request("GET", "/api/windows/handoff/"+nonce+"/status", nil); r.Code != 200 || !strings.Contains(r.Body.String(), `"attached":false`) {
				t.Fatalf("unattached status: %d %s", r.Code, r.Body)
			}
			if mode == "cancel" {
				if r := request("POST", "/api/windows/handoff/"+nonce+"/cancel", nil); r.Code != 200 || !strings.Contains(r.Body.String(), `"attached":false`) {
					t.Fatalf("cancel: %d %s", r.Code, r.Body)
				}
				old.until(t, func(m relayMessage) bool { return m.Type == "handoff-cancelled" })
				if r := request("GET", "/api/windows/handoff/"+nonce+"/status", nil); r.Code != 400 {
					t.Fatal("cancelled status still readable")
				}
				old.command(t, "printf '\\nWINDOW_CANCEL_OK\\n'", "\r\nWINDOW_CANCEL_OK\r\n")
			} else {
				next := dialRelayTest(t, address+"&handoff="+nonce)
				if r := request("GET", "/api/windows/handoff/"+nonce+"/status", nil); r.Code != 200 || !strings.Contains(r.Body.String(), `"attached":true`) {
					t.Fatalf("attached status: %d %s", r.Code, r.Body)
				}
				trimmed, err := a.WindowHandoff(nonce)
				if err != nil || !trimmed.SnapshotReleased || trimmed.Terminal != "" || len(trimmed.ClientState) != 0 {
					t.Fatal("committed handoff retained duplicate scrollback")
				}
				if r := request("GET", "/api/windows/handoff/"+nonce, nil); r.Code != 400 {
					t.Fatal("released snapshot remained restorable")
				}
				if r := request("POST", "/api/windows/handoff/"+nonce+"/cancel", nil); r.Code != 200 || !strings.Contains(r.Body.String(), `"attached":true`) {
					t.Fatalf("late cancellation did not preserve attached terminal: %d %s", r.Code, r.Body)
				}
				next.command(t, "printf '\\nWINDOW_ATTACHED_OK\\n'", "\r\nWINDOW_ATTACHED_OK\r\n")
			}
			config, _ := json.Marshal(a.store.List())
			if bytes.Contains(config, []byte(nonce)) || bytes.Contains(config, []byte("你好，DengShell")) {
				t.Fatal("runtime handoff leaked into persistent configuration")
			}
		})
	}
}
