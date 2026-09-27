//go:build !windows || !amd64

package rdpengine

import "errors"

func Available() bool          { return false }
func Prepare() (string, error) { return "", errors.New("此 RDP 测试版目前仅支持 Windows x64") }
