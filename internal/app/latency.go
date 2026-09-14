package app

import (
	"net/http"
	"sync/atomic"
	"time"
)

const sshHeartbeatTimeout = 90 * time.Second

type LatencySample struct {
	Milliseconds         float64   `json:"milliseconds"`
	SampledAt            time.Time `json:"sampledAt"`
	Ready                bool      `json:"ready"`
	Stale                bool      `json:"stale"`
	Pending              bool      `json:"pending"`
	PendingSince         time.Time `json:"pendingSince"`
	TimedOut             bool      `json:"timedOut"`
	Error                string    `json:"error,omitempty"`
	IntervalMilliseconds int       `json:"intervalMilliseconds"`
	TimeoutMilliseconds  int       `json:"timeoutMilliseconds"`
	Ping                 PingStats `json:"ping"`
}

func (s *Session) Latency() LatencySample {
	s.latencyMu.RLock()
	value := s.latency
	s.latencyMu.RUnlock()
	value.IntervalMilliseconds, value.TimeoutMilliseconds = 1000, 1000
	value.TimedOut = value.Pending && time.Since(value.PendingSince) >= time.Second
	value.Stale = value.TimedOut || (value.Ready && time.Since(value.SampledAt) > 2500*time.Millisecond)
	if value.Stale {
		value.Ready = false
	}
	if value.TimedOut {
		value.Error = "SSH 心跳等待已超过 1 秒；这不是 ICMP 丢包"
	}
	value.Ping = s.Ping()
	return value
}

// One sequential heartbeat loop measures an acknowledged SSH request on the
// actual transport. It also replaces the old keepalive, avoiding queue delay
// between competing global requests. No shell or monitoring commands are run.
func (s *Session) heartbeat() {
	s.heartbeatWithTimeout(sshHeartbeatTimeout)
}

func (s *Session) heartbeatWithTimeout(limit time.Duration) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		started := time.Now()
		s.latencyMu.Lock()
		s.latency.Pending = true
		s.latency.PendingSince = started
		s.latencyMu.Unlock()
		// A single delayed global reply cannot kill an active terminal/SFTP
		// stream. Only continuous absence of inbound SSH data expires the link.
		done, watchDone := make(chan struct{}), make(chan struct{})
		var expired atomic.Bool
		go func() {
			defer close(watchDone)
			timer := time.NewTimer(limit)
			defer timer.Stop()
			for {
				select {
				case <-done:
					return
				case <-s.ctx.Done():
					return
				case <-timer.C:
					idle := time.Since(started)
					if s.activity != nil {
						idle = s.activity.idleFor()
					}
					if idle < limit {
						timer.Reset(limit - idle)
						continue
					}
					expired.Store(true)
					s.forceCloseDiagnostic("DS-201", "transport-idle-timeout")
					return
				}
			}
		}()
		_, _, err := s.client.SendRequest("keepalive@openssh.com", true, nil)
		close(done)
		<-watchDone
		if err != nil || expired.Load() {
			s.latencyMu.Lock()
			s.latency.Pending = false
			s.latency.Ready = false
			if expired.Load() {
				s.latency.Error = "SSH 持续未收到数据，连接已断开"
			} else {
				s.latency.Error = err.Error()
			}
			s.latencyMu.Unlock()
			s.closeDiagnostic("DS-202", sshDiagnosticErrorKind(err))
			return
		}
		// An SSH REQUEST_FAILURE is also a reply (RFC 4254 §4), not packet loss.
		s.latencyMu.Lock()
		s.latency = LatencySample{Milliseconds: float64(time.Since(started).Microseconds()) / 1000, SampledAt: time.Now(), Ready: true}
		s.latencyMu.Unlock()
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) latencyHTTP(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, s.Latency())
}
