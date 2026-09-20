package syncserver

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

func lockServiceDirectory(dir string) (func(), error) {
	path := filepath.Join(dir, "service.lock")
	if st, e := os.Lstat(path); e == nil && !st.Mode().IsRegular() {
		return nil, errors.New("同步服务锁文件无效")
	}
	f, e := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if e != nil {
		return nil, e
	}
	if e = lockServiceFile(f); e != nil {
		f.Close()
		return nil, errors.New("同一数据目录已有同步服务运行，请勿同时启动多个实例")
	}
	var once sync.Once
	return func() { once.Do(func() { _ = f.Close() }) }, nil
}
