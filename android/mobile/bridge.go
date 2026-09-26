package mobile

import (
	"cloudshell/internal/app"
	"embed"
	"errors"
	"io/fs"
	"sync"
)

//go:embed assets/web
var webAssets embed.FS

var bridge struct {
	sync.Mutex
	app *app.App
}

// Start launches the existing DengShell API on Android's loopback interface.
// The URL contains a one-time bootstrap capability in its fragment.
func Start(configDir string) (string, error) {
	if configDir == "" {
		return "", errors.New("missing Android app data directory")
	}
	bridge.Lock()
	defer bridge.Unlock()
	if bridge.app != nil {
		return bridge.app.BrowserURL(), nil
	}
	assets, err := fs.Sub(webAssets, "assets/web")
	if err != nil {
		return "", err
	}
	instance, err := app.New(configDir)
	if err != nil {
		return "", err
	}
	if err := instance.Start("127.0.0.1:0", assets); err != nil {
		instance.Close()
		return "", err
	}
	bridge.app = instance
	return instance.BrowserURL(), nil
}

// Stop releases all SSH sessions and the loopback listener.
func Stop() {
	bridge.Lock()
	defer bridge.Unlock()
	if bridge.app != nil {
		// Keep Start waiting until sessions, sync jobs and the listener are
		// fully closed. Otherwise a quick close/reopen can start two backends
		// against the same encrypted configuration directory.
		bridge.app.Close()
		bridge.app = nil
	}
}
