//go:build windows && amd64

package rdpengine

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

//go:embed payload/windows-amd64.zip
var payload []byte
var extractionMu sync.Mutex

func Available() bool { return true }
func Prepare() (string, error) {
	extractionMu.Lock()
	defer extractionMu.Unlock()
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	dir := filepath.Join(cache, "DengShell", "rdp", "3.32.0-"+hex.EncodeToString(digest[:8]))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return "", err
	}
	found := false
	for _, entry := range archive.File {
		if filepath.Base(entry.Name) != entry.Name || entry.UncompressedSize64 > 64<<20 {
			return "", errors.New("RDP 内置引擎包无效")
		}
		r, err := entry.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(r, 64<<20+1))
		r.Close()
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, entry.Name)
		if info, e := os.Lstat(path); e == nil && info.Mode().IsRegular() {
			if existing, e := os.ReadFile(path); e == nil && sha256.Sum256(existing) == sha256.Sum256(data) {
				if entry.Name == "DengShellRDP.exe" {
					found = true
				}
				continue
			}
		}
		f, err := os.CreateTemp(dir, ".rdp-")
		if err != nil {
			return "", err
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(f.Name(), path)
		}
		if err != nil {
			_ = os.Remove(f.Name())
			return "", errors.New("无法准备内置 RDP 引擎，请检查目录权限或安全软件")
		}
		if entry.Name == "DengShellRDP.exe" {
			found = true
		}
	}
	if !found {
		return "", errors.New("内置 RDP 引擎缺失")
	}
	return filepath.Join(dir, "DengShellRDP.exe"), nil
}
