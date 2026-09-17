//go:build windows

package app

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockUpdateFile(f *os.File, exclusive bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	// Lock outside the marker content so shared holders can read it on Windows.
	o := windows.Overlapped{Offset: 4096}
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &o)
}
func updateProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err != windows.ERROR_INVALID_PARAMETER
	}
	defer windows.CloseHandle(h)
	s, err := windows.WaitForSingleObject(h, 0)
	return err != nil || s != windows.WAIT_OBJECT_0
}
