//go:build desktop && !windows

package main

func preparePlatformWindow()                  {}
func installPlatformWindowIcon()              {}
func platformWebviewDataPath(_ string) string { return "" }
