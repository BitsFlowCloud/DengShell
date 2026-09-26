package app

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

const updateLeaseName = ".dengshell-update.lock"
const updateCacheMarker = "DengShell update cache v1\n"

var updateDirectoryName = regexp.MustCompile(`^\.update-[0-9]+$`)
var updateHelperName = regexp.MustCompile(`^dengshell-updater-[0-9]+(?:\.exe)?$`)

// Shared by the downloader and helper across the process handoff. A cleaner
// must obtain an exclusive OS lock; crashes release leases automatically.
func holdUpdateDirectory(dir string, create bool) (func(), error) {
	path := filepath.Join(dir, updateLeaseName)
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	} else {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return func() {}, nil
		} // Earlier clients have no lease.
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("更新目录锁无效")
		}
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockUpdateFile(f, false); err != nil {
		f.Close()
		return nil, err
	}
	if create {
		_, err = io.WriteString(f, updateCacheMarker)
		if err == nil {
			err = f.Sync()
		}
	} else {
		var data []byte
		data, err = io.ReadAll(io.LimitReader(f, 128))
		if err == nil && string(data) != updateCacheMarker {
			err = errors.New("更新目录标记无效")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { f.Close() }) }, nil
}

// HoldUpdateDirectory protects an existing download while its helper runs.
func HoldUpdateDirectory(dir string) (func(), error) { return holdUpdateDirectory(dir, false) }

type cleanupUpdatePlan struct {
	Schema           int    `json:"schema"`
	Build            uint64 `json:"build"`
	ParentPID        int    `json:"parentPID"`
	ConfigDir        string `json:"configDir"`
	Staged           string `json:"staged"`
	Target           string `json:"target"`
	OldSHA256        string `json:"oldSHA256"`
	PackageSHA256    string `json:"packageSHA256"`
	ExecutableSHA256 string `json:"executableSHA256"`
}

func readUpdateCacheFile(path string, limit int64) ([]byte, error) {
	i, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !i.Mode().IsRegular() || i.Size() > limit {
		return nil, errors.New("更新缓存文件无效")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit+1))
}

func updateCacheEntries(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return nil, errors.New("更新缓存包含目录或链接")
		}
		switch entry.Name() {
		case updateLeaseName, "up.exe", "up.deb", "up.pkg.tar.zst", "plan.json", "helper-status.json", "ready", "error.txt", "installer.log", "previous-program.exe", "previous-program":
		default:
			if !updateHelperName.MatchString(entry.Name()) {
				return nil, errors.New("更新缓存包含未知文件")
			}
		}
	}
	return entries, err
}

func sameUpdateDirectory(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	ai, ae := os.Stat(a)
	bi, be := os.Stat(b)
	return ae == nil && be == nil && os.SameFile(ai, bi)
}

func readCleanupUpdatePlan(config, dir string) (cleanupUpdatePlan, error) {
	var p cleanupUpdatePlan
	b, err := readUpdateCacheFile(filepath.Join(dir, "plan.json"), 32768)
	if err != nil {
		return p, err
	}
	if json.Unmarshal(b, &p) != nil || p.Schema != 2 || p.Build == 0 || p.ParentPID <= 0 ||
		!sameUpdateDirectory(config, p.ConfigDir) || !sameUpdateDirectory(dir, filepath.Dir(p.Staged)) ||
		!filepath.IsAbs(p.Target) || !updateHashPattern.MatchString(p.OldSHA256) ||
		!updateHashPattern.MatchString(p.PackageSHA256) || !updateHashPattern.MatchString(p.ExecutableSHA256) {
		return p, errors.New("无法确认旧更新目录的归属")
	}
	switch filepath.Base(p.Staged) {
	case "up.exe", "up.deb", "up.pkg.tar.zst":
	default:
		return p, errors.New("未知更新包")
	}
	return p, nil
}

// No lease exists in older versions. Only their strictly recognized plans are
// reclaimed, and both their application and helper must have exited.
func legacyUpdateInactive(p cleanupUpdatePlan, dir string) bool {
	if updateProcessAlive(p.ParentPID) {
		return false
	}
	b, err := readUpdateCacheFile(filepath.Join(dir, "helper-status.json"), 4096)
	if os.IsNotExist(err) {
		return true
	}
	var status struct {
		PID int `json:"pid"`
	}
	return err == nil && json.Unmarshal(b, &status) == nil && status.PID > 0 && !updateProcessAlive(status.PID)
}

func (a *App) runUpdateCleanup(configDir string) {
	// Retries cover Windows helper image/log handles and the old-to-new startup
	// overlap. Periodic sweeps also collect later abandoned downloads.
	// Keep the startup directory for the worker's lifetime; do not read a Store
	// being replaced or redirected while testing persistence failure recovery.
	for _, delay := range []time.Duration{0, time.Second, 5 * time.Second, 30 * time.Second} {
		if !a.waitUpdateCleanup(delay) {
			return
		}
		cleanupUpdateDirectories(configDir)
	}
	for a.waitUpdateCleanup(time.Hour) {
		cleanupUpdateDirectories(configDir)
	}
}

func (a *App) waitUpdateCleanup(delay time.Duration) bool {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-a.ctx.Done():
		return false
	case <-t.C:
		return a.ctx.Err() == nil
	}
}

func cleanupUpdateDirectories(config string) {
	config, err := filepath.Abs(config)
	if err != nil {
		return
	}
	// Serialize collectors, including separate main windows using one config.
	guard, err := os.OpenFile(filepath.Join(config, ".dengshell-update-cleanup.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return
	}
	defer guard.Close()
	if lockUpdateFile(guard, true) != nil {
		return
	}
	entries, err := os.ReadDir(config)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !updateDirectoryName.MatchString(entry.Name()) {
			continue
		}
		cleanupUpdateDirectory(config, filepath.Join(config, entry.Name()))
	}
}

func cleanupUpdateDirectory(config, dir string) {
	entries, err := updateCacheEntries(dir)
	if err != nil {
		return
	}
	marker := filepath.Join(dir, updateLeaseName)
	data, markerErr := readUpdateCacheFile(marker, 128)
	managed := markerErr == nil && string(data) == updateCacheMarker
	if markerErr != nil && !os.IsNotExist(markerErr) || markerErr == nil && !managed {
		return
	}
	var lease *os.File
	if managed {
		lease, err = os.OpenFile(marker, os.O_RDWR, 0)
		if err != nil {
			return
		}
		defer lease.Close()
		if lockUpdateFile(lease, true) != nil {
			return
		}
	}
	// Re-read after acquiring the lease: the downloader may have finished while
	// the initial directory/marker snapshot was being inspected.
	entries, err = updateCacheEntries(dir)
	if err != nil {
		return
	}
	p, planErr := readCleanupUpdatePlan(config, dir)
	if !managed && (planErr != nil || !legacyUpdateInactive(p, dir)) {
		return
	}
	// A malformed plan might describe a recovery we cannot safely archive.
	if planErr != nil && !os.IsNotExist(planErr) {
		return
	}
	if err = archiveUpdateRecovery(config, dir, p); err != nil {
		return
	}
	// Keep identity files until other removals succeed, allowing retry after a
	// sharing violation. Never recursively delete a directory of unknown files.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Name() != "plan.json" && entries[j].Name() == "plan.json" })
	for _, entry := range entries {
		if entry.Name() == updateLeaseName {
			continue
		}
		if err = os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return
		}
	}
	if lease != nil {
		lease.Close()
		if os.Remove(marker) != nil {
			return
		}
	}
	_ = os.Remove(dir)
}

func updateRecoveryDirectory(config string) (string, error) {
	dir := filepath.Join(config, "update-backup")
	err := os.Mkdir(dir, 0700)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, ".dengshell-owned"), []byte(updateCacheMarker), 0600)
	} else if os.IsExist(err) {
		info, e := os.Lstat(dir)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("备份目录无效")
		}
		var b []byte
		b, err = readUpdateCacheFile(filepath.Join(dir, ".dengshell-owned"), 128)
		if err == nil && string(b) != updateCacheMarker {
			err = errors.New("未知备份目录")
		}
	}
	return dir, err
}

type updateRecoveryInfo struct {
	Build    uint64    `json:"build"`
	SHA256   string    `json:"sha256"`
	Modified time.Time `json:"modified"`
}

func archiveUpdateRecovery(config, dir string, p cleanupUpdatePlan) error {
	name := "previous-program" + filepath.Ext(p.Target)
	if name != "previous-program.exe" {
		name = "previous-program"
	}
	backup := filepath.Join(dir, name)
	if info, err := os.Lstat(backup); err == nil {
		if p.Build == 0 || !info.Mode().IsRegular() {
			return errors.New("无法确认回退备份")
		}
		dest, err := updateRecoveryDirectory(config)
		if err != nil {
			return err
		}
		var prior updateRecoveryInfo
		b, _ := readUpdateCacheFile(filepath.Join(dest, "backup.json"), 4096)
		_ = json.Unmarshal(b, &prior)
		if p.Build > prior.Build || p.Build == prior.Build && info.ModTime().After(prior.Modified) {
			hash, err := FileSHA256(backup)
			if err != nil {
				return err
			}
			if hash != p.OldSHA256 {
				return errors.New("回退备份校验失败")
			}
			if err = copyUpdateRecoveryFile(backup, filepath.Join(dest, name)); err != nil {
				return err
			}
			b, _ = json.Marshal(updateRecoveryInfo{p.Build, hash, info.ModTime()})
			if err = atomicConfigFile(filepath.Join(dest, "backup.json"), b); err != nil {
				return err
			}
		} else {
			hash, err := FileSHA256(filepath.Join(dest, name))
			if err != nil {
				return err
			}
			if hash != prior.SHA256 {
				return errors.New("已保存的回退备份校验失败")
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if info, err := os.Lstat(filepath.Join(dir, "error.txt")); err == nil {
		dest, err := updateRecoveryDirectory(config)
		if err != nil {
			return err
		}
		var prior struct {
			Modified time.Time `json:"modified"`
		}
		b, _ := readUpdateCacheFile(filepath.Join(dest, "last-error.json"), 2<<20)
		_ = json.Unmarshal(b, &prior)
		if info.ModTime().After(prior.Modified) {
			read := func(name string) string {
				f, e := os.Open(filepath.Join(dir, name))
				if e != nil {
					return ""
				}
				defer f.Close()
				b, _ := io.ReadAll(io.LimitReader(f, 64<<10))
				return string(b)
			}
			b, _ = json.Marshal(struct {
				Modified time.Time `json:"modified"`
				Error    string    `json:"error"`
				Log      string    `json:"log"`
			}{info.ModTime(), read("error.txt"), read("installer.log")})
			if err = atomicConfigFile(filepath.Join(dest, "last-error.json"), b); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func copyUpdateRecoveryFile(source, dest string) error {
	if info, err := os.Lstat(dest); err == nil && !info.Mode().IsRegular() {
		return errors.New("备份目标不是普通文件")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dest), ".backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = replaceConfigFile(out.Name(), dest)
	}
	return err
}
