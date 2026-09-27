//go:build desktop && windows

package main

import (
	"cloudshell/internal/app"
	"errors"
)

func (d *Desktop) RDPConnect(profileID, secret string) (app.RDPInfo, error) {
	a, ok := d.app.(*app.App)
	if !ok {
		return app.RDPInfo{}, errors.New("请在 DengShell 主窗口中打开 RDP 连接")
	}
	return a.StartRDPEmbedded(profileID, secret, platformWindowHandle())
}
func (d *Desktop) RDPViewport(id string, x, y, width, height, viewportWidth float64, visible bool) error {
	a, ok := d.app.(*app.App)
	if !ok {
		return errors.New("请在主窗口使用 RDP")
	}
	return a.RDPViewport(id, x, y, width, height, viewportWidth, visible)
}
func (d *Desktop) RDPSendSecureAttention(id string) error {
	a, ok := d.app.(*app.App)
	if !ok {
		return errors.New("请在主窗口使用 RDP")
	}
	return a.RDPSendSecureAttention(id)
}

func (d *Desktop) RDPResolution(id string, width, height int) error {
	a, ok := d.app.(*app.App)
	if !ok {
		return errors.New("请在主窗口使用 RDP")
	}
	return a.RDPResolution(id, width, height)
}
