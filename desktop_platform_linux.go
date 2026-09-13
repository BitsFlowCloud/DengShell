//go:build desktop && linux

package main

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

typedef struct { int x, y, width, height, wx, wy, ww, wh; } DengMonitor;
static int dengshell_monitor(DengMonitor *result) {
    if (!gtk_init_check(NULL, NULL)) return 0;
    GdkDisplay *display = gdk_display_get_default();
    if (!display) return 0;
    GdkMonitor *monitor = NULL;
    GdkSeat *seat = gdk_display_get_default_seat(display);
    GdkDevice *pointer = seat ? gdk_seat_get_pointer(seat) : NULL;
    if (pointer) {
        gint x = 0, y = 0;
        gdk_device_get_position(pointer, NULL, &x, &y);
        monitor = gdk_display_get_monitor_at_point(display, x, y);
    }
    if (!monitor) monitor = gdk_display_get_primary_monitor(display);
    if (!monitor && gdk_display_get_n_monitors(display) > 0) monitor = gdk_display_get_monitor(display, 0);
    if (!monitor) return 0;
    GdkRectangle full, work;
    gdk_monitor_get_geometry(monitor, &full);
    gdk_monitor_get_workarea(monitor, &work);
    result->x=full.x; result->y=full.y; result->width=full.width; result->height=full.height;
    result->wx=work.x; result->wy=work.y; result->ww=work.width; result->wh=work.height;
    return 1;
}
*/
import "C"

import (
	"context"
	"errors"
	"os"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func initialPlatformWindow() (initialWindowBounds, error) {
	// Match Wails' backend selection before the first GTK initialization. GDK
	// supplies logical pixels, including the desktop's configured scale factor.
	if os.Getenv("GDK_BACKEND") == "" {
		session := os.Getenv("XDG_SESSION_TYPE")
		if session == "" || session == "unspecified" || session == "x11" {
			_ = os.Setenv("GDK_BACKEND", "x11")
		}
	}
	var monitor C.DengMonitor
	if C.dengshell_monitor(&monitor) == 0 {
		return initialWindowBounds{}, errors.New("无法连接桌面显示服务。请在 Ubuntu 图形桌面中启动 DengShell；SSH/无桌面环境可使用 --browser")
	}
	return initialWindowForMonitor(monitorBounds{
		X: int(monitor.x), Y: int(monitor.y), Width: int(monitor.width), Height: int(monitor.height),
		WorkX: int(monitor.wx), WorkY: int(monitor.wy), WorkWidth: int(monitor.ww), WorkHeight: int(monitor.wh),
	}), nil
}

func finalizeInitialPlatformWindow(ctx context.Context, bounds initialWindowBounds) {
	// GTK/Wayland permits the compositor to choose placement; X11 honors this
	// work-area position. Initial size was already supplied before window creation.
	runtime.WindowSetPosition(ctx, bounds.X, bounds.Y)
}

func platformRuntimeDescription() string { return "GTK 3 / WebKitGTK 4.1" }
