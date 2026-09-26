package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const diagnosticOutputLimit = 512 << 10

type DiagnosticReport struct {
	ID          string                 `json:"id"`
	SessionID   string                 `json:"sessionId"`
	Direction   string                 `json:"direction"`
	Target      string                 `json:"target"`
	Status      string                 `json:"status"`
	Output      string                 `json:"output"`
	Error       string                 `json:"error,omitempty"`
	StartedAt   time.Time              `json:"startedAt"`
	FinishedAt  *time.Time             `json:"finishedAt,omitempty"`
	MissingTool *MissingDiagnosticTool `json:"missingTool,omitempty"`
}
type Diagnostic struct {
	mu     sync.Mutex
	report DiagnosticReport
	cancel context.CancelFunc
	done   chan struct{}
}

func (d *Diagnostic) snapshot() DiagnosticReport { d.mu.Lock(); defer d.mu.Unlock(); return d.report }
func (d *Diagnostic) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(p)
	remaining := diagnosticOutputLimit - len(d.report.Output)
	if remaining > 0 {
		d.report.Output += strings.ToValidUTF8(string(p[:min(n, remaining)]), "�")
	}
	return n, nil
}
func (d *Diagnostic) setOutput(value string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(value) > diagnosticOutputLimit {
		value = value[:diagnosticOutputLimit]
	}
	d.report.Output = strings.ToValidUTF8(value, "�")
}
func (d *Diagnostic) finish(ctx context.Context, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	d.report.FinishedAt = &now
	switch {
	case ctx.Err() == context.Canceled:
		d.report.Status = "cancelled"
	case ctx.Err() != nil:
		d.report.Status = "failed"
		d.report.Error = "诊断达到 75 秒时限，已停止"
	case err != nil:
		d.report.Status = "failed"
		d.report.Error = err.Error()
		var missing *MissingToolError
		if errors.As(err, &missing) {
			// Android has no supported local mtr installer or shell package
			// manager. Remote installation over SSH is still available.
			d.report.MissingTool = &MissingDiagnosticTool{Name: "mtr", Direction: missing.Direction, Installable: missing.Direction != "local" || runtime.GOOS != "android"}
		}
	default:
		d.report.Status = "done"
	}
}
func (a *App) registerDiagnosticsHTTP(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/{id}/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Direction, Target string }
		if !decode(w, r, &input) {
			return
		}
		d, err := a.startDiagnostic(r.Context(), r.PathValue("id"), input.Direction, input.Target)
		if err != nil {
			respond(w, nil, err)
			return
		}
		writeJSON(w, d.snapshot())
	})
	mux.HandleFunc("GET /api/diagnostics/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		d := a.diagnostics[r.PathValue("id")]
		a.mu.Unlock()
		if d == nil {
			writeError(w, 404, errors.New("诊断记录不存在"))
			return
		}
		writeJSON(w, d.snapshot())
	})
	mux.HandleFunc("DELETE /api/diagnostics/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		d := a.diagnostics[r.PathValue("id")]
		a.mu.Unlock()
		if d != nil {
			d.cancel()
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
}
func (a *App) startDiagnostic(requestCtx context.Context, sessionID, direction, target string) (*Diagnostic, error) {
	s, err := a.session(sessionID)
	if err != nil {
		return nil, err
	}
	if direction != "local" && direction != "remote" {
		return nil, errors.New("请选择本机或服务器诊断")
	}
	if direction == "local" {
		profile, err := a.connectionProfile(s.ProfileID)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(requestCtx, 5*time.Second)
		defer cancel()
		target, err = resolveDiagnosticTarget(ctx, profile.Host)
		if err != nil {
			return nil, err
		}
	} else {
		target, err = validateDiagnosticIP(target)
		if err != nil {
			return nil, err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.diagnostics == nil {
		a.diagnostics = map[string]*Diagnostic{}
	}
	running := 0
	for _, d := range a.diagnostics {
		if d.snapshot().Status == "running" {
			running++
		}
	}
	if running >= 4 {
		return nil, errors.New("最多同时运行 4 个诊断，请先停止已有诊断")
	}
	for len(a.diagnostics) >= 24 {
		var oldest *Diagnostic
		for _, d := range a.diagnostics {
			report := d.snapshot()
			if report.Status != "running" && (oldest == nil || report.StartedAt.Before(oldest.snapshot().StartedAt)) {
				oldest = d
			}
		}
		if oldest == nil {
			break
		}
		delete(a.diagnostics, oldest.snapshot().ID)
	}
	ctx, cancel := context.WithTimeout(s.ctx, 75*time.Second)
	d := &Diagnostic{report: DiagnosticReport{ID: randomID(), SessionID: s.ID, Direction: direction, Target: target, Status: "running", StartedAt: time.Now()}, cancel: cancel, done: make(chan struct{})}
	a.diagnostics[d.report.ID] = d
	go func() {
		defer close(d.done)
		defer cancel()
		var err error
		if direction == "local" {
			err = runLocalDiagnostic(ctx, target, d)
		} else {
			err = runRemoteDiagnostic(ctx, s, target, d)
		}
		d.finish(ctx, err)
	}()
	return d, nil
}
func (a *App) closeDiagnostics() {
	a.mu.Lock()
	all := make([]*Diagnostic, 0, len(a.diagnostics))
	for _, d := range a.diagnostics {
		all = append(all, d)
	}
	a.mu.Unlock()
	for _, d := range all {
		if d.cancel != nil {
			d.cancel()
		}
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for _, d := range all {
		if d.done != nil {
			select {
			case <-d.done:
			case <-timer.C:
				return
			}
		}
	}
}
func validateDiagnosticIP(value string) (string, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("请输入有效的 IPv4 或 IPv6 地址")
	}
	for _, c := range address.Zone() {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("_.-", c) {
			return "", errors.New("IPv6 网卡标识无效")
		}
	}
	return address.Unmap().String(), nil
}
func resolveDiagnosticTarget(ctx context.Context, host string) (string, error) {
	if ip, err := validateDiagnosticIP(host); err == nil {
		return ip, nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("解析服务器地址：%w", err)
	}
	if len(ips) == 0 {
		return "", errors.New("服务器地址没有可用 IP")
	}
	return ips[0].String(), nil
}
func diagnosticHeader(direction, target, engine string) string {
	return fmt.Sprintf("DengShell 网络诊断 · %s\n方向：%s → %s\n%s\n中间跳可能限制 ICMP；应结合后续跳与终点判断丢包。ICMP 不经过应用 SOCKS/HTTP 代理，系统 TUN 可能影响路径。\n\n", engine, direction, target, time.Now().Format("2006-01-02 15:04:05"))
}
func remoteDiagnosticCommand(target string) string {
	// target is a canonical IP plus an optional strictly validated interface ID.
	return "if ! command -v mtr >/dev/null 2>&1; then printf '%s\\n' '__DENGSHELL_MTR_MISSING__'; exit 127; fi; exec mtr -n -z -r -w -c 10 -i 1 -m 30 -- '" + target + "'"
}
func runRemoteDiagnostic(ctx context.Context, s *Session, target string, d *Diagnostic) error {
	fmt.Fprint(d, diagnosticHeader("服务器", target, "远程 mtr · 10 轮 · ASN 查询"))
	fmt.Fprintln(d, "ASN：mtr 的 AS 查询（默认 Team Cymru DNS）；不可用时显示 AS???。")
	_, err := s.commandCollector.runWithOutput(ctx, s, "diagnostic", remoteDiagnosticCommand(target), diagnosticOutputLimit, d)
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitStatus() == 127 && strings.Contains(d.snapshot().Output, "__DENGSHELL_MTR_MISSING__") {
		d.setOutput(strings.ReplaceAll(d.snapshot().Output, "__DENGSHELL_MTR_MISSING__", "远程服务器未安装 mtr。"))
		return &MissingToolError{Direction: "remote"}
	}
	return err
}
