//go:build !windows

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func lockConfigDirectory(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".dengshell.config.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("配置目录不可写，请把完整程序文件夹移动到可写位置：%w", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) || time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("配置文件正在被另一实例使用：%w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func replaceConfigFile(source, destination string) error { return os.Rename(source, destination) }
