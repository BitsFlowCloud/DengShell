package app

import (
	"runtime"
	"testing"
)

// These fixtures expose the host filesystem as a Linux SFTP filesystem. NTFS
// drive paths, permissions, symlinks and FIFOs cannot model that remote contract.
// They run on Linux; network-only SSH/SFTP tests continue to run on Windows.
func requirePOSIXFilesystemFixture(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX host filesystem for the local Linux SFTP fixture")
	}
}
func requireLinuxShellFixture(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux shell utilities; Windows Git Bash is not a remote Linux host")
	}
}
