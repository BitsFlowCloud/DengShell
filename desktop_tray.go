//go:build desktop

package main

import (
	"context"
	"errors"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"time"
)

func (d *Desktop) RestoreWindow() {
	d.mu.Lock()
	d.trayHidden = false
	d.windowClosed = false
	d.mu.Unlock()
	runtime.WindowShow(d.ctx)
	runtime.WindowUnminimise(d.ctx)
	platformRaiseWindow()
}
func (d *Desktop) MinimizeState() map[string]any {
	d.mu.Lock()
	pending, hidden := d.minimizePending, d.trayHidden
	d.mu.Unlock()
	return map[string]any{"available": platformTrayAvailable(), "pending": pending, "hidden": hidden, "minimized": runtime.WindowIsMinimised(d.ctx), "action": d.app.Appearance().MinimizeAction}
}
func (d *Desktop) requestMinimize() {
	d.mu.Lock()
	if d.minimizePending || d.quitPending {
		d.mu.Unlock()
		d.RestoreWindow()
		return
	}
	d.minimizeHandled = true
	d.mu.Unlock()
	action := d.app.Appearance().MinimizeAction
	if action == "ask" {
		d.RestoreWindow()
		d.mu.Lock()
		d.minimizePending = true
		d.mu.Unlock()
		runtime.EventsEmit(d.ctx, "dengshell:choose-minimize", d.MinimizeState())
		return
	}
	_ = d.ChooseMinimize(action)
}
func (d *Desktop) ChooseMinimize(action string) error {
	if action != "tray" && action != "minimize" && action != "cancel" {
		return errors.New("未知最小化方式")
	}
	d.mu.Lock()
	d.minimizePending = false
	d.minimizeHandled = true
	d.mu.Unlock()
	if action == "cancel" {
		d.RestoreWindow()
		return nil
	}
	if action == "tray" && platformTrayAvailable() {
		d.mu.Lock()
		d.trayHidden = true
		d.mu.Unlock()
		runtime.WindowHide(d.ctx)
		return nil
	}
	runtime.WindowMinimise(d.ctx)
	return nil
}
func (d *Desktop) startDesktopLifecycle(ctx context.Context, iconPath string) {
	ctx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.lifecycleCancel = cancel
	d.mu.Unlock()
	platformInitializeTray(iconPath)
	go func() {
		lastHidden := false
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				platformCloseTray()
				return
			case <-ticker.C:
				events := platformTrayEvents()
				if events&1 != 0 {
					d.RestoreWindow()
				}
				if events&2 != 0 {
					d.RestoreWindow()
					runtime.Quit(ctx)
				}
				minimized := runtime.WindowIsMinimised(ctx)
				d.mu.Lock()
				hidden, handled := d.trayHidden, d.minimizeHandled
				if !minimized && !hidden {
					d.minimizeHandled = false
				}
				d.mu.Unlock()
				isHidden := hidden || minimized
				if isHidden != lastHidden {
					lastHidden = isHidden
					runtime.EventsEmit(ctx, "dengshell:window-visibility", map[string]bool{"hidden": isHidden})
				}
				if hidden && !platformTrayAvailable() {
					d.RestoreWindow()
					runtime.EventsEmit(ctx, "dengshell:tray-unavailable")
				}
				if minimized && !handled && !hidden {
					d.requestMinimize()
				}
			}
		}
	}()
}
func (d *Desktop) InstallUpdate(id string) error {
	d.mu.Lock()
	if d.detachedNonce != "" || len(d.children) > 0 {
		d.mu.Unlock()
		return errors.New("请先关闭独立窗口，再在主窗口中更新")
	}
	if d.updateInstalling {
		d.mu.Unlock()
		return errors.New("更新安装已启动")
	}
	d.updateInstalling = true
	d.mu.Unlock()
	success := false
	defer func() {
		if !success {
			d.mu.Lock()
			d.updateInstalling = false
			d.mu.Unlock()
		}
	}()
	job, err := d.app.PreparedUpdate(id)
	if err != nil {
		return err
	}
	if err = d.CaptureWindowState(); err != nil {
		return err
	}
	if err = launchUpdateHelper(job, d.app.ConfigDirectory(), func(message string) {
		runtime.EventsEmit(d.ctx, "dengshell:update-progress", message)
	}); err != nil {
		var pending *updateHelperExitPendingError
		if errors.As(err, &pending) {
			// Do not let another attempt reuse a live helper's plan and markers.
			success = true
		}
		return err
	}
	d.mu.Lock()
	d.quitConfirmed = true
	d.quitPending = false
	d.mu.Unlock()
	success = true
	runtime.Quit(d.ctx)
	return nil
}
