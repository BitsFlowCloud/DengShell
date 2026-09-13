package app

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type ServerGroup struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
	Emoji    string `json:"emoji"`
}

func groupIndex(groups []ServerGroup) map[string]ServerGroup {
	result := make(map[string]ServerGroup, len(groups))
	for _, group := range groups {
		result[group.ID] = group
	}
	return result
}
func validateGroupGraph(groups []ServerGroup) error {
	byID := make(map[string]ServerGroup, len(groups))
	siblings := map[string]map[string]bool{}
	for _, group := range groups {
		if group.ID == "" || group.Name == "" {
			return errors.New("分组 ID 或名称为空")
		}
		if _, exists := byID[group.ID]; exists {
			return errors.New("分组 ID 重复")
		}
		byID[group.ID] = group
		if siblings[group.ParentID] == nil {
			siblings[group.ParentID] = map[string]bool{}
		}
		if siblings[group.ParentID][group.Name] {
			return fmt.Errorf("同一层级中分组「%s」重复", group.Name)
		}
		siblings[group.ParentID][group.Name] = true
	}
	for _, group := range groups {
		if group.ParentID != "" {
			if _, exists := byID[group.ParentID]; !exists {
				return errors.New("父分组不存在")
			}
		}
	}
	// Iterative traversal imposes no hierarchy-depth limit and does not consume
	// call-stack depth. Every node becomes complete once, including deep chains.
	done := map[string]bool{}
	for _, group := range groups {
		chain := []string{}
		visiting := map[string]bool{}
		id := group.ID
		for id != "" && !done[id] {
			if visiting[id] {
				return errors.New("不能将分组移到自身或其子分组中")
			}
			visiting[id] = true
			chain = append(chain, id)
			id = byID[id].ParentID
		}
		for _, id := range chain {
			done[id] = true
		}
	}
	return nil
}
func legacyGroupPath(id string, groups map[string]ServerGroup) string {
	parts := []string{}
	for id != "" {
		group, ok := groups[id]
		if !ok {
			break
		}
		parts = append(parts, group.Name)
		id = group.ParentID
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, " / ")
}
func (s *Store) refreshLegacyGroupsLocked() {
	groups := groupIndex(s.config.GroupNodes)
	s.config.Groups = make([]string, 0, len(s.config.GroupNodes))
	paths := map[string]string{}
	for _, group := range s.config.GroupNodes {
		value := legacyGroupPath(group.ID, groups)
		paths[group.ID] = value
		s.config.Groups = append(s.config.Groups, value)
	}
	for i, profile := range s.config.Servers {
		s.config.Servers[i].Group = paths[profile.GroupID]
	}
}
func (s *Store) assignProfileGroupLocked(profile *Profile) error {
	if profile.GroupID != "" {
		for _, group := range s.config.GroupNodes {
			if group.ID == profile.GroupID {
				profile.Group = legacyGroupPath(group.ID, groupIndex(s.config.GroupNodes))
				return nil
			}
		}
		return errors.New("所选分组不存在，请重新选择")
	}
	name := strings.TrimSpace(profile.Group)
	if name == "" {
		name = "我的服务器"
	}
	groups := groupIndex(s.config.GroupNodes)
	for _, group := range s.config.GroupNodes {
		if legacyGroupPath(group.ID, groups) == name {
			if profile.GroupID != "" {
				return errors.New("旧版分组名称存在歧义，请按分组 ID 重新选择")
			}
			profile.GroupID = group.ID
		}
	}
	if profile.GroupID == "" {
		if len(name) > 100 {
			return errors.New("分组名称过长")
		}
		group := ServerGroup{ID: randomID(), Name: name}
		s.config.GroupNodes = append(s.config.GroupNodes, group)
		profile.GroupID = group.ID
	}
	profile.Group = name
	return nil
}
func validateGroupInput(group ServerGroup) error {
	if group.Name == "" || len(group.Name) > 100 || strings.ContainsAny(group.Name, "\x00\r\n") {
		return errors.New("请填写有效分组名称（最多 100 字节）")
	}
	// Emoji may contain joiners, variation selectors, flags and skin modifiers.
	// Limit only its UTF-8 size; never split a multi-codepoint emoji sequence.
	if !utf8.ValidString(group.Emoji) || len(group.Emoji) > 128 || strings.ContainsAny(group.Emoji, "\x00\r\n") {
		return errors.New("分组图标无效或过长")
	}
	return nil
}
func (s *Store) saveGroupLocked(group ServerGroup) (ServerGroup, error) {
	group.Name = strings.TrimSpace(group.Name)
	group.Emoji = strings.TrimSpace(group.Emoji)
	if err := validateGroupInput(group); err != nil {
		return ServerGroup{}, err
	}
	old := cloneConnectionConfig(s.config)
	index := -1
	for i, current := range s.config.GroupNodes {
		if current.ID == group.ID {
			index = i
			break
		}
	}
	if group.ID == "" {
		group.ID = randomID()
	} else if index < 0 {
		return ServerGroup{}, errors.New("分组不存在")
	}
	next := append([]ServerGroup{}, s.config.GroupNodes...)
	if index < 0 {
		next = append(next, group)
	} else {
		next[index] = group
	}
	if err := validateGroupGraph(next); err != nil {
		return ServerGroup{}, err
	}
	s.config.GroupNodes = next
	s.refreshLegacyGroupsLocked()
	if err := s.writeLocked(); err != nil {
		s.config = old
		return ServerGroup{}, err
	}
	return group, nil
}
func (s *Store) SaveGroup(group ServerGroup) (ServerGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveGroupLocked(group)
}
func (s *Store) deleteGroupLocked(id string) error {
	found := false
	for _, group := range s.config.GroupNodes {
		if group.ID == id {
			found = true
		}
		if group.ParentID == id {
			return errors.New("请先移动或删除该分组的子分组")
		}
	}
	if !found {
		return errors.New("分组不存在")
	}
	for _, profile := range s.config.Servers {
		if profile.GroupID == id {
			if profile.DeletedAt != nil {
				return fmt.Errorf("回收站中的服务器「%s」仍属于此分组，请先恢复并移动或永久删除", profile.Name)
			}
			return errors.New("请先移动或删除分组内的服务器")
		}
	}
	old := cloneConnectionConfig(s.config)
	next := []ServerGroup{}
	for _, group := range s.config.GroupNodes {
		if group.ID != id {
			next = append(next, group)
		}
	}
	s.config.GroupNodes = next
	s.refreshLegacyGroupsLocked()
	if err := s.writeLocked(); err != nil {
		s.config = old
		return err
	}
	return nil
}
func (s *Store) DeleteGroup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteGroupLocked(id)
}

// Legacy clients still send flat display names. Resolve a unique display path
// and delegate to ID-based operations; never silently pick a name collision.
func (s *Store) Group(action, name, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	newName = strings.TrimSpace(newName)
	if name == "" || len(name) > 100 || len(newName) > 100 {
		return errors.New("请填写有效分组名称")
	}
	var match *ServerGroup
	byID := groupIndex(s.config.GroupNodes)
	for _, group := range s.config.GroupNodes {
		if legacyGroupPath(group.ID, byID) == name {
			if match != nil {
				return errors.New("分组名称存在歧义，请使用新版分组管理")
			}
			copy := group
			match = &copy
		}
	}
	switch action {
	case "add":
		if match != nil {
			return errors.New("分组已存在")
		}
		_, err := s.saveGroupLocked(ServerGroup{Name: name})
		return err
	case "rename":
		if match == nil || newName == "" {
			return errors.New("分组不存在或新名称为空")
		}
		match.Name = newName
		_, err := s.saveGroupLocked(*match)
		return err
	case "delete":
		if match == nil {
			return errors.New("分组不存在")
		}
		return s.deleteGroupLocked(match.ID)
	default:
		return errors.New("未知分组操作")
	}
}
