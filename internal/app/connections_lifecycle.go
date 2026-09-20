package app

import (
	"errors"
	"net/http"
	"path/filepath"
	"time"
)

type ConnectionHistory struct {
	ProfileID       string    `json:"profileId"`
	LastConnectedAt time.Time `json:"lastConnectedAt"`
	Count           uint64    `json:"count"`
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, profile := range s.config.Servers {
		if profile.ID == id {
			if profile.DeletedAt != nil {
				return nil
			}
			now := time.Now().UTC()
			s.config.Servers[i].DeletedAt = &now
			if err := s.writeLocked(); err != nil {
				s.config.Servers[i] = profile
				return err
			}
			return nil
		}
	}
	return errors.New("服务器配置不存在")
}
func (s *Store) Restore(id string) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, profile := range s.config.Servers {
		if profile.ID == id {
			if profile.DeletedAt == nil {
				return publicProfile(profile), nil
			}
			// Group/key/proxy deletion guards retain these links while in trash.
			s.config.Servers[i].DeletedAt = nil
			if err := s.writeLocked(); err != nil {
				s.config.Servers[i] = profile
				return Profile{}, err
			}
			return publicProfile(s.config.Servers[i]), nil
		}
	}
	return Profile{}, errors.New("回收站记录不存在")
}
func (s *Store) Purge(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, profile := range s.config.Servers {
		if profile.ID == id {
			if profile.DeletedAt == nil {
				return errors.New("只能永久删除回收站中的服务器")
			}
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("回收站记录不存在")
	}
	old := cloneConnectionConfig(s.config)
	s.config.Servers = append(append([]Profile{}, s.config.Servers[:index]...), s.config.Servers[index+1:]...)
	history := []ConnectionHistory{}
	for _, entry := range s.config.ConnectionHistory {
		if entry.ProfileID != id {
			history = append(history, entry)
		}
	}
	s.config.ConnectionHistory = history
	s.config.Appearance = cloneAppearance(s.config.Appearance)
	delete(s.config.Appearance.Layout, "dengshell.history."+id)
	delete(s.config.Appearance.Layout, "dengshell.nic."+id)
	s.dropPathHistoryLocked(map[string]bool{id: true})
	if err := s.writeLocked(); err != nil {
		s.config = old
		return err
	}
	delete(s.historyState, id)
	return nil
}
func (s *Store) MarkConnected(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for _, profile := range s.config.Servers {
		if profile.ID == id && profile.DeletedAt == nil {
			found = true
			break
		}
	}
	if !found {
		return errors.New("服务器已被删除或移入回收站")
	}
	old := append([]ConnectionHistory{}, s.config.ConnectionHistory...)
	index := -1
	for i, entry := range s.config.ConnectionHistory {
		if entry.ProfileID == id {
			index = i
			break
		}
	}
	if index < 0 {
		s.config.ConnectionHistory = append(s.config.ConnectionHistory, ConnectionHistory{ProfileID: id, LastConnectedAt: at.UTC(), Count: 1})
	} else {
		s.config.ConnectionHistory[index].LastConnectedAt = at.UTC()
		if s.config.ConnectionHistory[index].Count < ^uint64(0) {
			s.config.ConnectionHistory[index].Count++
		}
	}
	if err := s.writeLocked(); err != nil {
		s.config.ConnectionHistory = old
		return err
	}
	return nil
}
func (a *App) trashProfile(id string) error {
	// Serialize deletion with Connect's final history-and-session registration.
	// A connection already dialing cannot appear after its profile was trashed.
	a.mu.Lock()
	if err := a.store.Delete(id); err != nil {
		a.mu.Unlock()
		return err
	}
	closing := []*Session{}
	for sessionID, session := range a.sessions {
		if session.ProfileID == id {
			closing = append(closing, session)
			delete(a.sessions, sessionID)
		}
	}
	a.mu.Unlock()
	for _, session := range closing {
		session.Close()
	}
	return nil
}
func (a *App) registerConnectionManagementHTTP(mux *http.ServeMux) {
	a.registerGroupDeletionHTTP(mux)
	mux.HandleFunc("GET /api/config/storage", func(w http.ResponseWriter, r *http.Request) {
		dir, err := filepath.Abs(a.store.dir)
		if err != nil {
			respond(w, nil, err)
			return
		}
		writeJSON(w, map[string]any{
			"configPath": filepath.Join(dir, EncryptedConfigName), "configDir": dir,
			"keyPath": filepath.Join(dir, ConfigKeyName), "backupPath": filepath.Join(dir, EncryptedConfigName+".bak"),
			"assetsDir": filepath.Join(dir, "assets"), "keysDir": filepath.Join(dir, "keys"),
			"schemaVersion": ConfigSchemaVersion, "envelopeVersion": 1, "format": "AES-256-GCM",
			"encrypted": true, "tamperDetection": true, "automaticUnlock": true,
		})
	})
	mux.HandleFunc("POST /api/group-nodes", func(w http.ResponseWriter, r *http.Request) {
		var group ServerGroup
		if !decode(w, r, &group) {
			return
		}
		saved, err := a.store.SaveGroup(group)
		respond(w, saved, err)
	})
	mux.HandleFunc("DELETE /api/group-nodes/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, a.store.DeleteGroup(r.PathValue("id")))
	})
	mux.HandleFunc("POST /api/trash/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		profile, err := a.store.Restore(r.PathValue("id"))
		respond(w, profile, err)
	})
	mux.HandleFunc("DELETE /api/trash/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, a.store.Purge(r.PathValue("id")))
	})
}
