package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMonitorRejectsOversizedRemoteOutput(t *testing.T) {
	for _, path := range []string{"network", "core"} {
		t.Run(path, func(t *testing.T) {
			s, _, _ := backgroundNetworkSSHFixture(t, false, func(context.Context, int) ([]byte, error) {
				return bytes.Repeat([]byte("A"), 8<<20), nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var data []byte
			var err error
			limit := networkOutputLimit
			if path == "network" {
				data, err = s.runNetworkMonitor(ctx, networkMonitorCommand)
			} else {
				limit = monitorOutputLimit
				data, err = s.runBoundedSSH(ctx, networkMonitorCommand, limit)
			}
			if !errors.Is(err, errRemoteOutputLimit) || len(data) > limit {
				t.Fatalf("unbounded or silently truncated output: bytes=%d limit=%d err=%v", len(data), limit, err)
			}
		})
	}
}

// Also run against the audit's isolated server-side SSH fault fixture, which
// ignores CHANNEL_CLOSE. Both the ordinary peer and that hostile peer must
// finish within a bounded interval after the caller's deadline.
func TestMonitorAndScriptDeadlineReleasesBlockedWait(t *testing.T) {
	for _, path := range []string{"network", "core", "script"} {
		t.Run(path, func(t *testing.T) {
			entered := make(chan struct{})
			s, _, _ := backgroundNetworkSSHFixture(t, false, func(ctx context.Context, _ int) ([]byte, error) {
				close(entered)
				<-ctx.Done()
				return nil, ctx.Err()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch path {
				case "network":
					_, err = s.runNetworkMonitor(ctx, networkMonitorCommand)
				case "core":
					_, err = s.runBoundedSSH(ctx, networkMonitorCommand, monitorOutputLimit)
				case "script":
					_, err = runMTRScript(ctx, s, networkMonitorCommand, 4096)
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("fixture did not start")
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("lost deadline: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("SSH wait ignored caller deadline")
			}
			if os.Getenv("DENGSHELL_TEST_IGNORED_CHANNEL_CLOSE") == "1" && s.ctx.Err() != nil {
				t.Fatal("collector cancellation closed the SSH transport")
			}
		})
	}
}

func TestMonitorCancellationKeepsCooperativeTransport(t *testing.T) {
	if os.Getenv("DENGSHELL_TEST_IGNORED_CHANNEL_CLOSE") == "1" {
		t.Skip("intentional hostile peer")
	}
	gate := make(chan struct{})
	defer close(gate)
	s, _, _ := backgroundNetworkSSHFixture(t, false, func(_ context.Context, n int) ([]byte, error) {
		if n == 1 {
			<-gate
		}
		return []byte("ok"), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := s.runNetworkMonitor(ctx, networkMonitorCommand)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if s.ctx.Err() != nil {
		t.Fatal("cooperative cancellation disconnected the terminal")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	data, err := s.runNetworkMonitor(ctx2, networkMonitorCommand)
	if err != nil || string(data) != "ok" {
		t.Fatalf("subsequent command: %q %v", data, err)
	}
}

func TestCancelledChannelOpenAllowsCooperativePeerToFinish(t *testing.T) {
	for _, method := range []string{"network", "core"} {
		t.Run(method, func(t *testing.T) {
			s, _, opens := backgroundNetworkSSHFixture(t, false, func(context.Context, int) ([]byte, error) { return []byte("ok"), nil }, 100*time.Millisecond)
			read := s.runNetworkMonitor
			if method == "core" {
				read = func(ctx context.Context, command string) ([]byte, error) {
					return s.runBoundedSSH(ctx, command, monitorOutputLimit)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := read(ctx, networkMonitorCommand); done <- err }()
			awaitNetworkCondition(t, time.Second, func() bool { return opens.Load() == 1 })
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled opening worker did not exit")
			}
			if os.Getenv("DENGSHELL_TEST_IGNORED_CHANNEL_CLOSE") == "1" {
				if s.ctx.Err() != nil {
					t.Fatal("ignored collector close killed healthy SSH")
				}
				return
			}
			if s.ctx.Err() != nil {
				t.Fatal("ordinary channel-open RTT caused terminal disconnection")
			}
			ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
			defer cancel2()
			data, err := read(ctx2, networkMonitorCommand)
			if err != nil || string(data) != "ok" {
				t.Fatalf("transport not reusable: %q %v", data, err)
			}
		})
	}
}

func TestRejectedExecWaitIsStillBounded(t *testing.T) {
	s, _, _ := backgroundNetworkSSHFixture(t, false, nil, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.runNetworkMonitor(ctx, networkMonitorCommand); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("server rejected exec but command succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("rejected exec retained SSH request worker")
	}
	if os.Getenv("DENGSHELL_TEST_IGNORED_CHANNEL_CLOSE") == "1" && s.ctx.Err() != nil {
		t.Fatal("ignored collector close killed the terminal")
	}
}

func TestDiskIOCountsLeafDevicesOnce(t *testing.T) {
	for _, blocks := range []string{
		"sda\nsdb\nmd0\ndm-0\nloop0\n",
		"sda leaf\nsdb leaf\nmd0 stacked\ndm-0 stacked\nloop0 leaf\nvirtual-array stacked\n",
	} {
		data := "__CS_STAT__\ncpu 100 0 0 100 0 0 0 0\n__CS_MEM__\nMemTotal: 1024 kB\n__CS_BLOCKS__\n" + blocks + "__CS_DISKIO__\n8 0 sda 1 0 100 0 1 0 150 0\n8 16 sdb 1 0 100 0 1 0 150 0\n9 0 md0 1 0 200 0 1 0 300 0\n253 0 dm-0 1 0 200 0 1 0 300 0\n7 0 loop0 1 0 200 0 1 0 300 0\n254 0 virtual-array 1 0 200 0 1 0 300 0\n"
		stats, err := parseStats([]byte(data), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if stats.read != 200*512 || stats.write != 300*512 {
			t.Fatalf("stacked devices counted twice: read=%d write=%d", stats.read, stats.write)
		}
	}
}

func TestMonitorCommandDiscoversStackedDeviceTopology(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux monitor shell fixture")
	}
	root := t.TempDir()
	for _, name := range []string{"sda", "sdb", "md0", "dm-0", "virtual-array"} {
		if err := os.MkdirAll(filepath.Join(root, "sys/block", name, "slaves"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, slave := range map[string]string{"md0": "sda", "dm-0": "md0", "virtual-array": "dm-0"} {
		if err := os.Symlink(filepath.Join(root, "sys/block", slave), filepath.Join(root, "sys/block", name, "slaves", slave)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "proc"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"stat": "cpu 100 0 0 100 0 0 0 0\n", "meminfo": "MemTotal: 1024 kB\n", "uptime": "10 10\n", "loadavg": "0 0 0\n", "net/dev": "",
		"diskstats": "8 0 sda 1 0 100 0 1 0 150 0\n8 16 sdb 1 0 100 0 1 0 150 0\n9 0 md0 1 0 200 0 1 0 300 0\n253 0 dm-0 1 0 200 0 1 0 300 0\n254 0 virtual-array 1 0 200 0 1 0 300 0\n",
	}
	for name, value := range files {
		path := filepath.Join(root, "proc", name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := strings.ReplaceAll(coreMonitorCommand, "/sys/block/", filepath.ToSlash(filepath.Join(root, "sys/block"))+"/")
	command = strings.ReplaceAll(command, "/proc/", filepath.ToSlash(filepath.Join(root, "proc"))+"/")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "sh", "-c", command).Output()
	if err != nil {
		t.Fatal(err)
	}
	stats, err := parseStats(data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if stats.read != 200*512 || stats.write != 300*512 {
		t.Fatalf("wrong physical I/O: %d/%d, output=%s", stats.read, stats.write, data)
	}
}
