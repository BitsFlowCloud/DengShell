//go:build desktop && !linux && !windows && !darwin

package main

import (
	"context"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func initialPlatformWindow() (initialWindowBounds, error) {
	return initialWindowForMonitor(monitorBounds{}), nil
}
func finalizeInitialPlatformWindow(ctx context.Context, _ initialWindowBounds) {
	runtime.WindowCenter(ctx)
}
func platformRuntimeDescription() string { return "native webview" }
