package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const runtimeSuccessValue = "DengShell runtime ready v1\n"

func runtimeSuccessPath(configDir string) string {
	return filepath.Join(configDir, "runtime-success-v1-"+runtime.GOOS+"-"+runtime.GOARCH)
}

func runtimePreviouslyReady(configDir string) bool {
	data, err := os.ReadFile(runtimeSuccessPath(configDir))
	return err == nil && string(data) == runtimeSuccessValue
}

// Only the successful native frontend callback calls this. A dependency check
// alone, an installer cancellation, and browser mode never mark desktop ready.
func recordRuntimeReady(configDir string) error {
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(configDir, ".runtime-ready-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.WriteString(runtimeSuccessValue); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), runtimeSuccessPath(configDir))
}

// Configuration follows the real executable, never the current working directory
// or a legacy user profile. An explicit --config continues to override it.
func portableConfigDir() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return portableDirectoryForExecutable(executable)
}
func portableDirectoryForExecutable(executable string) (string, error) {
	real, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(real)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(absolute), "data"), nil
}

func desktopInstanceID(configDir string) string {
	const historicalID = "cloudshell-desktop-bitsflow"
	canonical, err := filepath.Abs(configDir)
	if err != nil {
		canonical = filepath.Clean(configDir)
	}
	if real, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = real
	}
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%s-%x", historicalID, digest[:12])
}

type monitorBounds struct {
	X, Y, Width, Height                 int
	WorkX, WorkY, WorkWidth, WorkHeight int
}

type initialWindowBounds struct {
	X, Y, Width, Height, MinWidth, MinHeight int
	WorkWidth, WorkHeight                    int
}

func initialWindowForMonitor(m monitorBounds) initialWindowBounds {
	if m.Width <= 0 || m.Height <= 0 {
		m = monitorBounds{Width: 1920, Height: 1080, WorkWidth: 1920, WorkHeight: 1080}
	}
	if m.WorkWidth <= 0 || m.WorkHeight <= 0 {
		m.WorkX, m.WorkY, m.WorkWidth, m.WorkHeight = m.X, m.Y, m.Width, m.Height
	}
	w := min((m.Width*3+2)/4, m.WorkWidth)
	h := min((m.Height*3+2)/4, m.WorkHeight)
	return initialWindowBounds{
		X: m.WorkX + (m.WorkWidth-w)/2, Y: m.WorkY + (m.WorkHeight-h)/2,
		Width: w, Height: h, MinWidth: min(640, w), MinHeight: min(360, h),
		WorkWidth: m.WorkWidth, WorkHeight: m.WorkHeight,
	}
}

func restoreSavedWindowBounds(bounds initialWindowBounds, width, height int) initialWindowBounds {
	if width <= 0 || height <= 0 {
		return bounds
	}
	width = max(bounds.MinWidth, min(bounds.WorkWidth, width))
	height = max(bounds.MinHeight, min(bounds.WorkHeight, height))
	bounds.X += (bounds.Width - width) / 2
	bounds.Y += (bounds.Height - height) / 2
	bounds.Width, bounds.Height = width, height
	return bounds
}
