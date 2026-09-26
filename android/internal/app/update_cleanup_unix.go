//go:build !windows

package app

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockUpdateFile(f *os.File, exclusive bool) error {
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	return unix.Flock(int(f.Fd()), mode|unix.LOCK_NB)
}
func updateProcessAlive(pid int) bool { return pid > 0 && unix.Kill(pid, 0) != unix.ESRCH }
