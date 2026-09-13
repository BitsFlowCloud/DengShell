//go:build desktop && linux

package main

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

static gboolean cloudshell_apply_theme(gpointer dark) {
	GtkSettings *settings = gtk_settings_get_default();
	if (settings) {
		if (GPOINTER_TO_INT(dark) < 0) {
			gtk_settings_reset_property(settings, "gtk-application-prefer-dark-theme");
		} else {
			g_object_set(settings, "gtk-application-prefer-dark-theme", GPOINTER_TO_INT(dark), NULL);
		}
	}
	return G_SOURCE_REMOVE;
}

static void cloudshell_set_theme(int dark) {
	// GTK settings must be changed on its main loop, not a Go bridge goroutine.
	g_idle_add(cloudshell_apply_theme, GINT_TO_POINTER(dark));
}
*/
import "C"

// Wails' Linux theme methods are no-ops; use this process's GTK settings for
// window decorations and native file dialogs. It does not alter the OS theme.
func setPlatformTheme(dark bool) {
	value := C.int(0)
	if dark {
		value = 1
	}
	C.cloudshell_set_theme(value)
}

// Remove only our process-local override; GTK then follows the desktop again.
func setPlatformSystemTheme() { C.cloudshell_set_theme(-1) }
