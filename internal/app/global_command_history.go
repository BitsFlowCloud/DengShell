package app

import (
	"encoding/json"
	"sort"
	"strings"
)

// The local history list collects commands from every connection. Per-profile
// history remains available for that terminal's up/down navigation.
type GlobalCommandHistory struct {
	Entries  []string `json:"entries"`
	Revision uint64   `json:"revision"`
}

const globalHistoryLimit = 2000
const globalHistoryBytes = 2 << 20

func cloneGlobalCommandHistory(history *GlobalCommandHistory) *GlobalCommandHistory {
	copy := &GlobalCommandHistory{Entries: []string{}}
	if history != nil {
		copy.Entries = append(copy.Entries, history.Entries...)
		copy.Revision = history.Revision
	}
	return copy
}

func boundedGlobalCommands(entries []string) []string {
	first, size := len(entries), 2
	for first > 0 && len(entries)-first < globalHistoryLimit {
		encoded, _ := json.Marshal(entries[first-1])
		if size+len(encoded)+1 > globalHistoryBytes {
			break
		}
		size += len(encoded) + 1
		first--
	}
	return append([]string{}, entries[first:]...)
}

func appendGlobalCommandHistory(history *GlobalCommandHistory, command string) *GlobalCommandHistory {
	next := cloneGlobalCommandHistory(history)
	if len(next.Entries) == 0 || next.Entries[len(next.Entries)-1] != command {
		next.Entries = append(next.Entries, command)
	}
	next.Entries = boundedGlobalCommands(next.Entries)
	next.Revision++
	return next
}

func migrateGlobalCommandHistory(config *Config) bool {
	if config.CommandHistory != nil {
		return false
	}
	// Legacy records carry no timestamps. Preserve each source's order using a
	// deterministic source order; do not invent a chronology between servers.
	keys := []string{}
	for key := range config.Appearance.Layout {
		if strings.HasPrefix(key, "dengshell.history.") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	entries := []string{}
	for _, key := range keys {
		var previous []string
		if json.Unmarshal(config.Appearance.Layout[key], &previous) != nil {
			continue
		}
		for _, command := range previous {
			if strings.TrimSpace(command) != "" && len(command) <= 65536 {
				entries = append(entries, command)
			}
		}
	}
	config.CommandHistory = &GlobalCommandHistory{Entries: boundedGlobalCommands(entries)}
	return true
}

// Clear operations are retry-safe: a lost response followed by another
// window's append must not cause the new command to disappear on retry.
func (s *Store) globalCommandHistory(clearOperation string) (*GlobalCommandHistory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clearOperation == "" {
		return cloneGlobalCommandHistory(s.config.CommandHistory), nil
	}
	for _, operation := range s.globalHistoryOperations {
		if operation == clearOperation {
			return cloneGlobalCommandHistory(s.config.CommandHistory), nil
		}
	}
	old := s.config.CommandHistory
	next := cloneGlobalCommandHistory(old)
	next.Entries = []string{}
	next.Revision++
	s.config.CommandHistory = next
	if err := s.writeLocked(); err != nil {
		s.config.CommandHistory = old
		return nil, err
	}
	s.globalHistoryOperations = append(s.globalHistoryOperations, clearOperation)
	if len(s.globalHistoryOperations) > 1024 {
		s.globalHistoryOperations = append([]string(nil), s.globalHistoryOperations[len(s.globalHistoryOperations)-1024:]...)
	}
	return cloneGlobalCommandHistory(next), nil
}
