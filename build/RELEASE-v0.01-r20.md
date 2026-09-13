# DengShell v0.01 r20 完整发行

构建号：20260913020；日期：2026-09-13。

Windows 提供当前用户安装版、绿色 ZIP 和单独 EXE。安装版注册控制面板卸载入口，保留个人 data 配置；Windows 二进制使用 UPX --best --lzma 压缩。安装／卸载仍需真实 Windows 验证。

Linux 提供 x86-64 DEB、RPM、通用 tar.gz，与 Windows 使用相同 r20 源码。原生 GTK3/WebKitGTK 4.1，Ubuntu 22.04 / glibc 2.35 基线。Ubuntu 22.04/24.04、Debian 12、Fedora 43、openSUSE Tumbleweed 的本轮原生容器启动证据见 build/linux/COMPATIBILITY.json；实体桌面、显卡、Wayland、托盘和 DPI 组合未覆盖。

功能沿用 r19：远端用户／用户组名称、大小／修改时间排序，以及密钥口令统一保存、多服务器命令历史、独立曲线样式。国际测速脚本按用户约定保留。

官网改用真实界面操作录制的 15 FPS 动态 WebP，SSH 与 SFTP 来自独立演示环境；下载文件、构建号和 SHA-256 配套更新。线上官网返回 HTTP 403，本次交付可部署的本地静态网站包，线上访问结果仍需部署后验证。

打包使用明确的源码／发行资源允许列表，排除真实用户配置、私钥、测试状态和缓存。程序包只包含主程序、使用说明、支持脚本与许可证。
