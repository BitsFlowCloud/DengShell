package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

type commandHistoryState struct {
	revision   uint64
	operations []string
}
type CommandHistory struct {
	Entries  []string              `json:"entries"`
	Revision uint64                `json:"revision"`
	Global   *GlobalCommandHistory `json:"global,omitempty"`
}

var historyOperationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{24,96}$`)

// All windows append against the current encrypted Store value under one lock.
// Operation IDs make a retry after a lost HTTP response idempotent. Only the
// latest 1024 requests per profile are retained, with no command text in this cache.
func (s *Store) commandHistory(id, operation, command string, clear bool) (CommandHistory, error) {
	return s.commandHistoryForProfile(id, operation, command, clear, false)
}

func (s *Store) commandHistoryForProfile(id, operation, command string, clear, live bool) (CommandHistory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := "dengshell.history." + id
	if !validSavedLayoutKey(key) {
		return CommandHistory{}, errors.New("服务器历史编号无效")
	}
	_, saved := s.config.Appearance.Layout[key]
	// A profile can disappear while a window still has a live session or an
	// unacknowledged command. Preserve its known history and allow flush/clear;
	// arbitrary caller-supplied IDs cannot create unbounded preference keys.
	found := live || saved || s.historyState[id] != nil
	for _, p := range s.config.Servers {
		if p.ID == id {
			found = true
			break
		}
	}
	if !found {
		return CommandHistory{}, errors.New("服务器配置不存在")
	}
	if s.historyState == nil {
		s.historyState = map[string]*commandHistoryState{}
	}
	state := s.historyState[id]
	if state == nil {
		state = &commandHistoryState{}
		s.historyState[id] = state
	}
	entries := []string{}
	_ = json.Unmarshal(s.config.Appearance.Layout[key], &entries)
	if entries == nil {
		entries = []string{}
	}
	if len(entries) > 200 {
		entries = entries[len(entries)-200:]
	}
	result := CommandHistory{Entries: entries, Revision: state.revision, Global: cloneGlobalCommandHistory(s.config.CommandHistory)}
	if operation == "" {
		return result, nil
	}
	if !historyOperationPattern.MatchString(operation) || (!clear && (strings.TrimSpace(command) == "" || len(command) > 65536)) {
		return CommandHistory{}, errors.New("命令历史请求无效（单条最多 64 KiB）")
	}
	for _, previous := range state.operations {
		if previous == operation {
			return result, nil
		}
	}
	if clear {
		entries = []string{}
	} else if len(entries) == 0 || entries[len(entries)-1] != command {
		entries = append(entries, command)
	}
	if len(entries) > 200 {
		entries = entries[len(entries)-200:]
	}
	value := cloneAppearance(s.config.Appearance)
	value.Layout[key], _ = json.Marshal(entries)
	previousGlobal := s.config.CommandHistory
	if !clear {
		s.config.CommandHistory = appendGlobalCommandHistory(previousGlobal, command)
	}
	if _, err := s.saveAppearanceLocked(value); err != nil {
		s.config.CommandHistory = previousGlobal
		return CommandHistory{}, err
	}
	state.revision++
	state.operations = append(state.operations, operation)
	if len(state.operations) > 1024 {
		state.operations = append([]string(nil), state.operations[len(state.operations)-1024:]...)
	}
	return CommandHistory{Entries: entries, Revision: state.revision, Global: cloneGlobalCommandHistory(s.config.CommandHistory)}, nil
}

func (a *App) commandHistory(id, operation, command string, clear bool) (CommandHistory, error) {
	a.mu.Lock()
	live := false
	for _, session := range a.sessions {
		if session.ProfileID == id {
			live = true
			break
		}
	}
	a.mu.Unlock()
	return a.store.commandHistoryForProfile(id, operation, command, clear, live)
}

func (a *App) registerCommandHistoryHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		value, err := a.store.globalCommandHistory("")
		respond(w, value, err)
	})
	mux.HandleFunc("DELETE /api/history", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Operation string `json:"operation"`
		}
		if !decode(w, r, &input) {
			return
		}
		if !historyOperationPattern.MatchString(input.Operation) {
			writeError(w, http.StatusBadRequest, errors.New("命令历史请求编号无效"))
			return
		}
		value, err := a.store.globalCommandHistory(input.Operation)
		respond(w, value, err)
	})
	mux.HandleFunc("GET /api/profiles/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		value, err := a.commandHistory(r.PathValue("id"), "", "", false)
		respond(w, value, err)
	})
	mutate := func(clear bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var input struct {
				Operation string `json:"operation"`
				Command   string `json:"command"`
			}
			if !decode(w, r, &input) {
				return
			}
			if input.Operation == "" {
				writeError(w, http.StatusBadRequest, errors.New("缺少命令历史请求编号"))
				return
			}
			value, err := a.commandHistory(r.PathValue("id"), input.Operation, input.Command, clear)
			respond(w, value, err)
		}
	}
	mux.HandleFunc("POST /api/profiles/{id}/history", mutate(false))
	mux.HandleFunc("DELETE /api/profiles/{id}/history", mutate(true))
}

// The legacy full-appearance endpoint still exists for integrations. Its stale
// layout snapshot must not replace histories owned by the append/clear API.
func (s *Store) saveFrontendAppearance(value Appearance) (Appearance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value = cloneAppearance(value)
	for key := range value.Layout {
		if strings.HasPrefix(key, "dengshell.history.") {
			delete(value.Layout, key)
		}
	}
	for key, raw := range s.config.Appearance.Layout {
		if strings.HasPrefix(key, "dengshell.history.") {
			value.Layout[key] = append(json.RawMessage(nil), raw...)
		}
	}
	return s.saveAppearanceLocked(value)
}
