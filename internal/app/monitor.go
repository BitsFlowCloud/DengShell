package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sampling is on demand, with static metadata on a slower schedule. Reading
// core procfs files in one awk avoids a fresh cat process for every counter.
const coreMonitorCommand = `
printf '\n__CS_BLOCKS__\n'
for dengshell_block in /sys/block/*; do
    [ -e "$dengshell_block" ] || continue
    dengshell_layer=leaf
    for dengshell_slave in "$dengshell_block"/slaves/*; do
        if [ -e "$dengshell_slave" ]; then dengshell_layer=stacked; break; fi
    done
    printf '%s %s\n' "${dengshell_block##*/}" "$dengshell_layer"
done
awk '
function emit(section, path, line) {
    printf "\n__CS_%s__\n", section
    while ((getline line < path) > 0) {
        if (section == "STAT" && line !~ /^cpu /) continue
        if (section == "DISKIO") { split(line, parts, " "); if (parts[3] ~ /^(loop|ram|dm-)/) continue }
        print line
        if (section == "STAT") break
    }
    close(path)
}
BEGIN {
    emit("STAT", "/proc/stat")
    emit("MEM", "/proc/meminfo")
    emit("UPTIME", "/proc/uptime")
    emit("LOAD", "/proc/loadavg")
    emit("NET", "/proc/net/dev")
    emit("DISKIO", "/proc/diskstats")
}' 2>/dev/null
`

const staticMonitorCommand = `
awk '
BEGIN {
    print "\n__CS_OS__"
    while ((getline line < "/etc/os-release") > 0) print line
    close("/etc/os-release")
    print "\n__CS_CPUINFO__"
    while ((getline line < "/proc/cpuinfo") > 0) {
        if (line ~ /^processor[ \t]*:/) print line
        if (!model && line ~ /^(model name|Hardware)[ \t]*:/) { print line; model = 1 }
    }
    close("/proc/cpuinfo")
}' 2>/dev/null
printf '\n__CS_LINK__\n'; ip -o link show 2>/dev/null
printf '\n__CS_ADDR__\n'; ip -o addr show 2>/dev/null
printf '\n__CS_ROUTES__\n'; ip -4 route show default 2>/dev/null; ip -6 route show default 2>/dev/null
printf '\n__CS_CONNECTION__\n'; printf '%s\n' "$SSH_CONNECTION"
printf '\n__CS_DF__\n'; df -Pk 2>/dev/null
`

const monitorMinimumInterval = 5 * time.Second
const monitorProcessInterval = 5 * time.Second
const processOutputLimit = 32 << 20
const monitorStaticInterval = 30 * time.Second

func monitorCommandFor(static, processes bool) string {
	command := "export LC_ALL=C\n"
	if static {
		command += staticMonitorCommand
	}
	command += coreMonitorCommand
	if processes {
		command += processMonitorCommand
	}
	return command + "\nprintf '\\n__CS_END__\\n'\n"
}

type Disk struct {
	Path      string `json:"path"`
	Total     uint64 `json:"total"`
	Used      uint64 `json:"used"`
	Available uint64 `json:"available"`
}
type Stats struct {
	OS                           string              `json:"os"`
	CPUModel                     string              `json:"cpuModel"`
	Cores                        int                 `json:"cores"`
	CPU                          float64             `json:"cpu"`
	MemoryTotal                  uint64              `json:"memoryTotal"`
	MemoryUsed                   uint64              `json:"memoryUsed"`
	SwapTotal                    uint64              `json:"swapTotal"`
	SwapUsed                     uint64              `json:"swapUsed"`
	Uptime                       float64             `json:"uptime"`
	Load                         string              `json:"load"`
	NetworkRX                    float64             `json:"networkRX"`
	NetworkTX                    float64             `json:"networkTX"`
	Interfaces                   []NetworkInterface  `json:"interfaces"`
	ServerIPv4                   []string            `json:"serverIPv4"`
	ServerIPv6                   []string            `json:"serverIPv6"`
	ServerAddressInfo            []ServerAddressInfo `json:"serverAddressInfo"`
	SSHConnection                SSHConnectionInfo   `json:"sshConnection"`
	DiskRead                     float64             `json:"diskRead"`
	DiskWrite                    float64             `json:"diskWrite"`
	Latency                      float64             `json:"latency"`
	LatencyReady                 bool                `json:"latencyReady"`
	SampleReady                  bool                `json:"sampleReady"`
	Disks                        []Disk              `json:"disks"`
	Processes                    []Process           `json:"processes"`
	ProcessMemoryTop             []Process           `json:"processMemoryTop,omitempty"`
	ProcessSample                ProcessSampleInfo   `json:"processSample"`
	SampledAt                    time.Time           `json:"sampledAt"`
	Cached                       bool                `json:"cached"`
	AgeMilliseconds              int64               `json:"ageMilliseconds"`
	SamplingIntervalMilliseconds int                 `json:"samplingIntervalMilliseconds"`
	NextSampleInMilliseconds     int64               `json:"nextSampleInMilliseconds"`
	MetadataSampledAt            time.Time           `json:"metadataSampledAt"`
	MetadataIntervalMilliseconds int                 `json:"metadataIntervalMilliseconds"`
}
type rawStats struct {
	Stats
	cpuTotal, cpuIdle, rx, tx, read, write uint64
	NetworkElapsedMilliseconds             float64
	network                                map[string]interfaceCounters
	processes                              map[int]processCounters
	processBootID                          string
	processCaptured                        bool
}

func number(value string) uint64   { n, _ := strconv.ParseUint(value, 10, 64); return n }
func decimal(value string) float64 { n, _ := strconv.ParseFloat(value, 64); return n }
func delta(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}
func parseStats(data []byte, at time.Time) (rawStats, error) {
	sections := map[string][]string{}
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "__CS_") && strings.HasSuffix(line, "__") {
			section = line
			continue
		}
		if line != "" {
			sections[section] = append(sections[section], line)
		}
	}
	r := rawStats{Stats: Stats{CPUModel: "未知 CPU", OS: "Linux", SampledAt: at, Disks: []Disk{}, Processes: []Process{}, Interfaces: []NetworkInterface{}, ServerIPv4: []string{}, ServerIPv6: []string{}}}
	if len(sections["__CS_STAT__"]) == 0 || len(sections["__CS_MEM__"]) == 0 {
		return r, errors.New("无法读取 Linux /proc 监控数据；请确认远程系统类型和账号权限")
	}
	for _, line := range sections["__CS_OS__"] {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			r.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"'")
		}
	}
	for _, line := range sections["__CS_CPUINFO__"] {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "processor" {
			r.Cores++
		}
		if key == "model name" || key == "Hardware" {
			r.CPUModel = value
		}
	}
	for _, line := range sections["__CS_STAT__"] {
		fields := strings.Fields(line)
		if len(fields) >= 9 && fields[0] == "cpu" {
			for _, f := range fields[1:9] {
				r.cpuTotal += number(f)
			}
			r.cpuIdle = number(fields[4]) + number(fields[5])
			break
		}
	}
	mem := map[string]uint64{}
	for _, line := range sections["__CS_MEM__"] {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			mem[strings.TrimSuffix(fields[0], ":")] = number(fields[1]) * 1024
		}
	}
	r.MemoryTotal = mem["MemTotal"]
	available, ok := mem["MemAvailable"]
	if !ok {
		available = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
	}
	r.MemoryUsed = r.MemoryTotal - min(r.MemoryTotal, available)
	r.SwapTotal = mem["SwapTotal"]
	r.SwapUsed = r.SwapTotal - min(r.SwapTotal, mem["SwapFree"])
	if fields := strings.Fields(strings.Join(sections["__CS_UPTIME__"], " ")); len(fields) > 0 {
		r.Uptime = decimal(fields[0])
	}
	if fields := strings.Fields(strings.Join(sections["__CS_LOAD__"], " ")); len(fields) >= 3 {
		r.Load = strings.Join(fields[:3], " / ")
	}
	parseNetworkStats(&r, sections)
	blocks := map[string]bool{}
	for _, line := range sections["__CS_BLOCKS__"] {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		device := fields[0]
		if strings.HasPrefix(device, "loop") || strings.HasPrefix(device, "ram") || strings.HasPrefix(device, "zram") {
			continue
		}
		// Sum physical/leaf block-device I/O. MD, LVM and other stacked
		// devices account for the same requests again, so use sysfs topology
		// rather than assuming all top-level diskstats rows are independent.
		if len(fields) == 2 && (fields[1] == "leaf" || fields[1] == "stacked") {
			blocks[device] = fields[1] == "leaf"
		} else {
			// Older captured snapshots do not carry topology metadata.
			for _, device := range fields {
				if !strings.HasPrefix(device, "md") && !strings.HasPrefix(device, "dm-") && !strings.HasPrefix(device, "loop") && !strings.HasPrefix(device, "ram") && !strings.HasPrefix(device, "zram") {
					blocks[device] = true
				}
			}
		}
	}
	for _, line := range sections["__CS_DISKIO__"] {
		fields := strings.Fields(line)
		if len(fields) >= 10 && blocks[fields[2]] {
			r.read += number(fields[5]) * 512
			r.write += number(fields[9]) * 512
		}
	}
	seen := map[string]bool{}
	for _, line := range sections["__CS_DF__"] {
		f := strings.Fields(line)
		if len(f) < 6 || f[0] == "Filesystem" {
			continue
		}
		mount := strings.Join(f[5:], " ")
		if !strings.HasPrefix(mount, "/") || seen[mount] || strings.HasPrefix(mount, "/snap/") || strings.Contains(mount, "/docker/overlay") || strings.Contains(mount, "/containers/storage/overlay") {
			continue
		}
		total := number(f[1]) * 1024
		if total == 0 {
			continue
		}
		seen[mount] = true
		r.Disks = append(r.Disks, Disk{mount, total, number(f[2]) * 1024, number(f[3]) * 1024})
	}
	parseProcessStats(&r, sections["__CS_PROCESS__"])
	return r, nil
}

// Stats collects an explicit fresh full snapshot for programmatic callers.
// The UI route below uses the bounded display cache and optional process scan.
func (s *Session) Stats(ctx context.Context) (Stats, error) {
	return s.collectStats(ctx, true, true)
}
func (s *Session) statsForDisplay(ctx context.Context, processes bool) (Stats, error) {
	value, err := s.collectStats(ctx, processes, false)
	if err == nil && processes {
		value.ProcessMemoryTop = topProcesses(value.Processes, "memory")
		value.Processes = topProcesses(value.Processes, "cpu")
		s.refineDisplayedProcessMemory(&value)
	}
	return value, err
}
func (s *Session) collectStats(ctx context.Context, processes, force bool) (Stats, error) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if err := ctx.Err(); err != nil {
		return Stats{}, err
	}
	now := time.Now()
	processDue := processes && (force || s.processPrevious == nil || !now.Before(s.processNextSampleAt))
	if !force && now.Before(s.statsNextSampleAt) {
		if s.statsLastError != nil {
			return Stats{}, s.statsLastError
		}
		if s.previous != nil {
			// A process refresh must not resample load/CPU/disk before
			// their five-second tick. Only the due process scan runs.
			if processDue {
				if err := s.collectProcessesOnly(ctx); err != nil {
					return Stats{}, err
				}
			}
			return s.cachedDisplayStats(processes, true), nil
		}
	}
	s.statsNextSampleAt = advanceSampleDue(s.statsNextSampleAt, now, monitorMinimumInterval)
	staticDue := s.staticPrevious == nil || now.Sub(s.staticPrevious.MetadataSampledAt) >= monitorStaticInterval
	coreCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	data, err := s.coreCollector.run(coreCtx, s, "system", monitorCommandFor(staticDue, false), monitorOutputLimit)
	cancel()
	if err != nil {
		s.statsLastError = err
		return Stats{}, err
	}
	current, err := parseStats(data, time.Now())
	if err != nil {
		s.statsLastError = err
		return Stats{}, err
	}
	s.statsLastError = nil
	if staticDue {
		s.applyServerAddresses(&current.Stats)
		current.MetadataSampledAt = current.SampledAt
		snapshot := current.Stats
		s.staticPrevious = &snapshot
		s.cacheNetworkMetadata(snapshot)
	} else {
		applyStaticMetadata(&current, s.staticPrevious)
	}
	current.Processes = []Process{}
	current.ProcessSample = ProcessSampleInfo{Paused: true}
	current.ProcessSample.IntervalMilliseconds = int(monitorProcessInterval / time.Millisecond)
	if previous := s.previous; previous != nil {
		seconds := current.Uptime - previous.Uptime
		total := delta(current.cpuTotal, previous.cpuTotal)
		idle := delta(current.cpuIdle, previous.cpuIdle)
		if total > 0 {
			current.CPU = 100 * float64(total-min(total, idle)) / float64(total)
		}
		if seconds > 0 {
			applyInterfaceRates(&current, previous, seconds)
			current.NetworkRX = float64(delta(current.rx, previous.rx)) / seconds
			current.NetworkTX = float64(delta(current.tx, previous.tx)) / seconds
			current.DiskRead = float64(delta(current.read, previous.read)) / seconds
			current.DiskWrite = float64(delta(current.write, previous.write)) / seconds
			current.SampleReady = true
		}
	}
	s.previous = &current
	if processDue {
		if err := s.collectProcessesOnly(ctx); err != nil {
			return Stats{}, err
		}
	}
	return s.cachedDisplayStats(processes, false), nil
}

// Called with statsMu held. Process-only refreshes leave the core sample's
// timestamp and load untouched; requests cannot increase sampling frequency.
func (s *Session) collectProcessesOnly(ctx context.Context) error {
	processCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	started := time.Now()
	data, err := s.processCollector.run(processCtx, s, "processes", "export LC_ALL=C\n"+processMonitorCommand, processOutputLimit)
	if err != nil {
		// Keep core CPU/memory/disk samples usable when the optional process
		// scan fails. Back off retries; a stuck worker cannot multiply on clicks.
		s.failedProcessSample("进程采集未完成：" + err.Error())
		s.processNextSampleAt = time.Now().Add(30 * time.Second)
		return ctx.Err()
	}
	current := rawStats{Stats: Stats{SampledAt: time.Now()}}
	lines := []string{}
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "__CS_PROCESS__" {
			inSection = true
			continue
		}
		if inSection && line != "" {
			lines = append(lines, line)
		}
	}
	parseProcessStats(&current, lines)
	if !current.ProcessSample.Available {
		s.failedProcessSample(current.ProcessSample.Error)
		s.processNextSampleAt = time.Now().Add(30 * time.Second)
		return nil
	}
	current.ProcessSample.SampledAt = current.SampledAt
	interval := processSampleInterval(len(current.Processes), time.Since(started))
	current.ProcessSample.IntervalMilliseconds = int(interval / time.Millisecond)
	if s.processPrevious != nil {
		applyProcessRates(&current, s.processPrevious)
	}
	s.processPrevious = &current
	// Long scans leave an idle interval instead of immediately starting again.
	s.processNextSampleAt = time.Now().Add(interval)
	return nil
}

func processSampleInterval(count int, elapsed time.Duration) time.Duration {
	interval := monitorProcessInterval
	if count > 5000 {
		interval = 10 * time.Second
	}
	if count > 20000 {
		interval = 20 * time.Second
	}
	if count > 50000 {
		interval = 30 * time.Second
	}
	return max(interval, min(time.Minute, 3*elapsed))
}

// Retain the last successful immutable snapshot and CPU baseline on failure;
// the list can still be searched/paged while explicitly displaying stale data.
func (s *Session) failedProcessSample(message string) {
	next := rawStats{Stats: Stats{Processes: []Process{}}}
	if s.processPrevious != nil {
		next = *s.processPrevious
	}
	next.ProcessSample.Available = false
	next.ProcessSample.Error = message
	next.ProcessSample.IntervalMilliseconds = 30000
	s.processPrevious = &next
}

func (s *Session) cachedDisplayStats(processes, cached bool) Stats {
	value := s.previous.Stats
	due := s.statsNextSampleAt
	if processes && s.processPrevious != nil {
		value.Processes, value.ProcessSample = s.processPrevious.Processes, s.processPrevious.ProcessSample
		if s.processNextSampleAt.Before(due) {
			due = s.processNextSampleAt
		}
	}
	value.NextSampleInMilliseconds = nextSampleMilliseconds(due)
	return displayStats(value, processes, cached, s.Latency())
}

func displayStats(value Stats, processes, cached bool, latency LatencySample) Stats {
	value.Cached = cached
	value.AgeMilliseconds = max(0, time.Since(value.SampledAt).Milliseconds())
	value.SamplingIntervalMilliseconds = int(monitorMinimumInterval / time.Millisecond)
	value.MetadataIntervalMilliseconds = int(monitorStaticInterval / time.Millisecond)
	value.Latency, value.LatencyReady = latency.Milliseconds, latency.Ready
	value.Interfaces = append([]NetworkInterface{}, value.Interfaces...)
	for i := range value.Interfaces {
		value.Interfaces[i].Addresses = append([]string{}, value.Interfaces[i].Addresses...)
	}
	value.ServerIPv4 = append([]string{}, value.ServerIPv4...)
	value.ServerIPv6 = append([]string{}, value.ServerIPv6...)
	value.ServerAddressInfo = append([]ServerAddressInfo{}, value.ServerAddressInfo...)
	value.Disks = append([]Disk{}, value.Disks...)
	if processes {
		value.Processes = append([]Process{}, value.Processes...)
		value.ProcessSample.Cached = value.ProcessSample.Cached || cached
	} else {
		value.Processes = []Process{}
		value.ProcessSample = ProcessSampleInfo{Paused: true, IntervalMilliseconds: int(monitorProcessInterval / time.Millisecond)}
	}
	return value
}

func applyStaticMetadata(current *rawStats, previous *Stats) {
	current.OS, current.CPUModel, current.Cores = previous.OS, previous.CPUModel, previous.Cores
	current.MetadataSampledAt = previous.MetadataSampledAt
	current.SSHConnection = previous.SSHConnection
	current.ServerIPv4 = append([]string{}, previous.ServerIPv4...)
	current.ServerIPv6 = append([]string{}, previous.ServerIPv6...)
	current.ServerAddressInfo = append([]ServerAddressInfo{}, previous.ServerAddressInfo...)
	current.Disks = append([]Disk{}, previous.Disks...)
	for i := range current.Interfaces {
		iface := &current.Interfaces[i]
		for _, old := range previous.Interfaces {
			if old.Name == iface.Name {
				iface.Up, iface.State, iface.Default, iface.Loopback = old.Up, old.State, old.Default, old.Loopback
				iface.Addresses = append([]string{}, old.Addresses...)
				break
			}
		}
	}
	sortNetworkInterfaces(current.Interfaces)
}
func (a *App) statsHTTP(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	stats, err := s.statsForDisplay(r.Context(), r.URL.Query().Get("processes") != "0")
	respond(w, stats, err)
}
