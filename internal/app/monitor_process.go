package app

import (
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"time"
)

// One awk process reads procfs directly. It does not fork a command for each
// process. A per-PID /proc/uptime timestamp excludes SSH and collection delay
// from CPU percentages; clock ticks and page size come from the remote host.
// Hex-encoding comm preserves spaces, parentheses, tabs and newlines without
// allowing process names to interfere with the monitor record framing.
const processMonitorCommand = `
printf '\n__CS_PROCESS__\n'
dengshell_hz=$(getconf CLK_TCK 2>/dev/null)
dengshell_pagesize=$(getconf PAGESIZE 2>/dev/null)
for dengshell_proc in /proc/[0-9]*/stat; do printf '%s\n' "$dengshell_proc"; done |
awk -v hz="$dengshell_hz" -v pages="$dengshell_pagesize" '
function now( text, parts, value) {
    value = -1
    if ((getline text < "/proc/uptime") > 0) {
        split(text, parts, " ")
        value = parts[1]
    }
    close("/proc/uptime")
    return value
}
function readstat(path, text, line) {
    text = ""
    while ((getline line < path) > 0) {
        if (text != "") text = text "\n"
        text = text line
    }
    close(path)
    return text
}
function fields(text, out, left, right, i, tail) {
    split("", out)
    left = index(text, "(")
    right = 0
    for (i = length(text); i > left; i--) {
        if (substr(text, i, 1) == ")") { right = i; break }
    }
    if (left < 2 || right <= left) return 0
    tail = substr(text, right + 2)
    if (split(tail, out, " ") < 22) return 0
    if (out[12] !~ /^[0-9]+$/ || out[13] !~ /^[0-9]+$/ || out[20] !~ /^[0-9]+$/) return 0
    out[0] = substr(text, left + 1, right - left - 1)
    return 1
}
function encode(text, i, result) {
    result = ""
    for (i = 1; i <= length(text); i++) result = result sprintf("%02x", ord[substr(text, i, 1)])
    return result
}
BEGIN {
    for (i = 1; i < 256; i++) ord[sprintf("%c", i)] = i
    boot = ""
    getline boot < "/proc/sys/kernel/random/boot_id"
    close("/proc/sys/kernel/random/boot_id")
    if (boot == "") {
        while ((getline line < "/proc/stat") > 0) {
            if (line ~ /^btime /) { boot = line; break }
        }
        close("/proc/stat")
    }
    start = now()
    printf "M\t%s\t%s\t%s\t%s\n", hz, pages, boot, start
}
/^\/proc\/[0-9]+\/stat$/ {
    path = $0
    split(path, components, "/")
    pid = components[3]
    visible++
    if (!fields(readstat(path), first)) {
        unavailable++
        printf "U\t%s\n", pid
        next
    }
    memory = first[22]
    source = "stat"
    rollup = "/proc/" pid "/smaps_rollup"
    while ((getline line < rollup) > 0) {
        if (line ~ /^Rss:[ \t]+[0-9]+[ \t]+kB/) {
            split(line, values, " ")
            memory = sprintf("%.0f", values[2] * 1024)
            source = "smaps_rollup"
            break
        }
    }
    close(rollup)
    if (source == "stat") {
        status = "/proc/" pid "/status"
        while ((getline line < status) > 0) {
            if (line ~ /^VmRSS:[ \t]+[0-9]+[ \t]+kB/) {
                split(line, values, " ")
                memory = sprintf("%.0f", values[2] * 1024)
                source = "status"
                break
            }
        }
        close(status)
    }
    if (!fields(readstat(path), last) || first[20] != last[20]) {
        unavailable++
        printf "U\t%s\n", pid
        next
    }
    if (source == "stat") memory = last[22]
    readable++
    printf "P\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", pid, last[12], last[13], last[20], now(), memory, source, last[7], last[1], encode(last[0])
}
END {
    printf "S\t%d\t%d\t%d\t%s\n", visible, readable, unavailable, now()
}
' 2>/dev/null
`

type Process struct {
	PID             int     `json:"pid"`
	Name            string  `json:"name"`
	Memory          uint64  `json:"memory"`
	MemoryReady     bool    `json:"memoryReady"`
	MemorySource    string  `json:"memorySource"`
	MemoryEstimated bool    `json:"memoryEstimated"`
	CPU             float64 `json:"cpu"`
	CPUReady        bool    `json:"cpuReady"`
	State           string  `json:"state"`
}

type ProcessSampleInfo struct {
	SampledAt              time.Time `json:"sampledAt"`
	Cached                 bool      `json:"cached"`
	Paused                 bool      `json:"paused"`
	IntervalMilliseconds   int       `json:"intervalMilliseconds"`
	Available              bool      `json:"available"`
	Ready                  bool      `json:"ready"`
	Visible                int       `json:"visible"`
	Readable               int       `json:"readable"`
	Unreadable             int       `json:"unreadable"`
	CPUReadyCount          int       `json:"cpuReadyCount"`
	ClockTicks             uint64    `json:"clockTicks"`
	PageSize               uint64    `json:"pageSize"`
	SampledAtUptime        float64   `json:"sampledAtUptime"`
	ElapsedSeconds         float64   `json:"elapsedSeconds"`
	CollectionMilliseconds float64   `json:"collectionMilliseconds"`
	Error                  string    `json:"error,omitempty"`
}

type processCounters struct {
	user, system, start uint64
	uptime              float64
}

func parseProcessStats(r *rawStats, lines []string) {
	r.processes = map[int]processCounters{}
	var collectionStart float64
	metadata, summary := false, false
	seen := map[int]bool{}
	for _, line := range lines {
		f := strings.Split(line, "\t")
		switch f[0] {
		case "M":
			if len(f) != 5 {
				continue
			}
			r.ProcessSample.ClockTicks = number(f[1])
			r.ProcessSample.PageSize = number(f[2])
			r.processBootID = f[3]
			collectionStart = decimal(f[4])
			metadata = collectionStart >= 0
		case "S":
			if len(f) != 5 {
				continue
			}
			r.ProcessSample.Visible, _ = strconv.Atoi(f[1])
			r.ProcessSample.Readable, _ = strconv.Atoi(f[2])
			r.ProcessSample.Unreadable, _ = strconv.Atoi(f[3])
			r.ProcessSample.SampledAtUptime = decimal(f[4])
			summary = true
		case "P", "U":
			// parseStats trims line endings; an empty Linux comm leaves the final
			// hex field empty, which is a valid name rather than a broken PID.
			if f[0] == "P" && len(f) == 10 {
				f = append(f, "")
			}
			if len(f) < 2 {
				continue
			}
			pid, err := strconv.Atoi(f[1])
			if err != nil || pid <= 0 || seen[pid] {
				continue
			}
			seen[pid] = true
			process := Process{PID: pid, Name: "PID " + f[1], MemorySource: "unavailable"}
			if f[0] == "P" && len(f) == 11 {
				user, uErr := strconv.ParseUint(f[2], 10, 64)
				system, sErr := strconv.ParseUint(f[3], 10, 64)
				start, stErr := strconv.ParseUint(f[4], 10, 64)
				uptime, tErr := strconv.ParseFloat(f[5], 64)
				name, nErr := hex.DecodeString(f[10])
				if uErr == nil && sErr == nil && stErr == nil && tErr == nil && uptime >= 0 && !math.IsInf(uptime, 0) && !math.IsNaN(uptime) && nErr == nil {
					if len(name) != 0 {
						process.Name = strings.ToValidUTF8(string(name), "�")
					}
					process.State = f[9]
					r.processes[pid] = processCounters{user, system, start, uptime}
					memory, mErr := strconv.ParseUint(f[6], 10, 64)
					flags, _ := strconv.ParseUint(f[8], 10, 64)
					if mErr == nil {
						process.MemorySource = f[7]
						switch f[7] {
						case "smaps_rollup":
							process.Memory, process.MemoryReady = memory, true
						case "status":
							process.Memory, process.MemoryReady, process.MemoryEstimated = memory, true, true
						case "stat":
							if r.ProcessSample.PageSize > 0 && memory <= math.MaxUint64/r.ProcessSample.PageSize {
								process.Memory, process.MemoryReady, process.MemoryEstimated = memory*r.ProcessSample.PageSize, true, true
							}
						default:
							process.MemorySource = "unavailable"
						}
						if process.MemoryEstimated && memory == 0 {
							// PF_KTHREAD and exited processes can have a known empty
							// address space. For a live user process, asynchronous RSS
							// counters returning zero are not evidence of no memory.
							if flags&0x00200000 != 0 || process.State == "Z" || process.State == "X" {
								process.MemoryEstimated = false
							} else {
								process.MemoryReady = false
							}
						}
					}
				}
			}
			r.Processes = append(r.Processes, process)
		}
	}
	r.processCaptured = metadata && summary
	r.ProcessSample.Available = r.processCaptured
	if r.processCaptured {
		r.ProcessSample.CollectionMilliseconds = math.Max(0, (r.ProcessSample.SampledAtUptime-collectionStart)*1000)
	}
	if !r.processCaptured {
		r.ProcessSample.Error = "进程采样不可用，请检查远端 awk 和 /proc 读取权限"
	} else if r.ProcessSample.ClockTicks == 0 || r.processBootID == "" {
		r.ProcessSample.Error = "无法读取远端时钟频率或启动标识，CPU 暂不可计算"
	} else if r.ProcessSample.Unreadable > 0 {
		r.ProcessSample.Error = "部分可见进程已退出或当前账号无权读取"
	}
}

func applyProcessRates(current, previous *rawStats) {
	if !current.processCaptured || !previous.processCaptured || current.processBootID == "" || current.processBootID != previous.processBootID || current.ProcessSample.ClockTicks == 0 || current.ProcessSample.ClockTicks != previous.ProcessSample.ClockTicks {
		return
	}
	elapsed := current.ProcessSample.SampledAtUptime - previous.ProcessSample.SampledAtUptime
	if elapsed <= 0 {
		return
	}
	current.ProcessSample.ElapsedSeconds = elapsed
	for i := range current.Processes {
		process := &current.Processes[i]
		now, exists := current.processes[process.PID]
		before, existed := previous.processes[process.PID]
		if !exists || !existed || now.start != before.start || now.user < before.user || now.system < before.system {
			continue
		}
		seconds := now.uptime - before.uptime
		if seconds <= 0 {
			continue
		}
		// utime already includes guest time; child CPU and guest time must not
		// be added again. 100% means one fully occupied logical CPU, like top.
		process.CPU = (float64(now.user-before.user) + float64(now.system-before.system)) / float64(current.ProcessSample.ClockTicks) / seconds * 100
		process.CPUReady = true
		current.ProcessSample.CPUReadyCount++
	}
	current.ProcessSample.Ready = current.ProcessSample.CPUReadyCount > 0
}
