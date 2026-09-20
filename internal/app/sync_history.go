package app

import (
	"cloudshell/internal/syncvault"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
)

func (a *App) registerSyncHistoryHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sync/history", func(w http.ResponseWriter, r *http.Request) {
		r, finish, ok := a.lockSyncHTTP(w, r)
		if !ok {
			return
		}
		defer finish()
		s := &a.syncState
		if s.profile == nil || s.backend == nil {
			writeError(w, 400, errors.New("请先解锁同步空间"))
			return
		}
		objects, e := s.backend.List(r.Context())
		if e == nil {
			_, e = syncvault.Heads(objects)
		}
		sort.Slice(objects, func(i, j int) bool {
			if objects[i].Device == objects[j].Device {
				return objects[i].Sequence > objects[j].Sequence
			}
			return objects[i].Device < objects[j].Device
		})
		a.respondSync(w, objects, e)
	})
	mux.HandleFunc("POST /api/sync/restore", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		if !decode(w, r, &in) {
			return
		}
		r, finish, ok := a.lockSyncHTTP(w, r)
		if !ok {
			return
		}
		defer finish()
		s := &a.syncState
		if s.profile == nil || s.backend == nil {
			writeError(w, 400, errors.New("请先解锁同步空间"))
			return
		}
		if s.profile.Pending != nil {
			writeError(w, 409, errors.New("请先完成待重试的同步，再恢复历史"))
			return
		}
		objects, e := s.backend.List(r.Context())
		if e == nil {
			_, e = syncvault.Heads(objects)
		}
		var selected syncvault.Object
		for _, o := range objects {
			if o.ID == in.ID {
				selected = o
				break
			}
		}
		if e == nil && selected.ID == "" {
			e = errors.New("历史版本不存在或已经按保留策略清理")
		}
		var snapshot syncvault.Snapshot
		if e == nil {
			var b []byte
			b, e = s.backend.Read(r.Context(), selected)
			if e == nil {
				snapshot, e = syncvault.DecodeSnapshot(selected, b, s.profile.Master, s.profile.Metadata.Vault)
			}
		}
		if e == nil {
			e = a.RequireUnlocked()
		}
		if e == nil {
			var current map[string]json.RawMessage
			current, e = a.store.syncProjection(s.profile.Metadata.Secrets)
			if e == nil {
				e = a.store.applySync(current, projectedEntries(snapshot.Entries), s.profile.Metadata.Secrets)
			}
			if e == nil {
				s.changed++
				s.conflicts = nil
				s.lastError = ""
				s.notice = "历史版本已恢复到本机，将作为一次新修改同步到其他设备。"
			}
		}
		a.respondSync(w, map[string]bool{"ok": true}, e)
	})
}
