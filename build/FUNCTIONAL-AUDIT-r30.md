# DengShell R30 功能验证范围

本次使用独立配置、虚构连接和隔离容器进行验证，不修改真实用户配置、SSH 密钥、字体或背景。

## 分栏服务器管理

- 左侧多级目录、右侧紧凑连接列表；单击选择、双击连接、Ctrl / ⌘ 多选。
- 全局搜索、包含子分组、路径导航、折叠、右键编辑／删除／恢复。
- 栏宽、目录和子分组选项保存；浅色／深色及小窗口布局。
- 数据层覆盖 1,200 级目录，浏览器覆盖五级目录和 50 条连接。
- 浏览器中的连接请求使用模拟响应，避免访问样例地址；SSH/SFTP 后端由独立协议与文件操作测试覆盖。

## 发布验证

发布前执行 Go 完整测试、并发检测、前端回归与终端实际渲染检查。Linux 使用 Ubuntu 22.04、Ubuntu 24.04、Debian 12、Fedora 43、openSUSE Tumbleweed 和 Arch Linux 容器，检查依赖、原生 GTK/WebKitGTK 窗口就绪、安装、卸载保留配置，以及 DEB/pacman 的升级流程。原生矩阵与程序哈希见 [COMPATIBILITY.json](linux/COMPATIBILITY.json)。

Windows 验证脚本为 `scripts/test-windows-release.ps1`，在 GitHub Windows 运行器中检查实际安装包及 ZIP、WebView2 原生就绪、控制面板卸载登记、卸载保留配置和更新后主窗口可见。对应运行结果和精确文件哈希保存在 [Windows release validation](https://github.com/BitsFlowCloud/DengShell/actions/workflows/windows-release-validation.yml) 的验证记录中；验证通过后才发布。

自动更新检查包括完整文件下载、发行签名、大小、SHA-256、平台和构建号。旧 Windows 更新助手须接受本次压缩 EXE；Linux 实际安装助手还验证更新后重启和失败时保留原配置。

## 边界

- 当前交付 Windows x64 与 Linux x86-64；不交付 macOS、ARM、32 位或 musl 包。
- Linux 原生测试使用容器、Xvfb 和软件渲染；不能覆盖全部实体显卡、Wayland、托盘及 polkit 授权代理组合。
- Windows 自动运行器不代表全部 Windows 10／11 实体桌面配置；终端多窗口拖动和物理多屏仍需按实际环境确认。
- 下一版本升级使用隔离的测试构建／计划，仅用于验证，不作为发行文件或签名更新发布。
- 不保证所有第三方测速脚本、远端定制 Shell 配置或网络环境均可用；生产数据未参与测试。
