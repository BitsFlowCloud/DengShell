package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"
	"unicode/utf8"
)

const directoryFavoritesPerServer = 100
const directoryFavoritesTotal = 5000
const directoryFavoritesBytes = 2 << 20

// Local encrypted preferences, scoped to the saved connection and verified SSH
// identity. They are not included in the public configuration or sync payload.
type DirectoryFavorite struct {
	ProfileID string `json:"profileId"`
	Identity  string `json:"identity"`
	Path      string `json:"path"`
}

func validDirectoryFavorite(e DirectoryFavorite) bool {
	clean, err := remotePath(e.Path)
	return err == nil && clean == e.Path && len(e.Path) <= 4096 && utf8.ValidString(e.Path) && e.ProfileID != "" && len(e.ProfileID) <= 128 && len(e.Identity) == 64
}
func boundedDirectoryFavorites(entries []DirectoryFavorite) []DirectoryFavorite {
	result := make([]DirectoryFavorite, 0, min(len(entries), directoryFavoritesTotal))
	counts, seen, bytes := map[string]int{}, map[DirectoryFavorite]bool{}, 0
	for _, e := range entries {
		key := e.ProfileID + "\x00" + e.Identity
		size := len(e.Path) + len(e.ProfileID) + 192
		if !validDirectoryFavorite(e) || seen[e] || counts[key] >= directoryFavoritesPerServer || bytes+size > directoryFavoritesBytes {
			continue
		}
		counts[key]++
		seen[e] = true
		bytes += size
		result = append(result, e)
		if len(result) == directoryFavoritesTotal {
			break
		}
	}
	return result
}
func (s *Store) directoryFavorites(profile, identity, target, method string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if profile == "" || len(profile) > 128 || len(identity) != 64 {
		return nil, errors.New("请先保存此连接，再收藏常用目录")
	}
	if !slices.ContainsFunc(s.config.Servers, func(p Profile) bool { return p.ID == profile && p.DeletedAt == nil }) {
		return nil, errors.New("连接已删除，无法使用常用目录")
	}
	belongs := func(e DirectoryFavorite) bool { return e.ProfileID == profile && e.Identity == identity }
	previous := s.config.DirectoryFavorites
	next := previous
	switch method {
	case "POST", "DELETE":
		clean, err := remotePath(target)
		entry := DirectoryFavorite{profile, identity, clean}
		if err != nil || !validDirectoryFavorite(entry) {
			return nil, errors.New("请输入有效的目录路径（最多 4096 字节）")
		}
		if method == "DELETE" {
			next = make([]DirectoryFavorite, 0, len(previous))
			for _, e := range previous {
				if e != entry {
					next = append(next, e)
				}
			}
		} else if !slices.Contains(previous, entry) {
			count, bytes := 0, 0
			for _, e := range previous {
				if belongs(e) {
					count++
				}
				bytes += len(e.Path) + len(e.ProfileID) + 192
			}
			if count >= directoryFavoritesPerServer || len(previous) >= directoryFavoritesTotal || bytes+len(clean)+len(profile)+192 > directoryFavoritesBytes {
				return nil, errors.New("常用目录已达上限，请先移除不再使用的收藏")
			}
			next = append([]DirectoryFavorite{entry}, previous...)
		}
	case "GET":
	default:
		return nil, errors.New("无效的收藏操作")
	}
	if !slices.Equal(previous, next) {
		s.config.DirectoryFavorites = next
		if err := s.writeLocked(); err != nil {
			s.config.DirectoryFavorites = previous
			return nil, err
		}
	}
	result := []string{}
	for _, e := range s.config.DirectoryFavorites {
		if belongs(e) {
			result = append(result, e.Path)
		}
	}
	return result, nil
}
func (s *Store) dropDirectoryFavoritesLocked(ids map[string]bool) {
	kept := make([]DirectoryFavorite, 0, len(s.config.DirectoryFavorites))
	for _, e := range s.config.DirectoryFavorites {
		if !ids[e.ProfileID] {
			kept = append(kept, e)
		}
	}
	s.config.DirectoryFavorites = kept
}
func (a *App) registerDirectoryFavoritesHTTP(mux *http.ServeMux) {
	for _, method := range []string{"GET", "POST", "DELETE"} {
		mux.HandleFunc(method+" /api/sessions/{id}/directory-favorites", func(w http.ResponseWriter, r *http.Request) {
			s, err := a.session(r.PathValue("id"))
			if err != nil {
				writeError(w, 400, err)
				return
			}
			var input struct {
				Path string `json:"path"`
			}
			if r.Method != "GET" && !decode(w, r, &input) {
				return
			}
			if s.ProfileID == "" {
				respond(w, map[string]any{"paths": []string{}, "saved": false, "limit": directoryFavoritesPerServer}, nil)
				return
			}
			if r.Method == "POST" {
				clean, e := remotePath(input.Path)
				if e != nil || len(clean) > 4096 || !utf8.ValidString(clean) {
					writeError(w, 400, errors.New("无效的目录路径"))
					return
				}
				ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
				defer cancel()
				op := startSFTPOperation(ctx, s, sftpIdleTimeout)
				defer op.close()
				info, e := s.files.Stat(clean)
				if e != nil {
					writeError(w, 400, op.err(e))
					return
				}
				if !info.IsDir() {
					writeError(w, 400, errors.New("只能收藏目录"))
					return
				}
				if e = op.ctx.Err(); e != nil {
					writeError(w, 400, e)
					return
				}
			}
			entries, err := a.store.directoryFavorites(s.ProfileID, s.pathHistoryIdentity(), input.Path, r.Method)
			respond(w, map[string]any{"paths": entries, "saved": true, "limit": directoryFavoritesPerServer}, err)
		})
	}
}
