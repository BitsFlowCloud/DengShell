//go:build !windows

package app

import (
	"errors"
	"os/exec"
)

func configureRDPProcess(cmd *exec.Cmd)           {}
func ownRDPProcess(cmd *exec.Cmd) (func(), error) { return func() {}, nil }

func validateRDPParent(parent uintptr) error {
	return errors.New("此 RDP 测试版目前仅支持 Windows x64")
}

func rdpHostDisplayArguments(parent uintptr) string { return "" }
