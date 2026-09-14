# DengShell v0.01 R40

全部平台构建号：20260915041。累计更新见 [R30～R40 更新日志](../CHANGELOG.md)。

本次提供 Windows x64 安装版与绿色 ZIP，Linux x64 DEB、RPM、pacman 与通用安装包，以及源码 ZIP。应用内置 IBM Plex Sans SC 和五款 Shell 字体；官网提供 20/30 款在线字体库。

Windows、DEB 与 pacman 自动更新文件由同一组验证后的程序生成，并使用既有发行密钥签名。Linux 继续以 Ubuntu 22.04、glibc 2.35、GTK3/WebKitGTK 4.1 为基线。

原生平台验证、功能复核及发布边界见 [R40 功能复核](FUNCTIONAL-AUDIT-r40.md)。

本次修复进程采集超时误断 SSH、重连丢失终端显示，并隔离文件通道故障；常驻前五进程、60 秒网速图、统一字体设置和在线加粗预览一并更新。Windows、DEB、pacman 的旧 R40 可识别新的内部构建号。
