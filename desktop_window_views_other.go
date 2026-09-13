//go:build desktop && !windows && !linux

package main

func platformWindowViewIdentity() (string, bool) { return "", true }
func platformWindowPointerTargets() ([]string, bool, string) {
	return nil, false, "此桌面不支持跨窗口鼠标定位，请使用“合并到窗口”菜单"
}
