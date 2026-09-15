//go:build darwin

package main

import (
	"os/exec"
)

func showStartupError(detail string) {
	// The message is an argv value, never interpolated into AppleScript source.
	const script = `on run argv
display alert "DengShell 启动失败" message (item 1 of argv) as critical buttons {"好"} default button 1
end run`
	message := "无法读取或保存配置。\n\n" + detail + "\n\n请检查配置目录权限，或恢复同一备份中的配置与解锁密钥。"
	_ = exec.Command("/usr/bin/osascript", "-e", script, message).Run()
}
