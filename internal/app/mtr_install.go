package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MissingDiagnosticTool struct {
	Name        string `json:"name"`
	Direction   string `json:"direction"`
	Installable bool   `json:"installable"`
}
type MissingToolError struct{ Direction string }

func (e *MissingToolError) Error() string {
	if e.Direction == "remote" {
		return "远程服务器未安装 mtr，可以查看安装方案并确认后安装。"
	}
	return "本机未安装 mtr，可以查看安装方案并确认后安装。"
}

type MTRInstallPlan struct {
	ID               string    `json:"id"`
	SessionID        string    `json:"sessionId"`
	Direction        string    `json:"direction"`
	Distro           string    `json:"distro"`
	PackageManager   string    `json:"packageManager"`
	PackageName      string    `json:"packageName"`
	Command          string    `json:"command"`
	Permission       string    `json:"permission"`
	CanAutoInstall   bool      `json:"canAutoInstall"`
	ManualReason     string    `json:"manualReason,omitempty"`
	AlreadyInstalled bool      `json:"alreadyInstalled"`
	CreatedAt        time.Time `json:"createdAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
	session          *Session
	scope            string
	baseCommand      string
	jobID            string
}
type MTRInstallationReport struct {
	ID           string         `json:"id"`
	Status       string         `json:"status"`
	Output       string         `json:"output"`
	Error        string         `json:"error,omitempty"`
	Plan         MTRInstallPlan `json:"plan"`
	LogPath      string         `json:"logPath,omitempty"`
	CheckCommand string         `json:"checkCommand,omitempty"`
	StartedAt    time.Time      `json:"startedAt"`
	FinishedAt   *time.Time     `json:"finishedAt,omitempty"`
}
type MTRInstallation struct {
	mu     sync.Mutex
	report MTRInstallationReport
	done   chan struct{}
}

func (j *MTRInstallation) snapshot() MTRInstallationReport {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.report
}
func (j *MTRInstallation) finish(status string, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.report.Status = status
	j.report.FinishedAt = &now
	if err != nil {
		j.report.Error = err.Error()
	}
}
func (a *App) registerMTRInstallationHTTP(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/{id}/mtr-install-plan", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Direction string `json:"direction"`
		}
		if !decode(w, r, &input) {
			return
		}
		plan, err := a.planMTRInstallation(r.Context(), r.PathValue("id"), input.Direction)
		respond(w, plan, err)
	})
	mux.HandleFunc("POST /api/mtr-installations", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			PlanID   string `json:"planId"`
			Approved bool   `json:"approved"`
		}
		if !decode(w, r, &input) {
			return
		}
		job, err := a.startMTRInstallation(input.PlanID, input.Approved)
		if err != nil {
			respond(w, nil, err)
			return
		}
		writeJSON(w, job.snapshot())
	})
	mux.HandleFunc("GET /api/mtr-installations/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mtrMu.Lock()
		job := a.mtrInstallations[r.PathValue("id")]
		a.mtrMu.Unlock()
		if job == nil {
			writeError(w, 404, errors.New("安装任务不存在"))
			return
		}
		writeJSON(w, job.snapshot())
	})
}

// This read-only probe never installs anything, prompts for a password, changes
// repositories, or reads account credentials. Package commands come from the
// fixed allowlist below, never from this command's output or the HTTP client.
const mtrEnvironmentCommand = `LC_ALL=C; export LC_ALL
printf 'os\t%s\n' "$(uname -s)"
if [ -r /etc/os-release ]; then
 . /etc/os-release
 printf 'id\t%s\nlike\t%s\nversion\t%s\n' "$ID" "$ID_LIKE" "$VERSION_ID"
fi
printf 'uid\t%s\n' "$(id -u)"
if command -v mtr >/dev/null 2>&1; then printf 'installed\t1\n'; fi
for tool in apt-get dnf yum pacman zypper apk xbps-install emerge; do
 if command -v "$tool" >/dev/null 2>&1; then printf 'manager\t%s\n' "$tool"; fi
done
if command -v pkexec >/dev/null 2>&1; then printf 'pkexec\t1\n'; fi
if [ -n "$DISPLAY" ] || [ -n "$WAYLAND_DISPLAY" ]; then printf 'gui\t1\n'; fi
if command -v sudo >/dev/null 2>&1; then
 printf 'sudo\t1\n'
 if sudo -n true </dev/null >/dev/null 2>&1; then printf 'sudo-nopass\t1\n'; fi
fi
if command -v nohup >/dev/null 2>&1 && command -v mktemp >/dev/null 2>&1 && command -v sh >/dev/null 2>&1; then printf 'detach\t1\n'; fi
`

type mtrEnvironment struct {
	os, id, like, version, uid                   string
	installed, sudo, nopass, detach, pkexec, gui bool
	managers                                     map[string]bool
}

func parseMTREnvironment(output string) mtrEnvironment {
	env := mtrEnvironment{managers: map[string]bool{}}
	for _, line := range strings.Split(output, "\n") {
		pair := strings.SplitN(line, "\t", 2)
		if len(pair) != 2 {
			continue
		}
		switch pair[0] {
		case "os":
			env.os = pair[1]
		case "id":
			env.id = pair[1]
		case "like":
			env.like = pair[1]
		case "version":
			env.version = pair[1]
		case "uid":
			env.uid = pair[1]
		case "installed":
			env.installed = pair[1] == "1"
		case "sudo":
			env.sudo = pair[1] == "1"
		case "sudo-nopass":
			env.nopass = pair[1] == "1"
		case "pkexec":
			env.pkexec = pair[1] == "1"
		case "gui":
			env.gui = pair[1] == "1"
		case "detach":
			env.detach = pair[1] == "1"
		case "manager":
			env.managers[pair[1]] = true
		}
	}
	return env
}
func mtrPlanForEnvironment(env mtrEnvironment) MTRInstallPlan {
	plan := MTRInstallPlan{Distro: strings.TrimSpace(env.id + " " + env.version), Permission: "unavailable", AlreadyInstalled: env.installed}
	if plan.Distro == "" {
		plan.Distro = env.os
	}
	if env.os != "Linux" {
		if env.os == "Darwin" {
			plan.Distro = "macOS"
			plan.PackageManager, plan.PackageName = "brew", "mtr"
			plan.Command = "brew install mtr"
			plan.ManualReason = "请在 macOS 终端中通过 Homebrew 安装 mtr，并按安装提示配置 mtr-packet 权限；DengShell 不会以 root 身份运行。"
			return plan
		}
		plan.ManualReason = "当前系统未提供自动安装方案，请使用该系统的包管理器安装 mtr。"
		return plan
	}
	managers := []string{"apt-get", "dnf", "yum", "pacman", "zypper", "apk", "xbps-install", "emerge"}
	// Prefer the distribution's own manager when more than one executable exists.
	for _, family := range strings.Fields(env.id + " " + env.like) {
		preferred := ""
		switch family {
		case "debian", "ubuntu", "linuxmint":
			preferred = "apt-get"
		case "fedora", "rhel", "centos", "rocky", "almalinux":
			preferred = "dnf"
		case "arch", "manjaro":
			preferred = "pacman"
		case "opensuse", "opensuse-leap", "opensuse-tumbleweed", "suse", "sles":
			preferred = "zypper"
		case "alpine":
			preferred = "apk"
		case "void":
			preferred = "xbps-install"
		case "gentoo":
			preferred = "emerge"
		}
		if env.managers[preferred] {
			managers = append([]string{preferred}, managers...)
			break
		}
	}
	for _, manager := range managers {
		if env.managers[manager] {
			plan.PackageManager = manager
			break
		}
	}
	base := ""
	plan.PackageName = "mtr"
	switch plan.PackageManager {
	case "apt-get":
		plan.PackageName = "mtr-tiny"
		base = "apt-get update && DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=60 install -y --no-install-recommends mtr-tiny"
	case "dnf":
		base = "dnf install -y mtr"
	case "yum":
		base = "yum install -y mtr"
	case "pacman":
		base = "pacman -S --needed --noconfirm mtr"
	case "zypper":
		base = "zypper --non-interactive install mtr"
	case "apk":
		base = "apk add mtr"
	case "xbps-install":
		base = "xbps-install -Sy mtr"
	case "emerge":
		plan.PackageName = "net-analyzer/mtr"
		base = "emerge --ask=n net-analyzer/mtr"
	default:
		plan.PackageName = ""
		plan.ManualReason = "未识别可用的 Linux 包管理器。请通过此发行版的官方软件源手动安装 mtr，再重试诊断。"
		return plan
	}
	plan.baseCommand = base
	switch {
	case env.uid == "0":
		plan.Permission = "root"
		plan.Command = base
		plan.CanAutoInstall = true
	case env.nopass:
		plan.Permission = "sudo-nopass"
		plan.Command = "sudo -n sh -c " + quoteMTRShell(base)
		plan.CanAutoInstall = true
	case env.sudo:
		plan.Permission = "interactive"
		plan.Command = "sudo sh -c " + quoteMTRShell(base)
		plan.ManualReason = "需要 sudo 密码。请在交互终端中运行下方命令，完成后重试；应用不会收集密码。"
	default:
		plan.Command = base
		plan.ManualReason = "当前账户没有 root 或 sudo 权限。请由管理员使用有权限的账户运行下方命令。"
	}
	if !env.detach && plan.CanAutoInstall {
		plan.CanAutoInstall = false
		plan.ManualReason = "系统缺少 sh、nohup 或 mktemp，无法可靠地在后台安装。请在终端中运行下方命令。"
	}
	return plan
}
func (a *App) planMTRInstallation(requestCtx context.Context, sessionID, direction string) (MTRInstallPlan, error) {
	s, err := a.session(sessionID)
	if err != nil {
		return MTRInstallPlan{}, err
	}
	if direction != "local" && direction != "remote" {
		return MTRInstallPlan{}, errors.New("请选择本机或服务器安装")
	}
	var plan MTRInstallPlan
	if direction == "local" && runtime.GOOS == "windows" {
		plan = MTRInstallPlan{Distro: "Windows", Permission: "native", AlreadyInstalled: true, ManualReason: "Windows 使用内置原生 ICMP 路径探测，无需安装 mtr。"}
	} else {
		ctx, cancel := context.WithTimeout(requestCtx, 8*time.Second)
		defer cancel()
		var remote *Session
		if direction == "remote" {
			remote = s
		}
		output, err := runMTRScript(ctx, remote, mtrEnvironmentCommand, 64<<10)
		if err != nil {
			return MTRInstallPlan{}, fmt.Errorf("检查安装环境：%w", err)
		}
		env := parseMTREnvironment(output)
		plan = mtrPlanForEnvironment(env)
		if direction == "local" && env.pkexec && env.gui && env.detach && plan.baseCommand != "" && !plan.CanAutoInstall {
			plan.Permission = "polkit"
			plan.Command = "pkexec --disable-internal-agent sh -c " + quoteMTRShell(plan.baseCommand)
			plan.CanAutoInstall = true
			plan.ManualReason = "确认后由系统授权窗口请求管理员权限；应用不会读取密码。关闭应用不会中断已启动的包安装。"
		}
	}
	now := time.Now()
	plan.ID = randomID()
	plan.SessionID = sessionID
	plan.Direction = direction
	plan.CreatedAt = now
	plan.ExpiresAt = now.Add(10 * time.Minute)
	plan.scope = "local"
	if direction == "remote" {
		profile, err := a.connectionProfile(s.ProfileID)
		if err != nil {
			return MTRInstallPlan{}, err
		}
		plan.scope = fmt.Sprintf("remote:%s:%d", strings.ToLower(profile.Host), profile.Port)
		plan.session = s
	}
	a.mtrMu.Lock()
	defer a.mtrMu.Unlock()
	if a.mtrPlans == nil {
		a.mtrPlans = map[string]*MTRInstallPlan{}
	}
	for id, stored := range a.mtrPlans {
		if now.After(stored.ExpiresAt) && stored.jobID == "" {
			delete(a.mtrPlans, id)
		}
	}
	if len(a.mtrPlans) >= 128 {
		return MTRInstallPlan{}, errors.New("安装方案过多，请稍后重试")
	}
	a.mtrPlans[plan.ID] = &plan
	return plan, nil
}
func (a *App) startMTRInstallation(planID string, approved bool) (*MTRInstallation, error) {
	if !approved {
		return nil, errors.New("安装前必须明确确认方案")
	}
	a.mtrMu.Lock()
	defer a.mtrMu.Unlock()
	plan := a.mtrPlans[planID]
	if plan == nil {
		return nil, errors.New("安装方案不存在，请重新检查环境")
	}
	if plan.jobID != "" {
		return a.mtrInstallations[plan.jobID], nil
	}
	if time.Now().After(plan.ExpiresAt) {
		return nil, errors.New("安装方案已过期，请重新检查环境")
	}
	if plan.AlreadyInstalled {
		return nil, errors.New("mtr 已可用，请直接重试诊断")
	}
	if !plan.CanAutoInstall {
		return nil, errors.New("此方案需要在交互终端中手动运行")
	}
	if plan.Direction == "remote" && (plan.session == nil || plan.session.ctx.Err() != nil) {
		return nil, errors.New("服务器连接已断开，请重新连接并检查安装环境")
	}
	if a.ctx.Err() != nil {
		return nil, errors.New("应用正在退出")
	}
	if a.mtrInstallations == nil {
		a.mtrInstallations = map[string]*MTRInstallation{}
	}
	for _, existing := range a.mtrInstallations {
		report := existing.snapshot()
		if report.Plan.scope == plan.scope && (report.Status == "running" || report.Status == "unmonitored") {
			plan.jobID = report.ID
			return existing, nil
		}
	}
	if len(a.mtrInstallations) >= 24 {
		return nil, errors.New("本次运行已创建 24 个安装任务，请重新启动应用后重试")
	}
	job := &MTRInstallation{report: MTRInstallationReport{ID: randomID(), Status: "running", Plan: *plan, StartedAt: time.Now()}, done: make(chan struct{})}
	plan.jobID = job.report.ID
	a.mtrInstallations[job.report.ID] = job
	go a.runMTRInstallation(job)
	return job, nil
}

func quoteMTRShell(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// A detached wrapper owns the status/log files as the connecting account. Only
// its fixed package command uses sudo; root-owned 0600 logs would be unreadable.
// Once launched, package transactions deliberately outlive UI/SSH cancellation.
func mtrDetachedCommand(command, template string) string {
	wrapper := `dir=$1
( ` + command + ` )
code=$?
if [ "$code" -eq 0 ] && ! command -v mtr >/dev/null 2>&1; then
 printf '%s\n' '包管理器已完成，但当前 PATH 中仍找不到 mtr。'; code=126
fi
printf '%s\n' "$code" > "$dir/status.next"
mv "$dir/status.next" "$dir/status"
`
	return `umask 077
dir=$(mktemp -d ` + quoteMTRShell(template) + `) || exit 1
printf 'running\n' > "$dir/status" || exit 1
nohup sh -c ` + quoteMTRShell(wrapper) + ` sh "$dir" > "$dir/output.log" 2>&1 </dev/null &
printf '__DENGSHELL_MTR_DIR__%s\n' "$dir"
`
}
func parseMTRLogDirectory(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "__DENGSHELL_MTR_DIR__") {
			dir := strings.TrimPrefix(line, "__DENGSHELL_MTR_DIR__")
			if strings.HasPrefix(dir, "/") && !strings.ContainsAny(dir, "\x00\r\n") && strings.Contains(filepath.Base(dir), "dengshell-mtr.") {
				return dir, nil
			}
		}
	}
	return "", errors.New("未收到后台安装日志路径；安装可能已启动，请勿重复安装")
}
func (a *App) runMTRInstallation(job *MTRInstallation) {
	defer close(job.done)
	plan := job.report.Plan
	template := "/tmp/dengshell-mtr.XXXXXXXX"
	if plan.Direction == "local" {
		dir := filepath.Join(a.store.dir, "mtr-installations")
		if err := os.MkdirAll(dir, 0700); err != nil {
			job.finish("failed", err)
			return
		}
		absolute, err := filepath.Abs(dir)
		if err != nil {
			job.finish("failed", err)
			return
		}
		template = filepath.Join(absolute, "dengshell-mtr.XXXXXXXX")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	output, err := runMTRScript(ctx, plan.session, mtrDetachedCommand(plan.Command, template), 64<<10)
	cancel()
	if err != nil {
		job.finish("unmonitored", fmt.Errorf("无法确认后台安装是否启动，请先在终端检查包管理器进程，避免重复安装：%w", err))
		return
	}
	dir, err := parseMTRLogDirectory(output)
	if err != nil {
		job.finish("unmonitored", err)
		return
	}
	job.mu.Lock()
	job.report.LogPath = dir + "/output.log"
	job.report.CheckCommand = "cat " + quoteMTRShell(dir+"/status") + "; tail -n 80 " + quoteMTRShell(dir+"/output.log")
	job.mu.Unlock()
	ctx, cancel = context.WithTimeout(a.ctx, 10*time.Minute)
	defer cancel()
	for {
		pollCtx, pollCancel := context.WithTimeout(ctx, 5*time.Second)
		status, output, err := readMTRInstallation(pollCtx, plan.session, dir)
		pollCancel()
		if err != nil {
			job.finish("unmonitored", fmt.Errorf("已停止读取安装进度，后台安装可能仍在继续。请在终端检查日志，不要重复安装：%w", err))
			return
		}
		job.mu.Lock()
		job.report.Output = output
		job.mu.Unlock()
		if status != "running" {
			code, err := strconv.Atoi(strings.TrimSpace(status))
			if err != nil {
				job.finish("unmonitored", errors.New("安装状态文件无效，请在终端检查日志"))
				return
			}
			if code == 0 {
				job.finish("done", nil)
			} else {
				job.finish("failed", fmt.Errorf("包管理器退出代码 %d，请查看输出；应用未修改软件源", code))
			}
			return
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			job.finish("unmonitored", errors.New("进度监视已结束，后台包安装不会被强制终止。请在终端检查日志后重试诊断"))
			return
		case <-timer.C:
		}
	}
}
func readMTRInstallation(ctx context.Context, s *Session, dir string) (string, string, error) {
	output, err := runMTRScript(ctx, s, "printf '__DENGSHELL_STATUS__'; cat "+quoteMTRShell(dir+"/status")+" || exit 1; printf '__DENGSHELL_OUTPUT__\\n'; tail -c 524288 "+quoteMTRShell(dir+"/output.log"), diagnosticOutputLimit+4096)
	if err != nil {
		return "", "", err
	}
	marker := strings.Index(output, "__DENGSHELL_STATUS__")
	if marker < 0 {
		return "", "", errors.New("缺少安装状态")
	}
	output = output[marker+len("__DENGSHELL_STATUS__"):]
	parts := strings.SplitN(output, "\n__DENGSHELL_OUTPUT__\n", 2)
	if len(parts) != 2 {
		return "", "", errors.New("安装状态输出不完整")
	}
	return strings.TrimSpace(parts[0]), strings.ToValidUTF8(parts[1], "�"), nil
}

// stdout/stderr may be written concurrently by SSH; keep only a bounded prefix.
type mtrLimitedOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	limit  int
}

func (w *mtrLimitedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if left := w.limit - w.buffer.Len(); left > 0 {
		w.buffer.Write(p[:min(left, n)])
	}
	return n, nil
}
func (w *mtrLimitedOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}
func runMTRScript(ctx context.Context, s *Session, command string, limit int) (string, error) {
	out := &mtrLimitedOutput{limit: limit}
	if s == nil {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Stdout = out
		cmd.Stderr = out
		cmd.WaitDelay = time.Second
		err := cmd.Run()
		return out.String(), err
	}
	data, err := s.commandCollector.run(ctx, s, "script", command, limit)
	return string(data), err
}

var _ io.Writer = (*mtrLimitedOutput)(nil)
