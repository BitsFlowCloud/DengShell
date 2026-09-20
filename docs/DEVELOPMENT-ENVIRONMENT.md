# DengShell 开发环境与源码结构

核查日期：2026-09-15。项目版本：v0.01 R40，内部构建号 `20260915042`。

## 源码范围

本次对项目自有源码、测试、脚本和相关构建文档做了全量文本扫描、依赖提取及平台调用检查。修改前清单包含 464 个文本文件、57,317 行；其中核心源码、配置和脚本为 311 个文件、46,489 行。第三方依赖、字体、图片、视频和已生成程序按依赖及资源核查，不作为自有代码逐行评审。

| 部分 | 职责 |
| --- | --- |
| `main.go`、`desktop*.go`、`theme*.go`、`window*.go` | 启动、Wails 桌面窗口、前后端桥接、主题、托盘、独立窗口 |
| `internal/app/app.go`、`store.go`、`persistence*.go` | 本机回环 HTTP API、加密配置、原子保存与数据迁移 |
| `internal/app/ssh*.go`、`terminal*.go`、`sftp*.go` | SSH 认证、主机指纹、会话、终端数据转发、SFTP 文件通道 |
| `internal/app/files.go`、`file_tools.go`、`upload_pipeline.go` | 文件浏览、编辑、上传下载队列 |
| `internal/app/monitor*.go`、`network*.go`、`icmp*.go` | 通过 SSH 监控远端 Linux，以及本机网络检测 |
| `internal/app/preferences*.go`、`font_library*.go`、`media.go` | 个性化、在线字体、导入背景 |
| `internal/app/updates*.go`、`internal/updatetrust/`、`update_helper*.go` | 签名更新描述、包验证和各平台更新助手 |
| `web/` | 原生 HTML/CSS/JavaScript，xterm.js 终端；资源通过 Go embed 内置 |
| `scripts/`、`cmd/`、`.github/workflows/` | 测试、依赖资源复制、平台打包、发行签名工具与 CI |

## 开发依赖

| 依赖 | 项目要求或用途 | 当前 Ubuntu 核查 |
| --- | --- | --- |
| Go | `go.mod` 要求 1.26.0+；本次采用 1.27.1 | 1.27.1，模块校验通过 |
| Node.js / npm | xterm 依赖复制、JavaScript 检查和测试；完整测试使用 Node 22+ | Node 22.23.2 / npm 10.9.8 |
| Wails | Go 模块锁定 v2.15.0；直接 `go build`，不依赖 Wails CLI | 已有模块 |
| xterm.js | 6.0.0；addon-fit 0.11.0；addon-serialize 0.14.0 | 安装及内置资源齐全 |
| C/C++、pkg-config | Linux CGO 原生桌面编译 | GCC 15.2.0，可用 |
| GTK3 / WebKitGTK 4.1 | Linux 原生窗口及 WebView | 3.24.52 / 2.52.6 |
| Python 3 | 打包、资源处理、测试辅助 | 3.14.4 |
| ping / mtr / OpenSSH | 本机延迟/MTR、隔离 SSH 测试 | 已安装 |
| Chrome、Bash、Zsh | 终端快照与提示符测试 | 本次测试通过 |
| Xvfb / xauth / xdotool / wmctrl | 无显示器的原生窗口测试 | 补装 Xvfb、xdotool、wmctrl；xauth 已有 |
| Python FontTools | 字体生成/处理脚本 | 已补装，含所需辅助库 |
| Docker、NSIS | 原有 Linux 兼容性构建、Windows 安装包 | 已有；普通开发不要求重新打包全部平台 |

应用没有数据库、Redis、独立 Node 后端或额外 Web 服务的运行依赖。SSH/SFTP 核心使用 Go 库，不依赖本机 `ssh` 可执行文件；OpenSSH 主要用于测试和辅助操作。MTR 属于按需功能。

当前主机为 Ubuntu 26.04.1 x86_64。直接在这里构建的 Linux 可执行文件依赖本机较新的 glibc；兼容旧发行版时应使用项目原有 `build/linux/Dockerfile` 构建流程。

## 日常检查与构建

```bash
python3 scripts/check-environment.py
go mod verify
npm ci
npm run vendor
npm run check
go test -buildvcs=false -timeout 12m ./...
go test -buildvcs=false -tags desktop,production,webkit2_41 -timeout 5m .
```

Linux 桌面开发构建使用 `bash build.sh`；Windows 交叉编译使用 `bash build.sh --windows`。这两个命令会写入各自的构建输出，执行前应确认当前需要的目标。隔离测试可通过 `go build -o 缓存目录/程序` 和运行参数 `--config 临时目录` 完成。

Windows 开发编译可在当前 Linux 主机完成；原生运行验证需要 Windows + WebView2。macOS 通用应用构建需要 Mac + Xcode Command Line Tools/Apple SDK，详见 [macOS 构建说明](MACOS.md)。现有 Ubuntu 工具链无法代替 Apple SDK。

## 本次验证范围

- Go 模块完整性检查、后端常规测试、Go vet。
- 全部 36 个自有前端 JavaScript 文件语法检查；package.json 中 15 组前端测试通过。
- Linux 原生桌面层测试，以及使用隔离配置和虚拟显示的实际 WebKit 窗口启动。
- Windows 桌面交叉编译成功；本次未在 Windows 真机运行新构建。
- macOS 配置路径识别、BSD ping 命令和退出状态的回归测试。
- Python 脚本与 GitHub Actions YAML 语法检查；Linux 启动器 11 项现有测试。

需要指定真实 SSH 测试环境的集成测试不会访问用户已有服务器；未设置专用夹具时按项目设计跳过。全量扫描与上述检查用于确认结构、依赖和构建能力，不表示所有功能已在每种操作系统上逐项人工验收。
