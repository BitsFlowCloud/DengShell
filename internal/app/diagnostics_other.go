//go:build !windows

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

func runLocalDiagnostic(ctx context.Context, target string, d *Diagnostic) error {
	path, err := exec.LookPath("mtr")
	if err != nil && runtime.GOOS == "darwin" {
		// Finder-launched apps do not inherit a user's Homebrew shell PATH.
		for _, candidate := range []string{"/opt/homebrew/sbin/mtr", "/usr/local/sbin/mtr", "/opt/homebrew/bin/mtr", "/usr/local/bin/mtr"} {
			if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				path, err = candidate, nil
				break
			}
		}
	}
	if err != nil {
		return &MissingToolError{Direction: "local"}
	}
	fmt.Fprint(d, diagnosticHeader("本机", target, "mtr · 10 轮 · ASN 查询"))
	fmt.Fprintln(d, "ASN：mtr 的 AS 查询（默认 Team Cymru DNS）；不可用时显示 AS???。")
	command := exec.CommandContext(ctx, path, "-n", "-z", "-r", "-w", "-c", "10", "-i", "1", "-m", "30", "--", target)
	command.Stdout = d
	command.Stderr = d
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
	return command.Run()
}
