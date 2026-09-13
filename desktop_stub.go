//go:build !desktop

package main

import (
	"cloudshell/internal/app"
	"errors"
	"io/fs"
)

func desktopAvailable() bool                         { return false }
func runDesktop(_ *app.App, _ fs.FS, _ string) error { return nil }

func runDetachedProcess(fs.FS) error { return errors.New("独立窗口需要桌面版本") }
