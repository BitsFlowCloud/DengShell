package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const pathHistoryPerServer = 200
const pathHistoryTotal = 5000
const pathHistoryBytes = 2 << 20

var errPathHistoryProfileUnavailable = errors.New("连接未保存或已删除，无法保存路径历史")

// Kept inside the encrypted local configuration, outside the sync projection
// and public Config list. The immutable SSH identity prevents reusing another
// host/account's history when an existing profile is edited during a session.
type PathHistoryEntry struct {
	ProfileID string `json:"profileId"`
	Identity  string `json:"identity"`
	PathHistoryItem
}

type PathHistoryItem struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind"`
	VisitedAt time.Time `json:"visitedAt"`
}

func (s *Session) pathHistoryIdentity() string {
	h := sha256.Sum256([]byte(s.ProfileID + "\x00" + s.fileWriteIdentity))
	return hex.EncodeToString(h[:])
}

// Entries are newest first. Both per-server and global budgets keep history
// cheap even when a user has thousands of servers or follows many directories.
func boundedPathHistory(entries []PathHistoryEntry) []PathHistoryEntry {
	result := make([]PathHistoryEntry, 0, min(len(entries), pathHistoryTotal))
	counts, seen, bytes := map[string]int{}, map[string]bool{}, 0
	for _, e := range entries {
		clean, err := remotePath(e.Path)
		if err != nil || clean != e.Path || len(e.Path) > 4096 || !utf8.ValidString(e.Path) || e.ProfileID == "" || len(e.ProfileID) > 128 || len(e.Identity) != 64 || (e.Kind != "folder" && e.Kind != "file") || e.VisitedAt.IsZero() {
			continue
		}
		key := e.ProfileID + "\x00" + e.Identity
		item := key + "\x00" + e.Path
		size := len(e.Path) + len(e.ProfileID) + 192
		if counts[key] >= pathHistoryPerServer || seen[item] || bytes+size > pathHistoryBytes {
			continue
		}
		counts[key]++
		seen[item] = true
		bytes += size
		result = append(result, e)
		if len(result) == pathHistoryTotal {
			break
		}
	}
	return result
}

func (s *Store) pathHistory(profileID, identity string, entry *PathHistoryItem, clear bool) ([]PathHistoryItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if profileID == "" || len(profileID) > 128 || len(identity) != 64 {
		return nil, errors.New("路径历史对应的服务器无效")
	}
	// Keep the existence check and write under the same lock as deletion. A late
	// SFTP completion cannot resurrect data removed by Purge or group deletion.
	if !slices.ContainsFunc(s.config.Servers, func(p Profile) bool { return p.ID == profileID && p.DeletedAt == nil }) {
		return nil, errPathHistoryProfileUnavailable
	}
	previous := s.config.PathHistory
	belongs := func(e PathHistoryEntry) bool { return e.ProfileID == profileID && e.Identity == identity }
	if entry != nil {
		clean, err := remotePath(entry.Path)
		if err != nil || len(clean) > 4096 || !utf8.ValidString(clean) || (entry.Kind != "folder" && entry.Kind != "file") {
			return nil, errors.New("路径历史无效")
		}
		item := *entry
		item.Path, item.VisitedAt = clean, time.Now().UTC()
		// A refresh of the most recent directory must not rewrite the entire
		// encrypted config each time a shell prompt arrives.
		latest := slices.IndexFunc(previous, belongs)
		if latest < 0 || previous[latest].Path != clean || previous[latest].Kind != item.Kind {
			next := make([]PathHistoryEntry, 0, len(previous)+1)
			next = append(next, PathHistoryEntry{profileID, identity, item})
			for _, e := range previous {
				if !belongs(e) || e.Path != clean {
					next = append(next, e)
				}
			}
			s.config.PathHistory = boundedPathHistory(next)
		}
	} else if clear {
		next := make([]PathHistoryEntry, 0, len(previous))
		for _, e := range previous {
			if !belongs(e) {
				next = append(next, e)
			}
		}
		s.config.PathHistory = next
	}
	if !slices.Equal(previous, s.config.PathHistory) {
		if err := s.writeLocked(); err != nil {
			s.config.PathHistory = previous
			return nil, err
		}
	}
	result := []PathHistoryItem{}
	for _, e := range s.config.PathHistory {
		if belongs(e) {
			result = append(result, e.PathHistoryItem)
		}
	}
	return result, nil
}

func (a *App) recordPathHistory(s *Session, path, kind string) string {
	if a.store == nil || s.ProfileID == "" {
		return ""
	}
	if _, err := a.store.pathHistory(s.ProfileID, s.pathHistoryIdentity(), &PathHistoryItem{Path: path, Kind: kind}, false); err != nil && !errors.Is(err, errPathHistoryProfileUnavailable) {
		return "路径已打开，但本机历史未保存：" + err.Error()
	}
	return ""
}

func (s *Store) dropPathHistoryLocked(ids map[string]bool) {
	kept := make([]PathHistoryEntry, 0, len(s.config.PathHistory))
	for _, e := range s.config.PathHistory {
		if !ids[e.ProfileID] {
			kept = append(kept, e)
		}
	}
	s.config.PathHistory = kept
}

func (a *App) registerPathHistoryHTTP(mux *http.ServeMux) {
	for _, method := range []string{"GET", "DELETE"} {
		mux.HandleFunc(method+" /api/sessions/{id}/path-history", func(w http.ResponseWriter, r *http.Request) {
			s, err := a.session(r.PathValue("id"))
			if err != nil {
				writeError(w, 400, err)
				return
			}
			entries, err := a.store.pathHistory(s.ProfileID, s.pathHistoryIdentity(), nil, r.Method == "DELETE")
			if errors.Is(err, errPathHistoryProfileUnavailable) {
				writeJSON(w, map[string]any{"entries": []PathHistoryItem{}, "limit": pathHistoryPerServer, "saved": false})
				return
			}
			query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
			if query != "" {
				entries = slices.DeleteFunc(entries, func(e PathHistoryItem) bool { return !strings.Contains(strings.ToLower(e.Path), query) })
			}
			respond(w, map[string]any{"entries": entries, "limit": pathHistoryPerServer, "saved": true}, err)
		})
	}
}
