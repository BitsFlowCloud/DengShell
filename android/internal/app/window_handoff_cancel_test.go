package app

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func reserveLiveWindow(t *testing.T, a *App, s *Session, peer *relayTestClient, nonce string) {
	t.Helper()
	peer.send(t, relayMessage{Type: "handoff-begin", Nonce: nonce})
	peer.until(t, func(m relayMessage) bool { return m.Type == "handoff-ready" && m.Nonce == nonce })
	if _, err := a.ReserveWindowHandoff(windowSnapshot(s.ID, nonce)); err != nil {
		t.Fatal(err)
	}
}
func expectCancelledWindow(t *testing.T, a *App, nonce string) {
	t.Helper()
	attached, err := a.CancelWindowHandoff(nonce)
	if err != nil || attached {
		t.Fatalf("known cancellation became uncertain/attached: %t %v", attached, err)
	}
	a.mu.Lock()
	value, ok := a.windowHandoffs[nonce]
	a.mu.Unlock()
	if !ok || !value.Cancelled || !value.SnapshotReleased || value.Terminal != "" || len(value.ClientState) != 0 {
		t.Fatalf("cancellation did not keep only its tombstone: %+v", value)
	}
}
func TestWindowHandoffRestoreFailureDoubleCancel(t *testing.T) {
	a, s, address := newRelayFixture(t)
	source := dialRelayTest(t, address)
	nonce := "window-child-restore-failure-0001"
	reserveLiveWindow(t, a, s, source, nonce)
	// A child whose local runtime snapshot cannot restore calls this HTTP route
	// before the native launcher notices its cancelled WaitTerminalHandoff.
	handler := a.Handler(fstest.MapFS{})
	request := func(method, path string) *httptest.ResponseRecorder {
		// This fixture has a running HTTP listener, whose Host must match.
		req := httptest.NewRequest(method, a.URL()+path, bytes.NewReader([]byte(`{}`)))
		req.Header.Set("X-CloudShell-Token", a.Token())
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	if response := request("POST", "/api/windows/handoff/"+nonce+"/cancel"); response.Code != 200 || !strings.Contains(response.Body.String(), `"attached":false`) {
		t.Fatalf("child cancellation failed: %d %s", response.Code, response.Body)
	}
	source.until(t, func(m relayMessage) bool { return m.Type == "handoff-cancelled" })
	expectCancelledWindow(t, a, nonce) // native cancelLaunch
	expectCancelledWindow(t, a, nonce) // native deferred cleanup
	if response := request("POST", "/api/windows/handoff/"+nonce+"/cancel"); response.Code != 200 {
		t.Fatalf("HTTP retry was not idempotent: %d %s", response.Code, response.Body)
	}
	for _, suffix := range []string{"", "/check"} {
		if response := request("GET", "/api/windows/handoff/"+nonce+suffix); response.Code != 400 || strings.Contains(response.Body.String(), "你好，DengShell") {
			t.Fatalf("cancelled handoff leaked restorable snapshot: %d %s", response.Code, response.Body)
		}
	}
	source.command(t, "printf '\\nDOUBLE_CANCEL_SOURCE_OK\\n'", "\r\nDOUBLE_CANCEL_SOURCE_OK\r\n")
	source.ws.Close()
	relayWaitClosed(t, s)
	// The lightweight proof remains useful after the original session closes.
	expectCancelledWindow(t, a, nonce)
}
func TestWindowHandoffAutoTimeoutThenNewHandoff(t *testing.T) {
	a, s, address := newRelayFixture(t)
	source := dialRelayTest(t, address)
	s.mu.Lock()
	relay := s.terminalRelay
	s.mu.Unlock()
	relay.mu.Lock()
	relay.timeout = 120 * time.Millisecond
	relay.mu.Unlock()
	oldNonce, newNonce := "window-auto-timeout-old-00001", "window-auto-timeout-new-00001"
	reserveLiveWindow(t, a, s, source, oldNonce)
	source.until(t, func(m relayMessage) bool { return m.Type == "handoff-cancelled" && m.Nonce == oldNonce })
	relay.mu.Lock()
	relay.timeout = 5 * time.Second
	relay.mu.Unlock()
	reserveLiveWindow(t, a, s, source, newNonce)
	// The relay now has a different current nonce. The old reservation still
	// knows that its timeout resumed the source, without cancelling the new one.
	expectCancelledWindow(t, a, oldNonce)
	expectCancelledWindow(t, a, oldNonce)
	if !a.TerminalHandoffReady(s.ID, newNonce) {
		t.Fatal("old cancellation interrupted a new handoff")
	}
	expectCancelledWindow(t, a, newNonce)
	source.until(t, func(m relayMessage) bool { return m.Type == "handoff-cancelled" && m.Nonce == newNonce })
	source.command(t, "printf '\\nTIMEOUT_SOURCE_OK\\n'", "\r\nTIMEOUT_SOURCE_OK\r\n")
}
func TestWindowHandoffConcurrentCancellation(t *testing.T) {
	a, s, address := newRelayFixture(t)
	source := dialRelayTest(t, address)
	nonce := "window-concurrent-cancel-0001"
	reserveLiveWindow(t, a, s, source, nonce)
	type result struct {
		attached bool
		err      error
	}
	results := make(chan result, 8)
	var done sync.WaitGroup
	for i := 0; i < 8; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			attached, err := a.CancelWindowHandoff(nonce)
			results <- result{attached, err}
		}()
	}
	done.Wait()
	close(results)
	for result := range results {
		if result.err != nil || result.attached {
			t.Fatalf("concurrent cancel failed: %+v", result)
		}
	}
	source.until(t, func(m relayMessage) bool { return m.Type == "handoff-cancelled" })
	source.command(t, "printf '\\nCONCURRENT_CANCEL_OK\\n'", "\r\nCONCURRENT_CANCEL_OK\r\n")
}
func TestWindowHandoffLateCancelPreservesSubsequentOwner(t *testing.T) {
	a, s, address := newRelayFixture(t)
	source := dialRelayTest(t, address)
	oldNonce, newNonce := "window-late-commit-old-000001", "window-late-commit-new-000001"
	reserveLiveWindow(t, a, s, source, oldNonce)
	child := dialRelayTest(t, address+"&handoff="+oldNonce)
	reserveLiveWindow(t, a, s, child, newNonce)
	for i := 0; i < 2; i++ {
		attached, err := a.CancelWindowHandoff(oldNonce)
		if err != nil || !attached {
			t.Fatalf("late cancel forgot committed owner: %t %v", attached, err)
		}
	}
	if !a.TerminalHandoffReady(s.ID, newNonce) {
		t.Fatal("late cancel disturbed the new owner's handoff")
	}
	old, err := a.WindowHandoff(oldNonce)
	if err != nil || !old.SnapshotReleased || old.Terminal != "" {
		t.Fatal("committed scrollback was retained")
	}
	encoded, _ := json.Marshal(old)
	if strings.Contains(string(encoded), "completion") || strings.Contains(string(encoded), "你好，DengShell") {
		t.Fatal("internal completion or snapshot escaped JSON")
	}
	expectCancelledWindow(t, a, newNonce)
	child.until(t, func(m relayMessage) bool { return m.Type == "handoff-cancelled" && m.Nonce == newNonce })
	child.command(t, "printf '\\nLATE_OWNER_OK\\n'", "\r\nLATE_OWNER_OK\r\n")
}
