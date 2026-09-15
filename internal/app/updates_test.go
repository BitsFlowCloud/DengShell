package app

import (
	"cloudshell/internal/updatetrust"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdatePackageMatchesLinuxDistribution(t *testing.T) {
	for _, tc := range []struct {
		name, release string
		tools, want   bool
	}{
		{"Ubuntu", "ID=ubuntu\nID_LIKE=debian", true, true},
		{"Debian", "ID=debian", true, true},
		{"Mint", "ID=linuxmint\nID_LIKE=\"ubuntu debian\"", true, true},
		{"Fedora with optional dpkg", "ID=fedora", true, false},
		{"openSUSE", "ID=opensuse-tumbleweed\nID_LIKE=\"opensuse suse\"", true, false},
		{"Arch", "ID=arch", true, false},
		{"missing tools", "ID=debian", false, false},
		{"unknown", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := debianPackageHost(tc.release, tc.tools); got != tc.want {
				t.Fatalf("got %t, want %t", got, tc.want)
			}
		})
	}
}

func TestUpdateReceiptHighWaterAndMalformedReceiptFailClosed(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateUpdateBuild(dir, ApplicationBuild+1, ApplicationVersion); err != nil {
		t.Fatal(err)
	}
	receipt, _ := json.Marshal(UpdateReceipt{Build: ApplicationBuild + 5, Version: "v0.02"})
	if err := WriteUpdateReceipt(dir, receipt); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct {
		build   uint64
		version string
	}{{ApplicationBuild + 4, "v0.02"}, {ApplicationBuild + 5, "v0.02"}, {ApplicationBuild + 6, "v0.01"}} {
		if err := ValidateUpdateBuild(dir, candidate.build, candidate.version); err == nil {
			t.Fatalf("rollback accepted: %+v", candidate)
		}
	}
	if err := ValidateUpdateBuild(dir, ApplicationBuild+6, "v0.02"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "update-receipt.json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadUpdateReceipt(dir); err == nil {
		t.Fatal("damaged receipt ignored")
	}
	if err := ValidateUpdateBuild(dir, ApplicationBuild+99, "v1.0"); err == nil {
		t.Fatal("cannot establish installed version but installation accepted")
	}
}

func TestUpdateDescriptorIdentityAndValidation(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trusted := map[string]ed25519.PublicKey{updatetrust.KeyID(public): public}
	now := time.Now()
	hash := strings.Repeat("a", 64)
	old := strings.Repeat("b", 64)
	base := UpdateDescriptor{SchemaVersion: 2, Build: ApplicationBuild + 1, Product: "DengShell", Platform: "linux-amd64", Version: ApplicationVersion, Notes: "中文更新说明", SHA256: hash, ExecutableSHA256: old, Size: 12345}
	for _, tc := range []struct {
		name, current, want string
		receipt             UpdateReceipt
		change              func(*UpdateDescriptor)
	}{
		{"different binary same display version", hash, "available", UpdateReceipt{}, nil},
		{"same ELF higher packaging build", old, "available", UpdateReceipt{}, nil},
		{"same installed receipt", old, "none", UpdateReceipt{Build: ApplicationBuild + 1, Version: ApplicationVersion, SHA256: hash, ExecutableSHA256: old}, nil},
		{"packaging-only change", old, "available", UpdateReceipt{SHA256: old, ExecutableSHA256: old}, nil},
		{"stale receipt high water blocks rollback", old, "none", UpdateReceipt{Build: ApplicationBuild + 2, SHA256: old, ExecutableSHA256: hash}, nil},
		{"legacy schema cannot establish freshness", hash, "none", UpdateReceipt{}, func(d *UpdateDescriptor) { d.SchemaVersion = 1 }},
		{"missing build", hash, "none", UpdateReceipt{}, func(d *UpdateDescriptor) { d.Build = 0 }},
		{"same build different bytes", hash, "none", UpdateReceipt{}, func(d *UpdateDescriptor) { d.Build = ApplicationBuild }},
		{"old build", hash, "none", UpdateReceipt{}, func(d *UpdateDescriptor) { d.Build = ApplicationBuild - 1 }},
		{"old display version with higher build", hash, "none", UpdateReceipt{}, func(d *UpdateDescriptor) { d.Version = "v0.00" }},
		{"unknown version syntax", hash, "none", UpdateReceipt{}, func(d *UpdateDescriptor) { d.Version = "v0.01-old" }},
		{"receipt version blocks old release", hash, "none", UpdateReceipt{Version: "v0.02"}, nil},
		{"invalid sha", hash, "none", UpdateReceipt{}, func(d *UpdateDescriptor) { d.SHA256 = "bad" }},
		{"invalid platform", hash, "none", UpdateReceipt{}, func(d *UpdateDescriptor) { d.Platform = "windows-amd64" }},
		{"invalid product", hash, "none", UpdateReceipt{}, func(d *UpdateDescriptor) { d.Product = "other" }},
		{"oversized", hash, "none", UpdateReceipt{}, func(d *UpdateDescriptor) { d.Size = maximumUpdateSize + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			if tc.change != nil {
				tc.change(&d)
			}
			d, err = updatetrust.Sign(d, private, now, 24*time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(d) }))
			defer server.Close()
			got := checkUpdateTrusted(context.Background(), server.Client(), server.URL, "linux-amd64", tc.current, tc.receipt, trusted, now)
			if got.Status != tc.want {
				t.Fatalf("got %+v", got)
			}
			if got.Status == "available" && (!got.InstallerReady || got.Package.URL != "https://ds.free-vps.org/up.deb") {
				t.Fatalf("wrong installer %+v", got)
			}
			if got.Status == "available" && (got.CurrentBuild != ApplicationBuild || got.LatestBuild != d.Build) {
				t.Fatalf("same-version update is missing its distinguishable builds: %+v", got)
			}
		})
	}
}

func TestUpdateProbeDeadlineIncludesSlowLocalHash(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	got := boundedUpdateProbe(ctx, func() UpdateStatus { <-release; return noUpdate("current") })
	if got.Reason != "timeout" || time.Since(start) > time.Second {
		t.Fatalf("slow disk blocked startup: %+v", got)
	}
}

func TestUpdateCancellationAfterVerifiedBodyDoesNotBecomeReady(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	dir, err := os.MkdirTemp(a.store.dir, ".update-")
	if err != nil {
		t.Fatal(err)
	}
	job := &UpdateDownload{ID: "cancel-after-sync", Status: "downloading", File: filepath.Join(dir, "up.deb")}
	if err = os.WriteFile(job.File, []byte("verified bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.finishUpdateDownload(ctx, job, nil)
	if job.Status != "cancelled" {
		t.Fatalf("cancelled download accepted: %+v", job)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cancelled download left installation bytes")
	}
}
func TestUpdateUnavailableDeadlineAndMalformed(t *testing.T) {
	for _, mode := range []string{"missing", "html", "oversized", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "missing":
					http.NotFound(w, r)
				case "html":
					w.Write([]byte("<html>not published</html>"))
				case "oversized":
					w.Write([]byte(strings.Repeat("x", 65537)))
				case "timeout":
					<-r.Context().Done()
				}
			}))
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
			defer cancel()
			start := time.Now()
			got := checkUpdate(ctx, s.Client(), s.URL, "linux-amd64", strings.Repeat("a", 64), UpdateReceipt{})
			if got.Status != "none" || time.Since(start) > time.Second {
				t.Fatalf("deadline/status %+v", got)
			}
		})
	}
}
func TestUpdateRejectsForeignRedirect(t *testing.T) {
	client := updateClient(time.Second)
	req, _ := http.NewRequest("GET", "https://evil.example/up.exe", nil)
	if client.CheckRedirect(req, nil) == nil {
		t.Fatal("foreign host accepted")
	}
	req, _ = http.NewRequest("GET", "http://ds.free-vps.org/up.exe", nil)
	if client.CheckRedirect(req, nil) == nil {
		t.Fatal("TLS downgrade accepted")
	}
}
func TestDownloadMustMatchOfferAndBytes(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "correct", true: "corrupt"}[corrupt], func(t *testing.T) {
			a, e := New(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			payload := []byte("verified update content")
			path := t.TempDir() + "/payload"
			os.WriteFile(path, payload, 0600)
			hash, _ := FileSHA256(path)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if corrupt {
					w.Write([]byte(strings.Repeat("x", len(payload))))
				} else {
					w.Write(payload)
				}
			}))
			defer s.Close()
			a.updateCheck.once.Do(func() {
				a.updateCheck.done = make(chan struct{})
				a.updateCheck.result = UpdateStatus{Status: "available", Package: &UpdatePackage{Format: NativePackageFormat(), Build: ApplicationBuild + 1, Version: ApplicationVersion, URL: s.URL + "/up.deb", SHA256: hash, ExecutableSHA256: hash, Size: int64(len(payload)), expiresAt: time.Now().Add(time.Hour).Unix()}}
				close(a.updateCheck.done)
			})
			if _, e = a.StartUpdateDownload("wrong"); e == nil {
				t.Fatal("unoffered download accepted")
			}
			job, e := a.StartUpdateDownload(hash)
			if e != nil {
				t.Fatal(e)
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				job, e = a.UpdateDownload(job.ID)
				if e != nil {
					t.Fatal(e)
				}
				if job.Status != "downloading" {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if corrupt {
				if job.Status != "failed" {
					t.Fatalf("corruption accepted %+v", job)
				}
				if _, e = os.Stat(job.File); !os.IsNotExist(e) {
					t.Fatal("corrupt file retained")
				}
			} else {
				if job.Status != "ready" || job.Received != int64(len(payload)) {
					t.Fatalf("not ready %+v", job)
				}
				if _, e = a.PreparedUpdate(job.ID); e != nil {
					t.Fatal(e)
				}
				os.WriteFile(job.File, []byte("modified"), 0600)
				if _, e = a.PreparedUpdate(job.ID); e == nil {
					t.Fatal("modified staging accepted")
				}
			}
		})
	}
}
