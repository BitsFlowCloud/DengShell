package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitialWindowUsesMonitorResolutionAndFitsWorkArea(t *testing.T) {
	for _, tc := range []struct {
		name    string
		monitor monitorBounds
		want    initialWindowBounds
	}{
		{"1080p", monitorBounds{Width: 1920, Height: 1080, WorkWidth: 1920, WorkHeight: 1040}, initialWindowBounds{240, 115, 1440, 810, 640, 360, 1920, 1040}},
		{"small laptop", monitorBounds{Width: 1366, Height: 768, WorkWidth: 1366, WorkHeight: 728}, initialWindowBounds{170, 76, 1025, 576, 640, 360, 1366, 728}},
		{"scaled logical display", monitorBounds{Width: 1093, Height: 614, WorkWidth: 1093, WorkHeight: 580}, initialWindowBounds{136, 59, 820, 461, 640, 360, 1093, 580}},
		{"secondary negative origin", monitorBounds{X: -1920, Width: 1920, Height: 1080, WorkX: -1920, WorkY: 32, WorkWidth: 1920, WorkHeight: 1048}, initialWindowBounds{-1680, 151, 1440, 810, 640, 360, 1920, 1048}},
		{"tiny usable area", monitorBounds{Width: 800, Height: 600, WorkWidth: 450, WorkHeight: 320}, initialWindowBounds{0, 0, 450, 320, 450, 320, 450, 320}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := initialWindowForMonitor(tc.monitor); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRuntimeSuccessRequiresExactSuccessfulMarker(t *testing.T) {
	dir := t.TempDir()
	if runtimePreviouslyReady(dir) {
		t.Fatal("fresh config was marked ready")
	}
	if err := os.WriteFile(runtimeSuccessPath(dir), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if runtimePreviouslyReady(dir) {
		t.Fatal("invalid marker was accepted")
	}
	if err := recordRuntimeReady(dir); err != nil {
		t.Fatal(err)
	}
	if !runtimePreviouslyReady(dir) {
		t.Fatal("successful runtime wasn't cached")
	}
	if err := recordRuntimeReady(filepath.Join(dir, "child")); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopInstancesIsolatePortableFolders(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	if desktopInstanceID(first) == desktopInstanceID(second) {
		t.Fatal("independent configs share a lock")
	}
	if desktopInstanceID(first) != desktopInstanceID(filepath.Join(first, ".")) {
		t.Fatal("same config received different locks")
	}
}

func TestRestoreSavedWindowBounds(t *testing.T) {
	initial := initialWindowForMonitor(monitorBounds{Width: 1920, Height: 1080, WorkWidth: 1920, WorkHeight: 1040})
	restored := restoreSavedWindowBounds(initial, 1400, 900)
	if restored.Width != 1400 || restored.Height != 900 || restored.X != 260 || restored.Y != 70 {
		t.Fatalf("saved dimensions not centered: %+v", restored)
	}
	clamped := restoreSavedWindowBounds(initial, 8000, 4000)
	if clamped.Width != 1920 || clamped.Height != 1040 || clamped.X != 0 || clamped.Y != 0 {
		t.Fatalf("off-screen saved dimensions not clamped: %+v", clamped)
	}
	if restored := restoreSavedWindowBounds(initial, 0, 0); restored != initial {
		t.Fatal("fresh window changed")
	}
}
