package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ApplicationVersion = "v0.01"

// Increase this integer for every published build, including packaging-only
// releases. Display versions alone do not distinguish the v0.01 revisions.
const ApplicationBuild uint64 = 20260913020
const UpdateManifestURL = "https://ds.free-vps.org/up.deb.json"
const updateTimeout = 3 * time.Second
const maximumUpdateSize int64 = 1 << 30

var updateHashPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

type UpdateDescriptor struct {
	SchemaVersion    int    `json:"schemaVersion"`
	Build            uint64 `json:"build"`
	Product          string `json:"product"`
	Platform         string `json:"platform"`
	Version          string `json:"version"`
	Notes            string `json:"notes"`
	SHA256           string `json:"sha256"`
	Size             int64  `json:"size"`
	ExecutableSHA256 string `json:"executableSHA256"`
}
type UpdatePackage struct {
	Build            uint64 `json:"build"`
	Version          string `json:"version"`
	URL              string `json:"url"`
	SHA256           string `json:"sha256"`
	Size             int64  `json:"size"`
	ExecutableSHA256 string `json:"executableSHA256"`
}
type UpdateStatus struct {
	Status         string         `json:"status"`
	CurrentVersion string         `json:"currentVersion"`
	LatestVersion  string         `json:"latestVersion,omitempty"`
	Notes          string         `json:"notes,omitempty"`
	Source         string         `json:"source"`
	Platform       string         `json:"platform"`
	InstallerReady bool           `json:"installerReady"`
	Reason         string         `json:"reason,omitempty"`
	Package        *UpdatePackage `json:"package,omitempty"`
}
type UpdateReceipt struct {
	Build            uint64 `json:"build"`
	Version          string `json:"version"`
	SHA256           string `json:"sha256"`
	ExecutableSHA256 string `json:"executableSHA256"`
	InstalledAt      string `json:"installedAt"`
}
type UpdateDownload struct {
	ID       string        `json:"id"`
	Status   string        `json:"status"`
	Received int64         `json:"received"`
	Total    int64         `json:"total"`
	Error    string        `json:"error,omitempty"`
	File     string        `json:"-"`
	Package  UpdatePackage `json:"-"`
	cancel   context.CancelFunc
}
type startupUpdate struct {
	once   sync.Once
	done   chan struct{}
	result UpdateStatus
	mu     sync.Mutex
	job    *UpdateDownload
}

func updateAddress(platform string) (string, string) {
	if platform == "windows-amd64" {
		return "https://ds.free-vps.org/up.exe.json", "https://ds.free-vps.org/up.exe"
	}
	if platform == "linux-amd64" {
		return UpdateManifestURL, "https://ds.free-vps.org/up.deb"
	}
	return "", ""
}
func updateClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 2 || req.URL.Scheme != "https" || req.URL.Host != "ds.free-vps.org" || req.URL.User != nil {
			return errors.New("更新地址跳转无效")
		}
		return nil
	}}
}
func noUpdate(reason string) UpdateStatus {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	source, _ := updateAddress(platform)
	return UpdateStatus{Status: "none", CurrentVersion: ApplicationVersion, Source: source, Platform: platform, Reason: reason}
}
func FileSHA256(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (a *App) ConfigDirectory() string { return a.store.dir }
func (a *App) beginUpdateCheck() {
	a.updateCheck.once.Do(func() {
		a.updateCheck.done = make(chan struct{})
		go func() {
			defer close(a.updateCheck.done)
			ctx, cancel := context.WithTimeout(a.ctx, updateTimeout)
			defer cancel()
			a.updateCheck.result = boundedUpdateProbe(ctx, func() UpdateStatus {
				exe, err := os.Executable()
				if err != nil {
					return noUpdate("unavailable")
				}
				hash, err := FileSHA256(exe)
				if err != nil {
					return noUpdate("unavailable")
				}
				receipt, err := ReadUpdateReceipt(a.store.dir)
				if err != nil {
					return noUpdate("invalid-receipt")
				}
				platform := runtime.GOOS + "-" + runtime.GOARCH
				address, _ := updateAddress(platform)
				result := checkUpdate(ctx, updateClient(updateTimeout), address, platform, hash, receipt)
				if result.Status == "available" && !NativePackageUpdateSupported() {
					result.InstallerReady = false
					result.Reason = "此 Linux 发行版请从官网下载 RPM 或通用安装包更新，保留原数据目录。内置自动安装目前适用于 Debian/Ubuntu 系列，不会在此系统运行 dpkg。"
				}
				return result
			})
		}()
	})
}

// A matching ELF can run across distributions; a Debian package transaction
// cannot. Finding an optional dpkg binary alone does not make Fedora Debian.
func NativePackageUpdateSupported() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	release, err := os.ReadFile("/etc/os-release")
	if err != nil {
		release, _ = os.ReadFile("/usr/lib/os-release")
	}
	_, dpkgErr := exec.LookPath("dpkg")
	_, debErr := exec.LookPath("dpkg-deb")
	return debianPackageHost(string(release), dpkgErr == nil && debErr == nil)
}
func debianPackageHost(release string, toolsPresent bool) bool {
	if !toolsPresent {
		return false
	}
	for _, line := range strings.Split(release, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || (key != "ID" && key != "ID_LIKE") {
			continue
		}
		value = strings.Trim(value, "\"'")
		for _, id := range strings.Fields(value) {
			if id == "debian" || id == "ubuntu" {
				return true
			}
		}
	}
	return false
}

// A slow filesystem must not hold startup or the check API past the deadline.
// At most one detached probe exists because beginUpdateCheck runs once.
func boundedUpdateProbe(ctx context.Context, probe func() UpdateStatus) UpdateStatus {
	done := make(chan UpdateStatus, 1)
	go func() { done <- probe() }()
	select {
	case result := <-done:
		if ctx.Err() == nil {
			return result
		}
	case <-ctx.Done():
	}
	return noUpdate("timeout")
}
func checkUpdate(ctx context.Context, client *http.Client, address, platform, currentHash string, receipt UpdateReceipt) UpdateStatus {
	result := noUpdate("unavailable")
	result.Source = address
	result.Platform = platform
	_, artifact := updateAddress(platform)
	if artifact == "" {
		result.Reason = "platform-unavailable"
		return result
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if e != nil {
		return result
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "DengShell/0.01")
	req.Header.Set("Cache-Control", "no-cache")
	response, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			result.Reason = "timeout"
		}
		return result
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(data) > 65536 {
		result.Reason = "invalid"
		return result
	}
	var descriptor UpdateDescriptor
	if json.Unmarshal(data, &descriptor) != nil || descriptor.SchemaVersion != 2 || descriptor.Build == 0 || descriptor.Product != "DengShell" || descriptor.Platform != platform || len(descriptor.Notes) > 16000 || len(descriptor.Version) > 64 || strings.TrimSpace(descriptor.Version) == "" || !updateHashPattern.MatchString(descriptor.SHA256) || !updateHashPattern.MatchString(descriptor.ExecutableSHA256) || descriptor.Size <= 0 || descriptor.Size > maximumUpdateSize {
		result.Reason = "invalid"
		return result
	}
	descriptor.SHA256 = strings.ToLower(descriptor.SHA256)
	descriptor.ExecutableSHA256 = strings.ToLower(descriptor.ExecutableSHA256)
	if platform == "windows-amd64" && descriptor.SHA256 != descriptor.ExecutableSHA256 {
		result.Reason = "invalid"
		return result
	}
	minimumBuild := ApplicationBuild
	if receipt.Build > minimumBuild {
		minimumBuild = receipt.Build
	}
	versionOrder, validVersion := compareUpdateVersions(descriptor.Version, ApplicationVersion)
	if !validVersion || versionOrder < 0 {
		result.Reason = "version-rollback"
		return result
	}
	if receipt.Version != "" {
		order, valid := compareUpdateVersions(descriptor.Version, receipt.Version)
		if !valid || order < 0 {
			result.Reason = "version-rollback"
			return result
		}
	}
	if descriptor.Build <= minimumBuild {
		result.Reason = "build-rollback"
		if descriptor.Build == minimumBuild && strings.EqualFold(currentHash, descriptor.ExecutableSHA256) {
			result.Reason = "current"
		}
		return result
	}
	result.Status = "available"
	result.Reason = ""
	result.InstallerReady = true
	result.LatestVersion = descriptor.Version
	result.Notes = descriptor.Notes
	result.Package = &UpdatePackage{Build: descriptor.Build, Version: descriptor.Version, URL: artifact, SHA256: descriptor.SHA256, Size: descriptor.Size, ExecutableSHA256: descriptor.ExecutableSHA256}
	return result
}

// Numeric dotted release versions are deliberately strict. Unknown prerelease
// syntax cannot silently downgrade a stable release.
func compareUpdateVersions(left, right string) (int, bool) {
	parse := func(value string) ([]uint64, bool) {
		parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
		if len(parts) < 2 || len(parts) > 4 {
			return nil, false
		}
		out := make([]uint64, len(parts))
		for i, part := range parts {
			if part == "" || len(part) > 10 {
				return nil, false
			}
			for _, c := range part {
				if c < '0' || c > '9' {
					return nil, false
				}
			}
			n, err := strconv.ParseUint(part, 10, 32)
			if err != nil {
				return nil, false
			}
			out[i] = n
		}
		return out, true
	}
	a, ok := parse(left)
	if !ok {
		return 0, false
	}
	b, ok := parse(right)
	if !ok {
		return 0, false
	}
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y uint64
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x < y {
			return -1, true
		}
		if x > y {
			return 1, true
		}
	}
	return 0, true
}

// Update helpers recheck the installed high-water mark immediately before an
// installation, so a previously staged plan cannot roll back a newer receipt.
func ValidateUpdateBuild(configDir string, build uint64, version string) error {
	minimum := ApplicationBuild
	receipt, err := ReadUpdateReceipt(configDir)
	if err != nil {
		return err
	}
	minimum = max(minimum, receipt.Build)
	if build <= minimum {
		return errors.New("更新构建必须高于当前及已安装构建")
	}
	if order, ok := compareUpdateVersions(version, ApplicationVersion); !ok || order < 0 {
		return errors.New("更新版本不能低于当前版本")
	}
	if receipt.Version != "" {
		if order, ok := compareUpdateVersions(version, receipt.Version); !ok || order < 0 {
			return errors.New("更新版本不能低于已安装版本")
		}
	}
	return nil
}

func ReadUpdateReceipt(configDir string) (UpdateReceipt, error) {
	var receipt UpdateReceipt
	file, err := os.Open(filepath.Join(configDir, "update-receipt.json"))
	if os.IsNotExist(err) {
		return receipt, nil
	}
	if err != nil {
		return receipt, fmt.Errorf("无法读取更新收据：%w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 || json.Unmarshal(data, &receipt) != nil {
		return receipt, errors.New("更新收据损坏，无法确认已安装构建")
	}
	return receipt, nil
}

// Reuse the config writer's private temporary file, fsync and platform atomic
// replacement (MoveFileEx with WRITE_THROUGH on Windows).
func WriteUpdateReceipt(configDir string, data []byte) error {
	if len(data) > 65536 || !json.Valid(data) {
		return errors.New("更新收据内容无效")
	}
	return atomicConfigFile(filepath.Join(configDir, "update-receipt.json"), data)
}

func (a *App) registerUpdateHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/updates/check", func(w http.ResponseWriter, r *http.Request) {
		a.beginUpdateCheck()
		select {
		case <-a.updateCheck.done:
			writeJSON(w, a.updateCheck.result)
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("POST /api/updates/download", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			SHA256 string `json:"sha256"`
		}
		if !decode(w, r, &request) {
			return
		}
		job, e := a.StartUpdateDownload(request.SHA256)
		respond(w, job, e)
	})
	mux.HandleFunc("GET /api/updates/download/{id}", func(w http.ResponseWriter, r *http.Request) {
		job, e := a.UpdateDownload(r.PathValue("id"))
		respond(w, job, e)
	})
	mux.HandleFunc("DELETE /api/updates/download/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.updateCheck.mu.Lock()
		defer a.updateCheck.mu.Unlock()
		job := a.updateCheck.job
		if job == nil || job.ID != r.PathValue("id") {
			respond(w, nil, errors.New("更新下载不存在"))
			return
		}
		if job.Status == "downloading" {
			job.cancel()
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
}
func (a *App) StartUpdateDownload(hash string) (UpdateDownload, error) {
	a.beginUpdateCheck()
	select {
	case <-a.updateCheck.done:
	default:
		return UpdateDownload{}, errors.New("更新检查尚未完成")
	}
	offer := a.updateCheck.result
	if offer.Status == "available" && !NativePackageUpdateSupported() {
		return UpdateDownload{}, errors.New("请从官网下载此发行版对应的 RPM 或通用安装包，不在此系统下载并执行 DEB 更新")
	}
	if offer.Status != "available" || offer.Package == nil || offer.Package.SHA256 != hash {
		return UpdateDownload{}, errors.New("更新文件与本次检查结果不一致")
	}
	if err := ValidateUpdateBuild(a.store.dir, offer.Package.Build, offer.Package.Version); err != nil {
		return UpdateDownload{}, err
	}
	a.updateCheck.mu.Lock()
	defer a.updateCheck.mu.Unlock()
	if old := a.updateCheck.job; old != nil && (old.Status == "downloading" || old.Status == "ready") {
		return *old, nil
	}
	dir, e := os.MkdirTemp(a.store.dir, ".update-")
	if e != nil {
		return UpdateDownload{}, e
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Minute)
	job := &UpdateDownload{ID: randomID(), Status: "downloading", Total: offer.Package.Size, File: filepath.Join(dir, filepath.Base(offer.Package.URL)), Package: *offer.Package, cancel: cancel}
	a.updateCheck.job = job
	go a.downloadUpdate(ctx, job)
	return *job, nil
}
func (a *App) UpdateDownload(id string) (UpdateDownload, error) {
	a.updateCheck.mu.Lock()
	defer a.updateCheck.mu.Unlock()
	if a.updateCheck.job == nil || a.updateCheck.job.ID != id {
		return UpdateDownload{}, errors.New("更新下载不存在")
	}
	return *a.updateCheck.job, nil
}
func (a *App) PreparedUpdate(id string) (UpdateDownload, error) {
	job, e := a.UpdateDownload(id)
	if e != nil {
		return job, e
	}
	if job.Status != "ready" {
		return job, errors.New("更新尚未下载完成")
	}
	hash, e := FileSHA256(job.File)
	if e != nil {
		return job, e
	}
	if hash != job.Package.SHA256 {
		return job, errors.New("更新文件校验失败")
	}
	return job, nil
}
func (a *App) downloadUpdate(ctx context.Context, job *UpdateDownload) {
	defer job.cancel()
	err := func() error {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, job.Package.URL, nil)
		if e != nil {
			return e
		}
		response, e := updateClient(15 * time.Minute).Do(req)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("下载失败 (%d)", response.StatusCode)
		}
		if response.ContentLength >= 0 && response.ContentLength != job.Total {
			return errors.New("更新文件大小与描述不符")
		}
		f, e := os.OpenFile(job.File, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer f.Close()
		h := sha256.New()
		reader := io.LimitReader(response.Body, job.Total+1)
		buf := make([]byte, 128<<10)
		var total int64
		for {
			n, re := reader.Read(buf)
			if n > 0 {
				total += int64(n)
				if total > job.Total {
					return errors.New("更新文件超过描述的大小")
				}
				if _, e = f.Write(buf[:n]); e != nil {
					return e
				}
				h.Write(buf[:n])
				a.updateCheck.mu.Lock()
				job.Received = total
				a.updateCheck.mu.Unlock()
			}
			if re == io.EOF {
				break
			}
			if re != nil {
				return re
			}
		}
		if total != job.Total || hex.EncodeToString(h.Sum(nil)) != job.Package.SHA256 {
			return errors.New("更新文件 SHA-256 校验失败")
		}
		return f.Sync()
	}()
	a.finishUpdateDownload(ctx, job, err)
}
func (a *App) finishUpdateDownload(ctx context.Context, job *UpdateDownload, err error) {
	a.updateCheck.mu.Lock()
	defer a.updateCheck.mu.Unlock()
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		if errors.Is(ctx.Err(), context.Canceled) {
			job.Status = "cancelled"
			job.Error = "下载已取消"
		}
		_ = os.RemoveAll(filepath.Dir(job.File))
		return
	}
	job.Status = "ready"
}
