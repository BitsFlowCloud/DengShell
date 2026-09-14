package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kernel RSS counters select candidates cheaply. Only the CPU/memory five-row
// candidate lists get a page-table walk; no estimated value reaches the UI.
// Rankings are sampled lists, not a simultaneous all-process memory snapshot.
type processMemoryKey struct {
	pid   int
	start uint64
	boot  string
}
type processMemoryValue struct {
	bytes  uint64
	source string
	at     time.Time
	tried  time.Time
}
type preciseProcessMemory struct {
	mu          sync.Mutex
	runner      monitorCommandRunner
	running     bool
	next        time.Time
	values      map[processMemoryKey]processMemoryValue
	failed      bool
	attemptedAt time.Time
}

func (s *Session) refineDisplayedProcessMemory(stats *Stats) {
	cache := &s.preciseMemory
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.values == nil {
		cache.values = make(map[processMemoryKey]processMemoryValue)
	}
	requested := make(map[int]processMemoryKey)
	for _, list := range [][]Process{stats.Processes, stats.ProcessMemoryTop} {
		for i := range list {
			p := &list[i]
			key := processMemoryKey{p.PID, p.startTicks, p.bootID}
			if p.MemoryReady && !p.MemoryEstimated && p.Memory == 0 && p.MemorySource == "stat" {
				p.MemorySource, p.MemorySampledAt = "kernel", stats.ProcessSample.SampledAt
				continue
			}
			if p.PID > 0 && p.bootID != "" {
				requested[p.PID] = key
			}
			p.Memory, p.MemoryReady, p.MemoryEstimated, p.MemorySource = 0, false, false, "unavailable"
			p.MemoryError = "等待精确内存采样"
			if cache.failed {
				p.MemoryError = "精确内存采样未完成，等待重试"
			}
			if value, ok := cache.values[key]; ok && !value.at.IsZero() && time.Since(value.at) < 2*time.Minute {
				p.Memory, p.MemoryReady, p.MemorySource, p.MemorySampledAt = value.bytes, true, value.source, value.at
				p.MemoryError = ""
				if cache.failed && value.at.Before(cache.attemptedAt) {
					p.MemoryError = "本次精读未完成，显示上次成功采样"
				}
			}
		}
	}
	if len(requested) == 0 || cache.running || time.Now().Before(cache.next) || s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	cache.running = true
	cache.attemptedAt = time.Now()
	order := make([]int, 0, len(requested))
	for pid := range requested {
		order = append(order, pid)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := cache.values[requested[order[i]]].tried, cache.values[requested[order[j]]].tried
		if !a.Equal(b) {
			return a.Before(b)
		}
		return order[i] < order[j]
	})
	command := preciseMemoryCommand(requested, order)
	go func() {
		started := time.Now()
		ctx, cancel := context.WithTimeout(s.ctx, 6*time.Second)
		defer cancel()
		reader := &processMemoryOutput{wanted: requested, values: make(map[processMemoryKey]processMemoryValue)}
		_, err := cache.runner.runWithOutput(ctx, s, "process-memory", command, 64<<10, reader)
		values := reader.snapshot()
		cache.mu.Lock()
		defer cache.mu.Unlock()
		completed := 0
		for _, value := range values {
			if !value.at.IsZero() {
				completed++
			}
		}
		cache.running, cache.failed = false, err != nil || completed < len(requested)
		for key, value := range cache.values {
			if time.Since(value.at) >= 2*time.Minute && time.Since(value.tried) >= 2*time.Minute || len(cache.values) > 128 {
				delete(cache.values, key)
			}
		}
		for key, value := range values {
			if value.at.IsZero() {
				old := cache.values[key]
				old.tried = value.tried
				value = old
			}
			cache.values[key] = value
		}
		interval := max(5*time.Second, min(time.Minute, 3*time.Since(started)))
		if err != nil {
			interval = max(interval, 30*time.Second)
		}
		cache.next = time.Now().Add(interval)
	}()
}

// Streaming retains completed PIDs when a later large process exceeds budget.
// The writer stays safe if channel cleanup outlives the caller's deadline.
type processMemoryOutput struct {
	mu      sync.Mutex
	pending string
	boot    string
	wanted  map[int]processMemoryKey
	values  map[processMemoryKey]processMemoryValue
}

func (w *processMemoryOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending += string(data)
	for {
		line, rest, ok := strings.Cut(w.pending, "\n")
		if !ok {
			break
		}
		w.pending = rest
		fields := strings.Split(line, "\t")
		if len(fields) == 2 && fields[0] == "K" {
			w.boot = fields[1]
			continue
		}
		if len(fields) == 2 && fields[0] == "I" {
			pid, err := strconv.Atoi(fields[1])
			key, ok := w.wanted[pid]
			if err == nil && ok && key.boot == w.boot {
				w.values[key] = processMemoryValue{tried: time.Now()}
			}
			continue
		}
		if len(fields) != 5 || fields[0] != "P" {
			continue
		}
		pid, e1 := strconv.Atoi(fields[1])
		start, e2 := strconv.ParseUint(fields[2], 10, 64)
		bytes, e3 := strconv.ParseUint(fields[3], 10, 64)
		key, ok := w.wanted[pid]
		if !ok || e1 != nil || e2 != nil || e3 != nil || key.start != start || key.boot != w.boot || (fields[4] != "smaps_rollup" && fields[4] != "smaps") {
			continue
		}
		w.values[key] = processMemoryValue{bytes: bytes, source: fields[4], at: time.Now(), tried: time.Now()}
	}
	if len(w.pending) > 1024 {
		w.pending = ""
	}
	return len(data), nil
}
func (w *processMemoryOutput) snapshot() map[processMemoryKey]processMemoryValue {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make(map[processMemoryKey]processMemoryValue, len(w.values))
	for k, v := range w.values {
		result[k] = v
	}
	return result
}

func preciseMemoryCommand(wanted map[int]processMemoryKey, ordered ...[]int) string {
	var pids []string
	var ids []int
	if len(ordered) > 0 {
		ids = ordered[0]
	} else {
		for pid := range wanted {
			ids = append(ids, pid)
		}
		sort.Ints(ids)
	}
	for _, pid := range ids {
		if pid > 0 && len(pids) < 10 {
			pids = append(pids, strconv.Itoa(pid))
		}
	}
	return fmt.Sprintf(`export LC_ALL=C
dengshell_memory_file() {
    dengshell_line=
    while IFS= read -r dengshell_line || [ -n "$dengshell_line" ]; do
        printf '%%s\t%%s\n' "$1" "$dengshell_line"
        dengshell_line=
    done < "$2"
}
{
    if [ -r /proc/sys/kernel/random/boot_id ]; then
        dengshell_memory_file K /proc/sys/kernel/random/boot_id
    else
        dengshell_memory_file K /proc/stat
    fi
    for dengshell_pid in %s; do
        printf 'I\t%%s\n' "$dengshell_pid"
        dengshell_memory_file A "/proc/$dengshell_pid/stat" 2>/dev/null
        if [ -e "/proc/$dengshell_pid/smaps_rollup" ]; then
            printf 'F\tsmaps_rollup\n'
            dengshell_memory_file R "/proc/$dengshell_pid/smaps_rollup" 2>/dev/null
        else
            printf 'F\tsmaps\n'
            dengshell_memory_file R "/proc/$dengshell_pid/smaps" 2>/dev/null
        fi
        dengshell_memory_file B "/proc/$dengshell_pid/stat" 2>/dev/null
        printf 'E\n'
    done
} | awk '
function start(text, fields, right,i) {
    right=0
    for(i=length(text);i>0;i--) if(substr(text,i,1)==")") {right=i;break}
    if(!right || split(substr(text,right+2),fields," ")<22 || fields[20]!~/^[0-9]+$/) return ""
    return fields[20]
}
/^K\t/ { value=substr($0,3); if(value~/^[0-9a-f-]+$/ || value~/^btime /) {printf "K\t%%s\n",value;fflush()} }
/^I\t/ {pid=$2;first="";last="";rss=0;rows=0;source="";printf "I\t%%s\n",pid;fflush();next}
/^A\t/ {first=first (first==""?"":"\n") substr($0,3);next}
/^B\t/ {last=last (last==""?"":"\n") substr($0,3);next}
/^F\t/ {source=$2;next}
/^R\tRss:/ {if($3~/^[0-9]+$/ && $4=="kB") {rss+=$3;rows++};next}
/^E$/ {a=start(first);b=start(last);if(rows>0 && a!="" && a==b) {printf "P\t%%s\t%%s\t%%.0f\t%%s\n",pid,a,rss*1024,source;fflush()}}
' 2>/dev/null
`, strings.Join(pids, " "))
}
