package app

import (
	"cloudshell/internal/updatetrust"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLinuxUpdateFamilySelection(t *testing.T) {
	for _, tc := range []struct{ release, want string }{
		{"ID=arch", "pacman"},
		{"ID=manjaro\nID_LIKE=arch", "pacman"},
		{"ID=cachyos\nID_LIKE=\"arch\"", "pacman"},
		{"ID=ubuntu\nID_LIKE=debian", "deb"},
		{"ID=linuxmint\nID_LIKE=\"ubuntu debian\"", "deb"},
		{"ID=fedora\nID_LIKE=fedora", ""},
		{"ID=opensuse-tumbleweed\nID_LIKE=\"opensuse suse\"", ""},
		{"ID=unknown\nID_LIKE=\"arch debian\"", ""},
		{"ID=archlinux-fake", ""},
		{"", ""},
	} {
		if got := linuxPackageFormat(tc.release); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.release, got, tc.want)
		}
	}
}

func TestArchUpdateSignatureBindsPackageFamily(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trusted := map[string]ed25519.PublicKey{updatetrust.KeyID(public): public}
	now := time.Now()
	for _, tc := range []struct {
		platform, want string
		tamper         bool
	}{
		{ArchUpdatePlatform, "available", false},
		{"linux-amd64", "none", false}, // Authentically signed DEB cannot become an Arch update.
		{"linux-amd64", "none", true},  // Relabelling DEB to pacman invalidates its signature.
	} {
		d := UpdateDescriptor{SchemaVersion: 2, Build: ApplicationBuild + 1, Product: "DengShell", Platform: tc.platform, Version: ApplicationVersion, SHA256: strings.Repeat("a", 64), ExecutableSHA256: strings.Repeat("b", 64), Size: 100}
		d, err = updatetrust.Sign(d, private, now, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if tc.tamper {
			d.Platform = ArchUpdatePlatform
		}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(d) }))
		got := checkUpdateTrusted(context.Background(), s.Client(), s.URL, ArchUpdatePlatform, strings.Repeat("c", 64), UpdateReceipt{}, trusted, now)
		s.Close()
		if got.Status != tc.want {
			t.Fatalf("%+v: %+v", tc, got)
		}
		if got.Status == "available" && (got.Package.Format != "pacman" || got.Package.URL != "https://ds.free-vps.org/up.pkg.tar.zst") {
			t.Fatalf("wrong Arch offer: %+v", got)
		}
	}
	manifest, artifact := updateAddress(ArchUpdatePlatform)
	if manifest != "https://ds.free-vps.org/up.pkg.tar.zst.json" || artifact != "https://ds.free-vps.org/up.pkg.tar.zst" {
		t.Fatal("Arch address changed")
	}
	if updatePackageFormat("linux-amd64") != "deb" || updatePackageFormat("windows-amd64") != "exe" {
		t.Fatal("legacy package formats changed")
	}
}
