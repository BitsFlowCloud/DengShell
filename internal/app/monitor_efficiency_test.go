package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func monitorFixtureSession(t *testing.T) *Session {
	t.Helper()
	key := os.Getenv("CLOUDSHELL_TEST_KEY")
	if key == "" {
		t.Skip("dedicated localhost SSH fixture required")
	}
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	profile, err := a.store.Save(Profile{Name: "monitor efficiency", Host: "127.0.0.1", Port: 19225, User: "bitsflow", Auth: "key", KeyPath: key}, false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := connectLocalSSHFixture(t, a, profile.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestMonitorDisplayCacheAvoidsRepeatedRemoteSampling(t *testing.T) {
	s := monitorFixtureSession(t)
	first, err := s.statsForDisplay(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached || !first.ProcessSample.Paused || len(first.Processes) != 0 || first.Cores == 0 || len(first.Disks) == 0 {
		t.Fatal("collapsed view sampled processes or lost metadata", first)
	}
	var workers sync.WaitGroup
	errors := make(chan string, 6)
	for i := 0; i < 6; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			sample, err := s.statsForDisplay(context.Background(), false)
			if err != nil {
				errors <- err.Error()
				return
			}
			if !sample.Cached || !sample.SampledAt.Equal(first.SampledAt) {
				errors <- "concurrent request created another remote sample"
			}
		}()
	}
	workers.Wait()
	close(errors)
	for failure := range errors {
		t.Error(failure)
	}
	expanded, err := s.statsForDisplay(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !expanded.SampledAt.Equal(first.SampledAt) {
		t.Fatal("expanding processes triggered an early core/load sample")
	}
	if expanded.ProcessSample.Paused || !expanded.ProcessSample.Available || len(expanded.Processes) == 0 || expanded.ProcessSample.SampledAt.IsZero() {
		t.Fatal("expanding process card did not get a real process sample")
	}
	// Force only the core refresh past its guard, retaining the real process
	// and metadata capture times; this avoids sleeping through five seconds.
	s.statsMu.Lock()
	s.statsNextSampleAt = time.Now().Add(-time.Millisecond)
	s.statsMu.Unlock()
	collapsed, err := s.statsForDisplay(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if collapsed.MetadataSampledAt != first.MetadataSampledAt || collapsed.Cores != first.Cores || len(collapsed.Disks) != len(first.Disks) {
		t.Fatal("cached static metadata lost on lightweight sample")
	}
	cachedProcesses, err := s.statsForDisplay(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !cachedProcesses.Cached || !cachedProcesses.ProcessSample.Cached || !cachedProcesses.ProcessSample.SampledAt.Equal(expanded.ProcessSample.SampledAt) || len(cachedProcesses.Processes) != len(expanded.Processes) {
		t.Fatal("rapid reopen either rescanned or lost recent process data")
	}
	cachedProcesses.Processes[0].Name = "mutated"
	again, err := s.statsForDisplay(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Processes[0].Name == "mutated" {
		t.Fatal("display result aliases cached process state")
	}
}

func TestMonitorCommandsOmitHeavyProcessWorkWhenCollapsed(t *testing.T) {
	lightweight := monitorCommandFor(false, false)
	for _, heavy := range []string{"smaps_rollup", "/proc/[0-9]*/stat", "df -Pk", "/proc/cpuinfo", "ip -o"} {
		if strings.Contains(lightweight, heavy) {
			t.Fatal("lightweight sample includes costly metadata/process work", heavy)
		}
	}
	full := monitorCommandFor(true, true)
	if strings.Contains(full, "smaps_rollup") {
		t.Fatal("routine monitoring scans process memory pages")
	}
	for _, required := range []string{"/proc/[0-9]*/stat", "df -Pk", "/proc/cpuinfo", "ip -o"} {
		if !strings.Contains(full, required) {
			t.Fatal("full sample dropped required information", required)
		}
	}
}

func TestMonitorRemoteEfficiencyMeasurement(t *testing.T) {
	if os.Getenv("DENGSHELL_MEASURE_MONITOR") == "" {
		t.Skip("explicit comparative performance measurement")
	}
	s := monitorFixtureSession(t)
	legacy, err := os.ReadFile(filepath.Join("testdata", "monitor_before_efficiency.sh"))
	if err != nil {
		t.Fatal(err)
	}
	commands := map[string]string{"legacy_full": string(legacy), "new_initial_full": monitorCommandFor(true, true), "new_initial_collapsed": monitorCommandFor(true, false), "new_steady_expanded": monitorCommandFor(false, true), "new_steady_collapsed": monitorCommandFor(false, false)}
	data, err := json.Marshal(commands)
	if err != nil {
		t.Fatal(err)
	}
	python := fmt.Sprintf(`import json,subprocess,resource,time,tempfile,pathlib,statistics
commands=json.loads(%q)
results={}
for name,command in commands.items():
    timings=[]; cpus=[]; sizes=[]
    for index in range(10):
        before=resource.getrusage(resource.RUSAGE_CHILDREN); start=time.perf_counter()
        result=subprocess.run(['sh','-c',command],stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
        elapsed=time.perf_counter()-start; after=resource.getrusage(resource.RUSAGE_CHILDREN)
        if index>=2:
            timings.append(elapsed*1000); cpus.append((after.ru_utime+after.ru_stime-before.ru_utime-before.ru_stime)*1000);sizes.append(len(result.stdout))
    with tempfile.TemporaryDirectory(prefix='dengshell-monitor-qa-') as directory:
        trace=pathlib.Path(directory)/'exec.trace'
        subprocess.run(['strace','-f','-qq','-e','trace=execve','-o',str(trace),'sh','-c',command],stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,check=True)
        executions=sum('execve(' in line and '= 0' in line for line in trace.read_text().splitlines())
    results[name]={'medianWallMilliseconds':round(statistics.median(timings),3),'meanChildCPUMilliseconds':round(statistics.mean(cpus),3),'meanOutputBytes':round(statistics.mean(sizes)),'successfulExecCalls':executions,'samples':len(timings)}
print(json.dumps(results,sort_keys=True))`, string(data))
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	output, err := s.run(ctx, "python3 -c "+terminalQuote(python))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]struct {
		CPU   float64 `json:"meanChildCPUMilliseconds"`
		Bytes int     `json:"meanOutputBytes"`
		Exec  int     `json:"successfulExecCalls"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal("invalid measurement", err, string(output))
	}
	if result["new_steady_collapsed"].Exec >= result["legacy_full"].Exec || result["new_steady_collapsed"].Bytes >= result["legacy_full"].Bytes {
		t.Fatal("no measured process/output reduction", string(output))
	}
	t.Log(string(output))
	if report := os.Getenv("DENGSHELL_MONITOR_RESULT"); report != "" {
		if err := os.WriteFile(report, append(output, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
