//go:build desktop && !linux

package main

// Other desktop platforms use Wails' window theme APIs directly.
func setPlatformTheme(dark bool) {}

func setPlatformSystemTheme() {}
