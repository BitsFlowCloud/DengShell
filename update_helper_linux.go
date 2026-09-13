//go:build linux

package main

import (
	"cloudshell/internal/app"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func prepareUpdaterProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func waitForUpdateParent(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if e := syscall.Kill(pid, 0); e == syscall.ESRCH {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("等待旧程序退出超时")
}
func validatePlatformUpdate(p updatePlan) error {
	if !app.NativePackageUpdateSupported() {
		return errors.New("此发行版不使用 DEB 自动安装，请从官网下载 RPM 或通用安装包并保留原数据目录")
	}
	tool, e := exec.LookPath("dpkg-deb")
	if e != nil {
		return errors.New("此系统缺少 dpkg-deb，请在 Debian/Ubuntu 上使用 .deb 更新")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, e := exec.CommandContext(ctx, tool, "--field", p.Staged, "Package", "Architecture").Output()
	if e != nil {
		return errors.New("下载的文件不是有效 Debian 安装包")
	}
	text := string(out)
	if !strings.Contains(text, "Package: dengshell\n") || !strings.Contains(text, "Architecture: amd64") {
		return errors.New("安装包名称或架构不是 DengShell/amd64")
	}
	if _, e = exec.LookPath("pkexec"); e != nil && os.Geteuid() != 0 {
		return errors.New("缺少 pkexec，无法打开系统授权窗口；请手动使用系统软件安装器安装下载的 .deb")
	}
	return nil
}
func applyPlatformUpdate(p updatePlan) (string, error) {
	dpkg, e := exec.LookPath("dpkg")
	if e != nil {
		return "", e
	}
	// The parent has exited and the user approved this package transaction.
	// Killing dpkg at an arbitrary deadline can leave packages half-configured.
	var cmd *exec.Cmd
	if os.Geteuid() == 0 {
		cmd = exec.Command(dpkg, "--install", p.Staged)
	} else {
		pk, e := exec.LookPath("pkexec")
		if e != nil {
			return "", e
		}
		cmd = exec.Command(pk, dpkg, "--install", p.Staged)
	}
	output, e := cmd.CombinedOutput()
	if e != nil {
		_ = os.WriteFile(filepath.Join(p.ConfigDir, "update-result.json"), []byte(`{"status":"failed","message":"系统安装未完成；原数据目录保持不变，请查看更新目录中的 installer.log。"}`), 0600)
		return "", fmt.Errorf("系统安装没有完成（授权可能被取消）: %w\n%s", e, strings.TrimSpace(string(output)))
	}
	return "/opt/dengshell/dengshell", nil
}

// dpkg may update package scripts and dependencies. Only a previous .deb can
// restore the complete package, so never pretend copying one ELF rolls it back.
func rollbackPlatformUpdate(p updatePlan) error {
	return errors.New("请使用此前版本的 .deb 恢复安装；配置目录未修改")
}
