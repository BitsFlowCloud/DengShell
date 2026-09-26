package app

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const ArchUpdatePlatform = "linux-amd64-pacman"

// The package family is authenticated through the descriptor's platform field.
// Keep the original Debian platform unchanged for deployed clients/signatures.
func updatePackageFormat(platform string) string {
	switch platform {
	case "windows-amd64":
		return "exe"
	case "linux-amd64":
		return "deb"
	case ArchUpdatePlatform:
		return "pacman"
	}
	return ""
}

func currentUpdatePlatform() string {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	if platform == "linux-amd64" && NativePackageFormat() == "pacman" {
		return ArchUpdatePlatform
	}
	return platform
}

func NativePackageFormat() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	release, err := os.ReadFile("/etc/os-release")
	if err != nil {
		release, _ = os.ReadFile("/usr/lib/os-release")
	}
	return linuxPackageFormat(string(release))
}

// An optional foreign package manager does not change the host distribution.
func linuxPackageFormat(release string) string {
	values := map[string]string{}
	for _, line := range strings.Split(release, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && (key == "ID" || key == "ID_LIKE") {
			values[key] = strings.Trim(value, "\"'")
		}
	}
	family := func(id string) string {
		switch id {
		case "debian", "ubuntu":
			return "deb"
		case "arch":
			return "pacman"
		}
		return ""
	}
	if format := family(values["ID"]); format != "" {
		return format
	}
	format := ""
	for _, id := range strings.Fields(values["ID_LIKE"]) {
		candidate := family(id)
		if candidate == "" {
			continue
		}
		if format != "" && format != candidate {
			return "" // Ambiguous families must not select an installer.
		}
		format = candidate
	}
	return format
}

func debianPackageHost(release string, toolsPresent bool) bool {
	return toolsPresent && linuxPackageFormat(release) == "deb"
}

func NativePackageUpdateSupported() bool {
	if runtime.GOOS != "linux" {
		return runtime.GOOS == "windows"
	}
	var required []string
	switch NativePackageFormat() {
	case "deb":
		required = []string{"dpkg", "dpkg-deb"}
	case "pacman":
		required = []string{"pacman", "bsdtar"}
	default:
		return false
	}
	for _, name := range required {
		if _, err := exec.LookPath(name); err != nil {
			return false
		}
	}
	return true
}
