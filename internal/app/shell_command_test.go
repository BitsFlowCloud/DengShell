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

func testLoginShell(t *testing.T, name string) (string, []string) {
	requireLinuxShellFixture(t)
	t.Helper()
	executable := os.Getenv("DENGSHELL_TEST_" + strings.ToUpper(name))
	if executable == "" {
		var err error
		executable, err = exec.LookPath(name)
		if err != nil {
			t.Skip(name + " unavailable")
		}
	}
	switch name {
	case "bash":
		return executable, []string{"--noprofile", "--norc"}
	case "zsh":
		return executable, []string{"-f"}
	default:
		return executable, []string{"--no-config"}
	}
}

func TestLoginShellScriptQuotingAndExitStatus(t *testing.T) {
	for _, name := range []string{"bash", "zsh", "fish"} {
		t.Run(name, func(t *testing.T) {
			executable, args := testLoginShell(t, name)
			marker := filepath.Join(t.TempDir(), "must-not-be-created")
			payloads := []string{"", `quotes '" and \\ and trailing \`, "中文\n__CS_END__\t; & | # * ? [x] ~", "$(touch " + marker + ") `touch " + marker + "` $HOME ${PWD}", "last line\n\n"}
			for _, payload := range payloads {
				script := "value=" + terminalQuote(payload) + "; for n in 1; do printf '%s' \"$value\"; done; exit 7"
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				output, err := exec.CommandContext(ctx, executable, append(args, "-c", posixShellCommand(script))...).CombinedOutput()
				cancel()
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 7 || string(output) != payload {
					t.Fatalf("script changed: exit=%v output=%q want=%q", err, output, payload)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("payload was expanded by the login shell")
			}
		})
	}
}

func TestUtilityWrapperInEachLoginShell(t *testing.T) {
	for _, name := range []string{"bash", "zsh", "fish"} {
		t.Run(name, func(t *testing.T) {
			executable, args := testLoginShell(t, name)
			dir := t.TempDir()
			// Only impersonate id for this harmless wrapper test; no sudo or
			// utility action runs and the host's effective UID never changes.
			if err := os.WriteFile(filepath.Join(dir, "id"), []byte("#!/bin/sh\nprintf '0\\n'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(executable, append(args, "-c", wrapUtilityScript("value='a \\\\ b'; printf '%s' \"$value\"; exit 19"))...)
			command.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 19 || string(output) != `a \\ b` {
				t.Fatalf("wrapper did not preserve script/status: %v %q", err, output)
			}
		})
	}
}

func TestProcessCollectorReadErrorsAndMultilineNames(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux procfs error fixture")
	}
	dir := t.TempDir()
	write := func(relative, data string) {
		t.Helper()
		name := filepath.Join(dir, relative)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stat := func(pid int, name string) string {
		fields := make([]string, 22)
		for i := range fields {
			fields[i] = "0"
		}
		fields[0], fields[11], fields[12], fields[19], fields[21] = "S", "100", "20", "8", "3"
		return fmt.Sprintf("%d (%s) %s\n", pid, name, strings.Join(fields, " "))
	}
	name := "name) with\n__CS_END__\t中文\\"
	write("101/stat", stat(101, name))
	write("101/status", "VmRSS: 72 kB\n")
	write("102/status", "Name: unreadable\n")
	write("103/stat", stat(103, "memory fallback"))
	write("103/status", "VmRSS: 36 kB\n")
	write("uptime", "10.50 0\n")
	write("sys/kernel/random/boot_id", "fixture-boot\n")
	// /proc/self/mem at offset zero reliably fails at read time (EIO), after
	// open succeeds. Old mawk getline aborted here instead of returning -1.
	for _, relative := range []string{"102/stat", "103/smaps_rollup"} {
		if err := os.Symlink("/proc/self/mem", filepath.Join(dir, relative)); err != nil {
			t.Fatal(err)
		}
	}
	script := "export LC_ALL=C\n" + strings.ReplaceAll(processMonitorCommand, "/proc/", filepath.ToSlash(dir)+"/")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var parsed rawStats
	parseProcessStats(&parsed, strings.Split(strings.TrimSpace(string(output)), "\n"))
	if !parsed.ProcessSample.Available || parsed.ProcessSample.Readable != 2 || parsed.ProcessSample.Unreadable != 1 || len(parsed.Processes) != 3 {
		t.Fatalf("one failed PID aborted other records: %+v %q", parsed.ProcessSample, output)
	}
	if p := parsed.Processes[0]; p.PID != 101 || p.Name != name || p.Memory != 3*parsed.ProcessSample.PageSize || p.MemorySource != "stat" || !p.MemoryEstimated {
		t.Fatalf("name framing or lightweight RSS changed: %+v", p)
	}
	if p := parsed.Processes[2]; p.PID != 103 || p.Memory != 3*parsed.ProcessSample.PageSize || p.MemorySource != "stat" || !p.MemoryEstimated {
		t.Fatalf("lightweight stat memory was not used: %+v", p)
	}
}

func TestUtilityInspectionCRLFMarkers(t *testing.T) {
	inspection, err := parseUtilityInspection("\r\n__DS_OS__\r\nLinux\r\n__DS_UID__\r\n0\r\n__DS_END__\r\n")
	if err != nil || inspection.sections["OS"] != "Linux" || inspection.sections["UID"] != "0" {
		t.Fatal("CRLF marker boundary rejected", inspection, err)
	}
}
