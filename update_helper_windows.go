//go:build windows

package main

import (
	"debug/pe"
	"errors"
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
	"time"
)

func prepareUpdaterProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008 | 0x00000200}
}

// Hide only the helper, never the GUI it launches after installation/rollback.
// STARTF_USESHOWWINDOW + SW_HIDE otherwise overrides the child's first ShowWindow.
func prepareRestartedApplication(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
func waitForUpdateParent(pid int, timeout time.Duration) error {
	h, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if e != nil {
		if e == windows.ERROR_INVALID_PARAMETER {
			return nil
		}
		return e
	}
	defer windows.CloseHandle(h)
	status, e := windows.WaitForSingleObject(h, uint32(timeout/time.Millisecond))
	if e != nil {
		return e
	}
	if status != windows.WAIT_OBJECT_0 {
		return errors.New("等待旧程序退出超时")
	}
	return nil
}
func validatePlatformUpdate(p updatePlan) error {
	f, e := pe.Open(p.Staged)
	if e != nil {
		return errors.New("下载的文件不是有效 Windows 程序")
	}
	defer f.Close()
	if f.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return errors.New("更新程序的架构不匹配")
	}
	return nil
}
func applyPlatformUpdate(p updatePlan) (string, error) { return replaceUpdateBinary(p) }
func rollbackPlatformUpdate(p updatePlan) error        { return restoreUpdateBinary(p) }
