DengShell v0.01 · 原生 Linux x64 发行说明

本发行包使用 Ubuntu 22.04 容器构建（glibc 2.35 基线），原生 GTK3 + WebKitGTK 4.1 桌面窗口，不需要 Go、Node、Chrome 或浏览器服务。

支持范围
• Ubuntu 22.04 / 24.04 / 26.04、Debian 12 / 13，以及满足相同依赖的衍生版：DEB。
• Fedora、openSUSE 等提供 glibc >= 2.35、GTK3 和 WebKitGTK 4.1 的 x86-64 桌面：RPM。
• Arch / Manjaro 等 glibc x86-64 桌面：通用 tar.gz，安装依赖后运行或安装。
• 不包含 ARM、32位、Alpine/musl、Ubuntu20.04、Debian11、默认仓库的 RHEL/Rocky/AlmaLinux8/9 支持。WebKitGTK4.0或6.0不能替代4.1。
本轮已实际通过Ubuntu22.04、24.04、Debian12、Fedora43、openSUSE Tumbleweed的原生前端启动及对应包安装，证据见Linux-兼容验证.json。
容器验证只覆盖运行库和原生前端启动，无法替代每种显卡、Wayland/X11、桌面托盘和缩放组合的实体桌面测试。

安装
Debian / Ubuntu：
  sudo apt install ./up.deb
Fedora：
  sudo dnf install ./DengShell-linux-x64.rpm
openSUSE：
  sudo zypper install ./DengShell-linux-x64.rpm
RPM 尚未签名；安装器会显示本地包签名提示。

通用包：
  tar -xzf DengShell-linux-x64.tar.gz
  cd DengShell-linux-x64
  bash data/support/start.sh
无需管理员权限安装到当前用户的应用菜单：
  bash data/support/install.sh
可选系统安装：
  sudo bash data/support/install.sh --system
默认用户安装位置：${XDG_DATA_HOME:-$HOME/.local/share}/dengshell/application。
系统安装位置：/opt/dengshell。安装器只更新程序、说明和许可证，不复制便携配置或密钥。

首次启动会检查共享库和 ping，并按发行版提示缺失包；原生前端成功运行后在配置目录记录检查成功，之后直接启动。MTR 在使用网络诊断时再检测和引导。
Arch / Manjaro 安装依赖：
  sudo pacman -S --needed gtk3 webkit2gtk-4.1 iputils
Fedora：sudo dnf install gtk3 webkit2gtk4.1 iputils
openSUSE：sudo zypper install libgtk-3-0 libwebkit2gtk-4_1-0 iputils
Ubuntu22.04 / Debian12：sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0 iputils-ping
Ubuntu24.04/26.04 / Debian13：sudo apt install libgtk-3-0t64 libwebkit2gtk-4.1-0 iputils-ping
有图形提示工具（zenity/kdialog/xmessage）时显示对话框，否则提示写入启动终端。缺少桌面会话时无法显示原生窗口。不要手工替换系统 libc。
Linux 托盘依赖 AppIndicator 与桌面托盘扩展；不可用时使用正常最小化，不隐藏到无法恢复的托盘。

配置和升级
便携包解压后，顶层仅有 dengshell 与 data/；便携配置也存入 data/。
DEB、RPM和通用安装版的数据位于 ${XDG_CONFIG_HOME:-$HOME/.config}/dengshell；程序目录仅保留发行资源。
显式 --config 路径始终优先。应用内“设置 → 连接配置文档”显示实际路径。
备份整个实际配置目录（含 .enc、.key、assets、keys）；只更新程序不会丢失 SSH、导入字体或背景。加密不能阻止同权限程序读取自动解锁密钥或删除文件，请保留备份。
从便携版转为安装版时，安装器不自动迁移私人配置；可在退出程序后将原 data/ 中的配置内容备份并复制至实际用户配置目录。
DEB 在线升级沿用 up.deb / up.deb.json；RPM 和通用包按照应用提示下载相应安装包更新。

主要功能
分组和多级服务器管理、SSH密钥/代理、20款中英文字体与独立配色/粗体、终端背景、快捷命令和常用应用、真实SFTP文件管理与文本编辑、实时终端与低开销按需监控、ICMP一分钟丢包率和MTR诊断。
上传最多4个文件同时传输，多出的排队；每个文件最多32个SFTP分块并行。进度按服务器已确认写入统计，重试从头开始，暂不支持断点续传。
终端复制 Ctrl+Shift+C，粘贴 Ctrl+Shift+V；Ctrl+C 保留中断。文本编辑 Ctrl+S 保存。系统关联打开的是本地副本，外部修改需要手动上传。
ICMP经过系统路由/TUN，不是SSH按键计时。进程仅在展开时采集，静态信息约30秒刷新。

可复现构建
先安装 Docker，在源码根目录执行：
  bash scripts/build-linux-release.sh
原生程序和构建证据：build/linux/native/dengshell、build-info.json。
镜像基础摘要和 Go1.27.1 的 SHA256 固定在 build/linux/Dockerfile；完整构建依赖版本和实际镜像ID记录在 build-info.json。apt安全更新会改变新建镜像的依赖版本；需要逐字节复现时应保存并复用记录的完整构建镜像，设置 DENGSHELL_LINUX_BUILDER 为其镜像ID。
脚本强制 GOAMD64=v1、CGO原生编译并拒绝 GLIBC符号需求超过2.35、错误架构、缺少GTK/WebKit链接或含构建机RPATH的程序。
构建后由 scripts/package-linux.py --stage <仅包含dengshell和data的发行资源目录> --release 26 生成 DEB、RPM、pacman .pkg.tar.zst、tar.gz 与 linux-packages.json。整体发布使用 scripts/package-release.py。

官方依赖参考：
https://v2.wails.io/docs/gettingstarted/installation/
https://packages.ubuntu.com/jammy-updates/libwebkit2gtk-4.1-dev
https://packages.debian.org/bookworm/libwebkit2gtk-4.1-0
https://packages.fedoraproject.org/pkgs/webkitgtk/webkit2gtk4.1/
https://archlinux.org/packages/extra/x86_64/webkit2gtk-4.1/

官网 https://ds.free-vps.org · 应用代码MIT开源，第三方许可证见 data/licenses（本文件位于data/docs时为../licenses）。详细功能见程序顶部说明书按钮。

Arch Linux / 兼容 pacman 的 x86-64 桌面：
  sudo pacman -U ./DengShell-linux-x64.pkg.tar.zst
卸载：sudo pacman -R dengshell（用户配置目录保留）。
Arch 依赖 gtk3、webkit2gtk-4.1、bash、iputils、glibc；托盘可选 libayatana-appindicator。
Windows/Debian 的内置更新会退出并重启；Arch 请用新版 pacman 包升级。
R20 至 R26 全部变化见 CHANGELOG.md；本轮验证边界见 FUNCTIONAL-AUDIT-r26.md。
