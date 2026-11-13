package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type Session struct {
	diagnosticLog       *sshDiagnosticLog
	diagnosticMu        sync.Mutex
	diagnosticCode      string
	diagnosticStarted   time.Time
	ID                  string `json:"id"`
	ProfileID           string `json:"profileId"`
	Home                string `json:"home"`
	Fingerprint         string `json:"fingerprint"`
	SFTPAvailable       bool   `json:"sftpAvailable"`
	SFTPPending         bool   `json:"sftpPending,omitempty"`
	connectionHost      string // immutable successful connection target; never resolved again
	connectionPeer      string // public direct TCP peer; empty for explicit proxies
	fileWriteIdentity   string // immutable verified server identity for text-save locks
	client              *ssh.Client
	activity            *sshActivityConn
	files               *sftp.Client
	fileBridge          atomic.Pointer[sshSFTPBridge]
	fileInitOnce        sync.Once
	fileDone            chan struct{} // initialized before publication; closed after the sole file-state write
	fileMu              sync.RWMutex
	filesHome           string
	preparedIntegration *terminalIntegration // immutable before session publication
	fileOwners          fileOwnerCache
	cancel              context.CancelFunc
	ctx                 context.Context
	closeOnce           sync.Once
	transportCloseOnce  sync.Once
	cleanupGrace        time.Duration // optional shorter interval for isolated fault fixtures
	mu                  sync.Mutex
	terminalRelay       *terminalRelay
	terminalStarted     bool
	terminalReady       bool
	statsMu             sync.Mutex
	coreCollector       monitorCommandRunner
	commandCollector    monitorCommandRunner
	processCollector    monitorCommandRunner
	preciseMemory       preciseProcessMemory
	networkCollector    monitorCommandRunner
	statsNextSampleAt   time.Time
	statsLastError      error
	processNextSampleAt time.Time
	networkMu           sync.Mutex
	networkReadMu       sync.Mutex
	networkStartOnce    sync.Once
	networkBackground   bool
	networkSampling     bool // protected by networkMu; cache reads never start another SSH command
	networkHistory      []NetworkHistorySample
	networkErrorAt      time.Time
	networkPrevious     *rawStats
	networkNextSampleAt time.Time
	networkLastError    error
	networkMetadataMu   sync.RWMutex
	networkMetadata     []NetworkInterface
	networkMetadataAt   time.Time
	previous            *rawStats
	processPrevious     *rawStats
	processList         processListCache
	processUsers        processUserCache
	staticPrevious      *Stats
	latencyMu           sync.RWMutex
	latency             LatencySample
	pingMu              sync.RWMutex
	ping                PingStats
	promptStyleMu       sync.Mutex
	promptStylePath     string
	promptUsernameColor string
	promptHostnameColor string
	promptUsername      string
	promptHostname      string
}

// Cleanup acknowledgements need several round trips on congested links. A
// short monitoring deadline must not turn 250 ms of network jitter into a
// terminal disconnect. Workers remain owned until cleanup finishes or expires.
func (s *Session) cleanupGracePeriod() time.Duration {
	if s.cleanupGrace > 0 {
		return s.cleanupGrace
	}
	return 30 * time.Second
}

func (s *Session) Close() {
	s.noteDisconnect("DS-299", "session-close")
	s.closeOnce.Do(func() { s.removePromptStyleBeforeClose(); s.forceClose() })
}

// forceClose never performs a remote operation. Closing the transport releases
// SSH requests and SFTP calls even when the peer ignores channel-close messages.
func (s *Session) forceClose() {
	s.noteDisconnect("DS-299", "transport-close")
	if s.cancel != nil {
		s.cancel()
	}
	if b := s.fileBridge.Load(); b != nil {
		b.close()
	}
	s.transportCloseOnce.Do(func() {
		if s.client != nil {
			_ = s.client.Close()
		} else if files, _ := s.fileClientSnapshot(); files != nil {
			_ = files.Close()
		}
	})
}
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func (a *App) Connect(ctx context.Context, profileID, secret string) (*Session, error) {
	return a.ConnectWithHostKeyApproval(ctx, profileID, secret, nil)
}

func (a *App) ConnectWithHostKeyApproval(ctx context.Context, profileID, secret string, approval *HostKeyApproval) (*Session, error) {
	return a.connectSSH(ctx, profileID, secret, "", approval)
}

func (a *App) connectSSH(ctx context.Context, profileID, secret, jumpSecret string, approval *HostKeyApproval) (*Session, error) {
	// The deadline covers the jump host, target and any SSH Agent requests.
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stopApp := context.AfterFunc(a.ctx, cancel)
	defer stopApp()
	if err := dialCtx.Err(); err != nil {
		return nil, err
	}
	p, err := a.connectionProfile(profileID)
	if err != nil {
		return nil, err
	}
	if p.Protocol == "rdp" {
		return nil, errors.New("此连接为 RDP，请使用远程桌面打开")
	}
	dial := dialSSH
	routeType := ""
	var jump *ssh.Client
	if p.JumpHostID != "" {
		host, err := a.store.Get(p.JumpHostID)
		if err != nil || !validSSHJumpHost(p, host) {
			return nil, errors.New("所选 SSH 跳板机不可用，请编辑目标连接并重新选择")
		}
		if p.ProxyID != "" || (p.Proxy.Type != "" && p.Proxy.Type != "direct") {
			return nil, errors.New("请在跳板机的连接配置中设置代理")
		}
		upstream, err := a.openSSHConnection(dialCtx, host, jumpSecret, approval, dialSSH)
		if err != nil {
			return nil, &sshJumpHostError{profileID: host.ID, name: host.Name, cause: err}
		}
		jump = upstream.client
		dial = func(ctx context.Context, address string, _ ProxyConfig) (net.Conn, error) {
			conn, err := jump.DialContext(ctx, "tcp", address)
			if err != nil {
				return nil, fmt.Errorf("SSH 跳板机「%s」无法转发到目标：%w", host.Name, err)
			}
			return &sshJumpConn{Conn: conn, jump: jump}, nil
		}
		routeType = "ssh-jump"
	}
	transport, err := a.openSSHConnection(dialCtx, p, secret, approval, dial)
	if err != nil {
		if jump != nil {
			_ = jump.Close()
		}
		return nil, err
	}
	client, activity, fingerprint := transport.client, transport.activity, transport.fingerprint
	if jump == nil {
		routeType = transport.proxy.Type
	}
	address := sshProfileAddress(p)
	sessionCtx, sessionCancel := context.WithCancel(a.ctx)
	writeIdentity := strings.Join([]string{address, fingerprint, p.User, routeType, transport.proxy.Host, strconv.Itoa(transport.proxy.Port), transport.proxy.User}, "\x00")
	if p.JumpHostID != "" {
		writeIdentity += "\x00" + p.JumpHostID
	}
	s := &Session{ID: randomID(), ProfileID: p.ID, Fingerprint: fingerprint, connectionHost: p.Host, connectionPeer: directSSHServerPeer(client.RemoteAddr(), routeType), fileWriteIdentity: writeIdentity, client: client, activity: activity, ctx: sessionCtx, cancel: sessionCancel}
	appearance := a.store.List().Appearance
	s.promptUsernameColor, s.promptHostnameColor = appearance.PromptUsernameColor, appearance.PromptHostnameColor
	// Publish immediately after SSH authentication. The terminal stages its own
	// bootstrap in its shell request; files do not open an SSH channel yet.
	integration := s.prepareTerminalLaunch()
	s.preparedIntegration = &integration
	s.Home = "/"
	s.prepareFileInitialization()
	connected := false
	defer func() {
		if !connected {
			s.Close()
		}
	}()
	if err := sessionCtx.Err(); err != nil {
		return nil, err
	}
	if err := dialCtx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	if _, exists := a.store.Get(p.ID); exists == nil {
		if err := a.store.MarkConnected(p.ID, time.Now()); err != nil {
			a.mu.Unlock()
			return nil, fmt.Errorf("无法保存成功连接记录：%w", err)
		}
	}
	connected = true
	s.diagnosticLog = &a.sshDiagnostics
	s.diagnosticStarted = time.Now()
	a.sessions[s.ID] = s
	a.mu.Unlock()
	s.startConnectionDiagnostics()
	go func() { <-sessionCtx.Done(); a.disconnect(s.ID) }()
	// This is only the deadline for attaching a window. Once attached, the
	// relay's independent startup deadline covers channel/PTY/shell readiness.
	time.AfterFunc(45*time.Second, func() {
		s.mu.Lock()
		started := s.terminalRelay != nil
		s.mu.Unlock()
		if !started {
			s.noteDisconnect("DS-110", "terminal-not-attached")
			a.disconnect(s.ID)
		}
	})
	s.startPing(p.Host, routeType, client.RemoteAddr())
	return s, nil
}

type sshConnection struct {
	client      *ssh.Client
	activity    *sshActivityConn
	fingerprint string
	proxy       ProxyConfig
}

func sshProfileAddress(p Profile) string {
	return net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
}

func (a *App) openSSHConnection(dialCtx context.Context, p Profile, secret string, approval *HostKeyApproval, dial func(context.Context, string, ProxyConfig) (net.Conn, error)) (sshConnection, error) {
	if p.NeedsProxy {
		return sshConnection{}, &AuthenticationError{code: "ssh_proxy_missing", message: "此导入连接尚未配置代理，请编辑连接后再连接。"}
	}
	var err error
	if p.ProxyID != "" {
		p.Proxy, err = a.store.ResolveProxy(p.ProxyID)
		if err != nil {
			return sshConnection{}, err
		}
	}
	if secret == "" {
		secret = p.Secret
	}
	var auth []ssh.AuthMethod
	var agentConn net.Conn
	switch p.Auth {
	case "password":
		if secret == "" {
			return sshConnection{}, &AuthenticationError{code: "ssh_password_required", message: "请输入 SSH 密码"}
		}
		auth = []ssh.AuthMethod{ssh.Password(secret), ssh.KeyboardInteractive(passwordChallenge(secret))}
	case "key":
		if p.KeyID == "" && p.KeyPath == "" {
			return sshConnection{}, &AuthenticationError{code: "ssh_key_missing", message: "此连接尚未配置私钥，请在编辑连接中选择密钥管理器里的密钥或私钥文件。"}
		}
		var data []byte
		var savedPassphrase string
		if p.KeyID != "" {
			data, savedPassphrase, err = a.store.keyMaterial(p.KeyID)
			if savedPassphrase != "" {
				// The key manager is authoritative, including over stale per-profile
				// credentials or a detached window's cached input.
				secret = savedPassphrase
			}
		} else {
			data, err = readPrivateKey(p.KeyPath)
		}
		if err != nil {
			if errors.Is(err, errManagedKeyMissing) || os.IsNotExist(err) {
				return sshConnection{}, &AuthenticationError{code: "ssh_key_missing", message: "此连接的私钥已缺失，请重新配置密钥。"}
			}
			return sshConnection{}, &AuthenticationError{code: "ssh_key_unavailable", message: "无法读取此连接的私钥，请检查文件权限或重新配置密钥。"}
		}
		defer clear(data)
		var signer ssh.Signer
		signer, err = ssh.ParsePrivateKey(data)
		var passphraseRequired *ssh.PassphraseMissingError
		if errors.As(err, &passphraseRequired) {
			if secret == "" {
				return sshConnection{}, &AuthenticationError{code: "ssh_key_passphrase_required", message: "此私钥已加密，请输入私钥口令；可在密钥管理器中保存，供所有引用此密钥的连接使用。"}
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(secret))
			if err != nil {
				if savedPassphrase != "" {
					return sshConnection{}, &AuthenticationError{code: "ssh_managed_key_invalid", message: "密钥管理器中保存的口令无法解锁此私钥，请在密钥管理器中检查并更新。"}
				}
				return sshConnection{}, &AuthenticationError{code: "ssh_key_passphrase_invalid", message: "私钥口令不正确，无法解锁此私钥。"}
			}
		}
		if err != nil {
			return sshConnection{}, &AuthenticationError{code: "ssh_private_key_invalid", message: "无法解析私钥，请确认所选文件是受支持的完整私钥。"}
		}
		auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	case "agent":
		agentConn, err = dialSSHAgent(dialCtx)
		if err != nil {
			return sshConnection{}, fmt.Errorf("连接 SSH Agent：%w", err)
		}
		defer agentConn.Close()
		stopAgent := context.AfterFunc(dialCtx, func() { _ = agentConn.Close() })
		defer stopAgent()
		deadline, _ := dialCtx.Deadline()
		if err := agentConn.SetDeadline(deadline); err != nil {
			return sshConnection{}, fmt.Errorf("设置 SSH Agent 超时：%w", err)
		}
		signers, err := agent.NewClient(agentConn).Signers()
		if err != nil {
			if dialCtx.Err() != nil {
				return sshConnection{}, dialCtx.Err()
			}
			return sshConnection{}, err
		}
		if len(signers) == 0 {
			return sshConnection{}, errors.New("SSH Agent 中没有可用密钥")
		}
		auth = []ssh.AuthMethod{ssh.PublicKeys(signers...)}
	default:
		return sshConnection{}, errors.New("认证方式无效")
	}
	address := sshProfileAddress(p)
	conn, err := dial(dialCtx, address, p.Proxy)
	if err != nil {
		return sshConnection{}, fmt.Errorf("连接 %s：%w", address, err)
	}
	activity := newSSHActivityConn(conn)
	conn = activity
	stop := context.AfterFunc(dialCtx, func() { conn.Close() })
	defer stop()
	deadline, _ := dialCtx.Deadline()
	_ = conn.SetDeadline(deadline)
	fingerprint := ""
	trace := authenticationTrace{}
	config := &ssh.ClientConfig{User: p.User, Auth: auth, AuthCallback: trace.observe, HostKeyCallback: func(host string, remote net.Addr, key ssh.PublicKey) error {
		fingerprint = ssh.FingerprintSHA256(key)
		return a.store.hostKeyWithApproval(host, key, approval)
	}}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		conn.Close()
		return sshConnection{}, explainSSHAuthentication(err, p.Auth, trace)
	}
	if !stop() || dialCtx.Err() != nil {
		conn.Close()
		return sshConnection{}, dialCtx.Err()
	}
	_ = conn.SetDeadline(time.Time{})
	return sshConnection{client: ssh.NewClient(sshConn, chans, reqs), activity: activity, fingerprint: fingerprint, proxy: p.Proxy}, nil
}

func (a *App) session(id string) (*Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[id]
	if s == nil {
		return nil, errors.New("SSH 会话不存在，请重新连接")
	}
	select {
	case <-s.ctx.Done():
		return nil, errors.New("SSH 连接已断开，请重新连接")
	default:
		return s, nil
	}
}
func (a *App) disconnect(id string) {
	a.mu.Lock()
	s := a.sessions[id]
	delete(a.sessions, id)
	a.mu.Unlock()
	if s != nil {
		s.closeDiagnostic("DS-100", "session-removed")
	}
}

func terminalSize(cols, rows int) (int, int) {
	if cols < 2 {
		cols = 100
	}
	if rows < 2 {
		rows = 30
	}
	return min(cols, 1000), min(rows, 500)
}

func (s *Session) run(ctx context.Context, command string) ([]byte, error) {
	return s.runBoundedSSH(ctx, command, monitorOutputLimit)
}
