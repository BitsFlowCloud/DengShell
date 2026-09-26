package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

var sshDiagnosticMessages = map[string]string{
	"DS-100": "用户主动断开、关闭或重新连接",
	"DS-101": "应用退出，关闭连接",
	"DS-110": "连接后未及时打开终端窗口",
	"DS-120": "SSH 终端初始化超时",
	"DS-201": "SSH 连续 90 秒未收到数据，连接超时",
	"DS-202": "SSH 底层传输结束或出现网络错误",
	"DS-210": "SSH 终端通道结束，请结合日志中的传输状态判断原因",
	"DS-211": "无法向远端终端写入数据",
	"DS-212": "无法创建或启动远端终端",
	"DS-220": "本地终端窗口通信通道关闭",
	"DS-221": "向本地终端窗口发送数据失败或超时",
	"DS-230": "后台 SSH 通道取消后，30 秒内仍未完成建立或清理",
	"DS-231": "后台 SSH 命令取消后，30 秒内仍未完成清理",
	"DS-240": "SFTP 操作取消或超时后，30 秒内仍未完成清理",
	"DS-299": "连接关闭，未收到更具体的触发原因",
}

// Diagnostics contain fixed categories and timing only: never command text,
// SSH input/output, paths on the server, addresses, credentials or raw errors.
type SSHDisconnectDiagnostic struct {
	TraceID        string    `json:"traceId"`
	Code           string    `json:"code"`
	Message        string    `json:"message"`
	Detail         string    `json:"detail,omitempty"`
	At             time.Time `json:"at"`
	AgeSeconds     float64   `json:"ageSeconds"`
	LogPath        string    `json:"logPath,omitempty"`
	LogWriteFailed bool      `json:"logWriteFailed,omitempty"`
}

type sshDiagnosticEvent struct {
	SSHDisconnectDiagnostic
	InboundIdleSeconds      float64 `json:"inboundIdleSeconds,omitempty"`
	Kind                    string  `json:"kind"`
	HeartbeatPending        bool    `json:"heartbeatPending,omitempty"`
	HeartbeatPendingSeconds float64 `json:"heartbeatPendingSeconds,omitempty"`
	HeartbeatMilliseconds   float64 `json:"heartbeatMilliseconds,omitempty"`
	HeartbeatAgeSeconds     float64 `json:"heartbeatAgeSeconds,omitempty"`
}

type sshDiagnosticLog struct {
	mu      sync.Mutex
	path    string
	closed  bool
	records map[string]SSHDisconnectDiagnostic
	order   []string
}

// close waits for the current append and prevents late transport callbacks
// from recreating log files after App.Close has returned.
func (l *sshDiagnosticLog) close() { l.mu.Lock(); l.closed = true; l.mu.Unlock() }

func (l *sshDiagnosticLog) record(id string, event sshDiagnosticEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if l.records == nil {
		l.records = make(map[string]SSHDisconnectDiagnostic)
	}
	if _, found := l.records[id]; !found {
		l.order = append(l.order, id)
		if len(l.order) > 256 {
			delete(l.records, l.order[0])
			l.order = l.order[1:]
		}
	}
	previous := l.records[id]
	// A network error produced by our own close cannot replace its trigger.
	if previous.Code == "" || event.Kind == "disconnect" && previous.Code == "DS-299" {
		previous = event.SSHDisconnectDiagnostic
	}
	previous.LogPath = l.path
	if l.path != "" {
		if err := l.append(event); err != nil {
			previous.LogWriteFailed = true
		}
	}
	l.records[id] = previous
}

func (l *sshDiagnosticLog) append(event sshDiagnosticEvent) error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0700); err != nil {
		return err
	}
	// Keep the active file and two older files, about 3 MiB in total.
	if info, err := os.Lstat(l.path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("diagnostic log is not a regular file")
		}
		if info.Size() >= 1<<20 {
			_ = os.Remove(l.path + ".2")
			if _, err := os.Lstat(l.path + ".1"); err == nil {
				if err = os.Rename(l.path+".1", l.path+".2"); err != nil {
					return err
				}
			}
			if err = os.Rename(l.path, l.path+".1"); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(event)
}

func (s *Session) diagnosticEvent(kind, code, detail string) sshDiagnosticEvent {
	now := time.Now()
	trace := s.ID
	if len(trace) > 12 {
		trace = trace[:12]
	}
	age := float64(0)
	if !s.diagnosticStarted.IsZero() {
		age = now.Sub(s.diagnosticStarted).Seconds()
	}
	e := sshDiagnosticEvent{SSHDisconnectDiagnostic: SSHDisconnectDiagnostic{TraceID: trace, Code: code, Message: sshDiagnosticMessages[code], Detail: detail, At: now, AgeSeconds: age}, Kind: kind}
	if s.activity != nil {
		e.InboundIdleSeconds = s.activity.idleFor().Seconds()
	}
	s.latencyMu.RLock()
	sample := s.latency
	s.latencyMu.RUnlock()
	e.HeartbeatPending = sample.Pending
	if sample.Pending && !sample.PendingSince.IsZero() {
		e.HeartbeatPendingSeconds = now.Sub(sample.PendingSince).Seconds()
	}
	if !sample.SampledAt.IsZero() {
		e.HeartbeatAgeSeconds = now.Sub(sample.SampledAt).Seconds()
		e.HeartbeatMilliseconds = sample.Milliseconds
	}
	return e
}

func (s *Session) noteDisconnect(code, detail string) {
	if s.diagnosticLog == nil {
		return
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	if s.diagnosticCode != "" && (s.diagnosticCode != "DS-299" || code == "DS-299") {
		return
	}
	s.diagnosticCode = code
	s.diagnosticLog.record(s.ID, s.diagnosticEvent("disconnect", code, detail))
}
func (s *Session) closeDiagnostic(code, detail string) { s.noteDisconnect(code, detail); s.Close() }
func (s *Session) forceCloseDiagnostic(code, detail string) {
	s.noteDisconnect(code, detail)
	s.forceClose()
}

func (s *Session) noteOperationCancellation(stage string, err error) {
	if s.diagnosticLog != nil {
		s.diagnosticLog.record(s.ID, s.diagnosticEvent("operation-cancelled", "", stage+":"+sshDiagnosticErrorKind(err)))
	}
}

func (s *Session) startConnectionDiagnostics() {
	if s.diagnosticLog == nil {
		return
	}
	s.diagnosticLog.record(s.ID, s.diagnosticEvent("connected", "", ""))
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.diagnosticLog.record(s.ID, s.diagnosticEvent("sample", "", ""))
			}
		}
	}()
	go func() {
		err := s.client.Wait()
		kind := sshDiagnosticErrorKind(err)
		s.noteDisconnect("DS-202", kind)
		s.diagnosticLog.record(s.ID, s.diagnosticEvent("transport-end", "", kind))
		s.Close()
	}()
}

func sshDiagnosticErrorKind(err error) string {
	if err == nil {
		return "completed"
	}
	if errors.Is(err, io.EOF) {
		return "eof"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline-exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "context-cancelled"
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return "connection-reset"
	}
	if errors.Is(err, syscall.ECONNABORTED) {
		return "connection-aborted"
	}
	if errors.Is(err, syscall.EPIPE) {
		return "broken-pipe"
	}
	if errors.Is(err, net.ErrClosed) {
		return "local-socket-closed"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "network-timeout"
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return "remote-exit-status"
	}
	var missing *ssh.ExitMissingError
	if errors.As(err, &missing) {
		return "remote-channel-ended-without-status"
	}
	return "transport-or-channel-error"
}

func (a *App) sshDisconnectDiagnosticHTTP(w http.ResponseWriter, r *http.Request) {
	a.sshDiagnostics.mu.Lock()
	value, ok := a.sshDiagnostics.records[r.PathValue("id")]
	a.sshDiagnostics.mu.Unlock()
	if !ok {
		writeJSON(w, map[string]string{"code": "", "message": "尚无断开记录"})
		return
	}
	writeJSON(w, value)
}
