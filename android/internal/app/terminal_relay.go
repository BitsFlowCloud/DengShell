package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

const terminalHandoffTimeout = 45 * time.Second
const terminalBoundaryTimeout = 3 * time.Second
const terminalRelayFrames = 16
const terminalStartupTimeout = 20 * time.Second

var errTerminalHandoffCancelled = errors.New("终端迁移已取消，已恢复原窗口")

type relayMessage struct {
	Type  string `json:"type"`
	Data  string `json:"data"`
	Nonce string `json:"nonce"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
}
type terminalPeer struct {
	ws   *websocket.Conn
	dead atomic.Bool
}
type terminalHandoff struct {
	nonce    string
	ready    bool
	done     chan struct{}
	err      error
	finished bool
}
type terminalControl struct {
	peer     *terminalPeer
	message  relayMessage
	attach   *terminalPeer
	response chan error
}

// The event loop alone writes frames, changes ownership and spends ACK credits.
// Readers retain at most 16 queued 32KiB frames plus one frame per SSH stream;
// another 16 frames may be in the renderer awaiting ACK. Pausing adds no spool.
type terminalRelay struct {
	app        *App
	session    *Session
	first      *terminalPeer
	cols, rows int
	controls   chan terminalControl
	output     chan []byte
	inputs     chan string
	mu         sync.Mutex
	peers      map[*terminalPeer]bool
	handoff    *terminalHandoff
	timeout    time.Duration
}

func (a *App) terminal(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	nonce := r.URL.Query().Get("handoff")
	s.mu.Lock()
	relay := s.terminalRelay
	if nonce != "" {
		s.mu.Unlock()
		if relay == nil || !relay.handoffReady(nonce) {
			writeError(w, 409, errors.New("终端迁移凭据无效、已使用或尚未暂停"))
			return
		}
	} else {
		if s.terminalStarted {
			s.mu.Unlock()
			writeError(w, 409, errors.New("此会话已有终端"))
			return
		}
		s.terminalStarted = true
		s.mu.Unlock()
	}
	upgrader := websocket.Upgrader{CheckOrigin: a.allowedOrigin, HandshakeTimeout: 5 * time.Second, ReadBufferSize: 4096, WriteBufferSize: 32768}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if nonce == "" {
			s.mu.Lock()
			s.terminalStarted = false
			s.mu.Unlock()
		}
		return
	}
	peer := &terminalPeer{ws: ws}
	ws.SetReadLimit(2 << 20)
	if nonce == "" {
		cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
		rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
		cols, rows = terminalSize(cols, rows)
		relay = &terminalRelay{app: a, session: s, first: peer, cols: cols, rows: rows, controls: make(chan terminalControl, 64), output: make(chan []byte, terminalRelayFrames), inputs: make(chan string, 64), peers: map[*terminalPeer]bool{peer: true}, timeout: terminalHandoffTimeout}
		go relay.run()
	} else {
		relay.mu.Lock()
		relay.peers[peer] = true
		relay.mu.Unlock()
		if err := relay.request(r.Context(), terminalControl{attach: peer, message: relayMessage{Nonce: nonce}}); err != nil {
			ws.Close()
			relay.forgetPeer(peer)
			return
		}
	}
	relay.readPeer(peer)
}
func (r *terminalRelay) forgetPeer(peer *terminalPeer) {
	r.mu.Lock()
	delete(r.peers, peer)
	r.mu.Unlock()
}
func (r *terminalRelay) readPeer(peer *terminalPeer) {
	defer func() {
		peer.dead.Store(true)
		peer.ws.Close()
		r.forgetPeer(peer)
		select {
		case r.controls <- terminalControl{peer: peer, message: relayMessage{Type: "peer-closed"}}:
		case <-r.session.ctx.Done():
		}
	}()
	for {
		var m relayMessage
		if err := peer.ws.ReadJSON(&m); err != nil {
			return
		}
		select {
		case r.controls <- terminalControl{peer: peer, message: m}:
		case <-r.session.ctx.Done():
			return
		}
	}
}
func (r *terminalRelay) request(ctx context.Context, c terminalControl) error {
	c.response = make(chan error, 1)
	select {
	case r.controls <- c:
	case <-ctx.Done():
		return ctx.Err()
	case <-r.session.ctx.Done():
		return errors.New("SSH 连接已关闭")
	}
	select {
	case err := <-c.response:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-r.session.ctx.Done():
		return errors.New("SSH 连接已关闭")
	}
}
func (r *terminalRelay) handoffReady(nonce string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.handoff
	return h != nil && h.nonce == nonce && h.ready && !h.finished && r.session.ctx.Err() == nil
}

// A reservation retains this small completion record even if the same PTY
// starts a subsequent handoff. Reading after done closes is race-safe; err is
// immutable from then on. No scrollback or renderer data lives in this record.
func (h *terminalHandoff) result() (bool, error) {
	if h == nil {
		return false, errors.New("终端交接记录不存在")
	}
	select {
	case <-h.done:
		return true, h.err
	default:
		return false, nil
	}
}
func (s *Session) readyTerminalHandoff(nonce string) *terminalHandoff {
	s.mu.Lock()
	r := s.terminalRelay
	s.mu.Unlock()
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.handoff
	if h != nil && h.nonce == nonce && h.ready && !h.finished && s.ctx.Err() == nil {
		return h
	}
	return nil
}

func (a *App) TerminalHandoffReady(sessionID, nonce string) bool {
	s, err := a.session(sessionID)
	if err != nil {
		return false
	}
	s.mu.Lock()
	r := s.terminalRelay
	s.mu.Unlock()
	return r != nil && r.handoffReady(nonce)
}
func (a *App) WaitTerminalHandoff(ctx context.Context, sessionID, nonce string) error {
	s, err := a.session(sessionID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	r := s.terminalRelay
	s.mu.Unlock()
	if r == nil {
		return errors.New("终端尚未建立")
	}
	r.mu.Lock()
	h := r.handoff
	r.mu.Unlock()
	if h == nil || h.nonce != nonce {
		return errors.New("终端迁移凭据不匹配")
	}
	select {
	case <-h.done:
		return h.err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return errors.New("SSH 连接已关闭")
	}
}
func (a *App) ResumeTerminalHandoff(sessionID, nonce string) error {
	s, err := a.session(sessionID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	r := s.terminalRelay
	s.mu.Unlock()
	if r == nil {
		return errors.New("终端尚未建立")
	}
	return r.request(s.ctx, terminalControl{message: relayMessage{Type: "handoff-cancel", Nonce: nonce}})
}
func validHandoffNonce(nonce string) bool {
	if len(nonce) < 16 || len(nonce) > 128 {
		return false
	}
	return strings.IndexFunc(nonce, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-')
	}) < 0
}
func (r *terminalRelay) finishHandoff(h *terminalHandoff, err error) {
	if h == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !h.finished {
		h.err = err
		h.ready = false
		h.finished = true
		close(h.done)
	}
}
func relayWrite(peer *terminalPeer, kind int, data []byte) error {
	peer.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return peer.ws.WriteMessage(kind, data)
}
func relayStatus(peer *terminalPeer, kind, message, nonce string) error {
	data, _ := json.Marshal(map[string]string{"type": kind, "message": message, "nonce": nonce})
	return relayWrite(peer, websocket.TextMessage, data)
}

func (r *terminalRelay) run() {
	s := r.session
	active := r.first
	write := func(peer *terminalPeer, kind int, data []byte) error {
		err := relayWrite(peer, kind, data)
		if err != nil && peer == active {
			s.noteDisconnect("DS-221", sshDiagnosticErrorKind(err))
		}
		return err
	}
	status := func(peer *terminalPeer, kind, message, nonce string) error {
		data, _ := json.Marshal(map[string]string{"type": kind, "message": message, "nonce": nonce})
		return write(peer, websocket.TextMessage, data)
	}
	startupCtx, cancelStartup := context.WithTimeout(s.ctx, terminalStartupTimeout)
	defer cancelStartup()
	stopStartup := context.AfterFunc(startupCtx, func() { s.forceCloseDiagnostic("DS-120", "terminal-startup-timeout") })
	defer stopStartup()
	// Publishing the relay means its hard startup deadline is already armed.
	// A reserved/failed WebSocket upgrade must not bypass the orphan deadline.
	s.mu.Lock()
	s.terminalRelay = r
	s.mu.Unlock()
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-s.ctx.Done():
			r.mu.Lock()
			for peer := range r.peers {
				peer.ws.Close()
			}
			r.mu.Unlock()
		case <-stopWatch:
		}
	}()
	defer close(stopWatch)
	defer func() {
		r.finishHandoff(r.handoff, errors.New("SSH 终端已关闭"))
		s.noteDisconnect("DS-299", "terminal-relay-ended")
		r.app.disconnect(s.ID)
		r.mu.Lock()
		for peer := range r.peers {
			peer.ws.Close()
		}
		r.mu.Unlock()
	}()
	sh, err := s.client.NewSession()
	if err != nil {
		s.noteDisconnect("DS-212", "create-terminal-channel")
		_ = status(active, "error", "无法创建 SSH 终端："+err.Error(), "")
		return
	}
	defer sh.Close()
	if err = sh.RequestPty("xterm-256color", r.rows, r.cols, ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 38400, ssh.TTY_OP_OSPEED: 38400}); err != nil {
		s.noteDisconnect("DS-212", "request-pty")
		_ = status(active, "error", err.Error(), "")
		return
	}
	in, err := sh.StdinPipe()
	if err != nil {
		return
	}
	out, err := sh.StdoutPipe()
	if err != nil {
		return
	}
	stderr, err := sh.StderrPipe()
	if err != nil {
		return
	}
	integration := s.prepareTerminalIntegration()
	defer s.cleanupTerminalIntegration(integration)
	if integration.command != "" {
		err = sh.Start(integration.command)
		// Restricted SSH services may allow shell but reject exec requests.
		if err != nil && integration.inline && s.ctx.Err() == nil {
			integration = terminalIntegration{}
			err = sh.Shell()
		}
	} else {
		err = sh.Shell()
	}
	if err != nil {
		s.noteDisconnect("DS-212", "start-shell")
		_ = status(active, "error", err.Error(), "")
		return
	}
	ready := terminalIntegrationReady(integration)
	if write(active, websocket.TextMessage, ready) != nil {
		return
	}
	if !stopStartup() || startupCtx.Err() != nil {
		return
	}
	cancelStartup()
	s.mu.Lock()
	s.terminalReady = true
	s.mu.Unlock()
	// Deliver the terminal's first output before competing optional channels.
	// Silent shells still get file service and heartbeats after a bounded grace.
	var backgroundOnce sync.Once
	startBackground := func() {
		backgroundOnce.Do(func() {
			if s.ctx.Err() != nil {
				return
			}
			s.startFileInitialization()
			s.startNetworkSampler()
			go s.heartbeat()
		})
	}
	backgroundTimer := time.AfterFunc(time.Second, startBackground)
	defer backgroundTimer.Stop()
	go func() {
		for {
			select {
			case data := <-r.inputs:
				if r.app.RequireUnlocked() != nil {
					continue
				}
				if _, err := io.WriteString(in, data); err != nil {
					s.closeDiagnostic("DS-211", sshDiagnosticErrorKind(err))
					return
				}
			case <-s.ctx.Done():
				return
			}
		}
	}()
	var copies sync.WaitGroup
	for _, reader := range []io.Reader{out, stderr} {
		copies.Add(1)
		go func(reader io.Reader) {
			defer copies.Done()
			buffer := make([]byte, 32768)
			for {
				n, err := reader.Read(buffer)
				if n > 0 {
					frame := append([]byte(nil), buffer[:n]...)
					select {
					case r.output <- frame:
					case <-s.ctx.Done():
						return
					}
				}
				if err != nil {
					return
				}
			}
		}(reader)
	}
	copiesDone := make(chan struct{})
	go func() { copies.Wait(); close(copiesDone) }()
	shellDone := make(chan error, 1)
	go func() { shellDone <- sh.Wait() }()
	var timer *time.Timer
	var timeout <-chan time.Time
	var paused, pending *terminalHandoff
	var boundary terminalBoundary
	credits := 0
	var integrationTail string
	integrationConfirmed := !integration.inline
	outputEnded, shellEnded := false, false
	var shellErr error
	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		timeout = nil
	}
	defer stopTimer()
	pause := func(h *terminalHandoff) error {
		pending, paused = nil, h
		stopTimer()
		r.mu.Lock()
		h.ready = true
		duration := r.timeout
		r.mu.Unlock()
		if err := status(active, "handoff-ready", "", h.nonce); err != nil {
			return err
		}
		timer = time.NewTimer(duration)
		timeout = timer.C
		return nil
	}
	resume := func(nonce string) error {
		h := paused
		if h == nil {
			h = pending
		}
		if h == nil || h.nonce != nonce {
			return errors.New("迁移已结束或凭据不匹配")
		}
		paused, pending = nil, nil
		stopTimer()
		r.finishHandoff(h, errTerminalHandoffCancelled)
		if active.dead.Load() {
			return errors.New("原窗口已关闭，无法恢复终端")
		}
		return status(active, "handoff-cancelled", "已恢复原窗口", nonce)
	}
	for {
		if shellEnded && outputEnded && len(r.output) == 0 && paused == nil {
			message := "终端已退出"
			if shellErr != nil {
				message = shellErr.Error()
			}
			_ = status(active, "exit", message, "")
			return
		}
		var output <-chan []byte
		if paused == nil && credits < terminalRelayFrames {
			output = r.output
		}
		select {
		case <-s.ctx.Done():
			return
		case <-copiesDone:
			outputEnded = true
			copiesDone = nil
		case shellErr = <-shellDone:
			s.noteDisconnect("DS-210", sshDiagnosticErrorKind(shellErr))
			shellEnded = true
			shellDone = nil
		case frame := <-output:
			if write(active, websocket.BinaryMessage, frame) != nil {
				return
			}
			// Only publish the style path after this shell proves its bootstrap
			// succeeded. Fallback/collision paths are never treated as owned.
			if !integrationConfirmed {
				text := integrationTail + string(frame)
				for _, shell := range []string{"bash", "zsh", "fish"} {
					marker := "\x1b]777;DengShell;ready;" + integration.Nonce + ";" + shell + "\x07"
					if strings.Contains(text, marker) {
						s.promptStyleMu.Lock()
						s.promptStylePath = integration.files[0]
						s.promptStyleMu.Unlock()
						integrationConfirmed = true
						break
					}
				}
				if !integrationConfirmed {
					integrationTail = text[max(0, len(text)-128):]
				}
			}
			backgroundTimer.Stop()
			startBackground()
			credits++
			boundary.write(frame)
			if pending != nil && boundary.safe() {
				if pause(pending) != nil {
					return
				}
			}
		case <-timeout:
			if pending != nil {
				h := pending
				pending = nil
				stopTimer()
				err := errors.New("终端正在接收未完成的字符或控制序列，请稍后重试")
				r.finishHandoff(h, err)
				if status(active, "handoff-error", err.Error(), h.nonce) != nil {
					return
				}
			} else if paused != nil && resume(paused.nonce) != nil {
				return
			}
		case control := <-r.controls:
			respond := func(err error) {
				if control.response != nil {
					control.response <- err
				}
			}
			if control.attach != nil {
				if paused == nil || paused.nonce != control.message.Nonce || !r.handoffReady(control.message.Nonce) {
					respond(errors.New("终端迁移凭据无效或已使用"))
					continue
				}
				next := control.attach
				if next.dead.Load() {
					respond(errors.New("新窗口已关闭"))
					continue
				}
				if err := write(next, websocket.TextMessage, ready); err != nil {
					respond(err)
					continue
				}
				old, h := active, paused
				active = next
				paused = nil
				credits = 0
				stopTimer()
				r.finishHandoff(h, nil)
				old.ws.Close()
				respond(nil)
				continue
			}
			if control.peer != nil && control.peer != active {
				respond(errors.New("窗口已失去终端所有权"))
				continue
			}
			m := control.message
			switch m.Type {
			case "peer-closed":
				if paused == nil {
					s.noteDisconnect("DS-220", "renderer-channel-closed")
					return
				}
			case "ack":
				if credits > 0 {
					credits--
				}
			case "handoff-begin":
				if r.app.RequireUnlocked() != nil {
					_ = status(active, "handoff-error", "软件已被锁定", m.Nonce)
					continue
				}
				if paused != nil || pending != nil || !validHandoffNonce(m.Nonce) {
					_ = status(active, "handoff-error", "迁移正在进行或凭据无效", m.Nonce)
					continue
				}
				r.mu.Lock()
				previous := r.handoff
				r.mu.Unlock()
				if previous != nil && previous.nonce == m.Nonce {
					_ = status(active, "handoff-error", "迁移凭据已使用", m.Nonce)
					continue
				}
				h := &terminalHandoff{nonce: m.Nonce, done: make(chan struct{})}
				pending = h
				r.mu.Lock()
				r.handoff = h
				r.mu.Unlock()
				if boundary.safe() {
					if pause(h) != nil {
						return
					}
				} else {
					timer = time.NewTimer(terminalBoundaryTimeout)
					timeout = timer.C
				}
			case "handoff-cancel":
				err := resume(m.Nonce)
				respond(err)
				if err != nil && active.dead.Load() {
					return
				}
			case "input":
				if r.app.RequireUnlocked() != nil {
					continue
				}
				if paused != nil || pending != nil {
					continue
				}
				if len(m.Data) > 65536 {
					_ = status(active, "error", "输入过多，请分批粘贴", "")
					return
				}
				select {
				case r.inputs <- m.Data:
				default:
					_ = status(active, "error", "输入过多，请分批粘贴", "")
					return
				}
			case "resize":
				if r.app.RequireUnlocked() != nil {
					continue
				}
				if paused != nil || pending != nil {
					continue
				}
				cols, rows := terminalSize(m.Cols, m.Rows)
				if sh.WindowChange(rows, cols) != nil {
					return
				}
			}
		}
	}
}
