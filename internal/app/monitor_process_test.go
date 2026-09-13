package app

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func processRecord(pid int, user, system, start uint64, uptime float64, memory uint64, source string, flags uint64, state, name string) string {
	return fmt.Sprintf("P\t%d\t%d\t%d\t%d\t%.2f\t%d\t%s\t%d\t%s\t%s", pid, user, system, start, uptime, memory, source, flags, state, hex.EncodeToString([]byte(name)))
}

func processFixture(records []string, uptime float64, boot string) rawStats {
	value := rawStats{Stats: Stats{Processes: []Process{}, SampledAt: time.Now()}}
	lines := []string{fmt.Sprintf("M\t100\t65536\t%s\t%.2f", boot, uptime)}
	lines = append(lines, records...)
	lines = append(lines, fmt.Sprintf("S\t%d\t%d\t0\t%.2f", len(records), len(records), uptime+.1))
	parseProcessStats(&value, lines)
	return value
}

func TestProcessIntervalCPUAndMemorySources(t *testing.T) {
	name := "worker) (tabs\t中文\n"
	beforeRecords := []string{
		processRecord(41, 1000, 100, 100, 50, 1048576, "smaps_rollup", 0, "R", name),
		processRecord(42, 900, 0, 101, 50, 10, "stat", 0, "S", "idle"),
		processRecord(43, 2, 0, 102, 50, 0, "status", 0, "R", "fresh"),
		processRecord(44, 10, 0, 103, 50, 0, "stat", 0x00200000, "S", "kernel"),
		"U\t45",
	}
	before := processFixture(beforeRecords, 50, "boot-a")
	if !before.ProcessSample.Available || before.ProcessSample.Ready || before.Processes[0].CPUReady || before.Processes[0].Name != name || !before.Processes[0].MemoryReady || before.Processes[0].MemoryEstimated {
		t.Fatalf("first sample or exact RSS invalid: %+v", before)
	}
	if p := before.Processes[1]; p.Memory != 655360 || !p.MemoryEstimated || !p.MemoryReady {
		t.Fatalf("remote page size / approximate RSS fallback ignored: %+v", p)
	}
	if before.Processes[2].MemoryReady || !before.Processes[3].MemoryReady || before.Processes[3].Memory != 0 || before.Processes[3].MemoryEstimated || before.Processes[4].MemoryReady || before.Processes[4].CPUReady {
		t.Fatal("unknown RSS fabricated a zero, or known kernel zero was lost")
	}
	afterRecords := []string{
		processRecord(41, 1100, 150, 100, 50.5, 2097152, "smaps_rollup", 0, "R", name),
		processRecord(42, 900, 0, 101, 50.5, 11, "stat", 0, "S", "idle"),
		processRecord(43, 2000, 0, 200, 50.5, 4096, "smaps_rollup", 0, "R", "reused PID"),
		processRecord(44, 8, 0, 103, 50.5, 0, "stat", 0x00200000, "S", "reset counter"),
		processRecord(46, 10, 0, 201, 50.5, 50000, "status", 0, "R", "new PID"),
	}
	after := processFixture(afterRecords, 50.5, "boot-a")
	after.SampledAt = before.SampledAt.Add(30 * time.Second)
	applyProcessRates(&after, &before)
	if p := after.Processes[0]; !p.CPUReady || p.CPU != 300 || p.Memory != 2097152 {
		t.Fatalf("interval CPU used wall/transport/lifetime time or clamped at 100%%: %+v", p)
	}
	if !after.Processes[1].CPUReady || after.Processes[1].CPU != 0 || after.Processes[2].CPUReady || after.Processes[3].CPUReady || after.Processes[4].CPUReady || after.ProcessSample.CPUReadyCount != 2 {
		t.Fatalf("idle CPU, PID reuse, counter reset or first sample handling wrong: %+v", after.Processes)
	}
	for _, tc := range []struct {
		name string
		edit func(*rawStats)
	}{
		{"reboot", func(s *rawStats) { s.processBootID = "boot-b" }},
		{"no HZ", func(s *rawStats) { s.ProcessSample.ClockTicks = 0 }},
		{"changed HZ", func(s *rawStats) { s.ProcessSample.ClockTicks = 250 }},
		{"partial capture", func(s *rawStats) { s.processCaptured = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := processFixture(afterRecords, 50.5, "boot-a")
			tc.edit(&current)
			applyProcessRates(&current, &before)
			if current.Processes[0].CPUReady {
				t.Fatal("invalid interval was reported as a current CPU value")
			}
		})
	}
}

// This helper owns only its own allocation and CPU workload. The parent test
// controls every transition and always kills/waits it; no existing app is touched.
func TestProcessWorkloadHelper(t *testing.T) {
	if os.Getenv("DENGSHELL_PROCESS_WORKLOAD_HELPER") != "1" {
		return
	}
	pages := make([]byte, 24<<20)
	for i := 0; i < len(pages); i += 4096 {
		pages[i] = byte(i/4096 + 1)
	}
	var busy atomic.Bool
	go func() {
		var value uint64 = 1
		for {
			if !busy.Load() {
				time.Sleep(time.Millisecond)
				continue
			}
			for i := 0; i < 10000; i++ {
				value = value*6364136223846793005 + 1
			}
			runtime.KeepAlive(value)
		}
	}()
	fmt.Println("ready")
	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		switch input.Text() {
		case "busy":
			busy.Store(true)
		case "idle":
			busy.Store(false)
		case "quit":
			runtime.KeepAlive(pages)
			return
		}
		fmt.Println(input.Text())
	}
	runtime.KeepAlive(pages)
}

func TestLocalProcessWorkloadIntegration(t *testing.T) {
	key := os.Getenv("CLOUDSHELL_TEST_KEY")
	if key == "" || runtime.GOOS != "linux" {
		t.Skip("requires the dedicated local Linux sshd on port 19225")
	}
	child := exec.Command(os.Args[0], "-test.run=^TestProcessWorkloadHelper$")
	child.Env = append(os.Environ(), "DENGSHELL_PROCESS_WORKLOAD_HELPER=1")
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatal("controlled process did not start")
	}
	setWorkload := func(mode string) {
		t.Helper()
		if _, err := io.WriteString(input, mode+"\n"); err != nil || !scanner.Scan() || scanner.Text() != mode {
			t.Fatalf("cannot set controlled workload to %s", mode)
		}
	}
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	profile, err := a.store.Save(Profile{Name: "process accuracy", Host: "127.0.0.1", Port: 19225, User: "bitsflow", Auth: "key", KeyPath: key}, false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := connectLocalSSHFixture(t, a, profile.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() (Process, ProcessSampleInfo) {
		t.Helper()
		stats, err := session.Stats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(stats.Processes) <= 5 || !stats.ProcessSample.Available {
			t.Fatal("collector returned only a top list or unavailable capture", stats.ProcessSample)
		}
		for _, process := range stats.Processes {
			if process.PID == child.Process.Pid {
				return process, stats.ProcessSample
			}
		}
		t.Fatal("controlled process PID absent from remote snapshot: " + strconv.Itoa(child.Process.Pid))
		return Process{}, ProcessSampleInfo{}
	}
	first, _ := snapshot()
	if first.CPUReady || !first.MemoryReady || first.Memory < 24<<20 || first.MemorySource != "smaps_rollup" || first.MemoryEstimated {
		t.Fatalf("first current memory sample is not accurate: %+v", first)
	}
	setWorkload("busy")
	snapshot() // Establish a baseline entirely inside the busy interval.
	time.Sleep(300 * time.Millisecond)
	active, meta := snapshot()
	if !active.CPUReady || active.CPU < 25 || active.CPU > 135 {
		t.Fatalf("single-core busy process CPU not measured from current interval: %+v", active)
	}
	setWorkload("idle")
	snapshot() // Flush the interval that still includes the workload transition.
	time.Sleep(300 * time.Millisecond)
	idle, _ := snapshot()
	if !idle.CPUReady || idle.CPU >= 10 || active.CPU-idle.CPU < 25 {
		t.Fatalf("CPU still reflects process lifetime after it went idle: busy=%+v idle=%+v", active, idle)
	}
	t.Logf("PID %d: current busy CPU %.2f%% -> idle %.2f%%; accurate RSS %d bytes (%s), %d visible PIDs, collection %.0fms", active.PID, active.CPU, idle.CPU, active.Memory, active.MemorySource, meta.Visible, meta.CollectionMilliseconds)
}

func TestProcessNameFramingAndUnavailableSnapshot(t *testing.T) {
	name := "a) b\n__CS_END__\t中"
	data := "__CS_STAT__\ncpu 1 2 3 4 5 6 7 8\n__CS_MEM__\nMemTotal: 1 kB\n__CS_PROCESS__\nM\t100\t4096\tboot\t10\n" + processRecord(1, 0, 0, 1, 10, 1024, "smaps_rollup", 0, "S", name) + "\nS\t1\t1\t0\t10.1\n__CS_END__\n"
	stats, err := parseStats([]byte(data), time.Now())
	if err != nil || len(stats.Processes) != 1 || stats.Processes[0].Name != name || !stats.ProcessSample.Available {
		t.Fatal("process name escaped the protocol framing", stats, err)
	}
	missing := rawStats{}
	parseProcessStats(&missing, nil)
	if missing.ProcessSample.Available || !strings.Contains(missing.ProcessSample.Error, "不可用") {
		t.Fatal("missing process collector is not reported")
	}
}
