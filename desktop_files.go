//go:build desktop

package main

import (
	"errors"
	"github.com/pkg/browser"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
)

func (d *Desktop) ChooseExternalEditor() (string, error) {
	if err := d.app.RequireUnlocked(); err != nil {
		return "", err
	}

	return runtime.OpenFileDialog(d.ctx, runtime.OpenDialogOptions{Title: "选择本地文本编辑器可执行文件"})
}
func (d *Desktop) OpenRemoteFile(sessionID, remote string) (string, error) {
	if err := d.app.RequireUnlocked(); err != nil {
		return "", err
	}

	local, e := d.app.PrepareExternalFile(sessionID, remote)
	if e != nil {
		return "", e
	}
	if e = d.app.RequireUnlocked(); e != nil {
		return "", e
	}
	editor := d.app.Appearance().ExternalEditor
	if editor == "" {
		e = browser.OpenFile(local)
	} else {
		if !filepath.IsAbs(editor) {
			return "", errors.New("自定义打开方式必须为可执行文件的绝对路径")
		}
		info, err := os.Stat(editor)
		isMacApp := err == nil && goruntime.GOOS == "darwin" && info.IsDir() && filepath.Ext(editor) == ".app"
		if err != nil || (info.IsDir() && !isMacApp) {
			return "", errors.New("找不到自定义编辑器，请在打开方式中重新选择")
		}
		cmd := exec.Command(editor, local)
		if isMacApp {
			cmd = exec.Command("/usr/bin/open", "-a", editor, "--", local)
		}
		e = cmd.Start()
		if e == nil {
			go cmd.Wait()
		}
	}
	return local, e
}
func (d *Desktop) DownloadArchive(sessionID, remote string) (string, error) {
	if err := d.app.RequireUnlocked(); err != nil {
		return "", err
	}

	name := path.Base(remote)
	if name == "/" {
		name = "root"
	}
	destination, e := runtime.SaveFileDialog(d.ctx, runtime.SaveDialogOptions{Title: "打包并下载", DefaultFilename: name + ".tar.gz"})
	if e != nil || destination == "" {
		return "", e
	}
	if e = d.app.RequireUnlocked(); e != nil {
		return "", e
	}
	if e = d.app.DownloadArchiveTo(sessionID, remote, destination); e != nil {
		return "", e
	}
	return destination, nil
}

// One save dialog for the entire selection, including selected directories.
func (d *Desktop) DownloadSelectionArchive(sessionID string, paths []string) (string, error) {
	if err := d.app.RequireUnlocked(); err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", errors.New("请先选择文件")
	}
	name := "DengShell-files.tar.gz"
	if len(paths) == 1 {
		name = path.Base(paths[0]) + ".tar.gz"
	}
	destination, err := runtime.SaveFileDialog(d.ctx, runtime.SaveDialogOptions{Title: "打包并下载所选项目", DefaultFilename: name})
	if err != nil || destination == "" {
		return "", err
	}
	if err = d.app.RequireUnlocked(); err != nil {
		return "", err
	}
	if err = d.app.DownloadSelectionArchiveTo(sessionID, paths, destination); err != nil {
		return "", err
	}
	return destination, nil
}
func (d *Desktop) OpenAbout() { runtime.BrowserOpenURL(d.ctx, "https://dengshell.com") }
