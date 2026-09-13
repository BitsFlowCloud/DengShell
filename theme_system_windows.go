//go:build desktop && windows

package main

import "golang.org/x/sys/windows/registry"

func platformSystemTheme() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return ""
	}
	if value == 0 {
		return "dark"
	}
	return "light"
}
