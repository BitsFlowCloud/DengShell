//go:build !linux && !windows

package main

import (
	"errors"
	"os/exec"
	"time"
)

func prepareUpdaterProcess(*exec.Cmd)       {}
func prepareRestartedApplication(*exec.Cmd) {}
func waitForUpdateParent(int, time.Duration) error {
	return errors.New("此平台不支持在线安装")
}
func validatePlatformUpdate(updatePlan) error { return errors.New("此平台不支持在线安装") }
func applyPlatformUpdate(updatePlan) (string, error) {
	return "", errors.New("此平台不支持在线安装")
}
func rollbackPlatformUpdate(updatePlan) error { return errors.New("此平台不支持在线安装") }
