//go:build !windows

package app

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func runLocalDiagnostic(ctx context.Context, target string, d *Diagnostic) error {
	path, err := exec.LookPath("mtr")
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
