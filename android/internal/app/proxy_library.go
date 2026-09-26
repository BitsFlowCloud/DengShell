package app

import (
	"errors"
	"fmt"
	"strings"
)

type ManagedProxy struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Proxy ProxyConfig `json:"proxy"`
}

func (s *Store) SaveProxy(value ManagedProxy) (ManagedProxy, error) {
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" || len(value.Name) > 100 {
		return ManagedProxy{}, errors.New("请填写代理名称（最多 100 字节）")
	}
	if err := value.Proxy.validate(); err != nil {
		return ManagedProxy{}, err
	}
	if value.Proxy.Type == "direct" {
		return ManagedProxy{}, errors.New("请选择 SOCKS5 或 HTTP CONNECT 代理")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, item := range s.config.Proxies {
		if item.ID != value.ID {
			continue
		}
		index = i
		if value.Proxy.Password == "" && !value.Proxy.ClearPassword && value.Proxy.Type == item.Proxy.Type && value.Proxy.Host == item.Proxy.Host && value.Proxy.Port == item.Proxy.Port && value.Proxy.User == item.Proxy.User {
			value.Proxy.Password = item.Proxy.Password
		}
		break
	}
	if value.ID == "" {
		value.ID = randomID()
	} else if index < 0 {
		return ManagedProxy{}, errors.New("代理不存在，请重新新建")
	}
	if value.Proxy.ClearPassword {
		value.Proxy.Password = ""
	}
	value.Proxy.HasPassword = false
	value.Proxy.ClearPassword = false
	old := s.config.Proxies
	s.config.Proxies = append([]ManagedProxy{}, old...)
	if index < 0 {
		s.config.Proxies = append(s.config.Proxies, value)
	} else {
		s.config.Proxies[index] = value
	}
	if err := s.writeLocked(); err != nil {
		s.config.Proxies = old
		return ManagedProxy{}, err
	}
	value.Proxy = value.Proxy.public()
	return value, nil
}

func (s *Store) ResolveProxy(id string) (ProxyConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, value := range s.config.Proxies {
		if value.ID == id {
			return value.Proxy, nil
		}
	}
	return ProxyConfig{}, errors.New("服务器绑定的代理不存在，请重新选择")
}

func (s *Store) DeleteProxy(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, profile := range s.config.Servers {
		if profile.ProxyID == id {
			return fmt.Errorf("代理仍被服务器「%s」使用，请先更换其代理设置", profile.Name)
		}
	}
	next := []ManagedProxy{}
	found := false
	for _, value := range s.config.Proxies {
		if value.ID == id {
			found = true
		} else {
			next = append(next, value)
		}
	}
	if !found {
		return errors.New("代理不存在")
	}
	old := s.config.Proxies
	s.config.Proxies = next
	if err := s.writeLocked(); err != nil {
		s.config.Proxies = old
		return err
	}
	return nil
}
