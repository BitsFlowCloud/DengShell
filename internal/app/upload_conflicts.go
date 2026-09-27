package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sync"
)

const maxUploadItems = 20000

type uploadItem struct {
	local, target string
	size          int64
	folder        bool
}

// Discover a native selection without creating remote directories or transfers.
func collectLocalUploads(ctx context.Context, paths []string, dir string) ([]uploadItem, error) {
	var err error
	items := []uploadItem{}
	for _, local := range paths {
		local = filepath.Clean(local)
		if !filepath.IsAbs(local) {
			err = errors.New("本地文件路径无效")
			break
		}
		err = filepath.WalkDir(local, func(current string, entry os.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if len(items) >= maxUploadItems {
				return errors.New("一次最多选择 20000 个项目")
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("请直接选择文件，暂不上传符号链接：%s", entry.Name())
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() && !entry.IsDir() {
				return fmt.Errorf("不支持特殊文件：%s", entry.Name())
			}
			rel, err := filepath.Rel(filepath.Dir(local), current)
			if err != nil {
				return err
			}
			items = append(items, uploadItem{current, path.Join(dir, filepath.ToSlash(rel)), info.Size(), entry.IsDir()})
			return nil
		})
		if err != nil {
			break
		}
	}

	if err != nil {
		return nil, err
	}
	if err := validateUploadItems(items); err != nil {
		return nil, err
	}
	return items, nil
}

func validateUploadItems(items []uploadItem) error {
	if len(items) > maxUploadItems {
		return errors.New("一次最多选择 20000 个项目")
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if seen[item.target] {
			return fmt.Errorf("同一批上传包含重复目标，请分开上传：%s", item.target)
		}
		seen[item.target] = true
	}
	return nil
}

func uploadTargetSet(paths []string) (map[string]bool, error) {
	if len(paths) > maxUploadItems {
		return nil, errors.New("一次最多选择 20000 个项目")
	}
	result := make(map[string]bool, len(paths))
	for _, value := range paths {
		target, err := remotePath(value)
		if err != nil {
			return nil, err
		}
		result[target] = true
	}
	return result, nil
}

func uploadTargetSkipped(target string, skipped map[string]bool) bool {
	for {
		if skipped[target] {
			return true
		}
		parent := path.Dir(target)
		if parent == target {
			return false
		}
		target = parent
	}
}

type uploadConflict struct {
	Path         string `json:"path"`
	CanOverwrite bool   `json:"canOverwrite"`
}

// Inspect with bounded concurrency: large folders must not make thousands of
// round trips serially, nor launch an unbounded number of SFTP requests.
func checkUploadConflicts(ctx context.Context, s *Session, items []uploadItem) (conflicts []uploadConflict, err error) {
	op := startSFTPOperation(ctx, s, sftpIdleTimeout)
	defer op.finish(&err)
	results := make([]*uploadConflict, len(items))
	errs := make([]error, len(items))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(items)) {
		workers.Go(func() {
			for i := range jobs {
				if op.ctx.Err() != nil {
					errs[i] = op.ctx.Err()
					continue
				}
				info, statErr := s.files.Lstat(items[i].target)
				op.touch()
				if os.IsNotExist(statErr) {
					continue
				}
				if statErr != nil {
					errs[i] = fmt.Errorf("无法检查目标 %s：%w", items[i].target, statErr)
					continue
				}
				if items[i].folder && info.IsDir() {
					continue
				}
				results[i] = &uploadConflict{Path: items[i].target, CanOverwrite: !items[i].folder && info.Mode().IsRegular()}
			}
		})
	}
	for i := range items {
		select {
		case jobs <- i:
		case <-op.ctx.Done():
			break
		}
		if op.ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	workers.Wait()
	if err := op.ctx.Err(); err != nil {
		return nil, err
	}
	conflicts = []uploadConflict{}
	blockedParents := make(map[string]bool)
	for i, conflict := range results {
		if conflict != nil && items[i].folder && !conflict.CanOverwrite {
			blockedParents[conflict.Path] = true
		}
	}
	for i, conflict := range results {
		// A selected folder may collide with a remote file/symlink. Report
		// that parent once; descendants will be skipped with it.
		if uploadTargetSkipped(path.Dir(items[i].target), blockedParents) {
			continue
		}
		if errs[i] != nil {
			return nil, errs[i]
		}
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
		}
	}
	return conflicts, nil
}

func (a *App) checkUploads(w http.ResponseWriter, r *http.Request) {
	s, err := a.fileSession(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var input struct {
		Paths     []string `json:"paths"`
		Directory string   `json:"directory"`
		Targets   []string `json:"targets"`
	}
	if !decode(w, r, &input) {
		return
	}
	var items []uploadItem
	if len(input.Paths) != 0 {
		if len(input.Targets) != 0 {
			writeError(w, 400, errors.New("请选择一种上传来源"))
			return
		}
		dir, pathErr := remotePath(input.Directory)
		if pathErr != nil {
			writeError(w, 400, pathErr)
			return
		}
		items, err = collectLocalUploads(r.Context(), input.Paths, dir)
	} else {
		if len(input.Targets) > maxUploadItems {
			writeError(w, 400, errors.New("一次最多选择 20000 个项目"))
			return
		}
		for _, value := range input.Targets {
			target, pathErr := remotePath(value)
			if pathErr != nil {
				writeError(w, 400, pathErr)
				return
			}
			items = append(items, uploadItem{target: target})
		}
		err = validateUploadItems(items)
	}
	if err != nil {
		writeError(w, 400, err)
		return
	}
	conflicts, err := checkUploadConflicts(r.Context(), s, items)
	respond(w, map[string]any{"conflicts": conflicts, "total": len(items)}, err)
}
