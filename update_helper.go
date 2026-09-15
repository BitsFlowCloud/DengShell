package main

import (
	"cloudshell/internal/app"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Cold process startup and full package verification share this bounded budget.
// Four seconds was shorter than valid preparation on some Windows machines.
const updateHelperReadyTimeout = 60 * time.Second

type updateHelperExitPendingError struct{ error }

func updateStageMessage(stage string) string {
	switch stage {
	case "plan":
		return "正在读取更新计划…"
	case "hash":
		return "正在校验更新文件…"
	case "format":
		return "正在检查安装文件…"
	case "ready":
		return "准备完成，正在关闭旧程序…"
	case "install":
		return "正在安装更新…"
	case "restart":
		return "正在启动新版本…"
	}
	return "正在启动更新助手…"
}

func recordUpdateStage(file, stage string) {
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	fmt.Fprintf(os.Stdout, "%s [%s] %s\n", stamp, stage, updateStageMessage(stage))
	data, _ := json.Marshal(struct {
		Stage string `json:"stage"`
		Time  string `json:"time"`
		PID   int    `json:"pid"`
	}{stage, stamp, os.Getpid()})
	// A reader ignores a partial snapshot and retries. The ready marker remains
	// the authoritative handshake, including for older update helpers.
	_ = os.WriteFile(filepath.Join(filepath.Dir(file), "helper-status.json"), data, 0600)
}

type updatePlan struct {
	PackageFormat    string `json:"packageFormat,omitempty"`
	Schema           int    `json:"schema"`
	Build            uint64 `json:"build"`
	Version          string `json:"version"`
	ParentPID        int    `json:"parentPID"`
	Target           string `json:"target"`
	Staged           string `json:"staged"`
	ConfigDir        string `json:"configDir"`
	OldSHA256        string `json:"oldSHA256"`
	PackageSHA256    string `json:"packageSHA256"`
	ExecutableSHA256 string `json:"executableSHA256"`
	Platform         string `json:"platform"`
}

func handleUpdateHelper() bool {
	if len(os.Args) != 3 || os.Args[1] != "--dengshell-update-helper" {
		return false
	}
	planFile := os.Args[2]
	err := runUpdateHelper(planFile)
	if err != nil {
		_ = os.WriteFile(filepath.Join(filepath.Dir(planFile), "error.txt"), []byte(err.Error()), 0600)
		fmt.Fprintln(os.Stderr, "DengShell update:", err)
		os.Exit(1)
	}
	return true
}
func readUpdatePlan(file string) (updatePlan, error) {
	recordUpdateStage(file, "plan")
	var p updatePlan
	b, e := os.ReadFile(file)
	if e != nil {
		return p, e
	}
	if len(b) > 32768 || json.Unmarshal(b, &p) != nil {
		return p, errors.New("更新计划无效")
	}
	if p.Schema != 2 || p.ParentPID <= 0 || p.ParentPID == os.Getpid() || p.Platform != runtime.GOOS+"-"+runtime.GOARCH || !filepath.IsAbs(p.Target) || !filepath.IsAbs(p.Staged) || !filepath.IsAbs(p.ConfigDir) || filepath.Dir(p.Staged) != filepath.Dir(file) || len(p.OldSHA256) != 64 || len(p.PackageSHA256) != 64 || len(p.ExecutableSHA256) != 64 {
		return p, errors.New("更新计划内容无效")
	}
	if err := app.ValidateUpdateBuild(p.ConfigDir, p.Build, p.Version); err != nil {
		return p, err
	}
	recordUpdateStage(file, "hash")
	hash, e := app.FileSHA256(p.Staged)
	if e != nil || hash != p.PackageSHA256 {
		return p, errors.New("下载文件校验失败")
	}
	return p, nil
}
func runUpdateHelper(file string) error {
	plan, e := readUpdatePlan(file)
	if e != nil {
		return e
	}
	recordUpdateStage(file, "format")
	if e = validatePlatformUpdate(plan); e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(filepath.Dir(file), "ready"), []byte("ready"), 0600); e != nil {
		return e
	}
	recordUpdateStage(file, "ready")
	if e = waitForUpdateParent(plan.ParentPID, 120*time.Second); e != nil {
		return e
	}
	// Verify again after the parent has exited, immediately before changing files.
	if hash, err := app.FileSHA256(plan.Staged); err != nil || hash != plan.PackageSHA256 {
		return errors.New("安装前的更新文件校验失败")
	}
	if e = app.ValidateUpdateBuild(plan.ConfigDir, plan.Build, plan.Version); e != nil {
		return e
	}
	recordUpdateStage(file, "install")
	target, e := applyPlatformUpdate(plan)
	if e != nil {
		restartPreviousUpdate(plan)
		return e
	}
	hash, e := app.FileSHA256(target)
	if e != nil || hash != plan.ExecutableSHA256 {
		_ = rollbackPlatformUpdate(plan)
		restartPreviousUpdate(plan)
		return errors.New("安装后的程序 SHA-256 不匹配")
	}
	recordUpdateStage(file, "restart")
	cmd := exec.Command(target, "--config", plan.ConfigDir)
	cmd.Dir = filepath.Dir(target)
	prepareRestartedApplication(cmd)
	if e = startUpdateWithReceipt(plan, cmd.Start); e != nil {
		_ = rollbackPlatformUpdate(plan)
		restartPreviousUpdate(plan)
		return fmt.Errorf("新程序未能启动，已尝试恢复原程序: %w", e)
	}
	_ = os.WriteFile(filepath.Join(plan.ConfigDir, "update-result.json"), []byte(`{"status":"installed"}`), 0600)
	return nil
}

// A durable receipt preserves the installed build high-water mark. Receipt
// failure aborts the install and lets the caller restore the previous binary.
// Write before Start so the new process's probe sees the receipt; undo it if
// launching fails so a rolled-back program cannot retain a false success.
func startUpdateWithReceipt(plan updatePlan, start func() error) error {
	receipt, _ := json.Marshal(app.UpdateReceipt{Build: plan.Build, Version: plan.Version, SHA256: plan.PackageSHA256, ExecutableSHA256: plan.ExecutableSHA256, InstalledAt: time.Now().UTC().Format(time.RFC3339)})
	file := filepath.Join(plan.ConfigDir, "update-receipt.json")
	previous, previousErr := os.ReadFile(file)
	if previousErr != nil && !os.IsNotExist(previousErr) {
		return fmt.Errorf("无法读取原更新收据：%w", previousErr)
	}
	if err := app.WriteUpdateReceipt(plan.ConfigDir, receipt); err != nil {
		return fmt.Errorf("更新收据无法保存，已停止启动新版本：%w", err)
	}
	if err := start(); err != nil {
		if previousErr == nil {
			if restoreErr := app.WriteUpdateReceipt(plan.ConfigDir, previous); restoreErr != nil {
				return fmt.Errorf("%w；原更新收据恢复失败：%v", err, restoreErr)
			}
		} else if removeErr := os.Remove(file); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("%w；失败更新收据清理失败：%v", err, removeErr)
		}
		return err
	}
	return nil
}
func copyUpdateFile(source, target string, mode os.FileMode) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if e != nil {
		return e
	}
	if _, e = io.Copy(out, in); e == nil {
		e = out.Sync()
	}
	closeErr := out.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		_ = os.Remove(target)
	}
	return e
}
func launchUpdateHelper(job app.UpdateDownload, configDir string, progress func(string)) error {
	target, e := os.Executable()
	if e != nil {
		return e
	}
	target, e = filepath.EvalSymlinks(target)
	if e != nil {
		return e
	}
	oldHash, e := app.FileSHA256(target)
	if e != nil {
		return e
	}
	configDir, e = filepath.Abs(configDir)
	if e != nil {
		return e
	}
	plan := updatePlan{PackageFormat: job.Package.Format, Schema: 2, Build: job.Package.Build, Version: job.Package.Version, ParentPID: os.Getpid(), Target: target, Staged: job.File, ConfigDir: configDir, OldSHA256: oldHash, PackageSHA256: job.Package.SHA256, ExecutableSHA256: job.Package.ExecutableSHA256, Platform: runtime.GOOS + "-" + runtime.GOARCH}
	if e = validatePlatformUpdate(plan); e != nil {
		return e
	}
	if e = app.ValidateUpdateBuild(configDir, plan.Build, plan.Version); e != nil {
		return e
	}
	dir := filepath.Dir(job.File)
	helper := filepath.Join(dir, fmt.Sprintf("dengshell-updater-%d", time.Now().UnixNano()))
	if runtime.GOOS == "windows" {
		helper += ".exe"
	}
	if e = copyUpdateFile(target, helper, 0700); e != nil {
		return e
	}
	for _, name := range []string{"ready", "error.txt", "helper-status.json"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("无法清理上次更新状态：%w", err)
		}
	}
	data, e := json.Marshal(plan)
	if e != nil {
		return e
	}
	planFile := filepath.Join(dir, "plan.json")
	if e = os.WriteFile(planFile, data, 0600); e != nil {
		return e
	}
	cmd := exec.Command(helper, "--dengshell-update-helper", planFile)
	prepareUpdaterProcess(cmd)
	log, e := os.OpenFile(filepath.Join(dir, "installer.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	fmt.Fprintf(log, "%s [launch] 启动更新助手，准备期限 %s\n", time.Now().UTC().Format(time.RFC3339Nano), updateHelperReadyTimeout)
	if e = cmd.Start(); e != nil {
		fmt.Fprintf(log, "[launch-error] %v\n", e)
		return e
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	err := waitUpdateHelperReady(dir, done, updateHelperReadyTimeout, cmd.Process.Kill, progress)
	if err != nil {
		fmt.Fprintf(log, "%s [preparation-error] %v\n", time.Now().UTC().Format(time.RFC3339Nano), err)
	}
	return err
}

func readUpdateHelperError(dir string, fallback error) error {
	f, err := os.Open(filepath.Join(dir, "error.txt"))
	if err == nil {
		defer f.Close()
		if b, err := io.ReadAll(io.LimitReader(f, 4096)); err == nil && strings.TrimSpace(string(b)) != "" {
			return errors.New(string(b))
		}
	}
	return fallback
}

// Used by the actual desktop launch path and by process-level regression tests.
func waitUpdateHelperReady(dir string, done <-chan error, timeout time.Duration, terminate func() error, progress func(string)) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	stage := "startup"
	report := func() {
		if progress != nil {
			progress(updateStageMessage(stage))
		}
	}
	report()
	check := func() (bool, error) {
		if f, err := os.Open(filepath.Join(dir, "helper-status.json")); err == nil {
			var status struct {
				Stage string `json:"stage"`
			}
			if json.NewDecoder(io.LimitReader(f, 4096)).Decode(&status) == nil && status.Stage != "" && status.Stage != stage {
				stage = status.Stage
				report()
			}
			f.Close()
		}
		_, err := os.Stat(filepath.Join(dir, "ready"))
		if os.IsNotExist(err) {
			return false, nil
		}
		return err == nil, err
	}
	stop := func(reason error) error {
		if err := terminate(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return &updateHelperExitPendingError{fmt.Errorf("%w；结束更新助手失败：%v", reason, err)}
		}
		// Reap it before allowing a retry to reuse the plan and handshake files.
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			return &updateHelperExitPendingError{fmt.Errorf("%w；更新助手仍在退出，请重新启动程序后重试", reason)}
		}
		return reason
	}
	readyResult := func() error {
		select {
		case err := <-done:
			return readUpdateHelperError(dir, fmt.Errorf("更新助手提前退出: %v", err))
		default:
			return nil
		}
	}
	for {
		select {
		case e := <-done:
			return readUpdateHelperError(dir, fmt.Errorf("更新助手提前退出: %v", e))
		case <-ticker.C:
			ready, err := check()
			if err != nil {
				return stop(fmt.Errorf("无法读取更新助手状态：%w", err))
			}
			if ready {
				return readyResult()
			}
		case <-deadline.C:
			// A ready marker may have arrived between the last poll and deadline.
			ready, err := check()
			if err != nil {
				return stop(fmt.Errorf("无法读取更新助手状态：%w", err))
			}
			if ready {
				return readyResult()
			}
			err = readUpdateHelperError(dir, fmt.Errorf("更新助手准备超时（%s，%s），程序保持运行；详情见更新目录中的 installer.log", timeout, strings.TrimSuffix(updateStageMessage(stage), "…")))
			return stop(err)
		}
	}
}

func restartPreviousUpdate(plan updatePlan) {
	hash, err := app.FileSHA256(plan.Target)
	if err != nil || hash != plan.OldSHA256 {
		return
	}
	cmd := exec.Command(plan.Target, "--config", plan.ConfigDir)
	cmd.Dir = filepath.Dir(plan.Target)
	prepareRestartedApplication(cmd)
	_ = cmd.Start()
}

// The replacement stays in the target directory so each rename is on one
// filesystem. The prior executable remains available until a later update.
func updateBackupPath(p updatePlan) string {
	return filepath.Join(filepath.Dir(p.Staged), "previous-program"+filepath.Ext(p.Target))
}
func replaceUpdateBinary(p updatePlan) (string, error) {
	hash, e := app.FileSHA256(p.Target)
	if e != nil || hash != p.OldSHA256 {
		return "", errors.New("原程序已变化，请重新检查更新")
	}
	backup := updateBackupPath(p)
	if existing, err := app.FileSHA256(backup); err != nil || existing != p.OldSHA256 {
		_ = os.Remove(backup)
		if e = copyUpdateFile(p.Target, backup, 0755); e != nil {
			return "", e
		}
	}
	if hash, e = app.FileSHA256(backup); e != nil || hash != p.OldSHA256 {
		return "", errors.New("原程序备份校验失败")
	}
	next := p.Target + ".update-new"
	localOld := p.Target + ".update-old"
	_ = os.Remove(next)
	if e = copyUpdateFile(p.Staged, next, 0755); e != nil {
		return "", e
	}
	hash, e = app.FileSHA256(next)
	if e != nil || hash != p.ExecutableSHA256 {
		_ = os.Remove(next)
		return "", errors.New("待替换程序校验失败")
	}
	_ = os.Remove(localOld)
	if e = os.Rename(p.Target, localOld); e != nil {
		_ = os.Remove(next)
		return "", e
	}
	if e = os.Rename(next, p.Target); e != nil {
		_ = os.Rename(localOld, p.Target)
		return "", e
	}
	_ = os.Remove(localOld)
	return p.Target, nil
}
func restoreUpdateBinary(p updatePlan) error {
	backup := updateBackupPath(p)
	hash, e := app.FileSHA256(backup)
	if e != nil || hash != p.OldSHA256 {
		return errors.New("原程序备份不可用")
	}
	restore := p.Target + ".restore"
	_ = os.Remove(restore)
	if e = copyUpdateFile(backup, restore, 0755); e != nil {
		return e
	}
	failed := p.Target + ".failed"
	_ = os.Remove(failed)
	if e = os.Rename(p.Target, failed); e != nil && !os.IsNotExist(e) {
		return e
	}
	if e = os.Rename(restore, p.Target); e != nil {
		_ = os.Rename(failed, p.Target)
		return e
	}
	_ = os.Remove(failed)
	return nil
}
