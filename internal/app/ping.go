package app

import (
	"context"
	"net"
	"strings"
	"time"
)

type PingSample struct {
	Sequence     uint64    `json:"sequence"`
	SampledAt    time.Time `json:"sampledAt"`
	Status       string    `json:"status"`
	Milliseconds float64   `json:"milliseconds"`
	Error        string    `json:"error,omitempty"`
}

type PingStats struct {
	Target            string       `json:"target"`
	Address           string       `json:"address"`
	AddressSource     string       `json:"addressSource"`
	ReplyAddress      string       `json:"replyAddress,omitempty"`
	RouteNote         string       `json:"routeNote"`
	Status            string       `json:"status"`
	Available         bool         `json:"available"`
	Ready             bool         `json:"ready"`
	Milliseconds      float64      `json:"milliseconds"`
	SampledAt         time.Time    `json:"sampledAt"`
	Sent              uint64       `json:"sent"`
	Received          uint64       `json:"received"`
	Lost              uint64       `json:"lost"`
	Pending           uint64       `json:"pending"`
	LossPercent       float64      `json:"lossPercent"`
	WindowSent        uint64       `json:"windowSent"`
	WindowReceived    uint64       `json:"windowReceived"`
	WindowLost        uint64       `json:"windowLost"`
	WindowLossPercent float64      `json:"windowLossPercent"`
	WindowReady       bool         `json:"windowReady"`
	WindowSeconds     int          `json:"windowSeconds"`
	WindowPending     uint64       `json:"windowPending"`
	WindowStartedAt   time.Time    `json:"windowStartedAt"`
	Samples           []PingSample `json:"samples"`
	Error             string       `json:"error,omitempty"`
	attempts          uint64
	pendingSince      time.Time
}

func (s *Session) Ping() PingStats {
	s.pingMu.RLock()
	defer s.pingMu.RUnlock()
	value := s.ping
	value.Samples = append([]PingSample{}, value.Samples...)
	refreshPingWindow(&value, time.Now())
	return value
}

func (s *Session) startPing(target, proxyType string, remote net.Addr) {
	address := ""
	addressSource := "local-dns"
	proxied := proxyType != "" && proxyType != "direct"
	if !proxied && remote != nil {
		address, _, _ = net.SplitHostPort(remote.String())
		if address != "" {
			addressSource = "ssh-peer"
		}
	}
	if address == "" && net.ParseIP(strings.Split(target, "%")[0]) != nil {
		address = target
		addressSource = "configured-ip"
	}
	note := "本机 ICMP 直达目标，遵循系统路由与 TUN；不是 SSH TCP 请求"
	if proxied {
		note = "SSH 使用代理；ICMP 由本机直连，不经过 SOCKS5/HTTP CONNECT，本机 DNS 结果可能与代理端不同"
	}
	s.pingMu.Lock()
	s.ping = PingStats{Target: target, Address: address, AddressSource: addressSource, RouteNote: pingRouteNote(note, address), Status: "pending", Samples: []PingSample{}}
	s.pingMu.Unlock()
	go s.pingLoop(address)
}

func (s *Session) pingLoop(address string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		if address == "" {
			ctx, cancel := context.WithTimeout(s.ctx, time.Second)
			resolved, err := net.DefaultResolver.LookupIPAddr(ctx, s.Ping().Target)
			cancel()
			if s.ctx.Err() != nil {
				return
			}
			if err != nil || len(resolved) == 0 {
				message := "本机无法解析 ICMP 目标地址"
				if err != nil {
					message += "：" + err.Error()
				}
				s.pingMu.Lock()
				s.ping.Status, s.ping.Available, s.ping.Ready, s.ping.Error = "unavailable", false, false, message
				s.pingMu.Unlock()
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
					continue
				}
			}
			address = resolved[0].String()
			for _, item := range resolved {
				if item.IP.To4() != nil {
					address = item.String()
					break
				}
			}
			s.pingMu.Lock()
			s.ping.Address = address
			s.ping.RouteNote = pingRouteNote(s.ping.RouteNote, address)
			s.pingMu.Unlock()
		}
		started := time.Now()
		s.beginPing()
		result := ProbeICMP(s.ctx, address, time.Second)
		if s.ctx.Err() != nil {
			// Cancellation is a local interruption, never a lost network packet.
			result.Status, result.Error = "unavailable", "ICMP 检测已停止"
		}
		s.finishPing(result, started)
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func pingRouteNote(note, address string) string {
	ip := net.ParseIP(address).To4()
	if len(ip) == 4 && ip[0] == 198 && (ip[1] == 18 || ip[1] == 19) {
		return note + "；当前地址位于测试网段，可能是 TUN Fake-IP，ICMP 结果可能由本机代理返回"
	}
	return note
}

func (s *Session) beginPing() {
	s.pingMu.Lock()
	defer s.pingMu.Unlock()
	s.ping.Sent++
	s.ping.Pending++
	s.ping.attempts++
	s.ping.pendingSince = time.Now()
}

func (s *Session) finishPing(result ICMPProbeResult, started time.Time) {
	s.pingMu.Lock()
	defer s.pingMu.Unlock()
	p := &s.ping
	if p.Pending > 0 {
		p.Pending--
	}
	p.Status, p.Error, p.SampledAt = result.Status, result.Error, time.Now()
	p.Available, p.Ready = result.Status != "unavailable", result.Status == "reply"
	// An ICMP error can originate at a router. Keep the target pinned and expose
	// the reply source separately rather than relabelling the probe destination.
	p.ReplyAddress = ""
	if result.Responded || result.Status == "reply" {
		p.ReplyAddress = result.Address
	}
	switch result.Status {
	case "reply":
		p.Received++
		p.Milliseconds = result.Milliseconds
	case "timeout", "unreachable":
		p.Lost++
	default:
		// Missing executable, permission failures, bad addresses and cancellation
		// never contribute to sent/lost counters or a fabricated 100% loss rate.
		p.Available, p.Ready = false, false
		p.Status = "unavailable"
		if p.Sent > 0 {
			p.Sent--
		}
	}
	p.Samples = append(p.Samples, PingSample{Sequence: p.attempts, SampledAt: started, Status: p.Status, Milliseconds: result.Milliseconds, Error: result.Error})
	refreshPingWindow(p, time.Now())
	p.LossPercent = lossPercent(p.Lost, p.Sent-p.Pending)
}

// The window is elapsed time, not an assumed count of one-second probes. It is
// also recomputed when read, so suspension or local failures cannot keep stale
// loss in the displayed last-minute statistic. Pending/local failures exclude
// themselves from the denominator without fabricating successful replies.
func refreshPingWindow(p *PingStats, now time.Time) {
	cutoff := now.Add(-time.Minute)
	kept := p.Samples[:0]
	for _, sample := range p.Samples {
		if sample.SampledAt.After(cutoff) && !sample.SampledAt.After(now) {
			kept = append(kept, sample)
		}
	}
	p.Samples = kept
	p.WindowSeconds, p.WindowStartedAt = 60, cutoff
	p.WindowPending = 0
	if p.Pending > 0 && p.pendingSince.After(cutoff) && !p.pendingSince.After(now) {
		p.WindowPending = p.Pending
	}
	p.WindowSent, p.WindowReceived, p.WindowLost = 0, 0, 0
	for _, sample := range p.Samples {
		switch sample.Status {
		case "reply":
			p.WindowSent++
			p.WindowReceived++
		case "timeout", "unreachable":
			p.WindowSent++
			p.WindowLost++
		}
	}
	p.WindowLossPercent = lossPercent(p.WindowLost, p.WindowSent)
	p.WindowReady = p.WindowSent > 0
}

func lossPercent(lost, completed uint64) float64 {
	if completed == 0 {
		return 0
	}
	return 100 * float64(lost) / float64(completed)
}
