//go:build linux

package main

import (
	"bytes"
	"cloudshell/internal/app"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func prepareUpdaterProcess(cmd *exec.Cmd)       { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func prepareRestartedApplication(cmd *exec.Cmd) { prepareUpdaterProcess(cmd) }
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

// Legacy DEB plans did not carry a package format. An Arch plan must always
// identify pacman explicitly so a DEB plan cannot be reinterpreted on Arch.
func linuxUpdatePlanFormat(format, host string) (string, error) {
	if format == "" && host == "deb" {
		format = "deb"
	}
	if (format != "deb" && format != "pacman") || format != host {
		return "", errors.New("更新安装包格式与当前发行版不匹配")
	}
	return format, nil
}

func validatePlatformUpdate(p updatePlan) error {
	format, err := linuxUpdatePlanFormat(p.PackageFormat, app.NativePackageFormat())
	if err != nil {
		return err
	}
	if !app.NativePackageUpdateSupported() {
		return errors.New("缺少系统安装工具，请使用对应安装包手动升级")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch format {
	case "deb":
		out, err := packageMetadata(ctx, "dpkg-deb", "--field", p.Staged, "Package", "Architecture")
		if err != nil {
			return errors.New("下载的文件不是有效 Debian 安装包")
		}
		text := string(out)
		if !strings.Contains(text, "Package: dengshell\n") || !strings.Contains(text, "Architecture: amd64") {
			return errors.New("安装包名称或架构不是 DengShell/amd64")
		}
	case "pacman":
		out, err := packageMetadata(ctx, "bsdtar", "-xOf", p.Staged, ".PKGINFO")
		if err != nil {
			return errors.New("下载的文件不是有效 Arch 安装包")
		}
		version, err := validatePacmanMetadata(string(out))
		if err != nil {
			return err
		}
		if !strings.HasSuffix(version, "-"+strconv.FormatUint(p.Build%1000, 10)) {
			return errors.New("Arch 安装包版本与更新构建号不一致")
		}
	}
	if _, err := exec.LookPath("pkexec"); err != nil && os.Geteuid() != 0 {
		return errors.New("缺少 pkexec，无法打开系统授权窗口；请使用系统包管理器手动安装更新")
	}
	return nil
}

// Inspect only metadata on stdout, never extract an archive onto the filesystem.
// A malformed archive cannot cause unbounded output allocation.
func packageMetadata(ctx context.Context, tool string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, tool, args...)
	out := &packageMetadataBuffer{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type packageMetadataBuffer struct{ buffer bytes.Buffer }

func (b *packageMetadataBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *packageMetadataBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 65536 {
		return 0, errors.New("安装包元数据过大")
	}
	return b.buffer.Write(p)
}
func validatePacmanMetadata(text string) (string, error) {
	if len(text) > 65536 || strings.ContainsRune(text, 0) {
		return "", errors.New("Arch 安装包元数据无效")
	}
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok || (key != "pkgname" && key != "arch" && key != "pkgver") {
			continue
		}
		if _, duplicate := values[key]; duplicate {
			return "", errors.New("Arch 安装包身份字段重复")
		}
		values[key] = strings.TrimSpace(value)
	}
	if values["pkgname"] != "dengshell" || values["arch"] != "x86_64" || !pacmanVersionPattern.MatchString(values["pkgver"]) {
		return "", errors.New("安装包名称、版本或架构不是有效 DengShell/x86_64")
	}
	return values["pkgver"], nil
}

var pacmanVersionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.+:~_-]*-[0-9]+$`)

func linuxPackageInstallCommand(format, staged string, root bool, lookup func(string) (string, error)) (*exec.Cmd, error) {
	var tool string
	var args []string
	switch format {
	case "deb":
		tool = "dpkg"
		args = []string{"--install", staged}
	case "pacman":
		tool = "pacman"
		args = []string{"--upgrade", "--noconfirm", "--needed", staged}
	default:
		return nil, errors.New("不支持的更新安装包格式")
	}
	executable, err := lookup(tool)
	if err != nil {
		return nil, err
	}
	if root {
		return exec.Command(executable, args...), nil
	}
	pk, err := lookup("pkexec")
	if err != nil {
		return nil, err
	}
	return exec.Command(pk, append([]string{executable}, args...)...), nil
}
func applyPlatformUpdate(p updatePlan) (string, error) {
	// Revalidate after the parent exits, before opening system authorization.
	if err := validatePlatformUpdate(p); err != nil {
		return "", err
	}
	format, err := linuxUpdatePlanFormat(p.PackageFormat, app.NativePackageFormat())
	if err != nil {
		return "", err
	}
	cmd, err := linuxPackageInstallCommand(format, p.Staged, os.Geteuid() == 0, exec.LookPath)
	if err != nil {
		return "", err
	}
	// The application already obtained confirmation. pacman must not wait on a
	// nonexistent terminal; dependency, signature and database-lock checks remain
	// enabled. Never kill an authorized package transaction on a timer.
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.WriteFile(filepath.Join(p.ConfigDir, "update-result.json"), []byte(`{"status":"failed","message":"系统安装未完成；原数据目录保持不变，请查看更新目录中的 installer.log。"}`), 0600)
		return "", fmt.Errorf("系统安装没有完成（授权可能被取消或包管理器正忙）: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return "/opt/dengshell/dengshell", nil
}

// Package scripts and dependency state require a complete previous package;
// copying one ELF is not a rollback of a dpkg or pacman transaction.
func rollbackPlatformUpdate(p updatePlan) error {
	return errors.New("请使用此前版本的对应安装包恢复安装；配置目录未修改")
}
