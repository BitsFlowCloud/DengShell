package app

import (
	"net/http"
	"time"
)

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
		timeout := time.AfterFunc(10*time.Second, s.Close)
		_, _, err := s.client.SendRequest("keepalive@openssh.com", true, nil)
		stopped := timeout.Stop()
		if err != nil || !stopped {
			s.latencyMu.Lock()
			s.latency.Pending = false
			s.latency.Ready = false
			if err != nil {
				s.latency.Error = err.Error()
			} else {
				s.latency.Error = "SSH 心跳超过 10 秒，连接已断开"
			}
			s.latencyMu.Unlock()
			s.Close()
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
