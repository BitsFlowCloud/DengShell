package app

import (
	"errors"
	"net/http"
	"slices"
	"strings"
)

// Move only the requested item under the store lock. An old browser view must
// never overwrite a command body or drop commands added in another window.
func (s *Store) MoveCommand(id, targetID, group string, after bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.config.Commands
	index := slices.IndexFunc(old, func(c QuickCommand) bool { return c.ID == id })
	if index < 0 {
		return errors.New("快捷命令不存在，请刷新后重试")
	}
	if group != "" && !slices.Contains(s.config.CommandGroups, group) {
		return errors.New("目标分组不存在，请刷新后重试")
	}
	if targetID == id {
		return nil
	}
	if targetID != "" && !slices.ContainsFunc(old, func(c QuickCommand) bool { return c.ID == targetID }) {
		return errors.New("目标命令不存在，请刷新后重试")
	}
	next := append([]QuickCommand{}, old...)
	item := next[index]
	if group != "" {
		item.Group = group
	}
	next = slices.Delete(next, index, index+1)
	position := len(next)
	if targetID != "" {
		position = slices.IndexFunc(next, func(c QuickCommand) bool { return c.ID == targetID })
		if after {
			position++
		}
	} else if group != "" {
		for i, c := range next {
			if c.Group == group {
				position = i + 1
			}
		}
	}
	s.config.Commands = slices.Insert(next, position, item)
	if err := s.writeLocked(); err != nil {
		s.config.Commands = old
		return err
	}
	return nil
}

func (s *Store) MoveCommandGroup(name, target string, after bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.config.CommandGroups
	i, j := slices.Index(old, name), slices.Index(old, target)
	if i < 0 || j < 0 {
		return errors.New("命令分组不存在，请刷新后重试")
	}
	if i == j {
		return nil
	}
	next := slices.Delete(append([]string{}, old...), i, i+1)
	j = slices.Index(next, target)
	if after {
		j++
	}
	s.config.CommandGroups = slices.Insert(next, j, name)
	if err := s.writeLocked(); err != nil {
		s.config.CommandGroups = old
		return err
	}
	return nil
}

func (s *Store) RenameCommandGroup(name, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	newName = strings.TrimSpace(newName)
	if err := validateCommandGroup(newName); err != nil {
		return err
	}
	i := slices.Index(s.config.CommandGroups, name)
	if i < 0 {
		return errors.New("命令分组不存在")
	}
	if name == newName {
		return nil
	}
	if slices.Contains(s.config.CommandGroups, newName) {
		return errors.New("此命令分组已存在")
	}
	old, oldGroups := s.config.Commands, s.config.CommandGroups
	s.config.CommandGroups = append([]string{}, oldGroups...)
	s.config.CommandGroups[i] = newName
	s.config.Commands = append([]QuickCommand{}, old...)
	for i := range s.config.Commands {
		if s.config.Commands[i].Group == name {
			s.config.Commands[i].Group = newName
		}
	}
	if err := s.writeLocked(); err != nil {
		s.config.Commands, s.config.CommandGroups = old, oldGroups
		return err
	}
	return nil
}

func (a *App) moveCommandHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID       string `json:"id"`
		TargetID string `json:"targetId"`
		Group    string `json:"group"`
		After    bool   `json:"after"`
	}
	if !decode(w, r, &input) {
		return
	}
	respond(w, map[string]bool{"ok": true}, a.store.MoveCommand(input.ID, input.TargetID, input.Group, input.After))
}

func (a *App) moveCommandGroupHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name   string `json:"name"`
		Target string `json:"target"`
		After  bool   `json:"after"`
	}
	if !decode(w, r, &input) {
		return
	}
	respond(w, map[string]bool{"ok": true}, a.store.MoveCommandGroup(input.Name, input.Target, input.After))
}

func (a *App) renameCommandGroupHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name    string `json:"name"`
		NewName string `json:"newName"`
	}
	if !decode(w, r, &input) {
		return
	}
	respond(w, map[string]bool{"ok": true}, a.store.RenameCommandGroup(input.Name, input.NewName))
}
