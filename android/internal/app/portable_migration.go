package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Move the previous portable layout as one validated directory. Never merge two
// configurations or search unrelated user directories. Copies survive failure.
func MigratePortableData(destination string) error {
	if filepath.Base(destination) != "data" {
		return nil
	}
	root := filepath.Dir(destination)
	if _, err := os.Stat(filepath.Join(destination, EncryptedConfigName)); err == nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(destination, "config.json")); err == nil {
		return nil
	}
	found := false
	for _, name := range []string{EncryptedConfigName, "config.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			found = true
		}
	}
	if !found {
		return nil
	}
	unlock, err := lockConfigDirectory(root)
	if err != nil {
		return err
	}
	defer func() { unlock(); _ = os.Remove(filepath.Join(root, ".dengshell.config.lock")) }()
	packaged, readErr := os.ReadDir(destination)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	for _, entry := range packaged {
		if entry.Name() != "docs" && entry.Name() != "licenses" && entry.Name() != "support" {
			return errors.New("发现旧版配置，但 data 目录已有用户数据。请先备份后迁移，避免覆盖现有文件")
		}
	}
	stage, err := os.MkdirTemp(root, ".dengshell-migration-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, entry := range packaged {
		if err = copyMigrationTree(filepath.Join(destination, entry.Name()), filepath.Join(stage, entry.Name())); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var moved []string
	for _, entry := range entries {
		n := entry.Name()
		own := n == EncryptedConfigName || n == ConfigKeyName || n == EncryptedConfigName+".bak" || n == "config.json" || n == "assets" || n == "keys" || strings.HasPrefix(n, "config.pre-") && strings.HasSuffix(n, ".enc") || strings.HasPrefix(n, "runtime-success-v1-")
		if !own {
			continue
		}
		moved = append(moved, n)
		if err = copyMigrationTree(filepath.Join(root, n), filepath.Join(stage, n)); err != nil {
			return fmt.Errorf("迁移旧版配置：%w", err)
		}
	}
	if _, err = OpenStore(stage); err != nil {
		return fmt.Errorf("旧版配置验证失败，原文件已保留：%w", err)
	}
	previous := destination + ".previous-" + randomID()
	hadDestination := readErr == nil
	if hadDestination {
		if err = os.Rename(destination, previous); err != nil {
			return err
		}
	}
	if err = os.Rename(stage, destination); err != nil {
		if hadDestination {
			_ = os.Rename(previous, destination)
		}
		return err
	}
	if hadDestination {
		_ = os.RemoveAll(previous)
	}
	for _, n := range moved {
		if err = os.RemoveAll(filepath.Join(root, n)); err != nil {
			return fmt.Errorf("配置已迁移至 data，旧文件清理失败：%w", err)
		}
	}
	return nil
}
func copyMigrationTree(source, destination string) error {
	return filepath.WalkDir(source, func(name string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if e.Type()&os.ModeSymlink != 0 {
			return errors.New("配置资源包含符号链接，请先转换成实际文件后迁移")
		}
		if e.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !e.Type().IsRegular() {
			return errors.New("配置资源不是普通文件")
		}
		in, err := os.Open(name)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
