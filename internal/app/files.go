package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

type Entry struct {
	Name       string `json:"name"`
	Size       int64  `json:"bytes"`
	Kind       string `json:"kind"`
	Modified   string `json:"time"`
	ModifiedAt int64  `json:"modifiedAt"`
	Mode       string `json:"mode"`
	Owner      string `json:"owner"`
	Link       bool   `json:"link"`
}

func remotePath(value string) (string, error) {
	if !path.IsAbs(value) || strings.ContainsRune(value, 0) {
		return "", errors.New("请输入有效的远程绝对路径")
	}
	return path.Clean(value), nil
}
func (a *App) listFiles(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = s.Home
	}
	dir, err = remotePath(dir)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	op := startSFTPOperation(ctx, s, sftpIdleTimeout)
	defer op.close()
	entries, err := s.files.ReadDirContext(op.ctx, dir)
	if err != nil {
		writeError(w, 400, fmt.Errorf("读取目录：%w", op.err(err)))
		return
	}
	result := make([]Entry, 0, len(entries))
	directoriesOnly := r.URL.Query().Get("directories") == "1"
	owners := fileOwnerNames{}
	if !directoriesOnly {
		owners = s.resolveFileOwners(op.ctx, entries)
	}
	for _, f := range entries {
		if err := op.ctx.Err(); err != nil {
			writeError(w, 400, op.err(err))
			return
		}
		kind := "file"
		if f.IsDir() {
			kind = "folder"
		}
		// The directory navigator needs real folders, including common Linux
		// symlink directories (/bin, /lib, ...), without listing file symlinks.
		if directoriesOnly && f.Mode()&os.ModeSymlink != 0 {
			if target, err := s.files.Stat(path.Join(dir, f.Name())); err == nil && target.IsDir() {
				kind = "folder"
			}
		}
		if directoriesOnly && kind != "folder" {
			continue
		}
		result = append(result, Entry{Name: f.Name(), Size: f.Size(), Kind: kind, Modified: f.ModTime().Format("2006-01-02 15:04"), ModifiedAt: f.ModTime().UnixMilli(), Mode: f.Mode().String(), Owner: owners.owner(f), Link: f.Mode()&os.ModeSymlink != 0})
	}
	writeJSON(w, map[string]any{"path": dir, "entries": result})
}
func (a *App) fileAction(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var input struct{ Action, Path, Name string }
	if !decode(w, r, &input) {
		return
	}
	target, err := remotePath(input.Path)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if input.Action == "mkdir" || input.Action == "rename" || input.Action == "new-file" {
		if input.Name == "" || input.Name == "." || input.Name == ".." || strings.ContainsAny(input.Name, "/\x00") {
			writeError(w, 400, errors.New("名称不能包含 / 或为空"))
			return
		}
	}
	op := startSFTPOperation(r.Context(), s, sftpIdleTimeout)
	defer op.close()
	if err := op.ctx.Err(); err != nil {
		writeError(w, 400, op.err(err))
		return
	}
	switch input.Action {
	case "new-file":
		var f *sftp.File
		f, err = s.files.OpenFile(path.Join(target, input.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if err == nil {
			err = f.Close()
		}
	case "mkdir":
		err = s.files.Mkdir(path.Join(target, input.Name))
	case "rename":
		destination := path.Join(path.Dir(target), input.Name)
		if _, statErr := s.files.Lstat(destination); statErr == nil {
			err = errors.New("目标名称已存在")
		} else if !os.IsNotExist(statErr) {
			err = statErr
		} else {
			err = s.files.Rename(target, destination)
		}
	case "delete":
		if target == "/" {
			err = errors.New("不能删除根目录")
			break
		}
		err = removeRemoteTree(op.ctx, s.files, target, 0)
	default:
		err = errors.New("未知文件操作")
	}
	respond(w, map[string]bool{"ok": true}, op.err(err))
}
func (a *App) download(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	target, err := remotePath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	op := startSFTPOperation(r.Context(), s, sftpIdleTimeout)
	defer op.close()
	if err := op.ctx.Err(); err != nil {
		writeError(w, 400, op.err(err))
		return
	}
	f, err := s.files.Open(target)
	if err != nil {
		writeError(w, 400, op.err(err))
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeError(w, 400, op.err(errors.New("请选择可下载的文件")))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(target)}))
	http.ServeContent(w, r.WithContext(op.ctx), path.Base(target), info.ModTime(), sftpProgressReadSeeker{f, op})
}
func (a *App) DownloadTo(sessionID, remote, local string) (err error) {
	s, err := a.session(sessionID)
	if err != nil {
		return err
	}
	remote, err = remotePath(remote)
	if err != nil {
		return err
	}
	op := startSFTPOperation(s.ctx, s, sftpIdleTimeout)
	defer op.finish(&err)
	if err := op.ctx.Err(); err != nil {
		return err
	}
	in, err := s.files.Open(remote)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(local), ".cloudshell-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	if _, err = io.Copy(sftpProgressWriter{out, op}, in); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = op.ctx.Err(); err != nil {
		return err
	}
	return os.Rename(out.Name(), local)
}

type Transfer struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"sessionId"`
	Name       string    `json:"name"`
	Target     string    `json:"target"`
	Total      int64     `json:"total"`
	Done       int64     `json:"done"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	Retryable  bool      `json:"retryable"`
	localPath  string
	cancel     context.CancelFunc
}

func (a *App) beginTransfer(ctx context.Context, s *Session, id, target string, total int64) (context.Context, *Transfer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id == "" {
		id = randomID()
	}
	if len(id) > 100 {
		return nil, nil, errors.New("任务标识无效")
	}
	if _, ok := a.transfers[id]; ok {
		return nil, nil, errors.New("任务标识重复")
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	t := &Transfer{ID: id, SessionID: s.ID, Name: path.Base(target), Target: target, Total: total, Status: "queued", StartedAt: time.Now(), cancel: func() { stop(); cancel() }}
	for key, entry := range a.transfers {
		if !entry.FinishedAt.IsZero() && time.Since(entry.FinishedAt) > time.Hour {
			delete(a.transfers, key)
		}
	}
	a.transfers[id] = t
	return ctx, t, nil
}

func (a *App) finishTransfer(t *Transfer, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t.FinishedAt = time.Now()
	switch {
	case errors.Is(err, context.Canceled):
		t.Status = "cancelled"
		t.Error = "已取消"
		var interrupted *sftpInterruptedError
		if errors.As(err, &interrupted) {
			t.Error = interrupted.Error()
		}
	case err != nil:
		t.Status = "failed"
		t.Error = err.Error()
	default:
		t.Status = "done"
	}
	t.cancel()
}
func (a *App) copyUpload(ctx context.Context, s *Session, t *Transfer, input io.Reader, overwrite bool) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	op := startSFTPOperation(ctx, s, sftpIdleTimeout)
	defer op.finish(&err)
	ctx = op.ctx
	if err := s.files.MkdirAll(path.Dir(t.Target)); err != nil {
		return err
	}
	parent, err := resolveRemoteSymlinks(ctx, s.files, path.Dir(t.Target))
	if err != nil {
		return err
	}
	target := path.Join(parent, path.Base(t.Target))
	unlock, err := lockRemoteWrite(ctx, s, target)
	if err != nil {
		return err
	}
	defer unlock()
	original, err := s.files.Lstat(target)
	if err == nil && !overwrite {
		return errors.New("目标文件已存在，请选择覆盖后重试")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if original != nil && !original.Mode().IsRegular() {
		return errors.New("只能覆盖普通文件；请先处理目标目录或符号链接")
	}
	f, tmp, cleanup, err := privateRemoteTemporary(s.files, target, "upload")
	if err != nil {
		return err
	}
	defer cleanup()
	defer f.Close()
	op.touch()
	var acknowledged int64
	acknowledged, err = uploadChunks(ctx, f, input, func(n int) {
		op.touch()
		a.mu.Lock()
		t.Done += int64(n)
		a.mu.Unlock()
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if t.Total >= 0 && acknowledged != t.Total {
		return errors.New("传输中断：文件大小不完整")
	}
	err = f.Close()
	// A cancellation can arrive while waiting for the CLOSE acknowledgement.
	// Check again before issuing the irreversible rename/replace request.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if err = preserveRemoteMetadata(s.files, tmp, original); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if overwrite {
		err = s.files.PosixRename(tmp, target)
	} else {
		err = s.files.Rename(tmp, target)
	}
	return err
}
func (a *App) uploadHTTP(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	target, err := remotePath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	ctx, t, err := a.beginTransfer(r.Context(), s, r.URL.Query().Get("task"), target, r.ContentLength)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	input, stopRead := protectHTTPUploadBody(ctx, w, r.Body)
	defer stopRead()
	release, err := a.acquireUploadSlot(ctx, t)
	if err == nil {
		defer release()
		err = a.copyUpload(ctx, s, t, input, r.URL.Query().Get("overwrite") == "1")
	}
	stopRead()
	a.finishTransfer(t, err)
	respond(w, map[string]string{"id": t.ID}, err)
}
func (a *App) uploadLocal(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var input struct {
		Paths     []string `json:"paths"`
		Directory string   `json:"directory"`
		Overwrite bool     `json:"overwrite"`
	}
	if !decode(w, r, &input) {
		return
	}
	dir, err := remotePath(input.Directory)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	type uploadItem struct {
		local, target string
		size          int64
		folder        bool
	}
	items := []uploadItem{}
	for _, local := range input.Paths {
		local = filepath.Clean(local)
		if !filepath.IsAbs(local) {
			err = errors.New("本地文件路径无效")
			break
		}
		err = filepath.WalkDir(local, func(current string, entry os.DirEntry, walkErr error) error {
			if err := r.Context().Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if len(items) >= 20000 {
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
		writeError(w, 400, err)
		return
	}
	ids := []string{}
	op := startSFTPOperation(r.Context(), s, sftpIdleTimeout)
	defer op.close()
	for _, item := range items {
		if err := op.ctx.Err(); err != nil {
			writeError(w, 400, op.err(err))
			return
		}
		if item.folder {
			if err := s.files.MkdirAll(item.target); err != nil {
				writeError(w, 400, op.err(err))
				return
			}
			op.touch()
			continue
		}
		ctx, t, err := a.beginTransfer(s.ctx, s, "", item.target, item.size)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		ids = append(ids, t.ID)
		a.mu.Lock()
		t.localPath = item.local
		t.Retryable = true
		t.Status = "queued"
		a.mu.Unlock()
		go a.runNativeUpload(ctx, s, t, item.local, input.Overwrite)
		op.touch()
	}
	writeJSON(w, map[string]any{"ids": ids, "directories": len(items) - len(ids)})
}

// Native selections/drops, browser pages and retries share the same limit.
// A queue belongs to the application process, not an individual UI window.
var uploadSlots = make(chan struct{}, 4)

func (a *App) acquireUploadSlot(ctx context.Context, t *Transfer) (func(), error) {
	select {
	case uploadSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-uploadSlots
		return nil, err
	}
	a.mu.Lock()
	t.Status = "uploading"
	a.mu.Unlock()
	return func() { <-uploadSlots }, nil
}

func (a *App) listTransfers(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := []Transfer{}
	for _, t := range a.transfers {
		items = append(items, *t)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.Before(items[j].StartedAt) })
	writeJSON(w, items)
}
func (a *App) cancelTransfer(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	t := a.transfers[r.PathValue("id")]
	if t != nil {
		if t.FinishedAt.IsZero() {
			t.cancel()
		} else {
			delete(a.transfers, t.ID)
		}
	}
	a.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) runNativeUpload(ctx context.Context, s *Session, t *Transfer, local string, overwrite bool) {
	release, err := a.acquireUploadSlot(ctx, t)
	if err != nil {
		a.finishTransfer(t, err)
		return
	}
	defer release()
	file, err := os.Open(local)
	if err == nil {
		defer file.Close()
		err = a.copyUpload(ctx, s, t, file, overwrite)
	}
	a.finishTransfer(t, err)
}
func (a *App) retryTransfer(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Overwrite bool `json:"overwrite"`
	}
	if !decode(w, r, &input) {
		return
	}
	a.mu.Lock()
	old := a.transfers[r.PathValue("id")]
	var local, sessionID, target string
	if old != nil && !old.FinishedAt.IsZero() && old.Retryable {
		local, sessionID, target = old.localPath, old.SessionID, old.Target
	}
	a.mu.Unlock()
	if local == "" {
		writeError(w, 400, errors.New("此任务不能重试"))
		return
	}
	s, err := a.session(sessionID)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	info, err := os.Stat(local)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if !info.Mode().IsRegular() {
		writeError(w, 400, errors.New("请选择普通文件"))
		return
	}
	ctx, t, err := a.beginTransfer(s.ctx, s, "", target, info.Size())
	if err != nil {
		writeError(w, 400, err)
		return
	}
	a.mu.Lock()
	t.localPath = local
	t.Retryable = true
	t.Status = "queued"
	a.mu.Unlock()
	go a.runNativeUpload(ctx, s, t, local, input.Overwrite)
	writeJSON(w, map[string]string{"id": t.ID})
}
