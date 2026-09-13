package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pkg/sftp"
)

type externalSnapshotLimits struct {
	bytes          int64
	entries, depth int
}

type externalSnapshot struct {
	ctx     context.Context
	client  *sftp.Client
	limits  externalSnapshotLimits
	bytes   int64
	entries int
}

func prepareExternalSnapshot(ctx context.Context, client *sftp.Client, remote, opened string, limits externalSnapshotLimits) (local string, err error) {
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// SFTP REALPATH need not resolve symlinks on every server. Inspect selected
	// ancestors explicitly, then inspect each child with Lstat during traversal.
	ancestor := "/"
	for _, component := range strings.Split(strings.TrimPrefix(path.Clean(remote), "/"), "/") {
		if component == "" {
			continue
		}
		ancestor = path.Join(ancestor, component)
		info, checkErr := client.Lstat(ancestor)
		if checkErr != nil {
			return "", checkErr
		}
		touchSFTPOperation(ctx)
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("所选路径经过符号链接；请直接选择实际文件或目录")
		}
	}
	if err = os.MkdirAll(opened, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(opened, "snapshot-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	name := path.Base(remote)
	if !externalNameUsable(name) {
		name = "remote-item"
	}
	local = filepath.Join(dir, name)
	snapshot := externalSnapshot{ctx: ctx, client: client, limits: limits}
	if err = snapshot.copy(remote, local, 0); err != nil {
		return "", fmt.Errorf("准备本地副本失败，未完成的副本已移除：%w", err)
	}
	return local, nil
}

func externalNameUsable(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	if runtime.GOOS != "windows" {
		return true
	}
	if strings.ContainsAny(name, `<>:"|?*`) || strings.TrimRight(name, " .") != name {
		return false
	}
	for _, ch := range name {
		if ch < 32 {
			return false
		}
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" {
		return false
	}
	return !(len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9')
}

func (s *externalSnapshot) copy(remote, local string, depth int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if depth > s.limits.depth {
		return fmt.Errorf("目录层级超过 %d 层，请选择较小的子目录", s.limits.depth)
	}
	s.entries++
	if s.entries > s.limits.entries {
		return fmt.Errorf("目录中的文件和文件夹超过 %d 项，请选择较小的子目录", s.limits.entries)
	}
	info, err := s.client.Lstat(remote)
	if err != nil {
		return err
	}
	touchSFTPOperation(s.ctx)
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("包含符号链接 %q，系统关联副本不跟随链接；请直接选择实际文件或目录", remote)
	}
	if info.IsDir() {
		if err = os.Mkdir(local, 0700); err != nil {
			return err
		}
		entries, err := s.client.ReadDirContext(s.ctx, remote)
		if err != nil {
			return err
		}
		if len(entries) > s.limits.entries-s.entries {
			return fmt.Errorf("目录中的文件和文件夹超过 %d 项，请选择较小的子目录", s.limits.entries)
		}
		for _, entry := range entries {
			if !externalNameUsable(entry.Name()) {
				return fmt.Errorf("文件名 %q 无法在本机保持原名，请改用下载压缩包", entry.Name())
			}
			if err = s.copy(path.Join(remote, entry.Name()), filepath.Join(local, entry.Name()), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("包含特殊文件 %q，仅支持普通文件和目录", remote)
	}
	remaining := s.limits.bytes - s.bytes
	if info.Size() < 0 || info.Size() > remaining {
		return fmt.Errorf("副本总大小超过 %d MiB，请选择较小的文件或目录", s.limits.bytes>>20)
	}
	in, err := s.client.Open(remote)
	if err != nil {
		return err
	}
	defer in.Close()
	openedInfo, err := in.Stat()
	if err != nil {
		return err
	}
	if !openedInfo.Mode().IsRegular() || openedInfo.Size() > remaining {
		return errors.New("远端文件类型或大小在复制时变化，请重试")
	}
	out, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	// Read at most one byte beyond the remaining budget to detect growth without
	// trusting the earlier stat. This is a byte-for-byte copy, never text decoding.
	n, copyErr := io.Copy(out, io.LimitReader(externalSnapshotReader{s.ctx, in}, remaining+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if n > remaining {
		return fmt.Errorf("副本总大小超过 %d MiB，请选择较小的文件或目录", s.limits.bytes>>20)
	}
	if closeErr != nil {
		return closeErr
	}
	s.bytes += n
	return s.ctx.Err()
}

type externalSnapshotReader struct {
	ctx    context.Context
	source io.Reader
}

func (r externalSnapshotReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.source.Read(p)
	if n > 0 {
		touchSFTPOperation(r.ctx)
	}
	return n, err
}
