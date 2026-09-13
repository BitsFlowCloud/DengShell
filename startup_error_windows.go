//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func showStartupError(detail string) {
	message, _ := windows.UTF16PtrFromString("DengShell 无法读取或保存配置。\n\n" + detail + "\n\n程序不会重置或覆盖原配置。请检查完整程序文件夹的写入权限，或恢复同一备份中的配置与解锁密钥。")
	title, _ := windows.UTF16PtrFromString("DengShell 启动失败")
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}
