package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const ConfigSchemaVersion = 2

// Preserve unknown top-level document fields during supported-schema upgrades.
// They are never copied into List(), which is the redacted public API view.
func (c *Config) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("配置必须是 JSON 对象")
	}
	type plain Config
	next := plain(*c)
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"temporaryServers", "schemaVersion", "servers", "groups", "groupNodes", "trash", "connectionHistory", "commandHistory", "pathHistory", "directoryFavorites", "hostKeys", "commands", "commandGroups", "keys", "proxies", "assets", "appearance"} {
		delete(fields, key)
	}
	next.TemporaryServers = nil // runtime-only quick connections never enter imported/persisted configuration
	next.Extra = fields
	*c = Config(next)
	return nil
}
func (c Config) MarshalJSON() ([]byte, error) {
	type plain Config
	known, err := json.Marshal(plain(c))
	if err != nil {
		return nil, err
	}
	if len(c.Extra) == 0 {
		return known, nil
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(known, &fields); err != nil {
		return nil, err
	}
	for key, value := range c.Extra {
		if _, known := fields[key]; !known {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}
func cloneConnectionConfig(c Config) Config {
	c.Servers = append([]Profile{}, c.Servers...)
	c.Groups = append([]string{}, c.Groups...)
	c.GroupNodes = append([]ServerGroup{}, c.GroupNodes...)
	c.ConnectionHistory = append([]ConnectionHistory{}, c.ConnectionHistory...)
	c.Trash = append([]Profile{}, c.Trash...)
	return c
}
func (s *Store) upgradeConfig() error {
	previousVersion := s.config.SchemaVersion
	if previousVersion > ConfigSchemaVersion {
		return fmt.Errorf("配置版本 %d 高于本程序支持的版本 %d，请使用较新版本打开；原配置未修改", previousVersion, ConfigSchemaVersion)
	}
	if previousVersion < 0 {
		return errors.New("配置版本无效，原配置未修改")
	}
	if s.config.ConnectionHistory == nil {
		s.config.ConnectionHistory = []ConnectionHistory{}
	}
	if s.config.GroupNodes == nil {
		s.config.GroupNodes = []ServerGroup{}
	}
	if previousVersion < ConfigSchemaVersion {
		// Legacy group strings are literal names, never split on slash. A name such
		// as "production/api" is one original group and must keep that identity.
		if len(s.config.GroupNodes) == 0 {
			names := map[string]string{}
			add := func(name string) string {
				if name == "" {
					name = "我的服务器"
				}
				if id := names[name]; id != "" {
					return id
				}
				id := randomID()
				names[name] = id
				s.config.GroupNodes = append(s.config.GroupNodes, ServerGroup{ID: id, Name: name})
				return id
			}
			for _, name := range s.config.Groups {
				add(name)
			}
			for i := range s.config.Servers {
				s.config.Servers[i].GroupID = add(s.config.Servers[i].Group)
			}
		}
	}
	if err := validateGroupGraph(s.config.GroupNodes); err != nil {
		return fmt.Errorf("配置分组无法读取，已保留原文件：%w", err)
	}
	groups := groupIndex(s.config.GroupNodes)
	for _, profile := range s.config.Servers {
		if err := validateProfileNotes(profile.Notes); err != nil {
			return err
		}
		if _, ok := groups[profile.GroupID]; !ok {
			return fmt.Errorf("服务器「%s」引用不存在的分组，已保留原文件", profile.Name)
		}
	}
	if len(s.config.Trash) > 0 {
		return errors.New("磁盘配置的 trash 是只读 API 字段；回收站服务器应位于 servers 并带 deletedAt，原文件已保留")
	}
	s.config.SchemaVersion = ConfigSchemaVersion
	s.config.PathHistory = boundedPathHistory(s.config.PathHistory)
	s.config.DirectoryFavorites = boundedDirectoryFavorites(s.config.DirectoryFavorites)
	s.refreshLegacyGroupsLocked()
	return nil
}
