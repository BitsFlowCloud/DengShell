package app

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type QuickCommand struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Group    string `json:"group"`
	Body     string `json:"body"`
	Color    string `json:"color"`
	AppendCR bool   `json:"appendCR"`
}

// Groups have their own collection so an empty group survives command deletion
// and restart. Legacy command.group names remain the association and are merged
// once when opening an older configuration.
func normalizeCommandGroups(c *Config) bool {
	groups := []string{}
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			groups = append(groups, name)
			seen[name] = true
		}
	}
	for _, name := range c.CommandGroups {
		add(strings.TrimSpace(name))
	}
	changed := c.CommandGroups == nil
	for i := range c.Commands {
		name := strings.TrimSpace(c.Commands[i].Group)
		if name == "" {
			name = "常用命令"
		}
		if c.Commands[i].Group != name {
			c.Commands[i].Group = name
			changed = true
		}
		add(name)
	}
	changed = changed || !slices.Equal(groups, c.CommandGroups)
	c.CommandGroups = groups
	return changed
}

func validateCommandGroup(name string) error {
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return errors.New("分组名称应为 1–100 个字符，不能包含换行或控制字符")
	}
	return nil
}

func (s *Store) SaveCommandGroup(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	if err := validateCommandGroup(name); err != nil {
		return "", err
	}
	if slices.Contains(s.config.CommandGroups, name) {
		return "", errors.New("此命令分组已存在，请使用已有分组或更换名称")
	}
	if len(s.config.CommandGroups) >= 1000 {
		return "", errors.New("命令分组已达到 1000 个上限")
	}
	old := s.config.CommandGroups
	s.config.CommandGroups = append(append([]string{}, old...), name)
	if err := s.writeLocked(); err != nil {
		s.config.CommandGroups = old
		return "", err
	}
	return name, nil
}

func (s *Store) SaveCommand(command QuickCommand) (QuickCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	command.Name = strings.TrimSpace(command.Name)
	command.Group = strings.TrimSpace(command.Group)
	if command.Group == "" {
		command.Group = "常用命令"
	}
	if err := validateCommandGroup(command.Group); err != nil {
		return QuickCommand{}, err
	}
	command.Body = strings.ReplaceAll(strings.ReplaceAll(command.Body, "\r\n", "\n"), "\r", "\n")
	if command.Name == "" || len(command.Name) > 100 || strings.TrimSpace(command.Body) == "" || len(command.Body) > 65536 || strings.ContainsFunc(command.Body, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return QuickCommand{}, errors.New("请填写名称和命令内容（命令最多 64 KB）")
	}
	switch command.Color {
	case "blue", "green", "amber", "purple", "rose", "teal":
	default:
		return QuickCommand{}, errors.New("请选择有效的按钮颜色")
	}
	next := append([]QuickCommand{}, s.config.Commands...)
	if command.ID == "" {
		if len(next) >= 1000 {
			return QuickCommand{}, errors.New("快捷命令已达到 1000 条上限")
		}
		command.ID = randomID()
		next = append(next, command)
	} else {
		found := false
		for i := range next {
			if next[i].ID == command.ID {
				next[i] = command
				found = true
				break
			}
		}
		if !found {
			return QuickCommand{}, errors.New("快捷命令不存在")
		}
	}
	old, oldGroups := s.config.Commands, s.config.CommandGroups
	if !slices.Contains(oldGroups, command.Group) {
		if len(oldGroups) >= 1000 {
			return QuickCommand{}, errors.New("命令分组已达到 1000 个上限，请选择已有分组")
		}
		s.config.CommandGroups = append(append([]string{}, oldGroups...), command.Group)
	}
	s.config.Commands = next
	if err := s.writeLocked(); err != nil {
		s.config.Commands = old
		s.config.CommandGroups = oldGroups
		return QuickCommand{}, err
	}
	return command, nil
}

func (s *Store) DeleteCommand(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := []QuickCommand{}
	found := false
	for _, command := range s.config.Commands {
		if command.ID == id {
			found = true
		} else {
			next = append(next, command)
		}
	}
	if !found {
		return errors.New("快捷命令不存在")
	}
	old := s.config.Commands
	s.config.Commands = next
	if err := s.writeLocked(); err != nil {
		s.config.Commands = old
		return err
	}
	return nil
}

func (a *App) saveCommandHTTP(w http.ResponseWriter, r *http.Request) {
	var command QuickCommand
	if !decode(w, r, &command) {
		return
	}
	command, err := a.store.SaveCommand(command)
	respond(w, command, err)
}

func (a *App) saveCommandGroupHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &input) {
		return
	}
	name, err := a.store.SaveCommandGroup(input.Name)
	respond(w, map[string]string{"name": name}, err)
}
