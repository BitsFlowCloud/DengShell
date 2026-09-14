package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	networkSampleInterval = time.Second
	networkHistoryWindow  = 60 * time.Second
	networkHistoryLimit   = 62
)

// History omits discovery metadata and retains only actual observations.
// An empty Interfaces slice marks a failed observation, never a measured zero.
type NetworkHistoryInterface struct {
	Name     string  `json:"name"`
	RX       float64 `json:"rx"`
	TX       float64 `json:"tx"`
	RXBytes  uint64  `json:"rxBytes"`
	TXBytes  uint64  `json:"txBytes"`
	Ready    bool    `json:"ready"`
	Loopback bool    `json:"loopback"`
}

type NetworkHistorySample struct {
	Interfaces          []NetworkHistoryInterface `json:"interfaces"`
	ElapsedMilliseconds float64                   `json:"elapsedMilliseconds"`
	SampledAt           time.Time                 `json:"sampledAt"`
}

// One awk, two tiny procfs files. The remote monotonic uptime excludes SSH
// delivery jitter from byte rates; no interface discovery or process scan runs
// on this one-second path. Interface metadata comes from the 30-second cache.
const networkMonitorCommand = `export LC_ALL=C
awk 'BEGIN {
    if ((getline stamp < "/proc/uptime") <= 0) exit 1
    close("/proc/uptime")
    print "__CS_UPTIME__"; print stamp
    print "__CS_NET__"
    while ((getline line < "/proc/net/dev") > 0) print line
    close("/proc/net/dev")
}' 2>/dev/null
`

type NetworkStats struct {
	History                      []NetworkHistorySample `json:"history"`
	SampleError                  string                 `json:"sampleError,omitempty"`
	Interfaces                   []NetworkInterface     `json:"interfaces"`
	NetworkRX                    float64                `json:"networkRX"`
	NetworkTX                    float64                `json:"networkTX"`
	SampleReady                  bool                   `json:"sampleReady"`
	SampledAt                    time.Time              `json:"sampledAt"`
	Cached                       bool                   `json:"cached"`
	AgeMilliseconds              int64                  `json:"ageMilliseconds"`
	SamplingIntervalMilliseconds int                    `json:"samplingIntervalMilliseconds"`
	NextSampleInMilliseconds     int64                  `json:"nextSampleInMilliseconds"`
	ElapsedMilliseconds          float64                `json:"elapsedMilliseconds"`
	MetadataSampledAt            time.Time              `json:"metadataSampledAt"`
	MetadataIntervalMilliseconds int                    `json:"metadataIntervalMilliseconds"`
}

// Preserve the first request's cadence, instead of adding collection duration
// to each interval. Missed/hidden ticks are skipped, never replayed in a burst.
func advanceSampleDue(due, now time.Time, interval time.Duration) time.Time {
	if due.IsZero() {
		return now.Add(interval)
	}
	if due.After(now) {
		return due
	}
	return due.Add((now.Sub(due)/interval + 1) * interval)
}

func nextSampleMilliseconds(due time.Time) int64 {
	delay := time.Until(due)
	if delay <= 0 {
		return 0
	}
	return int64((delay + time.Millisecond - 1) / time.Millisecond)
}

func cloneNetworkInterfaces(interfaces []NetworkInterface) []NetworkInterface {
	result := append([]NetworkInterface{}, interfaces...)
	for i := range result {
		result[i].Addresses = append([]string{}, result[i].Addresses...)
	}
	return result
}

func (s *Session) cacheNetworkMetadata(stats Stats) {
	interfaces := cloneNetworkInterfaces(stats.Interfaces)
	s.networkMetadataMu.Lock()
	s.networkMetadata, s.networkMetadataAt = interfaces, stats.MetadataSampledAt
	s.networkMetadataMu.Unlock()
}

func parseNetworkSnapshot(data []byte, at time.Time) (rawStats, error) {
	result := rawStats{Stats: Stats{SampledAt: at, Interfaces: []NetworkInterface{}}}
	section := ""
	lines := []string{}
	uptimeValid := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "__CS_") {
			section = line
			continue
		}
		fields := strings.Fields(line)
		if section == "__CS_UPTIME__" && len(fields) > 0 {
			value, err := strconv.ParseFloat(fields[0], 64)
			if err == nil && value > 0 && value < 1e12 {
				result.Uptime, uptimeValid = value, true
			}
		}
		if section != "__CS_NET__" {
			continue
		}
		name, counters, ok := strings.Cut(line, ":")
		values := strings.Fields(counters)
		if !ok || strings.TrimSpace(name) == "" || len(values) < 16 {
			continue
		}
		_, rxErr := strconv.ParseUint(values[0], 10, 64)
		_, txErr := strconv.ParseUint(values[8], 10, 64)
		if rxErr == nil && txErr == nil {
			lines = append(lines, line)
		}
	}
	if !uptimeValid || len(lines) == 0 {
		return result, errors.New("无法读取网卡流量：远程 /proc/net/dev 或 /proc/uptime 数据不可用")
	}
	parseNetworkStats(&result, map[string][]string{"__CS_NET__": lines})
	return result, nil
}

func applyNetworkSnapshotRates(current, previous *rawStats) float64 {
	if previous == nil {
		return 0
	}
	seconds := current.Uptime - previous.Uptime
	// A stalled sampler or transport resumes with a fresh baseline rather than
	// drawing a long-window average as a one-second observation.
	if seconds <= 0 || seconds > 3*networkSampleInterval.Seconds() {
		return 0
	}
	applyInterfaceRates(current, previous, seconds)
	current.SampleReady = len(current.Interfaces) > 0
	for _, iface := range current.Interfaces {
		current.SampleReady = current.SampleReady && iface.Ready
		if iface.Loopback {
			continue
		}
		if iface.Ready {
			current.NetworkRX += iface.RX
			current.NetworkTX += iface.TX
		}
	}
	return seconds * 1000
}

// Network sampling belongs to the SSH session, not to the selected UI tab.
// Moving a terminal between windows retains this one sampler and its history.
func (s *Session) startNetworkSampler() {
	s.startNetworkSamplerWithReader(s.runNetworkMonitor)
}

func (s *Session) startNetworkSamplerWithReader(read func(context.Context, string) ([]byte, error)) {
	s.networkStartOnce.Do(func() {
		s.networkMu.Lock()
		s.networkBackground = true
		s.networkMu.Unlock()
		go func() {
			for s.ctx.Err() == nil {
				s.collectNetworkSnapshot(s.ctx, read)
				s.networkMu.Lock()
				due := s.networkNextSampleAt
				s.networkMu.Unlock()
				timer := time.NewTimer(max(0, time.Until(due)))
				select {
				case <-s.ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	})
}

// The collector retains its worker through cancellation, without closing the
// shared SSH transport or blocking another server's sampling.
func (s *Session) runNetworkMonitor(ctx context.Context, command string) ([]byte, error) {
	return s.networkCollector.run(ctx, s, "network", command, networkOutputLimit)
}

func (s *Session) retainNetworkHistory(sample NetworkHistorySample) {
	s.networkHistory = append(s.networkHistory, sample)
	cutoff := sample.SampledAt.Add(-networkHistoryWindow)
	first := 0
	for first < len(s.networkHistory) && (s.networkHistory[first].SampledAt.Before(cutoff) || len(s.networkHistory)-first > networkHistoryLimit) {
		first++
	}
	if first > 0 {
		remaining := copy(s.networkHistory, s.networkHistory[first:])
		clear(s.networkHistory[remaining:])
		s.networkHistory = s.networkHistory[:remaining]
	}
}

func (s *Session) collectNetworkSnapshot(ctx context.Context, read func(context.Context, string) ([]byte, error)) (bool, error) {
	s.networkReadMu.Lock()
	defer s.networkReadMu.Unlock()
	if err := ctx.Err(); err != nil {
		return true, err
	}
	s.networkMu.Lock()
	now := time.Now()
	if now.Before(s.networkNextSampleAt) {
		err := s.networkLastError
		s.networkMu.Unlock()
		return true, err
	}
	s.networkNextSampleAt = advanceSampleDue(s.networkNextSampleAt, now, networkSampleInterval)
	s.networkMu.Unlock()
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	data, err := read(readCtx, networkMonitorCommand)
	cancel()
	at := time.Now()
	var current rawStats
	if err == nil {
		current, err = parseNetworkSnapshot(data, at)
	}
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	// Skip elapsed deadlines when a slow server responds, never catch up with
	// a burst of remote commands. Keep the original wall-clock cadence.
	s.networkNextSampleAt = advanceSampleDue(s.networkNextSampleAt, at, networkSampleInterval)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		s.networkLastError, s.networkErrorAt = err, at
		s.retainNetworkHistory(NetworkHistorySample{Interfaces: []NetworkHistoryInterface{}, SampledAt: at})
		return false, err
	}
	previous := s.networkPrevious
	if s.networkLastError != nil {
		previous = nil // a missing observation must not become a zero or averaged rate
	}
	current.NetworkElapsedMilliseconds = applyNetworkSnapshotRates(&current, previous)
	s.networkPrevious, s.networkLastError = &current, nil
	history := NetworkHistorySample{Interfaces: make([]NetworkHistoryInterface, len(current.Interfaces)), SampledAt: at, ElapsedMilliseconds: current.NetworkElapsedMilliseconds}
	for i, iface := range current.Interfaces {
		history.Interfaces[i] = NetworkHistoryInterface{iface.Name, iface.RX, iface.TX, iface.RXBytes, iface.TXBytes, iface.Ready, iface.Loopback}
	}
	s.retainNetworkHistory(history)
	return false, nil
}

func (s *Session) networkForDisplay(ctx context.Context) (NetworkStats, error) {
	if err := ctx.Err(); err != nil {
		return NetworkStats{}, err
	}
	s.networkMu.Lock()
	background := s.networkBackground
	s.networkMu.Unlock()
	cached := true
	if !background {
		// Preserve on-demand behavior for standalone fixtures and callers that
		// do not create their session through Connect.
		var err error
		cached, err = s.collectNetworkSnapshot(ctx, s.run)
		if err != nil {
			return NetworkStats{}, err
		}
	}
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	current := s.networkPrevious
	if current == nil {
		current = &rawStats{Stats: Stats{Interfaces: []NetworkInterface{}}}
	}
	s.networkMetadataMu.RLock()
	metadata := Stats{Interfaces: s.networkMetadata, MetadataSampledAt: s.networkMetadataAt}
	view := rawStats{Stats: Stats{Interfaces: cloneNetworkInterfaces(current.Interfaces)}}
	applyStaticMetadata(&view, &metadata)
	s.networkMetadataMu.RUnlock()
	history := []NetworkHistorySample{}
	cutoff := time.Now().Add(-networkHistoryWindow)
	for _, item := range s.networkHistory {
		if item.SampledAt.Before(cutoff) {
			continue
		}
		item.Interfaces = append([]NetworkHistoryInterface{}, item.Interfaces...)
		history = append(history, item)
	}
	result := NetworkStats{
		Interfaces: view.Interfaces, NetworkRX: current.NetworkRX, NetworkTX: current.NetworkTX,
		SampleReady: current.SampleReady, SampledAt: current.SampledAt, Cached: cached, History: history,
		SamplingIntervalMilliseconds: int(networkSampleInterval / time.Millisecond),
		NextSampleInMilliseconds:     nextSampleMilliseconds(s.networkNextSampleAt),
		ElapsedMilliseconds:          current.NetworkElapsedMilliseconds,
		MetadataSampledAt:            metadata.MetadataSampledAt, MetadataIntervalMilliseconds: int(monitorStaticInterval / time.Millisecond),
	}
	if s.networkLastError != nil {
		result.SampleError = s.networkLastError.Error()
		result.SampledAt, result.SampleReady, result.ElapsedMilliseconds = s.networkErrorAt, false, 0
		result.NetworkRX, result.NetworkTX = 0, 0
		for i := range result.Interfaces {
			result.Interfaces[i].Ready = false
			result.Interfaces[i].RX, result.Interfaces[i].TX = 0, 0
		}
	}
	if !result.SampledAt.IsZero() {
		result.AgeMilliseconds = max(0, time.Since(result.SampledAt).Milliseconds())
	}
	return result, nil
}

func (a *App) networkHTTP(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	value, err := s.networkForDisplay(r.Context())
	respond(w, value, err)
}
