package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMTRPlansUseAvailableDistributionManager(t *testing.T) {
	for _, tc := range []struct{ id, manager, pkg, fragment string }{
		{"ubuntu", "apt-get", "mtr-tiny", "DPkg::Lock::Timeout=60"},
		{"fedora", "dnf", "mtr", "dnf install -y mtr"},
		{"centos", "yum", "mtr", "yum install -y mtr"},
		{"arch", "pacman", "mtr", "pacman -S --needed --noconfirm mtr"},
		{"opensuse-leap", "zypper", "mtr", "zypper --non-interactive install mtr"},
		{"alpine", "apk", "mtr", "apk add mtr"},
		{"void", "xbps-install", "mtr", "xbps-install -Sy mtr"},
		{"gentoo", "emerge", "net-analyzer/mtr", "emerge --ask=n net-analyzer/mtr"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			env := parseMTREnvironment("os\tLinux\nid\t" + tc.id + "\nuid\t0\ndetach\t1\nmanager\t" + tc.manager + "\n")
			plan := mtrPlanForEnvironment(env)
			if plan.PackageManager != tc.manager || plan.PackageName != tc.pkg || !plan.CanAutoInstall || !strings.Contains(plan.Command, tc.fragment) {
				t.Fatalf("bad plan: %+v", plan)
			}
			if strings.Contains(plan.Command, "--noconfirm -Sy") || strings.Contains(plan.Command, "add-repo") {
				t.Fatal("unreviewed repository mutation")
			}
		})
	}
	env := parseMTREnvironment("os\tLinux\nid\tfedora\nuid\t0\ndetach\t1\nmanager\tapt-get\nmanager\tdnf\nmanager\tevil; touch /tmp/no\n")
	if plan := mtrPlanForEnvironment(env); plan.PackageManager != "dnf" {
		t.Fatalf("wrong native manager: %+v", plan)
	}
}
func TestMTRPlanPermissionsAndUnknownPlatforms(t *testing.T) {
	base := "os\tLinux\nid\tdebian\nuid\t1000\ndetach\t1\nmanager\tapt-get\n"
	for _, tc := range []struct {
		extra, permission, command string
		auto                       bool
	}{
		{"sudo\t1\nsudo-nopass\t1\n", "sudo-nopass", "sudo -n sh -c ", true},
		{"sudo\t1\n", "interactive", "sudo sh -c ", false},
		{"", "unavailable", "apt-get update", false},
	} {
		plan := mtrPlanForEnvironment(parseMTREnvironment(base + tc.extra))
		if plan.Permission != tc.permission || plan.CanAutoInstall != tc.auto || !strings.HasPrefix(plan.Command, tc.command) {
			t.Fatalf("bad permissions: %+v", plan)
		}
	}
	for _, probe := range []string{"os\tLinux\nid\tunknown\nuid\t0\ndetach\t1\nmanager\tevil -c bad\n", "os\tFreeBSD\nuid\t0\nmanager\tapt-get\n", "os\tLinux\nuid\t0\nmanager\tapt-get\n"} {
		plan := mtrPlanForEnvironment(parseMTREnvironment(probe))
		if plan.CanAutoInstall || plan.ManualReason == "" {
			t.Fatalf("unsupported system received auto install: %+v", plan)
		}
	}
}
func fixtureMTRPath(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("detached Linux shell fixture")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "mtr"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}
func fixtureInstallPlan(a *App, command string) *MTRInstallPlan {
	plan := &MTRInstallPlan{ID: randomID(), Direction: "local", Permission: "root", CanAutoInstall: true, Command: command, scope: "local", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	a.mtrPlans = map[string]*MTRInstallPlan{plan.ID: plan}
	return plan
}
func TestMTRApprovalAndIdempotentInstallation(t *testing.T) {
	fixtureMTRPath(t)
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.cancel()
	marker := filepath.Join(t.TempDir(), "exactly-once")
	// Fake package transaction: no package manager runs in any installation test.
	plan := fixtureInstallPlan(a, "sleep 0.15; printf 'installed\\n' >> "+quoteMTRShell(marker))
	if _, err := a.startMTRInstallation(plan.ID, false); err == nil {
		t.Fatal("installed without consent")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("rejected request had a side effect")
	}
	if _, err := a.startMTRInstallation("made-up", true); err == nil {
		t.Fatal("client invented a plan")
	}
	jobs := make(chan *MTRInstallation, 12)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); job, err := a.startMTRInstallation(plan.ID, true); jobs <- job; errs <- err }()
	}
	wg.Wait()
	close(jobs)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var job *MTRInstallation
	for got := range jobs {
		if job != nil && job != got {
			t.Fatal("double click launched multiple jobs")
		}
		job = got
	}
	select {
	case <-job.done:
	case <-time.After(6 * time.Second):
		t.Fatal("fake install stalled")
	}
	report := job.snapshot()
	if report.Status != "done" {
		t.Fatalf("fake install failed: %+v", report)
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "installed\n" {
		t.Fatalf("duplicate transaction: %q %v", content, err)
	}
	for _, name := range []string{report.LogPath, filepath.Join(filepath.Dir(report.LogPath), "status")} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("log permissions %s: %v %v", name, info, err)
		}
	}
	info, _ := os.Stat(filepath.Dir(report.LogPath))
	if info.Mode().Perm() != 0700 {
		t.Fatal("log directory not private")
	}
	// Internal session/scope/command bookkeeping must not leak into JSON.
	data, _ := json.Marshal(report)
	if strings.Contains(string(data), "baseCommand") || strings.Contains(string(data), "scope") {
		t.Fatal(string(data))
	}
}
func TestMTRRejectsExpiredAndManualPlans(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.cancel()
	plan := fixtureInstallPlan(a, "false")
	plan.ExpiresAt = time.Now().Add(-time.Second)
	if _, err := a.startMTRInstallation(plan.ID, true); err == nil {
		t.Fatal("expired plan executed")
	}
	plan.ExpiresAt = time.Now().Add(time.Minute)
	plan.CanAutoInstall = false
	if _, err := a.startMTRInstallation(plan.ID, true); err == nil {
		t.Fatal("manual plan executed")
	}
	plan.CanAutoInstall = true
	plan.AlreadyInstalled = true
	if _, err := a.startMTRInstallation(plan.ID, true); err == nil {
		t.Fatal("already available tool reinstalled")
	}
}
func TestMTRDetachedTransactionSurvivesCancellation(t *testing.T) {
	fixtureMTRPath(t)
	parent := t.TempDir()
	marker := filepath.Join(parent, "finished; literal ' filename")
	ctx, cancel := context.WithCancel(context.Background())
	output, err := runMTRScript(ctx, nil, mtrDetachedCommand("sleep 0.15; printf ok > "+quoteMTRShell(marker), filepath.Join(parent, "dengshell-mtr.XXXXXXXX")), 64<<10)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := parseMTRLogDirectory(output)
	if err != nil {
		t.Fatal(output, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, _, err := readMTRInstallation(context.Background(), nil, dir)
		if err != nil {
			t.Fatal(err)
		}
		if status == "0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel terminated detached transaction")
		}
		time.Sleep(20 * time.Millisecond)
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "ok" {
		t.Fatalf("detached command did not finish: %q %v", content, err)
	}
}
func TestMTRMissingToolReportAndASNCommand(t *testing.T) {
	d := &Diagnostic{report: DiagnosticReport{Status: "running"}}
	d.finish(context.Background(), &MissingToolError{Direction: "remote"})
	r := d.snapshot()
	if r.MissingTool == nil || r.MissingTool.Name != "mtr" || r.MissingTool.Direction != "remote" || !r.MissingTool.Installable {
		t.Fatalf("missing structured error: %+v", r)
	}
	if command := remoteDiagnosticCommand("::1"); !strings.Contains(command, " -z ") || !strings.Contains(command, "__DENGSHELL_MTR_MISSING__") {
		t.Fatal(command)
	}
}

func TestMTRConcurrentOutputCannotBypassLimit(t *testing.T) {
	out := &mtrLimitedOutput{limit: 1024}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := io.Copy(out, io.LimitReader(strings.NewReader(strings.Repeat("x", 8192)), 8192))
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(out.String()) != 1024 {
		t.Fatalf("output bypassed cap: %d", len(out.String()))
	}
}
