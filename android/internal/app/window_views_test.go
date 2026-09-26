package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

const sourceWindowID = "source-window-id-000001"
const targetWindowID = "target-window-id-000001"

func registerWindowPair(t *testing.T, a *App, s *Session) {
	t.Helper()
	for _, v := range []WindowViewUpdate{{ID: sourceWindowID, Title: "原窗口", SessionIDs: []string{s.ID}, Visible: true}, {ID: targetWindowID, Title: "目标窗口", SessionIDs: []string{}, Visible: true}} {
		if _, err := a.RegisterWindowView(v); err != nil {
			t.Fatal(err)
		}
	}
}
func TestWindowViewsLifecycleAndCopies(t *testing.T) {
	a := &App{}
	input := WindowViewUpdate{ID: sourceWindowID, Title: "  工作窗口  ", SessionIDs: []string{"one", "one", "two"}, Visible: true, NativeHandle: "c0ffee"}
	got, err := a.RegisterWindowView(input)
	if err != nil {
		t.Fatal(err)
	}
	input.SessionIDs[0] = "modified"
	if got.Title != "工作窗口" || len(got.SessionIDs) != 2 || got.SessionIDs[0] != "one" {
		t.Fatalf("registry must normalize and copy: %+v", got)
	}
	values := a.WindowViews()
	values[0].SessionIDs[0] = "mutated-copy"
	if a.WindowViews()[0].SessionIDs[0] != "one" {
		t.Fatal("list leaked mutable registry slice")
	}
	input.SessionIDs = []string{"one"}
	input.NativeHandle = ""
	got, err = a.RegisterWindowView(input)
	if err != nil || got.NativeHandle != "c0ffee" {
		t.Fatal("browser heartbeat erased native window identity")
	}
	a.mu.Lock()
	expired := a.windowViews[sourceWindowID]
	expired.UpdatedAt = time.Now().Add(-windowViewTTL - time.Second)
	a.windowViews[sourceWindowID] = expired
	a.mu.Unlock()
	if len(a.WindowViews()) != 0 {
		t.Fatal("offline window remained selectable")
	}
	if _, err = a.IncomingWindowHandoffs(sourceWindowID); err == nil {
		t.Fatal("offline target polled incoming transfers")
	}
	a.ForgetWindowView(sourceWindowID)
	for _, v := range []WindowViewUpdate{{ID: "short"}, {ID: sourceWindowID, Title: strings.Repeat("x", 241)}, {ID: sourceWindowID, SessionIDs: []string{""}}} {
		if _, err = a.RegisterWindowView(v); err == nil {
			t.Fatalf("accepted invalid view: %+v", v)
		}
	}
}
func TestWindowMergeTargetValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*App, *WindowMergeRequest)
	}{
		{"same window", func(a *App, r *WindowMergeRequest) { r.TargetID = r.SourceID }},
		{"source missing", func(a *App, r *WindowMergeRequest) { delete(a.windowViews, r.SourceID) }},
		{"target missing", func(a *App, r *WindowMergeRequest) { delete(a.windowViews, r.TargetID) }},
		{"target hidden", func(a *App, r *WindowMergeRequest) {
			v := a.windowViews[r.TargetID]
			v.Visible = false
			a.windowViews[r.TargetID] = v
		}},
		{"target stale", func(a *App, r *WindowMergeRequest) {
			v := a.windowViews[r.TargetID]
			v.UpdatedAt = time.Now().Add(-2 * windowViewTTL)
			a.windowViews[r.TargetID] = v
		}},
		{"source no longer owns tab", func(a *App, r *WindowMergeRequest) {
			v := a.windowViews[r.SourceID]
			v.SessionIDs = nil
			a.windowViews[r.SourceID] = v
		}},
		{"target already owns tab", func(a *App, r *WindowMergeRequest) {
			v := a.windowViews[r.TargetID]
			v.SessionIDs = []string{r.Handoff.SessionID}
			a.windowViews[r.TargetID] = v
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, s := reservedWindowFixture(t, "merge-validation-nonce-00001")
			registerWindowPair(t, a, s)
			r := WindowMergeRequest{SourceID: sourceWindowID, TargetID: targetWindowID, Handoff: windowSnapshot(s.ID, "merge-validation-nonce-00001")}
			tc.change(a, &r)
			if _, err := a.MergeWindow(r); err == nil {
				t.Fatal("invalid target accepted")
			}
			if len(a.windowHandoffs) != 0 {
				t.Fatal("invalid merge retained snapshot")
			}
		})
	}
}
func TestWindowMergeOnlyTargetCanReadAndCompletionFilters(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "cancelled"}[cancelled], func(t *testing.T) {
			nonce := "merge-target-access-nonce-0001"
			a, s := reservedWindowFixture(t, nonce)
			registerWindowPair(t, a, s)
			info, err := a.MergeWindow(WindowMergeRequest{SourceID: sourceWindowID, TargetID: targetWindowID, Handoff: windowSnapshot(s.ID, nonce)})
			if err != nil {
				t.Fatal(err)
			}
			if info.Nonce != nonce || info.SessionID != s.ID {
				t.Fatalf("wrong delivery: %+v", info)
			}
			own, _ := a.IncomingWindowHandoffs(sourceWindowID)
			target, _ := a.IncomingWindowHandoffs(targetWindowID)
			if len(own) != 0 || len(target) != 1 {
				t.Fatal("handoff sent to wrong window")
			}
			mux := http.NewServeMux()
			a.registerWindowHandoffHTTP(mux)
			for _, q := range []string{"", "?viewId=" + sourceWindowID} {
				r := httptest.NewRecorder()
				mux.ServeHTTP(r, httptest.NewRequest("GET", "/api/windows/handoff/"+nonce+q, nil))
				if r.Code != 403 {
					t.Fatalf("wrong window read snapshot: %d", r.Code)
				}
			}
			r := httptest.NewRecorder()
			mux.ServeHTTP(r, httptest.NewRequest("GET", "/api/windows/handoff/"+nonce+"?viewId="+targetWindowID, nil))
			if r.Code != 200 {
				t.Fatalf("target denied: %d %s", r.Code, r.Body)
			}
			var received WindowHandoff
			if err = json.Unmarshal(r.Body.Bytes(), &received); err != nil || received.Terminal == "" {
				t.Fatal("target did not receive terminal content")
			}
			var completionErr error
			if cancelled {
				completionErr = errTerminalHandoffCancelled
			}
			s.terminalRelay.finishHandoff(s.terminalRelay.handoff, completionErr)
			target, _ = a.IncomingWindowHandoffs(targetWindowID)
			if len(target) != 0 {
				t.Fatal("completed handoff remained in inbox")
			}
			if _, err = a.MergeWindow(WindowMergeRequest{SourceID: sourceWindowID, TargetID: targetWindowID, Handoff: windowSnapshot(s.ID, nonce)}); err == nil {
				t.Fatal("completed handoff delivered twice")
			}
		})
	}
}
func TestWindowViewsConcurrentHeartbeat(t *testing.T) {
	a := &App{}
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 30; j++ {
				_, _ = a.RegisterWindowView(WindowViewUpdate{ID: sourceWindowID, Title: "并发窗口", SessionIDs: []string{"one"}, Visible: true})
				_ = a.WindowViews()
				_, _ = a.IncomingWindowHandoffs(sourceWindowID)
			}
		}()
	}
	workers.Wait()
	if len(a.WindowViews()) != 1 {
		t.Fatal("concurrent heartbeat duplicated window")
	}
}
func TestWindowViewsHTTPAuthorization(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	handler := a.Handler(fstest.MapFS{})
	for _, route := range [][2]string{{"GET", "/api/windows/views"}, {"POST", "/api/windows/views"}, {"DELETE", "/api/windows/views/" + sourceWindowID}, {"GET", "/api/windows/views/" + sourceWindowID + "/incoming"}, {"POST", "/api/windows/merge"}} {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(route[0], route[1], strings.NewReader(`{}`)))
		if r.Code != 403 {
			t.Fatalf("%s accessible without backend token: %d", route[1], r.Code)
		}
	}
}

func TestWindowMergeRoundTripKeepsSamePTY(t *testing.T) {
	a, s, address := newRelayFixture(t)
	registerWindowPair(t, a, s)
	peer := dialRelayTest(t, address)
	peer.command(t, "export DENG_MERGE_PID=$$; export DENG_MERGE_VALUE=roundtrip; cd /tmp; printf '\\nMERGE_INITIAL_READY\\n'", "\r\nMERGE_INITIAL_READY\r\n")
	source, target := sourceWindowID, targetWindowID
	for i, nonce := range []string{"merge-pty-first-nonce-0000001", "merge-pty-return-nonce-000001"} {
		peer.send(t, relayMessage{Type: "handoff-begin", Nonce: nonce})
		peer.until(t, func(m relayMessage) bool { return m.Type == "handoff-ready" && m.Nonce == nonce })
		if _, err := a.MergeWindow(WindowMergeRequest{SourceID: source, TargetID: target, Handoff: windowSnapshot(s.ID, nonce)}); err != nil {
			t.Fatal(err)
		}
		incoming, err := a.IncomingWindowHandoffs(target)
		if err != nil || len(incoming) != 1 {
			t.Fatalf("pending merge missing: %+v %v", incoming, err)
		}
		next := dialRelayTest(t, address+"&handoff="+nonce)
		attached, err := a.CancelWindowHandoff(nonce)
		if err != nil || !attached {
			t.Fatalf("committed merge not preserved: %v %v", attached, err)
		}
		next.command(t, "if [ \"$DENG_MERGE_PID\" = \"$$\" ] && [ \"$DENG_MERGE_VALUE\" = roundtrip ] && [ \"$PWD\" = /tmp ]; then printf '\\nMERGE_PTY_PRESERVED\\n'; fi", "\r\nMERGE_PTY_PRESERVED\r\n")
		pending, _ := a.IncomingWindowHandoffs(target)
		if len(pending) != 0 {
			t.Fatal("committed merge remained pending")
		}
		if _, err = a.RegisterWindowView(WindowViewUpdate{ID: source, Visible: true, SessionIDs: []string{}}); err != nil {
			t.Fatal(err)
		}
		if _, err = a.RegisterWindowView(WindowViewUpdate{ID: target, Visible: true, SessionIDs: []string{s.ID}}); err != nil {
			t.Fatal(err)
		}
		peer = next
		source, target = target, source
		t.Logf("hop %d retained shell PID, environment and cwd", i+1)
	}
}
func TestWindowMergeTargetCloseRestoresSource(t *testing.T) {
	a, s, address := newRelayFixture(t)
	registerWindowPair(t, a, s)
	peer := dialRelayTest(t, address)
	nonce := "merge-target-closed-nonce-00001"
	peer.send(t, relayMessage{Type: "handoff-begin", Nonce: nonce})
	peer.until(t, func(m relayMessage) bool { return m.Type == "handoff-ready" })
	if _, err := a.MergeWindow(WindowMergeRequest{SourceID: sourceWindowID, TargetID: targetWindowID, Handoff: windowSnapshot(s.ID, nonce)}); err != nil {
		t.Fatal(err)
	}
	a.ForgetWindowView(targetWindowID)
	peer.until(t, func(m relayMessage) bool { return m.Type == "handoff-cancelled" })
	peer.command(t, "printf '\\nMERGE_SOURCE_RECOVERED\\n'", "\r\nMERGE_SOURCE_RECOVERED\r\n")
	attached, err := a.CancelWindowHandoff(nonce)
	if err != nil || attached {
		t.Fatalf("source recovery not idempotent: %v %v", attached, err)
	}
}
