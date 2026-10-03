package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func aiRelayTestApp(t *testing.T) *App {
	t.Helper()
	a := lockTestApp(t)
	t.Cleanup(a.closeAIWindows)
	a.aiWindows.now = func() time.Time { return time.Unix(10000, 0) }
	return a
}

func advanceAIRelayClock(a *App, duration time.Duration) {
	a.aiWindows.mu.Lock()
	now := a.aiWindows.clockLocked().Add(duration)
	a.aiWindows.now = func() time.Time { return now }
	a.aiWindows.mu.Unlock()
}

func newAIRelayID(t *testing.T, a *App) string {
	t.Helper()
	id, err := a.createAIWindow()
	if err != nil {
		t.Fatal(err)
	}
	if !aiWindowIDPattern.MatchString(id) {
		t.Fatal("window ID is not 24 random bytes in lowercase hex")
	}
	return id
}

func TestAIWindowRelayIsolationReplayAndAcknowledgement(t *testing.T) {
	a := aiRelayTestApp(t)
	first, second := newAIRelayID(t, a), newAIRelayID(t, a)
	if first == second {
		t.Fatal("window identifiers collided")
	}
	payload := json.RawMessage(`{"type":"state","text":"<script>not executable</script>"}`)
	seq, err := a.postAIWindow(first, "owner", payload)
	if err != nil || seq != 1 {
		t.Fatal("send failed", seq, err)
	}
	payload[2] = 'X' // The registry must own an immutable copy.
	if _, err := a.postAIWindow(second, "owner", json.RawMessage(`{"type":"other"}`)); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		poll, err := a.pollAIWindow(first, "assistant", 0, false)
		if err != nil || poll.Closed || poll.LastSeq != 1 || len(poll.Events) != 1 || !bytes.Contains(poll.Events[0].Payload, []byte(`"type":"state"`)) {
			t.Fatal("message was consumed, changed, or mixed across rooms", poll, err)
		}
		poll.Events[0].Payload[2] = 'Y' // A caller cannot mutate replay storage.
	}
	owner, err := a.pollAIWindow(first, "owner", 0, false)
	if err != nil || len(owner.Events) != 0 {
		t.Fatal("sender received its own message")
	}
	if _, err := a.postAIWindow(first, "assistant", json.RawMessage(`{"type":"prompt","text":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	owner, err = a.pollAIWindow(first, "owner", 0, false)
	if err != nil || len(owner.Events) != 1 || !bytes.Contains(owner.Events[0].Payload, []byte("hello")) {
		t.Fatal("reverse relay failed", err)
	}
	acknowledged, err := a.pollAIWindow(first, "assistant", 1, false)
	if err != nil || len(acknowledged.Events) != 0 || acknowledged.LastSeq != 1 {
		t.Fatal("acknowledgement failed", err)
	}
	if probe, err := a.pollAIWindow(first, "assistant", 0, true); err != nil || probe.Closed || len(probe.Events) != 0 || probe.LastSeq != 1 {
		t.Fatal("native focus probe failed after child acknowledgement", probe, err)
	}
	a.aiWindows.mu.Lock()
	remaining := a.aiWindows.rooms[first].queues[1].bytes
	a.aiWindows.mu.Unlock()
	if remaining != 0 {
		t.Fatal("acknowledged payload memory was not released")
	}
	if _, err := a.pollAIWindow(first, "assistant", 0, false); err == nil {
		t.Fatal("backward cursor silently lost events")
	}
	if _, err := a.pollAIWindow(first, "assistant", 2, false); err == nil {
		t.Fatal("future cursor accepted")
	}
	if err := a.deleteAIWindow(first); err != nil {
		t.Fatal(err)
	}
	if err := a.deleteAIWindow(first); err != nil {
		t.Fatal("delete is not idempotent", err)
	}
	closed, err := a.pollAIWindow(first, "owner", 0, false)
	if err != nil || !closed.Closed || closed.Reason != "closed" || len(closed.Events) != 0 {
		t.Fatal("closed relay retained payload or did not signal peer", err)
	}
	if _, err := a.postAIWindow(first, "owner", json.RawMessage(`{}`)); err == nil {
		t.Fatal("closed relay accepted another message")
	}
	other, err := a.pollAIWindow(second, "assistant", 0, false)
	if err != nil || other.Closed || len(other.Events) != 1 {
		t.Fatal("closing one room affected another", err)
	}
}

func TestAIWindowStartupProbeDoesNotShortenGrace(t *testing.T) {
	a := aiRelayTestApp(t)
	id := newAIRelayID(t, a)
	if _, err := a.postAIWindow(id, "owner", json.RawMessage(`{"ready":true}`)); err != nil {
		t.Fatal(err)
	}
	for _, step := range []time.Duration{0, 10 * time.Second, 10 * time.Second, 10 * time.Second, 10 * time.Second, 4 * time.Second} {
		advanceAIRelayClock(a, step)
		if poll, err := a.pollAIWindow(id, "owner", 0, false); err != nil || poll.Closed {
			t.Fatal("startup grace closed early", poll, err)
		}
		if probe, err := a.pollAIWindow(id, "assistant", 0, true); err != nil || probe.Closed || len(probe.Events) != 0 {
			t.Fatal("native probe consumed state or started heartbeat timeout", probe, err)
		}
	}
	// A real first child poll at 44 seconds is still allowed, including after
	// an earlier native probe using exactly the same zero cursor.
	if poll, err := a.pollAIWindow(id, "assistant", 0, false); err != nil || poll.Closed || len(poll.Events) != 1 {
		t.Fatal("child failed to join during initial grace", poll, err)
	}
	advanceAIRelayClock(a, 10*time.Second)
	if poll, err := a.pollAIWindow(id, "owner", 0, false); err != nil || poll.Closed {
		t.Fatal("joined child was closed too early", poll, err)
	}
	advanceAIRelayClock(a, 5*time.Second)
	if poll, err := a.pollAIWindow(id, "owner", 0, false); err != nil || !poll.Closed || poll.Reason != "assistant_timeout" {
		t.Fatal("missing assistant heartbeat did not close relay", poll, err)
	}
}

func TestAIWindowHeartbeatExpiryAndTombstoneCleanup(t *testing.T) {
	for _, target := range []string{"owner", "startup"} {
		t.Run(target, func(t *testing.T) {
			a := aiRelayTestApp(t)
			id := newAIRelayID(t, a)
			if target == "owner" {
				advanceAIRelayClock(a, aiWindowHeartbeat)
			} else {
				for i := 0; i < 4; i++ {
					advanceAIRelayClock(a, 10*time.Second)
					_, _ = a.pollAIWindow(id, "owner", 0, false)
				}
				advanceAIRelayClock(a, 5*time.Second)
			}
			poll, err := a.pollAIWindow(id, "assistant", 0, true)
			if err != nil || !poll.Closed || poll.Reason != target+"_timeout" {
				t.Fatal("heartbeat timeout not reported", poll, err)
			}
			advanceAIRelayClock(a, aiWindowClosedTTL)
			poll, err = a.pollAIWindow(id, "assistant", 0, true)
			if err != nil || !poll.Closed || poll.Reason != "expired" {
				t.Fatal("expired tombstone retained", poll, err)
			}
			a.aiWindows.mu.Lock()
			count := len(a.aiWindows.rooms)
			a.aiWindows.mu.Unlock()
			if count != 0 {
				t.Fatal("expired room not removed")
			}
		})
	}
}

func TestAIWindowBoundedQueuesCloseExplicitly(t *testing.T) {
	for _, kind := range []string{"count", "bytes", "message"} {
		t.Run(kind, func(t *testing.T) {
			a := aiRelayTestApp(t)
			id := newAIRelayID(t, a)
			var err error
			switch kind {
			case "count":
				for i := 0; i <= aiWindowEventLimit; i++ {
					_, err = a.postAIWindow(id, "owner", json.RawMessage(`null`))
					if i < aiWindowEventLimit && err != nil {
						t.Fatal("event queue closed before its bound", err)
					}
				}
			case "bytes":
				payload := json.RawMessage(`"` + strings.Repeat("x", aiWindowPayloadMax-2) + `"`)
				for i, side := range []string{"owner", "assistant", "owner"} {
					_, err = a.postAIWindow(id, side, payload)
					if i < 2 && err != nil {
						t.Fatal("byte queue closed before its bound", err)
					}
				}
			case "message":
				_, err = a.postAIWindow(id, "owner", json.RawMessage(`"`+strings.Repeat("x", aiWindowPayloadMax)+`"`))
			}
			if err == nil {
				t.Fatal("queue limit not enforced")
			}
			poll, err := a.pollAIWindow(id, "assistant", 0, false)
			if err != nil || !poll.Closed || poll.Reason != "overflow" || len(poll.Events) != 0 {
				t.Fatal("overflow did not close and clear room", poll, err)
			}
			a.aiWindows.mu.Lock()
			room := a.aiWindows.rooms[id]
			cleared := room.queues[0].bytes == 0 && room.queues[1].bytes == 0 && len(room.queues[0].events) == 0 && len(room.queues[1].events) == 0
			a.aiWindows.mu.Unlock()
			if !cleared {
				t.Fatal("closed queue retained payloads")
			}
		})
	}
}

func TestAIWindowRegistryLimitsSuspendAndClose(t *testing.T) {
	a := aiRelayTestApp(t)
	ids := []string{}
	for i := 0; i < aiWindowLimit; i++ {
		ids = append(ids, newAIRelayID(t, a))
	}
	if _, err := a.createAIWindow(); err == nil {
		t.Fatal("active window limit not enforced")
	}
	if _, err := a.postAIWindow(ids[0], "assistant", json.RawMessage(`{"secret":"fixture only"}`)); err != nil {
		t.Fatal(err)
	}
	a.suspendAIWindows("locked")
	for _, id := range ids {
		poll, err := a.pollAIWindow(id, "owner", 0, false)
		if err != nil || !poll.Closed || poll.Reason != "locked" || len(poll.Events) != 0 {
			t.Fatal("lock suspension did not erase messages", poll, err)
		}
	}
	for i := 0; i < aiWindowTombstones+20; i++ {
		if err := a.deleteAIWindow(newAIRelayID(t, a)); err != nil {
			t.Fatal(err)
		}
	}
	a.aiWindows.mu.Lock()
	count := len(a.aiWindows.rooms)
	a.aiWindows.mu.Unlock()
	if count > aiWindowTombstones {
		t.Fatal("closed room tombstones grew without bound", count)
	}
	newID := newAIRelayID(t, a) // A lock suspension must not permanently close registry.
	a.closeAIWindows()
	if _, err := a.createAIWindow(); err == nil {
		t.Fatal("app close allowed creation")
	}
	if poll, err := a.pollAIWindow(newID, "owner", 0, false); err != nil || !poll.Closed || poll.Reason != "app_closed" {
		t.Fatal("app closure not reported", err)
	}
	if _, err := a.postAIWindow(newID, "owner", json.RawMessage(`{}`)); err == nil {
		t.Fatal("app close allowed messages")
	}
	a.closeAIWindows() // Idempotent even after ticker has stopped.
}

func TestAIWindowTickerExpiresWithoutPollingAndContextClears(t *testing.T) {
	a := aiRelayTestApp(t)
	id := newAIRelayID(t, a)
	_, _ = a.postAIWindow(id, "assistant", json.RawMessage(`{"text":"temporary input"}`))
	advanceAIRelayClock(a, aiWindowHeartbeat)
	deadline := time.After(2500 * time.Millisecond)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		a.aiWindows.mu.Lock()
		room := a.aiWindows.rooms[id]
		cleared := room.closed && len(room.queues[0].events) == 0
		a.aiWindows.mu.Unlock()
		if cleared {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("background cleanup did not expire unpolled relay")
		}
	}
	a.cancel()
	a.aiWindows.mu.Lock()
	done := a.aiWindows.done
	a.aiWindows.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("app context did not stop relay cleanup")
	}
	a.aiWindows.mu.Lock()
	count, closed := len(a.aiWindows.rooms), a.aiWindows.closed
	a.aiWindows.mu.Unlock()
	if count != 0 || !closed {
		t.Fatal("app context retained relay payloads")
	}
}

func TestAIWindowHTTPAuthLockAndLimits(t *testing.T) {
	a := aiRelayTestApp(t)
	handler := a.Handler(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("fixture")}})
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("X-CloudShell-Token", token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/api/ai/windows", `{}`, ""); w.Code != http.StatusForbidden {
		t.Fatal("relay creation bypassed App token", w.Code)
	}
	if w := request("POST", "/api/ai/windows?token="+a.Token(), `{}`, ""); w.Code != http.StatusForbidden {
		t.Fatal("relay accepted credential in URL")
	}
	w := request("POST", "/api/ai/windows", `{}`, a.Token())
	var created struct {
		ID string `json:"id"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &created) != nil || !aiWindowIDPattern.MatchString(created.ID) {
		t.Fatal("HTTP creation failed", w.Code, w.Body.String())
	}
	base := "/api/ai/windows/" + created.ID
	if w := request("POST", base+"/messages", `{"side":"owner","payload":{"kind":"state"}}`, a.Token()); w.Code != http.StatusOK {
		t.Fatal("HTTP relay post failed", w.Code)
	}
	for _, query := range []string{"?side=bad&after=0", "?side=assistant&after=-1", "?side=assistant&after=no"} {
		if w := request("GET", base+query, "", a.Token()); w.Code != http.StatusBadRequest {
			t.Fatal("invalid poll accepted", query, w.Code)
		}
	}
	if w := request("GET", base+"?side=assistant&after=2", "", a.Token()); w.Code != http.StatusConflict {
		t.Fatal("future cursor accepted over HTTP", w.Code)
	}
	if w := request("POST", base+"/messages", `{"side":"owner","payload":{}} {}`, a.Token()); w.Code != http.StatusBadRequest {
		t.Fatal("trailing JSON accepted")
	}
	if w := request("POST", base+"/messages", `{"side":"owner","payload":"`+strings.Repeat("x", 2<<20)+`"}`, a.Token()); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatal("HTTP body limit not enforced", w.Code)
	}
	if w := request("GET", base+"?side=assistant&after=0", "", a.Token()); w.Code != 200 || !strings.Contains(w.Body.String(), `"reason":"overflow"`) {
		t.Fatal("body overflow did not close relay", w.Code, w.Body.String())
	}
	trailingID := newAIRelayID(t, a)
	if w := request("POST", "/api/ai/windows/"+trailingID+"/messages", `{"side":"owner","payload":{}}`+strings.Repeat(" ", 2<<20), a.Token()); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatal("oversized trailing data bypassed HTTP size limit", w.Code)
	}
	if poll, err := a.pollAIWindow(trailingID, "owner", 0, false); err != nil || !poll.Closed || poll.Reason != "overflow" {
		t.Fatal("trailing body overflow did not close room", poll, err)
	}
	lockedID := newAIRelayID(t, a)
	_, _ = a.postAIWindow(lockedID, "assistant", json.RawMessage(`{"text":"fixture input"}`))
	enableTestPassword(t, a, 0)
	if _, err := a.LockNow(); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/api/ai/windows/"+lockedID+"?side=owner&after=0", "", a.Token()); w.Code != http.StatusLocked || strings.Contains(w.Body.String(), "fixture input") {
		t.Fatal("locked relay leaked queued input", w.Code)
	}
	if w := request("POST", "/api/ai/windows", `{}`, a.Token()); w.Code != http.StatusLocked {
		t.Fatal("locked relay creation accepted", w.Code)
	}
}

func TestAIWindowConcurrentRelaysAreIndependent(t *testing.T) {
	a := aiRelayTestApp(t)
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = newAIRelayID(t, a)
	}
	var workers sync.WaitGroup
	for i, id := range ids {
		workers.Go(func() {
			for n := 0; n < 40; n++ {
				payload := json.RawMessage(fmt.Sprintf(`{"window":%d,"event":%d}`, i, n))
				seq, err := a.postAIWindow(id, "owner", payload)
				if err != nil {
					t.Error(err)
					return
				}
				poll, err := a.pollAIWindow(id, "assistant", seq-1, false)
				if err != nil || len(poll.Events) != 1 || !bytes.Equal(poll.Events[0].Payload, payload) {
					t.Error("concurrent relay lost or crossed events", err)
					return
				}
			}
		})
	}
	workers.Wait()
}
