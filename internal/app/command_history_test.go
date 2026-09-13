package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"
)

func historyStoreFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "isolated history", Host: "history.invalid", User: "fixture"}, false)
	if err != nil {
		t.Fatal(err)
	}
	return s, p.ID
}

func TestCommandHistoryDeletedProfileFlushAndClear(t *testing.T) {
	for _, mode := range []string{"live session without prior history", "persisted history", "previously loaded profile"} {
		t.Run(mode, func(t *testing.T) {
			a, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			p, err := a.store.Save(Profile{Name: "deleted fixture", Host: "history.invalid", User: "fixture"}, false)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "live session without prior history" {
				ctx, cancel := context.WithCancel(context.Background())
				a.sessions["isolated-session"] = &Session{ID: "isolated-session", ProfileID: p.ID, ctx: ctx, cancel: cancel}
			} else {
				operation, command := "", ""
				if mode == "persisted history" {
					operation, command = randomID(), "BEFORE_DELETE"
				}
				if _, err := a.store.commandHistory(p.ID, operation, command, false); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.store.Delete(p.ID); err != nil {
				t.Fatal(err)
			}
			if err := a.store.Purge(p.ID); err != nil {
				t.Fatal(err)
			}
			handler := a.Handler(fstest.MapFS{"index.html": {Data: []byte("fixture")}})
			request := func(method, id, command string) (int, CommandHistory) {
				body, _ := json.Marshal(map[string]string{"operation": randomID(), "command": command})
				req := httptest.NewRequest(method, "http://wails.localhost/api/profiles/"+id+"/history", bytes.NewReader(body))
				req.Header.Set("X-CloudShell-Token", a.Token())
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				var value CommandHistory
				_ = json.Unmarshal(response.Body.Bytes(), &value)
				return response.Code, value
			}
			if status, _ := request("GET", p.ID, ""); status != 200 {
				t.Fatalf("known deleted history cannot load: %d", status)
			}
			if status, got := request("POST", p.ID, "AFTER_DELETE"); status != 200 || len(got.Entries) == 0 || got.Entries[len(got.Entries)-1] != "AFTER_DELETE" {
				t.Fatalf("pending append cannot flush: %d %+v", status, got)
			}
			if status, got := request("DELETE", p.ID, ""); status != 200 || len(got.Entries) != 0 {
				t.Fatalf("deleted profile cannot clear: %d %+v", status, got)
			}
			if status, _ := request("POST", randomID(), "UNKNOWN"); status == 200 {
				t.Fatal("unrestricted unknown history accepted")
			}
			reopened, err := OpenStore(a.store.dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reopened.commandHistory(p.ID, randomID(), "KNOWN_AFTER_REOPEN", false); err != nil {
				t.Fatal("persisted deleted profile history lost ownership", err)
			}
		})
	}
}

func TestCommandHistoryConcurrentWindowsAndHandoffKeepEveryAppend(t *testing.T) {
	s, id := historyStoreFixture(t)
	before := s.List().Appearance
	if _, err := s.commandHistory(id, randomID(), "BEFORE_DETACH", false); err != nil {
		t.Fatal(err)
	}
	const count = 48
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.commandHistory(id, randomID(), fmt.Sprintf("WINDOW_%02d", i), false); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	// A receiving window may submit an unrelated setting from before detach.
	if _, err := s.saveFrontendAppearance(before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchAppearance(preferencePatch(t, `{"layout":{"dengshell.history.`+id+`":["BEFORE_DETACH"]}}`)); err == nil {
		t.Fatal("stale array replacement accepted")
	}
	if _, err := s.commandHistory(id, randomID(), "AFTER_MERGE", false); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.commandHistory(id, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != count+2 || got.Entries[0] != "BEFORE_DETACH" || got.Entries[len(got.Entries)-1] != "AFTER_MERGE" {
		t.Fatalf("lost history: %#v", got.Entries)
	}
	seen := map[string]bool{}
	for _, entry := range got.Entries {
		seen[entry] = true
	}
	for i := 0; i < count; i++ {
		if !seen[fmt.Sprintf("WINDOW_%02d", i)] {
			t.Fatalf("lost window %d", i)
		}
	}
}

func TestCommandHistoryRetryAndClearDoNotResurrectStaleArray(t *testing.T) {
	s, id := historyStoreFixture(t)
	first, clear := randomID(), randomID()
	if _, err := s.commandHistory(id, first, "OLD", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.commandHistory(id, clear, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.commandHistory(id, randomID(), "NEW", false); err != nil {
		t.Fatal(err)
	}
	// Simulate HTTP responses lost after commit, then retried by another window.
	if _, err := s.commandHistory(id, first, "OLD", false); err != nil {
		t.Fatal(err)
	}
	got, err := s.commandHistory(id, clear, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Entries, []string{"NEW"}) || got.Revision != 3 {
		t.Fatalf("replayed mutation changed history: %+v", got)
	}
}

func TestCommandHistoryBoundsAndConsecutiveDedup(t *testing.T) {
	s, id := historyStoreFixture(t)
	for i := 0; i < 205; i++ {
		if _, err := s.commandHistory(id, randomID(), fmt.Sprintf("cmd %d", i), false); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.commandHistory(id, randomID(), "cmd 204", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 200 || got.Entries[0] != "cmd 5" {
		t.Fatalf("incorrect history bound: %d %q", len(got.Entries), got.Entries[0])
	}
}
