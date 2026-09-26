package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/pkg/sftp"
)

// REALPATH is not a reliable symlink resolver across SFTP servers. Resolve
// components explicitly so atomic text replacement writes the target file,
// preserving both a selected symlink and any links in its parent path.
func resolveRemoteSymlinks(ctx context.Context, client *sftp.Client, target string) (string, error) {
	if !path.IsAbs(target) || strings.ContainsRune(target, 0) {
		return "", errors.New("请输入有效的远程绝对路径")
	}
	pending := strings.Split(strings.TrimPrefix(target, "/"), "/")
	resolved := "/"
	links := 0
	for len(pending) != 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			resolved = path.Dir(resolved)
			continue
		}
		candidate := path.Join(resolved, component)
		info, err := client.Lstat(candidate)
		if err != nil {
			return "", fmt.Errorf("读取远端路径 %q：%w", candidate, err)
		}
		touchSFTPOperation(ctx)
		if info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > 40 {
				return "", errors.New("符号链接超过 40 次解析，可能存在循环；请直接选择实际文件")
			}
			link, err := client.ReadLink(candidate)
			if err != nil {
				return "", err
			}
			if link == "" || strings.ContainsRune(link, 0) {
				return "", errors.New("符号链接目标无效")
			}
			if path.IsAbs(link) {
				resolved = "/"
			}
			pending = append(strings.Split(link, "/"), pending...)
			continue
		}
		if len(pending) != 0 && !info.IsDir() {
			return "", fmt.Errorf("远端路径 %q 不是目录", candidate)
		}
		resolved = candidate
	}
	return resolved, nil
}
