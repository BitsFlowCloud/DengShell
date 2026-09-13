//go:build desktop && windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockDetachedCacheFile(file *os.File) error {
	var overlap windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
}
