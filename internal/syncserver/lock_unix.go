//go:build !windows

package syncserver

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockServiceFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
