//go:build desktop && !linux && !windows && !darwin

package main

func platformSystemTheme() string { return "" }
