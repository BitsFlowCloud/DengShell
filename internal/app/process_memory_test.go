package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPreciseMemoryUsesRollupFallbackAndRejectsPIDReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("POSIX procfs fixture")
	}
	root := t.TempDir()
	put := func(name, value string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put("sys/kernel/random/boot_id", "aabbcc\n")
	stat := func(pid int, start string) string {
		fields := make([]string, 22)
		for i := range fields {
			fields[i] = "0"
		}
		fields[0] = "S"
		fields[19] = start
		return fmt.Sprintf("%d (name (with) parens) %s\n", pid, strings.Join(fields, " "))
	}
	wanted := map[int]processMemoryKey{}
	for pid := 1; pid <= 4; pid++ {
		wanted[pid] = processMemoryKey{pid, 123, "aabbcc"}
		put(fmt.Sprintf("%d/stat", pid), stat(pid, "123"))
	}
	put("1/smaps_rollup", "Rss: 12345 kB\nPss: 9000 kB\n")
	put("1/smaps", "Rss: 99999 kB\n") // Rollup must be preferred.
	put("2/smaps", "Rss: 12 kB\nPss: 3 kB\nRss: 34 kB\n")
	put("3/smaps_rollup", "Rss: 800 kB\n")
	put("3/stat", stat(3, "456"))
	put("4/smaps_rollup", "Pss: 100 kB\n") // Missing RSS cannot be zero.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-c", strings.ReplaceAll(preciseMemoryCommand(wanted), "/proc/", filepath.ToSlash(root)+"/"))
	data, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	reader := &processMemoryOutput{wanted: wanted, values: map[processMemoryKey]processMemoryValue{}}
	// Arbitrary SSH packet boundaries, including inside a number.
	for _, b := range data {
		_, _ = reader.Write([]byte{b})
	}
	got := reader.snapshot()
	if v := got[wanted[1]]; v.bytes != 12345*1024 || v.source != "smaps_rollup" || v.at.IsZero() {
		t.Fatalf("wrong rollup: %+v / %s", v, data)
	}
	if v := got[wanted[2]]; v.bytes != 46*1024 || v.source != "smaps" {
		t.Fatalf("wrong fallback: %+v / %s", v, data)
	}
	for _, pid := range []int{3, 4} {
		if !got[wanted[pid]].at.IsZero() {
			t.Fatalf("accepted unavailable or reused PID %d", pid)
		}
	}
	if strings.Contains(preciseMemoryCommand(wanted), "/proc/[0-9]") {
		t.Fatal("precise path scans all PIDs")
	}
}

func TestPreciseMemoryNeverExposesEstimateAndRetainsDatedSample(t *testing.T) {
	key := processMemoryKey{7, 99, "boot"}
	at := time.Now().Add(-12 * time.Second)
	s := &Session{} // No transport: exercise display projection without a worker.
	s.preciseMemory.values = map[processMemoryKey]processMemoryValue{key: {bytes: 123456, source: "smaps_rollup", at: at}}
	s.preciseMemory.failed = true
	s.preciseMemory.attemptedAt = time.Now()
	stats := Stats{Processes: []Process{{PID: 7, startTicks: 99, bootID: "boot", Memory: 999, MemoryReady: true, MemoryEstimated: true}, {PID: 8, startTicks: 99, bootID: "boot", Memory: 555, MemoryReady: true, MemoryEstimated: true}}}
	s.refineDisplayedProcessMemory(&stats)
	if p := stats.Processes[0]; p.Memory != 123456 || !p.MemoryReady || p.MemoryEstimated || !p.MemorySampledAt.Equal(at) || p.MemoryError == "" {
		t.Fatalf("lost precise dated snapshot: %+v", p)
	}
	if p := stats.Processes[1]; p.MemoryReady || p.Memory != 0 || p.MemoryEstimated || p.MemoryError == "" {
		t.Fatalf("estimate leaked: %+v", p)
	}
	stats.Processes[0].startTicks++
	s.refineDisplayedProcessMemory(&stats)
	if stats.Processes[0].MemoryReady {
		t.Fatal("old PID memory reused for a new process")
	}
}

func TestPreciseMemorySlowWorkerDoesNotBlockStatsOrSSH(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	s, _ := ownerSSHFixture(t, func(command string) string {
		if strings.Contains(command, "dengshell_memory_file") {
			close(entered)
			<-release
			return "K\tboot\nI\t7\nP\t7\t99\t123456\tsmaps_rollup\n"
		}
		return "healthy"
	})
	defer close(release)
	stats := Stats{Processes: []Process{{PID: 7, startTicks: 99, bootID: "boot", Memory: 999, MemoryReady: true, MemoryEstimated: true}}}
	started := time.Now()
	s.refineDisplayedProcessMemory(&stats)
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("display waited for precise scan")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("memory worker not started")
	}
	for i := 0; i < 20; i++ {
		s.refineDisplayedProcessMemory(&stats)
	}
	if _, _, err := s.client.SendRequest("keepalive@openssh.com", true, nil); err != nil || s.ctx.Err() != nil {
		t.Fatalf("memory scan damaged SSH: %v", err)
	}
}
