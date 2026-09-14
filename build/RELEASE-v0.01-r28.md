# DengShell v0.01 r28

构建号：`20260914028`；发布日期：2026-09-14。

新增 Arch pacman 软件内自动更新：启动检查，确认后验证发行签名、请求系统授权、安装并重启，保留原配置。旧 Arch 版须先手动安装一次 R28，此后使用内置更新；不添加 pacman 软件仓库。

Windows/DEB R20–R27 可直接升级。本版同时包含最小化弹窗修复，全部主要变化见 [R20 → R28 更新日志](../CHANGELOG.md)。Windows 提供安装版和绿色 ZIP；Linux 提供 DEB、RPM、通用 tar.gz 和 Arch pacman 包。

验证范围见 [R28 验证记录](FUNCTIONAL-AUDIT-r28.md) 与发行附件 RELEASE-VALIDATION-r28.json。官网必须同步部署三组签名描述和安装文件，并清除旧缓存；GitHub Release 上传不会自动部署官网。
