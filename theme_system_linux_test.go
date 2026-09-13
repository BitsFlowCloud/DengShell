//go:build desktop && linux

package main

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPortalThemeKnownUnknownAndLegacyVariants(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{"dark", uint32(1), "dark"},
		{"light", uint32(2), "light"},
		{"no preference", uint32(0), ""},
		{"unknown", uint32(99), ""},
		{"invalid type", "dark", ""},
		{"variant", dbus.MakeVariant(uint32(1)), "dark"},
		{"legacy nested variant", dbus.MakeVariant(dbus.MakeVariant(uint32(2))), "light"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := portalTheme(test.value); got != test.want {
				t.Fatalf("portalTheme = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSystemThemeUnavailableGSettingsSchema(t *testing.T) {
	if value := desktopSetting("org.dengshell.nonexistent.schema", "color-scheme"); value != "" {
		t.Fatalf("unavailable schema should be unknown, got %q", value)
	}
	if value := desktopSetting("org.gnome.desktop.interface", "dengshell-no-such-property"); value != "" {
		t.Fatalf("unavailable setting should be unknown, got %q", value)
	}
}

// This integration fixture runs only inside an explicitly isolated dbus-run-session;
// it never owns or changes the real desktop's portal service.
func TestSystemThemePortalLive(t *testing.T) {
	if os.Getenv("DENGSHELL_THEME_PORTAL_TEST") != "1" {
		t.Skip("requires isolated DENGSHELL_THEME_PORTAL_TEST=1 dbus-run-session")
	}
	bus, err := dbus.SessionBus()
	if err != nil {
		t.Fatal(err)
	}
	portal := &themePortalFixture{}
	portal.value.Store(1)
	if err := bus.Export(portal, "/org/freedesktop/portal/desktop", "org.freedesktop.portal.Settings"); err != nil {
		t.Fatal(err)
	}
	reply, err := bus.RequestName("org.freedesktop.portal.Desktop", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("isolated portal ownership: %v, %v", reply, err)
	}
	defer bus.ReleaseName("org.freedesktop.portal.Desktop")
	for _, test := range []struct {
		value uint32
		want  string
	}{{1, "dark"}, {2, "light"}, {1, "dark"}} {
		portal.value.Store(test.value)
		if got := platformSystemTheme(); got != test.want {
			t.Fatalf("live portal preference = %q, want %q", got, test.want)
		}
	}
}

type themePortalFixture struct{ value atomic.Uint32 }

func (p *themePortalFixture) Read(namespace, key string) (dbus.Variant, *dbus.Error) {
	if namespace != "org.freedesktop.appearance" || key != "color-scheme" {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("unexpected desktop setting"))
	}
	return dbus.MakeVariant(dbus.MakeVariant(p.value.Load())), nil
}
