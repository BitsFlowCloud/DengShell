//go:build desktop

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const detachedCacheMarker = "DengShell detached WebView cache v1\n"
const detachedCacheLeaseName = ".dengshell-window-cache.lock"

func detachedCacheDirectory(configDir, nonce string) (string, error) {
	if !filepath.IsAbs(configDir) || !detachedNoncePattern.MatchString(nonce) {
		return "", errors.New("独立窗口缓存路径无效")
	}
	root := filepath.Join(configDir, "window-cache")
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("独立窗口缓存根目录不能是链接")
	}
	dir := filepath.Join(root, nonce)
	if info, err = os.Lstat(dir); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", errors.New("独立窗口缓存目录不能是链接")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return dir, nil
}

// The child holds the lease for its entire WebView lifetime. Never reuse a
// nonce directory: an unlocked marker therefore cannot become active again.
func acquireDetachedWebviewCache(configDir, nonce string) (func(), error) {
	if err := os.MkdirAll(filepath.Join(configDir, "window-cache"), 0700); err != nil {
		return nil, err
	}
	dir, err := detachedCacheDirectory(configDir, nonce)
	if err != nil {
		return nil, err
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, detachedCacheLeaseName), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	if err = lockDetachedCacheFile(f); err != nil {
		_ = f.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if _, err = io.WriteString(f, detachedCacheMarker); err == nil {
		err = f.Sync()
	}
	if err != nil {
		_ = f.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// Legacy/unmarked directories and symlinks are intentionally left alone. An
// unclean exit releases the OS lock; the next main window can reclaim it.
func removeDetachedWebviewCache(configDir, nonce string) bool {
	dir, err := detachedCacheDirectory(configDir, nonce)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil || !info.IsDir() {
		return false
	}
	marker := filepath.Join(dir, detachedCacheLeaseName)
	info, err = os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.OpenFile(marker, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	if err = lockDetachedCacheFile(f); err != nil {
		_ = f.Close()
		return false
	}
	content, err := io.ReadAll(io.LimitReader(f, int64(len(detachedCacheMarker)+1)))
	if err != nil || string(content) != detachedCacheMarker {
		_ = f.Close()
		return false
	}
	// Keep the marker until all other entries are gone, so a Windows sharing
	// violation remains eligible for a later retry. RemoveAll never follows links.
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, entry := range entries {
			if entry.Name() != detachedCacheLeaseName {
				if e := os.RemoveAll(filepath.Join(dir, entry.Name())); e != nil {
					err = e
				}
			}
		}
	}
	_ = f.Close()
	if err != nil {
		return false
	}
	if err = os.Remove(marker); err != nil {
		return false
	}
	return os.Remove(dir) == nil
}

func cleanupDetachedWebviewCache(configDir, nonce string) {
	for _, delay := range []time.Duration{0, 100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second, 5 * time.Second} {
		if delay != 0 {
			time.Sleep(delay)
		}
		if removeDetachedWebviewCache(configDir, nonce) {
			return
		}
	}
}

func sweepDetachedWebviewCaches(configDir string) {
	root := filepath.Join(configDir, "window-cache")
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && detachedNoncePattern.MatchString(entry.Name()) {
			removeDetachedWebviewCache(configDir, entry.Name())
		}
	}
}
