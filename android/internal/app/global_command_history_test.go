package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestGlobalCommandHistoryCollectsAllProfilesAndSurvivesDeletion(t *testing.T) {
	s, first := historyStoreFixture(t)
	second, err := s.Save(Profile{Name: "second", Host: "second.invalid", User: "fixture"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := first
			if i%2 == 1 {
				id = second.ID
			}
			if _, err := s.commandHistory(id, randomID(), fmt.Sprintf("cmd-%02d", i), false); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	all, err := s.globalCommandHistory("")
	if err != nil || len(all.Entries) != 40 {
		t.Fatal("lost commands from different profiles", err)
	}
	seen := map[string]bool{}
	for _, command := range all.Entries {
		seen[command] = true
	}
	for i := 0; i < 40; i++ {
		if !seen[fmt.Sprintf("cmd-%02d", i)] {
			t.Fatal("missing command", i)
		}
	}
	perProfile, _ := s.commandHistory(first, "", "", false)
	if len(perProfile.Entries) != 20 {
		t.Fatal("global listing altered per-terminal navigation")
	}
	all.Entries[0] = "tampered public copy"
	if err := s.Delete(first); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(first); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.List().CommandHistory
	if len(got.Entries) != 40 || got.Entries[0] == "tampered public copy" {
		t.Fatal("history lost on delete/restart or public snapshot aliased store")
	}
}

func TestGlobalCommandHistoryMigrationClearAndRetry(t *testing.T) {
	s, first := historyStoreFixture(t)
	second, err := s.Save(Profile{Name: "second", Host: "second.invalid", User: "fixture"}, false)
	if err != nil {
		t.Fatal(err)
	}
	s.config.Appearance.Layout["dengshell.history."+first] = json.RawMessage(`["legacy-a-1","legacy-a-2"]`)
	s.config.Appearance.Layout["dengshell.history."+second.ID] = json.RawMessage(`["legacy-b"]`)
	s.config.CommandHistory = nil
	if err := s.writeLocked(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := s.globalCommandHistory("")
	if len(all.Entries) != 3 {
		t.Fatal("legacy records not migrated")
	}
	appendOperation, clearOperation := randomID(), randomID()
	if _, err := s.commandHistory(first, appendOperation, "once", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.globalCommandHistory(clearOperation); err != nil {
		t.Fatal(err)
	}
	if _, err := s.commandHistory(second.ID, randomID(), "after-clear", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.commandHistory(first, appendOperation, "once", false); err != nil {
		t.Fatal(err)
	}
	all, err = s.globalCommandHistory(clearOperation)
	if err != nil || !reflect.DeepEqual(all.Entries, []string{"after-clear"}) {
		t.Fatal("lost-response retry changed later records", err)
	}
	if _, err := s.globalCommandHistory(randomID()); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List().CommandHistory.Entries) != 0 {
		t.Fatal("restart remigrated cleared legacy records")
	}
}

func TestGlobalCommandHistoryFailedWritesAreAtomic(t *testing.T) {
	s, id := historyStoreFixture(t)
	if _, err := s.commandHistory(id, randomID(), "before", false); err != nil {
		t.Fatal(err)
	}
	before := s.List()
	if err := os.WriteFile(filepath.Join(s.dir, ConfigKeyName), make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.commandHistory(id, randomID(), "failed", false); err == nil {
		t.Fatal("append ignored disk failure")
	}
	if _, err := s.globalCommandHistory(randomID()); err == nil {
		t.Fatal("clear ignored disk failure")
	}
	if !reflect.DeepEqual(before, s.List()) {
		t.Fatal("failed save changed global or per-profile history")
	}
}

func TestGlobalCommandHistoryStorageBounds(t *testing.T) {
	entries := make([]string, 2100)
	for i := range entries {
		entries[i] = fmt.Sprintf("%d", i)
	}
	got := boundedGlobalCommands(entries)
	if len(got) != 2000 || got[0] != "100" {
		t.Fatal("count limit did not keep latest commands")
	}
	for i := range entries {
		entries[i] = strings.Repeat("\x01", 16000)
	}
	encoded, err := json.Marshal(boundedGlobalCommands(entries))
	if err != nil || len(encoded) > globalHistoryBytes {
		t.Fatal("escaped JSON exceeded byte limit")
	}
}
