# DengShell Android（0.1.0 alpha 4）

这一版把 DengShell 的现有 SSH/SFTP 后端装入 Android 应用，并针对竖屏和横屏提供触控布局。应用在设备本机的 `127.0.0.1` 启动服务，界面通过内置 WebView 访问；服务器配置保存在应用私有目录中。

安装包已放在 [DengShell 官网](https://dengshell.com/#downloads) 和 [GitHub Android 预发布页](https://github.com/BitsFlowCloud/DengShell/releases/tag/android-v0.1.0-alpha5)。

## 选择安装包

| 安装包后缀 | 适用 CPU |
| --- | --- |
| `arm64-v8a` | 现代 64 位 ARM 手机和平板，优先选择 |
| `armeabi-v7a` | 旧款 32 位 ARM 设备 |
| `x86_64` | 64 位 Intel/AMD Android 设备及模拟器 |
| `x86` | 32 位 Intel Android 设备及模拟器 |
| `universal` | 不清楚 CPU 类型时使用，包含以上四种架构，因此体积较大 |

最低系统版本：Android 7.0（API 24）。这些是调试签名的 alpha 安装包，用于试用与反馈；正式分发前应使用长期保管的发布签名密钥重新签名。换用不同签名的包时，Android 可能要求先卸载旧包。安装新版本前请备份应用内的重要配置。`release/SHA256SUMS-alpha4` 可用于核对下载文件。

## 当前功能

服务器配置、密码或私钥 SSH 登录、终端、SFTP 文件管理、文件传输、远程资源监控、快捷命令和设置共用现有 DengShell 后端。终端提供 Esc、Tab、Ctrl+C、Ctrl+D 和方向键。竖屏使用底部导航，横屏使用侧边导航和会话列表。上传与下载接入 Android 文件选择器。

Android 没有桌面 SSH Agent 套接字，因此此版不提供 SSH Agent 登录。桌面独立窗口和本机路径选择也不适用于 Android。自动安装更新尚未接入 Android；后续版本需通过新的 APK 安装。

## 构建

需要 Android SDK/NDK、Go、`gomobile` 和 Gradle。当前开发机的路径写在 `build_core.sh` 中；迁移到其他电脑时先修改 SDK、NDK 与 `gomobile` 路径。执行：

```sh
./build_core.sh
GRADLE_USER_HOME="$PWD/.gradle" gradle :app:assembleDebug
```

生成的分架构及通用 APK 位于 `app/build/outputs/apk/debug/`。界面源码在 `mobile/assets/web/`，Android 容器在 `app/src/main/`。

## 验证范围

五种 APK 均已构建。ARM64 版已在 MEIZU 21（Android 16）通过 USB 调试实测：应用启动、SSH 密码与私钥登录、主机指纹确认、终端命令、SFTP 上传/下载/改名/删除、系统文件选择器、监控刷新、竖横屏排版、返回键关闭弹窗、后台恢复及会话关闭。最终安装包下载 `.txt`、未知扩展名和 16 MiB 文件时，均核对了文件名与 SHA-256。浏览器回归覆盖竖横屏导航、服务器抽屉、表单滚动、私钥文件读取、远程文本编辑冲突与同名上传保护；Go 后端测试覆盖配置、SSH/SFTP 和监控 API。详细记录见 [REGRESSION-alpha4.md](REGRESSION-alpha4.md)。

32 位 ARM、x86 和 x86_64 安装包完成构建与签名校验，尚未在相应实体设备上逐一运行。不同服务器、输入法和系统版本仍需继续兼容性测试。横屏使用浮动输入法时，键盘可能遮住一部分应用；可将输入法切换为停靠模式。

## 2026-09-27 · alpha5

- 同步桌面新版的 SSH 优先启动与后台 SFTP 初始化，降低连接后的等待。
- 修复网速采样完成后界面额外滞后一轮的问题，保留远程采集频率。
- 保留 alpha4 的移动界面、文件选择器和下载修复。Android 仍为调试签名测试版，手动安装新版。

构建依赖：Go 1.27.1、JDK 17 或更新版、Gradle 9、Android SDK 36、NDK 28.2.13676358，以及 go.mod 对应版本的 gomobile/gobind。先运行 `go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260908204917-8b95e45f8d3e` 和同版本 `gobind`，再执行上述构建命令。签名密钥不在源码中；自行构建使用自己的签名，不能覆盖官方 APK。

运行时请保持 Android System WebView 为系统支持的最新版；系统长期未更新的旧 WebView 无法解析当前界面脚本。
