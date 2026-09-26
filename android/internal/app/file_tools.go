package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"github.com/pkg/sftp"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

const maxEditableBytes = 8 << 20

type textDocument struct {
	Path         string `json:"path"`
	Text         string `json:"text"`
	Encoding     string `json:"encoding"`
	SHA256       string `json:"sha256"`
	Bytes        int    `json:"bytes"`
	HistoryError string `json:"historyError,omitempty"`
}
type fileToolError struct{ message, code string }

func (e *fileToolError) Error() string { return e.message }
func (e *fileToolError) Code() string  { return e.code }
func textEncoding(name string) encoding.Encoding {
	switch name {
	case "gb18030":
		return simplifiedchinese.GB18030
	case "gbk":
		return simplifiedchinese.GBK
	case "big5":
		return traditionalchinese.Big5
	case "utf-16le", "utf-16le-bom":
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	case "utf-16be", "utf-16be-bom":
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
	}
	return nil
}
func decodeText(data []byte, choice string) (string, string, error) {
	name := choice
	if name == "" || name == "auto" {
		switch {
		case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
			name = "utf-8-bom"
		case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
			name = "utf-16le-bom"
		case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
			name = "utf-16be-bom"
		case utf8.Valid(data):
			name = "utf-8"
		default:
			return "", "", &fileToolError{"无法可靠识别编码，请选择原文件编码后重新打开。文件未被修改。", "encoding_required"}
		}
	}
	raw := data
	if name == "utf-8-bom" {
		raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	}
	if name == "utf-16le-bom" {
		raw = bytes.TrimPrefix(raw, []byte{0xff, 0xfe})
	}
	if name == "utf-16be-bom" {
		raw = bytes.TrimPrefix(raw, []byte{0xfe, 0xff})
	}
	var decoded []byte
	var err error
	if name == "utf-8" || name == "utf-8-bom" {
		if !utf8.Valid(raw) {
			return "", "", errors.New("内容不是有效 UTF-8，请重新选择编码")
		}
		decoded = raw
	} else {
		codec := textEncoding(name)
		if codec == nil {
			return "", "", errors.New("不支持此编码")
		}
		decoded, err = codec.NewDecoder().Bytes(raw)
		if err != nil {
			return "", "", err
		}
	}
	if bytes.ContainsRune(decoded, 0) {
		return "", "", errors.New("文件含有二进制空字节，不能用文本编辑器打开；请选择系统关联或下载")
	}
	rebuilt, err := encodeText(string(decoded), name)
	if err != nil || !bytes.Equal(rebuilt, data) {
		return "", "", errors.New("所选编码无法无损还原原文件，请换一种编码；文件未被修改")
	}
	return string(decoded), name, nil
}
func encodeText(text, name string) ([]byte, error) {
	if !utf8.ValidString(text) {
		return nil, errors.New("文本包含无效 Unicode 数据")
	}
	data := []byte(text)
	if name != "utf-8" && name != "utf-8-bom" {
		codec := textEncoding(name)
		if codec == nil {
			return nil, errors.New("不支持此编码")
		}
		var err error
		data, err = codec.NewEncoder().Bytes(data)
		if err != nil {
			return nil, fmt.Errorf("当前编码无法表示输入的文字，请选择 UTF-8 或恢复内容：%w", err)
		}
	}
	switch name {
	case "utf-8-bom":
		data = append([]byte{0xef, 0xbb, 0xbf}, data...)
	case "utf-16le-bom":
		data = append([]byte{0xff, 0xfe}, data...)
	case "utf-16be-bom":
		data = append([]byte{0xfe, 0xff}, data...)
	}
	return data, nil
}
func digestText(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func readRemoteText(op *sftpOperation, s *Session, target string) ([]byte, os.FileInfo, error) {
	f, e := s.files.Open(target)
	if e != nil {
		return nil, nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > maxEditableBytes {
		return nil, nil, errors.New("文本编辑器支持最大 8 MiB 的普通文件，请使用下载或系统关联打开")
	}
	data, e := io.ReadAll(io.LimitReader(sftpProgressReader{f, op}, maxEditableBytes+1))
	if len(data) > maxEditableBytes {
		return nil, nil, errors.New("文件超过 8 MiB")
	}
	return data, info, e
}
func (a *App) registerFileToolsHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{id}/file-content", a.readTextHTTP)
	mux.HandleFunc("POST /api/sessions/{id}/file-content", a.saveTextHTTP)
	mux.HandleFunc("GET /api/sessions/{id}/file-permissions", a.permissionsHTTP)
	mux.HandleFunc("POST /api/sessions/{id}/file-permissions", a.permissionsHTTP)
	mux.HandleFunc("GET /api/sessions/{id}/archive", a.archiveHTTP)
}
func (a *App) readTextHTTP(w http.ResponseWriter, r *http.Request) {
	s, e := a.fileSession(r.PathValue("id"))
	if e != nil {
		writeError(w, 400, e)
		return
	}
	op := startSFTPOperation(r.Context(), s, sftpIdleTimeout)
	defer op.close()
	r = r.WithContext(op.ctx)
	target, e := remotePath(r.URL.Query().Get("path"))
	if e == nil {
		target, e = resolveRemoteSymlinks(r.Context(), s.files, target)
	}
	if e != nil {
		writeError(w, 400, op.err(e))
		return
	}
	data, _, e := readRemoteText(op, s, target)
	if e != nil {
		writeError(w, 400, op.err(e))
		return
	}
	text, name, e := decodeText(data, r.URL.Query().Get("encoding"))
	historyError := ""
	if e == nil && op.ctx.Err() == nil {
		historyError = a.recordPathHistory(s, target, "file")
	}
	respond(w, textDocument{target, text, name, digestText(data), len(data), historyError}, op.err(e))
}
func (a *App) saveTextHTTP(w http.ResponseWriter, r *http.Request) {
	s, e := a.fileSession(r.PathValue("id"))
	if e != nil {
		writeError(w, 400, e)
		return
	}
	var input textDocument
	r.Body = http.MaxBytesReader(w, r.Body, 40<<20)
	if e = json.NewDecoder(r.Body).Decode(&input); e != nil {
		writeError(w, 400, errors.New("文本请求过大或格式无效"))
		return
	}
	op := startSFTPOperation(r.Context(), s, sftpIdleTimeout)
	defer op.close()
	r = r.WithContext(op.ctx)
	target, e := remotePath(input.Path)
	if e == nil {
		target, e = resolveRemoteSymlinks(r.Context(), s.files, target)
	}
	if e != nil {
		writeError(w, 400, op.err(e))
		return
	}
	unlock, e := lockRemoteWrite(r.Context(), s, target)
	if e != nil {
		writeError(w, 400, op.err(e))
		return
	}
	defer unlock()
	before, info, e := readRemoteText(op, s, target)
	if e != nil {
		writeError(w, 400, op.err(e))
		return
	}
	if digestText(before) != input.SHA256 {
		writeError(w, 409, &fileToolError{"远程文件已被其他程序修改，请重新打开并合并内容后保存。", "file_changed"})
		return
	}
	data, e := encodeText(input.Text, input.Encoding)
	if e == nil && len(data) > maxEditableBytes {
		e = errors.New("保存内容超过 8 MiB")
	}
	if e != nil {
		writeError(w, 400, e)
		return
	}
	if !bytes.Equal(before, data) {
		e = writeRemoteText(op, s, target, data, info, input.SHA256)
	}
	respond(w, textDocument{target, input.Text, input.Encoding, digestText(data), len(data), ""}, op.err(e))
}
func writeRemoteText(op *sftpOperation, s *Session, target string, data []byte, info os.FileInfo, expectedHash string) error {
	if _, ok := s.files.HasExtension("posix-rename@openssh.com"); !ok {
		return errors.New("此 SFTP 服务不支持原子替换。为保留原文件，请下载后使用系统编辑器处理")
	}
	f, temporary, cleanup, e := privateRemoteTemporary(s.files, target, "edit")
	if e != nil {
		return e
	}
	defer cleanup()
	if _, e = uploadChunks(op.ctx, f, bytes.NewReader(data), func(int) { op.touch() }); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = preserveRemoteMetadata(s.files, temporary, info); e != nil {
		return e
	}
	latest, _, err := readRemoteText(op, s, target)
	if err != nil {
		return err
	}
	if digestText(latest) != expectedHash {
		return &fileToolError{"保存过程中远程文件已改变，已保留远程版本，请重新打开后合并。", "file_changed"}
	}
	if err := op.ctx.Err(); err != nil {
		return err
	}
	return s.files.PosixRename(temporary, target)
}
func (a *App) permissionsHTTP(w http.ResponseWriter, r *http.Request) {
	s, e := a.fileSession(r.PathValue("id"))
	if e != nil {
		writeError(w, 400, e)
		return
	}
	var input struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	input.Path = r.URL.Query().Get("path")
	if r.Method == http.MethodPost && !decode(w, r, &input) {
		return
	}
	target, e := remotePath(input.Path)
	if e != nil {
		writeError(w, 400, e)
		return
	}
	op := startSFTPOperation(r.Context(), s, sftpIdleTimeout)
	defer op.close()
	if e := op.ctx.Err(); e != nil {
		writeError(w, 400, op.err(e))
		return
	}
	info, e := s.files.Lstat(target)
	if e != nil {
		writeError(w, 400, op.err(e))
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		writeError(w, 400, errors.New("符号链接权限由目标决定，请打开目标路径后修改"))
		return
	}
	if r.Method == http.MethodPost {
		n, err := strconv.ParseUint(input.Mode, 8, 12)
		if err != nil || len(input.Mode) < 3 || len(input.Mode) > 4 {
			writeError(w, 400, errors.New("权限应为 000 至 7777 的八进制数字"))
			return
		}
		m := os.FileMode(n & 0777)
		if n&04000 != 0 {
			m |= os.ModeSetuid
		}
		if n&02000 != 0 {
			m |= os.ModeSetgid
		}
		if n&01000 != 0 {
			m |= os.ModeSticky
		}
		if e = s.files.Chmod(target, m); e != nil {
			writeError(w, 400, op.err(e))
			return
		}
		info, e = s.files.Stat(target)
		if e != nil {
			writeError(w, 400, op.err(e))
			return
		}
	}
	mode := uint32(info.Mode().Perm())
	if info.Mode()&os.ModeSetuid != 0 {
		mode |= 04000
	}
	if info.Mode()&os.ModeSetgid != 0 {
		mode |= 02000
	}
	if info.Mode()&os.ModeSticky != 0 {
		mode |= 01000
	}
	owner := s.resolveFileOwners(op.ctx, []os.FileInfo{info}).owner(info)
	writeJSON(w, map[string]string{"path": target, "mode": fmt.Sprintf("%04o", mode), "owner": owner})
}
func removeRemoteTree(ctx context.Context, c *sftp.Client, target string, depth int) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if target == "/" {
		return errors.New("不能删除根目录")
	}
	if depth > 256 {
		return errors.New("目录深度超过安全遍历范围")
	}
	info, e := c.Lstat(target)
	if e != nil {
		return e
	}
	touchSFTPOperation(ctx)
	if e := ctx.Err(); e != nil {
		return e
	}
	if !info.IsDir() {
		return c.Remove(target)
	}
	children, e := c.ReadDirContext(ctx, target)
	if e != nil {
		return e
	}
	for _, child := range children {
		if e = removeRemoteTree(ctx, c, path.Join(target, child.Name()), depth+1); e != nil {
			return e
		}
	}
	return c.RemoveDirectory(target)
}
func archiveRemote(ctx context.Context, c *sftp.Client, target string, out io.Writer) error {
	gzipWriter := gzip.NewWriter(out)
	tw := tar.NewWriter(gzipWriter)
	var visit func(string, string, int) error
	visit = func(remote, name string, depth int) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if depth > 256 {
			return errors.New("目录层级过深")
		}
		info, e := c.Lstat(remote)
		if e != nil {
			return e
		}
		touchSFTPOperation(ctx)
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, e = c.ReadLink(remote)
			if e != nil {
				return e
			}
		}
		if !info.Mode().IsRegular() && !info.IsDir() && link == "" {
			return fmt.Errorf("无法打包特殊文件：%s", remote)
		}
		h, e := tar.FileInfoHeader(info, link)
		if e != nil {
			return e
		}
		h.Name = name
		if st, ok := info.Sys().(*sftp.FileStat); ok {
			h.Uid = int(st.UID)
			h.Gid = int(st.GID)
		}
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if info.IsDir() {
			children, e := c.ReadDirContext(ctx, remote)
			if e != nil {
				return e
			}
			for _, f := range children {
				if e = visit(path.Join(remote, f.Name()), path.Join(name, f.Name()), depth+1); e != nil {
					return e
				}
			}
		} else if info.Mode().IsRegular() {
			f, e := c.Open(remote)
			if e != nil {
				return e
			}
			_, e = io.CopyN(tw, externalSnapshotReader{ctx, f}, info.Size())
			f.Close()
			if e != nil {
				return e
			}
		}
		return nil
	}
	name := path.Base(target)
	if name == "/" {
		name = "root"
	}
	e := visit(target, name, 0)
	tErr := tw.Close()
	gErr := gzipWriter.Close()
	if e != nil {
		return e
	}
	if tErr != nil {
		return tErr
	}
	return gErr
}
func (a *App) archiveHTTP(w http.ResponseWriter, r *http.Request) {
	s, e := a.fileSession(r.PathValue("id"))
	if e != nil {
		writeError(w, 400, e)
		return
	}
	target, e := remotePath(r.URL.Query().Get("path"))
	if e != nil {
		writeError(w, 400, e)
		return
	}
	// Finish locally before emitting a download: an incomplete archive is never
	// presented as a successful response, and the server only performs SFTP reads.
	dir := filepath.Join(a.store.dir, "temporary")
	if e = os.MkdirAll(dir, 0700); e != nil {
		writeError(w, 400, e)
		return
	}
	f, e := os.CreateTemp(dir, "archive-*.tar.gz")
	if e != nil {
		writeError(w, 400, e)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	op := startSFTPOperation(r.Context(), s, sftpIdleTimeout)
	defer op.close()
	if e = op.err(archiveRemote(op.ctx, s.files, target, f)); e != nil {
		writeError(w, 400, e)
		return
	}
	op.close() // The remainder serves a local file and performs no SFTP I/O.
	f.Seek(0, 0)
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": archiveName(target)}))
	http.ServeContent(w, r, archiveName(target), info.ModTime(), f)
}
func archiveName(target string) string {
	n := path.Base(target)
	if n == "/" {
		n = "root"
	}
	return n + ".tar.gz"
}
func (a *App) DownloadArchiveTo(sessionID, remote, local string) (e error) {
	s, e := a.fileSession(sessionID)
	if e != nil {
		return e
	}
	remote, e = remotePath(remote)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(local), ".dengshell-archive-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	op := startSFTPOperation(s.ctx, s, sftpIdleTimeout)
	defer op.finish(&e)
	if e = archiveRemote(op.ctx, s.files, remote, f); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = op.ctx.Err(); e != nil {
		return e
	}
	return replaceConfigFile(f.Name(), local)
}
func (a *App) PrepareExternalFile(sessionID, remote string) (local string, err error) {
	s, err := a.fileSession(sessionID)
	if err != nil {
		return "", err
	}
	remote, err = remotePath(remote)
	if err != nil {
		return "", err
	}
	op := startSFTPOperation(s.ctx, s, sftpIdleTimeout)
	defer op.finish(&err)
	return prepareExternalSnapshot(op.ctx, s.files, remote, filepath.Join(a.store.dir, "opened"), externalSnapshotLimits{bytes: 256 << 20, entries: 10000, depth: 128})
}
