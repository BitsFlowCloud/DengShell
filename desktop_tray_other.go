//go:build desktop && !windows && !linux

package main

func platformInitializeTray(string) {}
func platformTrayAvailable() bool   { return false }
func platformTrayEvents() int       { return 0 }
func platformCloseTray()            {}
func platformRaiseWindow()          {}
