# DengShell macOS 预览版

## 安装

支持 macOS 13 Ventura 或更高版本。Universal 通用应用同时包含 Apple 芯片 arm64 和 Intel x86_64 原生程序。

打开 `DengShell-macos-universal.dmg`，把 `DengShell.app` 拖到 `Applications` 后启动；也可以解压 ZIP 后拖入应用程序文件夹。SSH 终端和文件管理无需另装 Go、Node.js、WebView2 或 GTK。

当前默认构建采用本地临时签名（ad-hoc），尚未经过 Apple Developer ID 签名和公证。它是供测试的预览版。通过浏览器下载后，macOS 可能阻止第一次打开；核对来源后，可在“系统设置 → 隐私与安全性”中对该应用选择“仍要打开”。无需关闭系统的 Gatekeeper。

## 配置和功能

- 默认配置保存于 `~/Library/Application Support/DengShell/`，包含加密配置、密钥、导入字体和背景。更新或移动应用时保留整个目录。
- `--config /绝对路径` 可指定隔离配置。裸可执行程序仍使用旁边的 `data/`。
- macOS 使用系统 WebKit、菜单栏和 Dock；支持系统明暗主题和原生文件选择。
- 最小化进入 Dock；当前未提供 macOS 状态栏托盘。
- 独立窗口支持通过“合并到窗口”菜单合并；跨窗口鼠标定位暂不支持。
- 本机 IPv4/IPv6 延迟检测使用系统 ping/ping6。Linux 远端监控仍通过 SSH 执行。
- 本机 MTR 是可选功能：在 Mac 终端中执行 `brew install mtr`，再按 Homebrew 提示配置 `mtr-packet` 权限。Finder 启动也能查找标准 Homebrew 安装位置；DengShell 不自动提权。远端 MTR 继续使用远端 Linux 安装流程。
- 此 macOS 预览版尚未接入自动更新；新版本退出应用后替换 `.app`，配置保留。
- 第三方编辑器可以选择 `.app`；通过 macOS `open -a` 打开文件。

## 从源码构建

在 Mac 上安装 Xcode Command Line Tools、Go 1.27.1（本项目验证版本）、Node.js 22 和 Python 3。应用运行时不需要这些开发工具。

```bash
xcode-select --install
npm ci
npm run vendor
python3 scripts/build-macos.py --arch universal
python3 scripts/test-macos-app.py build/macos/DengShell.app
```

输出位于 `build/macos/`，包括 `.app`、DMG、ZIP、校验清单和构建信息。已有应用不会被覆盖，可通过 `--output 新目录` 保存下一次构建。

脚本使用系统 SDK、Cocoa/WebKit、CGO 和 `lipo` 合并双架构程序，不需要额外安装 Wails CLI。Ubuntu 能测试共享后端和前端，但不能在缺少 Apple SDK 的情况下构建这个原生桌面应用。

GitHub Actions 的 `.github/workflows/macos-build.yml` 在 macOS 环境中构建通用包，并在 Apple 芯片和 Intel 上分别执行原生启动验证。产物保留为 Actions 构建附件。

## 正式分发签名

有 Apple Developer ID Application 证书时，使用 `--sign-identity 'Developer ID Application: …'` 构建。该选项签名应用并开启 hardened runtime；仍须使用自己的 Apple 开发者账户通过 `xcrun notarytool` 公证，再使用 `xcrun stapler` 装订票据并重新打包 ZIP/DMG、重新计算校验值。

签名证书和 Apple 账户凭据不应放入源码或分发包。没有公证记录的包不标记为“已公证”。

## 技术依据

- [Wails 平台依赖](https://wails.io/docs/gettingstarted/installation/)
- [Go 最低系统要求](https://go.dev/wiki/MinimumRequirements)
- [GitHub macOS 构建环境](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
