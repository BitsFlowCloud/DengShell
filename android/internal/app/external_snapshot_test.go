package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareExternalDirectoryPreservesNamesBytesAndEmptyFolders(t *testing.T) {
	a, s, remoteRoot := fileToolFixture(t)
	a.store = &Store{dir: t.TempDir()}
	remote := filepath.Join(remoteRoot, "中文目录")
	if err := os.MkdirAll(filepath.Join(remote, "空文件夹"), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte{0xff, 0xfe, 0x2d, 0x4e, 0x0d, 0x00, 0x0a, 0x00, 0x00, 0x80}
	if err := os.WriteFile(filepath.Join(remote, "原始.bin"), original, 0600); err != nil {
		t.Fatal(err)
	}
	local, err := a.PrepareExternalFile(s.ID, remote)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(local) != "中文目录" || !strings.HasPrefix(local, filepath.Join(a.store.dir, "opened")+string(os.PathSeparator)) {
		t.Fatal("snapshot escaped data directory", local)
	}
	actual, err := os.ReadFile(filepath.Join(local, "原始.bin"))
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatal("changed file bytes", err)
	}
	if info, err := os.Stat(filepath.Join(local, "空文件夹")); err != nil || !info.IsDir() {
		t.Fatal("empty directory lost", err)
	}
	file, err := a.PrepareExternalFile(s.ID, filepath.Join(remote, "原始.bin"))
	if err != nil {
		t.Fatal(err)
	}
	actual, err = os.ReadFile(file)
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatal("single-file association changed bytes", err)
	}
}

func TestExternalSnapshotRejectsLinksSpecialEntriesLimitsAndCleansPartialOutput(t *testing.T) {
	_, s, remoteRoot := fileToolFixture(t)
	for _, scenario := range []string{"bytes", "entries", "depth", "symlink", "special"} {
		t.Run(scenario, func(t *testing.T) {
			remote := filepath.Join(remoteRoot, scenario)
			if err := os.MkdirAll(remote, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(remote, "a-copied-first.txt"), []byte("first"), 0600); err != nil {
				t.Fatal(err)
			}
			limits := externalSnapshotLimits{bytes: 1 << 20, entries: 20, depth: 5}
			expected := ""
			switch scenario {
			case "bytes":
				limits.bytes = 6
				expected = "大小"
				os.WriteFile(filepath.Join(remote, "z-too-big"), []byte("second"), 0600)
			case "entries":
				limits.entries = 1
				expected = "超过"
			case "depth":
				limits.depth = 0
				expected = "层级"
			case "symlink":
				expected = "符号链接"
				if err := os.Symlink(remoteRoot, filepath.Join(remote, "z-cycle")); err != nil {
					t.Skip(err)
				}
			case "special":
				expected = "特殊文件"
				tool, err := exec.LookPath("mkfifo")
				if err != nil {
					t.Skip("FIFO fixture requires mkfifo")
				}
				if err = exec.Command(tool, filepath.Join(remote, "z-pipe")).Run(); err != nil {
					t.Fatal(err)
				}
			}
			opened := t.TempDir()
			local, err := prepareExternalSnapshot(context.Background(), s.files, remote, opened, limits)
			if err == nil || local != "" || !strings.Contains(err.Error(), expected) {
				t.Fatalf("expected %q rejection, local=%q err=%v", expected, local, err)
			}
			remaining, err := os.ReadDir(opened)
			if err != nil || len(remaining) != 0 {
				t.Fatal("incomplete snapshot retained", err, remaining)
			}
		})
	}
}

func TestExternalSnapshotRejectsRootAndAncestorSymlinksAndCancellation(t *testing.T) {
	_, s, remoteRoot := fileToolFixture(t)
	real := filepath.Join(remoteRoot, "actual")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(remoteRoot, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	opened := t.TempDir()
	for _, remote := range []string{link, filepath.Join(link, "file")} {
		_, err := prepareExternalSnapshot(context.Background(), s.files, remote, opened, externalSnapshotLimits{bytes: 100, entries: 10, depth: 5})
		if err == nil || !strings.Contains(err.Error(), "符号链接") {
			t.Fatal("followed selected symlink", remote, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepareExternalSnapshot(ctx, s.files, real, opened, externalSnapshotLimits{bytes: 100, entries: 10, depth: 5}); err != context.Canceled {
		t.Fatal("ignored cancellation", err)
	}
	remaining, err := os.ReadDir(opened)
	if err != nil || len(remaining) != 0 {
		t.Fatal("rejection created output", err)
	}
}
