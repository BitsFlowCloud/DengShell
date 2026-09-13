//go:build desktop && linux

package main

/*
#cgo pkg-config: gio-2.0
#include <gio/gio.h>
#include <stdlib.h>

// GSettings reads desktop configuration directly, independently of the
// application-specific GtkSettings override used for manual light/dark mode.
static char *dengshell_desktop_setting(const char *schema_name, const char *key) {
	GSettingsSchemaSource *source = g_settings_schema_source_get_default();
	if (!source) return NULL;
	GSettingsSchema *schema = g_settings_schema_source_lookup(source, schema_name, TRUE);
	if (!schema) return NULL;
	char *value = NULL;
	if (g_settings_schema_has_key(schema, key)) {
		GSettings *settings = g_settings_new_full(schema, NULL, NULL);
		value = g_settings_get_string(settings, key);
		g_object_unref(settings);
	}
	g_settings_schema_unref(schema);
	return value;
}
*/
import "C"

import (
	"context"
	"os"
	"strings"
	"time"
	"unsafe"

	"github.com/godbus/dbus/v5"
)

func desktopSetting(schema, name string) string {
	cSchema, cName := C.CString(schema), C.CString(name)
	defer C.free(unsafe.Pointer(cSchema))
	defer C.free(unsafe.Pointer(cName))
	value := C.dengshell_desktop_setting(cSchema, cName)
	if value == nil {
		return ""
	}
	defer C.g_free(C.gpointer(value))
	return C.GoString(value)
}

func portalTheme(value any) string {
	// Read on older portals wraps the uint32 in two variants, while ReadOne
	// and SettingChanged use one. Accept both without mistaking unknown=0
	// for an explicit light preference.
	for depth := 0; depth < 4; depth++ {
		if variant, ok := value.(dbus.Variant); ok {
			value = variant.Value()
			continue
		}
		break
	}
	switch value {
	case uint32(1):
		return "dark"
	case uint32(2):
		return "light"
	default:
		return ""
	}
}

func platformSystemTheme() string {
	// GNOME, KDE Plasma and other portal-enabled desktops expose the same
	// read-only setting. No child shell, gsettings command, or network access
	// is needed; the shared D-Bus connection is reused between reads.
	if bus, err := dbus.SessionBus(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		var value dbus.Variant
		err = bus.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop").
			CallWithContext(ctx, "org.freedesktop.portal.Settings.Read", 0, "org.freedesktop.appearance", "color-scheme").Store(&value)
		cancel()
		if err == nil {
			if theme := portalTheme(value); theme != "" {
				return theme
			}
		}
	}
	// Older GNOME/Cinnamon desktops can lack the appearance portal. Do not
	// consult an installed-but-unused GNOME schema on a KDE/Xfce session.
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
	schema := ""
	if strings.Contains(desktop, "cinnamon") {
		schema = "org.cinnamon.desktop.interface"
	} else if strings.Contains(desktop, "gnome") || strings.Contains(desktop, "unity") || strings.Contains(desktop, "budgie") {
		schema = "org.gnome.desktop.interface"
	}
	if schema != "" {
		switch desktopSetting(schema, "color-scheme") {
		case "prefer-dark":
			return "dark"
		case "prefer-light":
			return "light"
		}
		if theme := desktopSetting(schema, "gtk-theme"); theme != "" {
			if strings.Contains(strings.ToLower(theme), "dark") {
				return "dark"
			}
			return "light"
		}
	}
	// GTK's process override is reset in system mode, making WebKit's own
	// native prefers-color-scheme the final fallback on other desktops.
	return ""
}
