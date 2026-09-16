package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func syntheticProcesses(n int) *rawStats {
	s := &rawStats{}
	for pid := 1; pid <= n; pid++ {
		s.Processes = append(s.Processes, Process{PID: pid, Name: fmt.Sprintf("worker-%05d", pid), State: "S", CPU: float64(pid), CPUReady: true, Memory: uint64(n - pid + 1), MemoryReady: true, MemoryEstimated: true})
	}
	return s
}
func TestProcessPageLargeHost(t *testing.T) {
	for _, n := range []int{5000, 50000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			source := syntheticProcesses(n)
			var cache processListCache
			q := processListQuery{Page: 1, Size: 100, Sort: "cpu", Desc: true}
			started := time.Now()
			first, err := cache.page(context.Background(), source, q)
			if err != nil || first.Total != n || first.Pages != n/100 || len(first.Processes) != 100 || first.Processes[0].PID != n {
				t.Fatalf("invalid bounded page: %+v %v", first, err)
			}
			t.Logf("%d processes: filter/sort/page in %s, %d rows delivered", n, time.Since(started), len(first.Processes))
			q.Page = 1000000
			last, err := cache.page(context.Background(), source, q)
			if err != nil || last.Page != n/100 || last.Processes[99].PID != 1 {
				t.Fatal("out-of-range page not clamped", err)
			}
			q.Search = "WORKER-00042"
			q.Search = strings.ToLower(q.Search)
			q.Page = 1
			found, err := cache.page(context.Background(), source, q)
			if err != nil || found.Matched != 1 || found.Processes[0].PID != 42 {
				t.Fatal("search failed", err)
			}
			found.Processes[0].Name = "modified"
			if source.Processes[41].Name == "modified" {
				t.Fatal("page mutated CPU baseline")
			}
			q.Search = "<absent>"
			empty, err := cache.page(context.Background(), source, q)
			if err != nil || len(empty.Processes) != 0 || empty.Pages != 1 || empty.Page != 1 {
				t.Fatal("empty page broken", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			q.Search = "different"
			if _, err = cache.page(ctx, source, q); err == nil {
				t.Fatal("cancelled scan completed")
			}
		})
	}
}
func TestProcessPageUnknownsAndQueryLimits(t *testing.T) {
	for _, metric := range []string{"cpu", "memory"} {
		for _, desc := range []bool{true, false} {
			a, b := Process{PID: 2, CPUReady: true, MemoryReady: true}, Process{PID: 1, CPU: 1e9, Memory: 1e9}
			q := processListQuery{Sort: metric, Desc: desc}
			if !processLess(a, b, q) || processLess(b, a, q) {
				t.Fatal("unknown values sort ahead of measured values")
			}
		}
	}
	for _, query := range []string{"page=0", "page=-1", "pageSize=201", "pageSize=9999999999999999999999", "sort=user", "direction=sideways", "search=" + strings.Repeat("a", 257)} {
		if _, err := parseProcessQuery(httptest.NewRequest("GET", "/?"+query, nil)); err == nil {
			t.Fatal("invalid query accepted", query)
		}
	}
	q, err := parseProcessQuery(httptest.NewRequest("GET", "/?page=2&pageSize=50&sort=name&direction=asc&search=Worker", nil))
	if err != nil || q.Page != 2 || q.Size != 50 || q.Desc || q.Search != "worker" {
		t.Fatal(q, err)
	}
}
func TestProcessPageSharesScanAndRetainsFailedSnapshot(t *testing.T) {
	s, calls := ownerSSHFixture(t, func(command string) string {
		if !strings.Contains(command, "__CS_PROCESS__") {
			t.Error("unexpected collector")
		}
		return "__CS_PROCESS__\nM\t100\t4096\t\t100\n" + processRecord(42, 10, 0, 1, 100, 50, "stat", 0, "S", "worker") + "\nS\t1\t1\t0\t100\n"
	})
	q := processListQuery{Page: 1, Size: 100, Sort: "cpu", Desc: true}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			page, err := s.processPage(context.Background(), q)
			if err != nil || page.Total != 1 {
				t.Errorf("page failed %v total=%d", err, page.Total)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("page requests duplicated remote scan", calls.Load())
	}
	s.statsMu.Lock()
	s.failedProcessSample("fixture failure")
	s.statsMu.Unlock()
	page, err := s.processPage(context.Background(), q)
	if err != nil || page.Total != 1 || page.Sample.Error != "fixture failure" || page.Sample.Available {
		t.Fatal("failed scan discarded last list", err, page.Sample)
	}
}
func TestProcessUserIdentityAndBoundedLookup(t *testing.T) {
	wanted := map[int]processMemoryKey{42: {42, 123, "abc-def"}}
	for _, input := range []string{"K\tabc-def\nP\t42\t124\t0\t726f6f74\n", "K\tdead\nP\t42\t123\t0\t726f6f74\n", "K\tabc-def\nP\t43\t123\t0\t726f6f74\n"} {
		if len(parseProcessUsers([]byte(input), wanted)) != 0 {
			t.Fatal("PID reuse/boot mismatch accepted")
		}
	}
	values := parseProcessUsers([]byte("K\tabc-def\nP\t42\t123\t1000\t\n"), wanted)
	if v := values[wanted[42]]; !v.ready || v.user != "1000" {
		t.Fatal("numeric UID fallback failed", v)
	}
	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sys/kernel/random"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "42"), 0700); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields("S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 123 0 30")
	for name, content := range map[string]string{"sys/kernel/random/boot_id": "abc-def\n", "42/stat": "42 (worker (name)) " + strings.Join(fields, " ") + "\n", "42/status": "Name:\tworker\nUid:\t1001\t1002\t1003\t1004\n", "passwd": "effective:x:1002:1002::/:/bin/sh\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := strings.ReplaceAll(processUsersCommand(wanted), "/proc/", filepath.ToSlash(dir)+"/")
	script = strings.ReplaceAll(script, "/etc/passwd", filepath.ToSlash(filepath.Join(dir, "passwd")))
	data, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	value := parseProcessUsers(data, wanted)[wanted[42]]
	if !value.ready || value.uid != 1002 || value.user != "effective" {
		t.Fatalf("effective user lookup failed: %q %+v", data, value)
	}
	if strings.Contains(script, "smaps") || strings.Contains(script, "[0-9]*") {
		t.Fatal("user lookup scans all processes")
	}
}
func TestProcessUserCacheAndPageCap(t *testing.T) {
	s, calls := ownerSSHFixture(t, func(command string) string {
		return "K\tabc-def\nP\t42\t123\t0\t" + hex.EncodeToString([]byte("root")) + "\n"
	})
	rows := []Process{{PID: 42, startTicks: 123, bootID: "abc-def"}}
	s.readProcessUsers(context.Background(), rows)
	s.readProcessUsers(context.Background(), rows)
	if calls.Load() != 1 || !rows[0].UserReady || rows[0].User != "root" {
		t.Fatal("user cache failed", calls.Load(), rows)
	}
	for i := 0; i < 800; i++ {
		s.processUsers.values[processMemoryKey{i, 1, "old"}] = processUserValue{at: time.Now().Add(-time.Minute)}
	}
	s.readProcessUsers(context.Background(), rows)
	if len(s.processUsers.values) > 512 {
		t.Fatal("unbounded user cache")
	}
	if processSampleInterval(5000, 100*time.Millisecond) != 5*time.Second || processSampleInterval(50000, time.Second) != 20*time.Second || processSampleInterval(1, 10*time.Second) != 30*time.Second {
		t.Fatal("adaptive interval failed")
	}
}
