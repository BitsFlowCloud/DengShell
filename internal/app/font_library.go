package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const fontLibraryOrigin = "https://ds.free-vps.org"

// The reviewed catalog is part of the application, so a compromised website
// cannot substitute font files, license notices, destinations or checksums.
// No network access is needed at startup or to list the catalog.
//
//go:embed font_library_catalog.json
var fontLibraryJSON []byte

type LibraryFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type LibraryFont struct {
	ID           string      `json:"id"`
	Kind         string      `json:"kind"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	Style        string      `json:"style"`
	Project      string      `json:"project"`
	Version      string      `json:"version"`
	LicenseName  string      `json:"licenseName"`
	LicenseText  string      `json:"licenseText"`
	WeightRange  string      `json:"weightRange,omitempty"`
	BuiltinID    string      `json:"builtinId,omitempty"`
	FallbackNote string      `json:"fallbackNote"`
	File         LibraryFile `json:"file"`
	Preview      LibraryFile `json:"preview"`
}

type FontLibraryCatalog struct {
	Version int           `json:"version"`
	Fonts   []LibraryFont `json:"fonts"`
}

var libraryCatalogOnce = sync.OnceValues(func() (FontLibraryCatalog, error) {
	var catalog FontLibraryCatalog
	if err := json.Unmarshal(fontLibraryJSON, &catalog); err != nil {
		return catalog, err
	}
	seen := map[string]bool{}
	for _, font := range catalog.Fonts {
		if font.ID == "" || seen[font.ID] || (font.Kind != "font" && font.Kind != "ui-font") || len(font.LicenseText) > 32768 || font.LicenseText == "" {
			return catalog, errors.New("内置在线字体目录无效")
		}
		seen[font.ID] = true
		for _, file := range []LibraryFile{font.File, font.Preview} {
			if !validLibraryFile(file) {
				return catalog, errors.New("内置在线字体文件描述无效")
			}
		}
		if !strings.HasPrefix(font.File.Path, "/fonts/files/") || !strings.HasPrefix(font.Preview.Path, "/fonts/previews/") || font.Preview.Size > 2<<20 || !strings.HasPrefix(font.Project, "https://") {
			return catalog, errors.New("内置在线字体类型或来源无效")
		}
	}
	return catalog, nil
})

var libraryPathPattern = regexp.MustCompile(`^/fonts/(files/[a-z0-9-]+\.woff2|previews/[a-z0-9-]+\.png)$`)

func validLibraryFile(file LibraryFile) bool {
	return libraryPathPattern.MatchString(file.Path) && updateHashPattern.MatchString(file.SHA256) && file.Size > 0 && file.Size <= maxFontBytes
}

func fontPreviewDataURL(data []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

func (s *Store) cachedLibraryAsset(font LibraryFont) (ManagedAsset, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, asset := range s.config.Assets {
		if asset.LibraryID != font.ID || asset.Kind != font.Kind || !validAssetID(asset.ID) || asset.Size != font.File.Size {
			continue
		}
		file, err := os.Open(filepath.Join(s.dir, "assets", asset.ID))
		if err != nil {
			continue
		}
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(file, font.File.Size+1))
		file.Close()
		if err == nil && n == font.File.Size && strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), font.File.SHA256) {
			return asset, true
		}
	}
	return ManagedAsset{}, false
}

func (s *Store) importLibraryAsset(font LibraryFont, data []byte) (ManagedAsset, error) {
	return s.importAsset(font.Kind, font.ID+".woff2", font.Name, data, &font)
}

func findLibraryFont(id string) (LibraryFont, error) {
	catalog, err := libraryCatalogOnce()
	if err != nil {
		return LibraryFont{}, err
	}
	for _, font := range catalog.Fonts {
		if font.ID == id {
			return font, nil
		}
	}
	return LibraryFont{}, errors.New("在线字体不存在，请重新选择")
}

type FontDownload struct {
	ID       string        `json:"id"`
	FontID   string        `json:"fontId"`
	Status   string        `json:"status"`
	Received int64         `json:"received"`
	Total    int64         `json:"total"`
	Error    string        `json:"error,omitempty"`
	Asset    *ManagedAsset `json:"asset,omitempty"`
	cancel   context.CancelFunc
}

type fontLibraryState struct {
	mu      sync.Mutex
	jobs    map[string]*FontDownload
	client  *http.Client // nil in production; tests provide an isolated transport.
	preview map[string][]byte
}

func (a *App) fontLibraryClient() *http.Client {
	if a.fontLibrary.client != nil {
		return a.fontLibrary.client
	}
	return updateClient(2 * time.Minute)
}

func fetchLibraryFile(ctx context.Context, client *http.Client, file LibraryFile, progress func(int64)) ([]byte, error) {
	if !validLibraryFile(file) {
		return nil, errors.New("字体下载地址无效")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fontLibraryOrigin+file.Path, nil)
	if err != nil {
		return nil, err
	}
	preview := strings.HasPrefix(file.Path, "/fonts/previews/")
	if preview && file.Size > 2<<20 {
		return nil, errors.New("字体预览文件过大")
	}
	if preview {
		req.Header.Set("Accept", "image/png")
	} else {
		req.Header.Set("Accept", "font/woff2")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接官网字体库失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("官网字体文件暂不可用（HTTP %d），请稍后重试", resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != file.Size {
		return nil, errors.New("字体文件大小不符，未安装，请重试")
	}
	reader := io.LimitReader(resp.Body, file.Size+1)
	data := make([]byte, 0, file.Size)
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			data = append(data, buffer[:n]...)
			if int64(len(data)) > file.Size {
				return nil, errors.New("字体文件超出声明大小，未安装")
			}
			if progress != nil {
				progress(int64(len(data)))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if int64(len(data)) != file.Size || !strings.EqualFold(hex.EncodeToString(hash[:]), file.SHA256) {
		return nil, errors.New("字体完整性校验失败，未安装，请重试")
	}
	if preview {
		info, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || info.Width < 1 || info.Height < 1 || info.Width > 4096 || info.Height > 2048 {
			return nil, errors.New("字体预览图片无效")
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			return nil, err
		}
	} else if _, err := validateFont(data); err != nil {
		return nil, fmt.Errorf("字体格式无效，未安装：%w", err)
	}
	return data, nil
}

func (a *App) fontDownload(id string) (FontDownload, error) {
	a.fontLibrary.mu.Lock()
	defer a.fontLibrary.mu.Unlock()
	job := a.fontLibrary.jobs[id]
	if job == nil {
		return FontDownload{}, errors.New("字体下载任务不存在")
	}
	copy := *job
	copy.cancel = nil
	return copy, nil
}

func (a *App) beginFontDownload(id string) (FontDownload, error) {
	font, err := findLibraryFont(id)
	if err != nil {
		return FontDownload{}, err
	}
	if font.BuiltinID != "" {
		return FontDownload{}, errors.New("此字体已经内置，可以直接使用")
	}
	a.fontLibrary.mu.Lock()
	defer a.fontLibrary.mu.Unlock()
	if a.fontLibrary.jobs == nil {
		a.fontLibrary.jobs = map[string]*FontDownload{}
	}
	active := 0
	for _, job := range a.fontLibrary.jobs {
		if job.Status == "downloading" {
			if job.FontID == id {
				return *job, nil
			}
			active++
		}
	}
	if active >= 2 {
		return FontDownload{}, errors.New("已有两个字体正在下载，请完成或取消后再试")
	}
	if len(a.fontLibrary.jobs) >= 32 {
		for id, job := range a.fontLibrary.jobs {
			if job.Status != "downloading" {
				delete(a.fontLibrary.jobs, id)
			}
		}
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	job := &FontDownload{ID: randomID(), FontID: id, Status: "downloading", Total: font.File.Size, cancel: cancel}
	a.fontLibrary.jobs[job.ID] = job
	go a.runFontDownload(ctx, job, font)
	return *job, nil
}

func (a *App) runFontDownload(ctx context.Context, job *FontDownload, font LibraryFont) {
	defer job.cancel()
	// Reuse only a verified local copy. A missing or damaged file is repaired by
	// downloading a fresh copy without changing the current font selection.
	asset, found := a.store.cachedLibraryAsset(font)
	var err error
	if !found {
		var data []byte
		data, err = fetchLibraryFile(ctx, a.fontLibraryClient(), font.File, func(received int64) {
			a.fontLibrary.mu.Lock()
			job.Received = received
			a.fontLibrary.mu.Unlock()
		})
		if err == nil {
			a.fontLibrary.mu.Lock()
			if err = ctx.Err(); err == nil {
				asset, err = a.store.importLibraryAsset(font, data)
			}
			a.fontLibrary.mu.Unlock()
		}
	}
	if found {
		err = ctx.Err()
	}
	if err == nil && font.Kind == "ui-font" {
		a.uiFontMu.Lock()
		a.uiFontsAtStartup[asset.ID] = true
		a.uiFontMu.Unlock()
	}
	a.fontLibrary.mu.Lock()
	defer a.fontLibrary.mu.Unlock()
	if err != nil {
		job.Status, job.Error = "failed", err.Error()
		if errors.Is(err, context.Canceled) {
			job.Status, job.Error = "cancelled", "已取消下载"
		}
		return
	}
	job.Status, job.Received, job.Asset = "complete", job.Total, &asset
}

func (a *App) registerFontLibraryHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/font-library", func(w http.ResponseWriter, r *http.Request) {
		catalog, err := libraryCatalogOnce()
		respond(w, catalog, err)
	})
	mux.HandleFunc("POST /api/font-library/downloads", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			FontID string `json:"fontId"`
		}
		if !decode(w, r, &input) {
			return
		}
		job, err := a.beginFontDownload(input.FontID)
		respond(w, job, err)
	})
	mux.HandleFunc("GET /api/font-library/downloads/{id}", func(w http.ResponseWriter, r *http.Request) {
		job, err := a.fontDownload(r.PathValue("id"))
		respond(w, job, err)
	})
	mux.HandleFunc("DELETE /api/font-library/downloads/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.fontLibrary.mu.Lock()
		job := a.fontLibrary.jobs[r.PathValue("id")]
		if job != nil && job.Status == "downloading" {
			job.cancel()
		}
		a.fontLibrary.mu.Unlock()
		if job == nil {
			writeError(w, 404, errors.New("字体下载任务不存在"))
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/font-library/previews/{id}", func(w http.ResponseWriter, r *http.Request) {
		font, err := findLibraryFont(r.PathValue("id"))
		if err != nil {
			writeError(w, 400, err)
			return
		}
		a.fontLibrary.mu.Lock()
		data := a.fontLibrary.preview[font.ID]
		a.fontLibrary.mu.Unlock()
		if data == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			data, err = fetchLibraryFile(ctx, a.fontLibraryClient(), font.Preview, nil)
			if err != nil {
				writeError(w, 502, err)
				return
			}
			a.fontLibrary.mu.Lock()
			if a.fontLibrary.preview == nil {
				a.fontLibrary.preview = map[string][]byte{}
			}
			a.fontLibrary.preview[font.ID] = data
			a.fontLibrary.mu.Unlock()
		}
		writeJSON(w, map[string]string{"dataUrl": fontPreviewDataURL(data)})
	})
}
