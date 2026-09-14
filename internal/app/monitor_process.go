package app

import (
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A POSIX shell reads each process stat once and frames it for one awk
// parser. CPU, RSS, flags and start time come from the same small record;
// no status/smaps files or page-table walks are needed. Unlike mawk getline,
// a failed read (ESRCH/EIO/permission/racing exit) cannot abort all PIDs.
// No cat/awk subprocess is forked for each process.
const processMonitorCommand = `
printf '\n__CS_PROCESS__\n'
dengshell_hz=$(getconf CLK_TCK 2>/dev/null)
dengshell_pagesize=$(getconf PAGESIZE 2>/dev/null)
dengshell_process_file() {
    dengshell_line=
    while IFS= read -r dengshell_line || [ -n "$dengshell_line" ]; do
        printf '%s\t%s\n' "$1" "$dengshell_line"
        dengshell_line=
    done < "$2"
}
for dengshell_proc in /proc/[0-9]*/stat; do
    dengshell_dir=${dengshell_proc%/stat}
    dengshell_pid=${dengshell_dir##*/}
    case "$dengshell_pid" in ''|*[!0-9]*) continue;; esac
    printf 'I\t%s\n' "$dengshell_pid"
    dengshell_process_file A "$dengshell_proc" 2>/dev/null
    dengshell_uptime=
    read -r dengshell_uptime dengshell_rest < /proc/uptime 2>/dev/null
    printf 'E\t%s\n' "$dengshell_uptime"
done |
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
    printf "M\t%s\t%s\t%s\t%s\n", hz, pages, boot, now()
}
/^I\t[0-9]+$/ { pid=$2; first_text=""; source="stat"; next }
/^A\t/ { first_text=first_text (first_text=="" ? "" : "\n") substr($0,3); next }
/^E\t/ {
    visible++
    if (!fields(first_text, last)) {
        unavailable++
        printf "U\t%s\n", pid
        next
    }
    memory=last[22]
    sampled=($2 ~ /^[0-9]+([.][0-9]+)?$/ ? $2 : -1)
    readable++
    printf "P\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", pid, last[12], last[13], last[20], sampled, memory, source, last[7], last[1], encode(last[0])
}
END {
    printf "S\t%d\t%d\t%d\t%s\n", visible, readable, unavailable, now()
}
' 2>/dev/null
`

type Process struct {
	PID             int       `json:"pid"`
	Name            string    `json:"name"`
	Memory          uint64    `json:"memory"`
	MemoryReady     bool      `json:"memoryReady"`
	MemorySource    string    `json:"memorySource"`
	MemoryEstimated bool      `json:"memoryEstimated"`
	MemorySampledAt time.Time `json:"memorySampledAt,omitempty"`
	MemoryError     string    `json:"memoryError,omitempty"`
	CPU             float64   `json:"cpu"`
	CPUReady        bool      `json:"cpuReady"`
	State           string    `json:"state"`
	startTicks      uint64
	bootID          string
}

// Keep full lightweight counters in the backend for correct interval CPU/PID
// reuse detection; only the two five-row rankings cross into the renderer.
func topProcesses(processes []Process, metric string) []Process {
	result := append([]Process{}, processes...)
	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if metric == "memory" {
			if a.MemoryReady != b.MemoryReady {
				return a.MemoryReady
			}
			if a.Memory != b.Memory {
				return a.Memory > b.Memory
			}
		} else {
			if a.CPUReady != b.CPUReady {
				return a.CPUReady
			}
			if a.CPU != b.CPU {
				return a.CPU > b.CPU
			}
		}
		if a.MemoryReady != b.MemoryReady {
			return a.MemoryReady
		}
		if a.Memory != b.Memory {
			return a.Memory > b.Memory
		}
		return a.PID < b.PID
	})
	return append([]Process{}, result[:min(5, len(result))]...)
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
					process.startTicks, process.bootID = start, r.processBootID
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
