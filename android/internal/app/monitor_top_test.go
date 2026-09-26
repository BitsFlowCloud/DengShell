package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProcessDisplayReturnsOnlyTopFiveAndFailureKeepsCoreStats(t *testing.T) {
	var round atomic.Int32
	var broken atomic.Bool
	s, calls := ownerSSHFixture(t, func(command string) string {
		if !strings.Contains(command, "__CS_PROCESS__") {
			return "__CS_STAT__\ncpu 100 0 0 100 0 0 0 0\n__CS_MEM__\nMemTotal: 1024 kB\nMemAvailable: 512 kB\n__CS_UPTIME__\n100 0\n__CS_LOAD__\n0 0 0\n"
		}
		if strings.Contains(command, "__CS_BLOCKS__") {
			t.Error("process collector still shares the core command")
		}
		if broken.Load() {
			return "incomplete process result"
		}
		n := int(round.Add(1))
		var result strings.Builder
		fmt.Fprintf(&result, "__CS_PROCESS__\nM\t100\t4096\tfixture-boot\t%d\n", 100+n)
		for pid := 1; pid <= 1000; pid++ {
			result.WriteString(processRecord(pid, uint64(n*pid*100), 0, 1, float64(100+n), uint64(1001-pid), "stat", 0, "S", fmt.Sprint(pid)) + "\n")
		}
		fmt.Fprintf(&result, "S\t1000\t1000\t0\t%d\n", 100+n)
		return result.String()
	})
	first, err := s.statsForDisplay(context.Background(), true)
	if err != nil || len(first.Processes) != 5 || len(first.ProcessMemoryTop) != 5 || first.ProcessSample.Visible != 1000 {
		t.Fatalf("display response was not bounded: %+v %v", first.ProcessSample, err)
	}
	s.processNextSampleAt = time.Now().Add(-time.Second)
	next, err := s.statsForDisplay(context.Background(), true)
	if err != nil || next.Processes[0].PID != 1000 || next.ProcessMemoryTop[0].PID != 1 || !next.Processes[0].CPUReady || !next.SampledAt.Equal(first.SampledAt) {
		t.Fatalf("rankings or separate core cache incorrect: %+v %v", next.ProcessSample, err)
	}
	broken.Store(true)
	s.processNextSampleAt = time.Now().Add(-time.Second)
	failed, err := s.statsForDisplay(context.Background(), true)
	if err != nil || failed.MemoryTotal != 1024*1024 || failed.ProcessSample.Available || failed.ProcessSample.Error == "" || time.Until(s.processNextSampleAt) < 29*time.Second || s.ctx.Err() != nil {
		t.Fatalf("process error damaged core/SSH or lacked backoff: %+v %v", failed.ProcessSample, err)
	}
	before := calls.Load()
	if _, err = s.statsForDisplay(context.Background(), true); err != nil || calls.Load() != before {
		t.Fatal("immediate reopen retried a failed process collector", err)
	}
}

func TestProcessTopFiveRankingsAreIndependentAndKeepFullBaseline(t *testing.T) {
	var processes []Process
	for i := 1; i <= 1000; i++ {
		processes = append(processes, Process{PID: i, Name: fmt.Sprint(i), CPU: float64(i), CPUReady: true, Memory: uint64(1001 - i), MemoryReady: true})
	}
	processes = append(processes, Process{PID: 1001, CPU: 999999, Memory: 999999}) // unknown values must not win
	cpu, memory := topProcesses(processes, "cpu"), topProcesses(processes, "memory")
	if len(cpu) != 5 || len(memory) != 5 || cpu[0].PID != 1000 || cpu[4].PID != 996 || memory[0].PID != 1 || memory[4].PID != 5 {
		t.Fatalf("wrong rankings: %+v / %+v", cpu, memory)
	}
	cpu[0].Name = "changed"
	if len(processes) != 1001 || processes[0].PID != 1 || processes[999].Name != "1000" {
		t.Fatal("display rankings mutated the full sampling baseline")
	}
}

func TestProcessCollectorSixThousandLightweightRecords(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux procfs fixture")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sys/kernel/random"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"uptime": "100.50 0\n", "sys/kernel/random/boot_id": "fixture-boot\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for pid := 1; pid <= 6000; pid++ {
		path := filepath.Join(dir, fmt.Sprint(pid))
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		fields := make([]string, 22)
		for i := range fields {
			fields[i] = "0"
		}
		fields[0], fields[11], fields[12], fields[19], fields[21] = "S", fmt.Sprint(pid), "20", "8", "30"
		stat := fmt.Sprintf("%d (worker %d) %s\n", pid, pid, strings.Join(fields, " "))
		if err := os.WriteFile(filepath.Join(path, "stat"), []byte(stat), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := "export LC_ALL=C\n" + strings.ReplaceAll(processMonitorCommand, "/proc/", filepath.ToSlash(dir)+"/")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	started := time.Now()
	command := exec.CommandContext(ctx, "sh", "-c", script)
	command.WaitDelay = time.Second
	data, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	var parsed rawStats
	parseProcessStats(&parsed, strings.Split(strings.TrimSpace(string(data)), "\n"))
	if !parsed.ProcessSample.Available || parsed.ProcessSample.Readable != 6000 || len(parsed.Processes) != 6000 {
		t.Fatal("large process host lost records", parsed.ProcessSample, len(parsed.Processes))
	}
	for _, process := range parsed.Processes {
		if process.MemorySource != "stat" || !process.MemoryEstimated || !process.MemoryReady {
			t.Fatal("collector did not use lightweight RSS", process)
		}
	}
	t.Logf("6,000 PIDs: %.0f ms, %d bytes of lightweight counters; no per-process status/smaps files", float64(elapsed.Microseconds())/1000, len(data))
}
