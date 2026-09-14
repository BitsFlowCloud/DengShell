//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPacmanPackageIdentity(t *testing.T) {
	valid := "pkgname = dengshell\npkgver = 0.1.0-28\narch = x86_64\ndepend = gtk3\ndepend = polkit\n"
	for _, tc := range []struct {
		name, text string
		want       bool
	}{
		{"valid", valid, true},
		{"wrong package", strings.Replace(valid, "dengshell", "another-app", 1), false},
		{"wrong architecture", strings.Replace(valid, "x86_64", "aarch64", 1), false},
		{"missing version", strings.Replace(valid, "pkgver = 0.1.0-28\n", "", 1), false},
		{"duplicate identity", valid + "pkgname = dengshell\n", false},
		{"version control character", strings.Replace(valid, "0.1.0-28", "0.1.0-28\x1b", 1), false},
		{"oversized", valid + strings.Repeat("x", 65536), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validatePacmanMetadata(tc.text)
			if (err == nil) != tc.want {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestLinuxInstallerRejectsForeignAndLegacyArchPlan(t *testing.T) {
	for _, tc := range []struct {
		format, host string
		want         bool
	}{
		{"deb", "deb", true}, {"", "deb", true}, {"pacman", "pacman", true},
		{"deb", "pacman", false}, {"pacman", "deb", false}, {"", "pacman", false}, {"pacman", "", false},
	} {
		_, err := linuxUpdatePlanFormat(tc.format, tc.host)
		if (err == nil) != tc.want {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

func TestLinuxUpdateSystemAuthorizationArguments(t *testing.T) {
	lookup := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	file := "/private configuration/更新包;$(not-a-command).pkg.tar.zst"
	for _, tc := range []struct {
		format string
		root   bool
		args   []string
	}{
		{"pacman", false, []string{"/usr/bin/pkexec", "/usr/bin/pacman", "--upgrade", "--noconfirm", "--needed", file}},
		{"pacman", true, []string{"/usr/bin/pacman", "--upgrade", "--noconfirm", "--needed", file}},
		{"deb", false, []string{"/usr/bin/pkexec", "/usr/bin/dpkg", "--install", file}},
	} {
		cmd, err := linuxPackageInstallCommand(tc.format, file, tc.root, lookup)
		if err != nil || !reflect.DeepEqual(cmd.Args, tc.args) {
			t.Fatalf("%+v: %v %v", tc, cmd, err)
		}
	}
	_, err := linuxPackageInstallCommand("pacman", file, false, func(name string) (string, error) {
		if name == "pkexec" {
			return "", errors.New("missing")
		}
		return "/usr/bin/" + name, nil
	})
	if err == nil {
		t.Fatal("missing authorization tool silently bypassed")
	}
}

func TestPackageMetadataOutputIsBounded(t *testing.T) {
	t.Setenv("DENGSHELL_METADATA_CHILD", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = packageMetadata(ctx, executable, "-test.run=^TestPackageMetadataChild$"); err == nil {
		t.Fatal("oversized command output accepted")
	}
}
func TestPackageMetadataChild(t *testing.T) {
	if os.Getenv("DENGSHELL_METADATA_CHILD") != "1" {
		return
	}
	_, _ = os.Stdout.Write([]byte(strings.Repeat("x", 131072)))
	os.Exit(0)
}
