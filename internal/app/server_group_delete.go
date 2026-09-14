package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"sync"
)

// The revision binds confirmation to the entire subtree, including credentials.
// Concurrent edits invalidate it; unrelated groups and new history do not.
type GroupDeletePlan struct {
	GroupID  string `json:"groupId"`
	Name     string `json:"name"`
	Groups   int    `json:"groups"`
	Servers  int    `json:"servers"`
	Trashed  int    `json:"trashed"`
	Revision string `json:"revision"`
}

func (s *Store) groupDeletePlanLocked(id string) (GroupDeletePlan, map[string]bool, map[string]bool, error) {
	byID := groupIndex(s.config.GroupNodes)
	root, ok := byID[id]
	if !ok {
		return GroupDeletePlan{}, nil, nil, errors.New("分组不存在，请刷新列表")
	}
	children := map[string][]string{}
	for _, g := range s.config.GroupNodes {
		children[g.ParentID] = append(children[g.ParentID], g.ID)
	}
	queue := []string{id}
	groups := map[string]bool{id: true}
	for i := 0; i < len(queue); i++ {
		for _, child := range children[queue[i]] {
			if !groups[child] {
				groups[child] = true
				queue = append(queue, child)
			}
		}
	}
	nodes := []ServerGroup{}
	for groupID := range groups {
		nodes = append(nodes, byID[groupID])
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	profiles := []Profile{}
	ids := map[string]bool{}
	plan := GroupDeletePlan{GroupID: id, Name: root.Name, Groups: len(nodes)}
	for _, p := range s.config.Servers {
		if groups[p.GroupID] {
			profiles = append(profiles, p)
			ids[p.ID] = true
			if p.DeletedAt != nil {
				plan.Trashed++
			} else {
				plan.Servers++
			}
		}
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	data, _ := json.Marshal(struct {
		Groups   []ServerGroup
		Profiles []Profile
	}{nodes, profiles})
	digest := sha256.Sum256(data)
	plan.Revision = hex.EncodeToString(digest[:])
	return plan, groups, ids, nil
}

func (s *Store) GroupDeletionPlan(id string) (GroupDeletePlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, _, _, err := s.groupDeletePlanLocked(id)
	return plan, err
}

func (s *Store) deleteGroupTree(id, revision, name string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, groups, ids, err := s.groupDeletePlanLocked(id)
	if err != nil {
		return nil, err
	}
	if revision == "" || revision != plan.Revision {
		return nil, errors.New("分组内容已变化，请重新检查并完成三次确认")
	}
	if name != plan.Name {
		return nil, errors.New("输入的分组名称不匹配，未删除任何数据")
	}
	old := cloneConnectionConfig(s.config)
	s.config.Appearance = cloneAppearance(s.config.Appearance)
	s.config.GroupNodes = []ServerGroup{}
	for _, g := range old.GroupNodes {
		if !groups[g.ID] {
			s.config.GroupNodes = append(s.config.GroupNodes, g)
		}
	}
	s.config.Servers = []Profile{}
	for _, p := range old.Servers {
		if !ids[p.ID] {
			s.config.Servers = append(s.config.Servers, p)
		}
	}
	s.config.ConnectionHistory = []ConnectionHistory{}
	for _, h := range old.ConnectionHistory {
		if !ids[h.ProfileID] {
			s.config.ConnectionHistory = append(s.config.ConnectionHistory, h)
		}
	}
	for profileID := range ids {
		delete(s.config.Appearance.Layout, "dengshell.history."+profileID)
		delete(s.config.Appearance.Layout, "dengshell.nic."+profileID)
	}
	s.refreshLegacyGroupsLocked()
	if err = s.writeLocked(); err != nil {
		s.config = old
		return nil, err
	}
	for profileID := range ids {
		delete(s.historyState, profileID)
	}
	return ids, nil
}

func (a *App) deleteGroupTree(id, revision, name string) ([]string, error) {
	// Same lock order as connection registration and history flushing. A dial
	// which finishes after deletion cannot register a deleted profile again.
	a.mu.Lock()
	ids, err := a.store.deleteGroupTree(id, revision, name)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	closing := []*Session{}
	for sessionID, session := range a.sessions {
		if ids[session.ProfileID] {
			closing = append(closing, session)
			delete(a.sessions, sessionID)
		}
	}
	a.mu.Unlock()
	var finished sync.WaitGroup
	for _, session := range closing {
		finished.Add(1)
		go func() { defer finished.Done(); session.Close() }()
	}
	finished.Wait()
	result := []string{}
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func (a *App) registerGroupDeletionHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/group-nodes/{id}/deletion", func(w http.ResponseWriter, r *http.Request) {
		plan, err := a.store.GroupDeletionPlan(r.PathValue("id"))
		respond(w, plan, err)
	})
	mux.HandleFunc("POST /api/group-nodes/{id}/deletion", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Revision      string `json:"revision"`
			Name          string `json:"name"`
			Confirmations int    `json:"confirmations"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Confirmations != 3 {
			respond(w, nil, errors.New("请完成三次删除确认"))
			return
		}
		ids, err := a.deleteGroupTree(r.PathValue("id"), input.Revision, input.Name)
		respond(w, map[string]any{"profileIds": ids}, err)
	})
}
