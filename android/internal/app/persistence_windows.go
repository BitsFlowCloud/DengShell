//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func lockConfigDirectory(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".dengshell.config.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("配置目录不可写，请把完整程序文件夹移动到可写位置：%w", err)
	}
	var overlap windows.Overlapped
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
		if err == nil {
			return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlap); _ = f.Close() }, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) || time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("配置文件正在被另一实例使用：%w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func replaceConfigFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
