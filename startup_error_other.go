//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func showStartupError(detail string) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return
	}
	message := "DengShell 无法读取或保存配置。\n\n" + detail + "\n\n程序不会重置或覆盖原配置。请检查完整程序文件夹的写入权限，或恢复同一备份中的配置与解锁密钥。"
	for _, command := range [][]string{
		{"zenity", "--error", "--no-markup", "--width=640", "--title=DengShell 启动失败", "--text=" + message},
		{"kdialog", "--title", "DengShell 启动失败", "--error", message},
		{"xmessage", "-center", "-title", "DengShell 启动失败", message},
	} {
		if path, err := exec.LookPath(command[0]); err == nil {
			if err := exec.Command(path, command[1:]...).Run(); err == nil {
				return
			}
		}
	}
}
