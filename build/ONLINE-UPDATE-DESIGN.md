# DengShell 在线更新协议（R40）

新客户端启动检查固定来源 `https://dengshell.com`，用户确认后下载、校验、安装并重启。已发布的旧客户端仍访问 `https://ds.free-vps.org`；两个域名应映射同一网站目录，且旧域名的更新路径不能跨域跳转。更新会关闭 SSH 会话，安装前应保存编辑内容。配置、密钥、自定义字体和背景沿用原数据目录。

## 发布文件

| 平台 | 签名描述 | 安装文件 | 签名中的 platform |
| --- | --- | --- | --- |
| Windows x64 | `/up.exe.json` | `/up.exe` | `windows-amd64` |
| Debian / Ubuntu AMD64 | `/up.deb.json` | `/up.deb` | `linux-amd64` |
| Arch / ID_LIKE=arch x86-64 | `/up.pkg.tar.zst.json` | `/up.pkg.tar.zst` | `linux-amd64-pacman` |

Arch 从 R28 开始支持；旧版须先手动安装一次 R28 或更新版。不添加 pacman 仓库，系统的 `pacman -Syu` 不会从官网获取此软件。RPM 和通用 Linux 包继续手动升级。

JSON 使用 schema 2，最大 64 KiB。身份包含 product、platform、version 和递增的 build；还包含安装包 size、sha256、包内程序 executableSHA256、notes 以及 signingKeyID、issuedAt、expiresAt、signature。由 `scripts/package-release.py` 生成真实文件大小与哈希，再调用发行签名工具。完整签名协议和密钥管理见 [SIGNED-UPDATES.md](SIGNED-UPDATES.md)。Arch 通过已有 platform 字段区分包类型，未改变旧 Windows/DEB 的签名字节协议。

每次发布都递增 `internal/app/updates.go` 中的 ApplicationBuild。描述必须与程序构建号一致；已运行版本和安装收据记录的构建高水位均禁止回退。每个包最大 1 GiB。

## 下载与信任

每次启动检查描述，整体检查期限为 3 秒；超时、缺失、签名不可信或描述过期均跳过，不阻止主界面。下载仅在确认后开始，支持进度与取消，最长 15 分钟。HTTPS 跳转只能留在固定域名。描述不能提供任意下载地址或安装命令。

R21 起强制验证内置公钥的 Ed25519 签名、有效期、文件大小、SHA-256 和更高版本构建。R20 会忽略新增签名字段，首次迁移仍须信任旧渠道。暂存文件位于原配置目录的私有 `.update-*` 子目录；安装助手启动前及父进程退出后再次核对文件和收据。

## 安装与重启

Windows 验证 AMD64 PE，通过独立助手等待旧进程退出，在原程序目录原子替换 EXE，并保留已验证的备份。失败时尝试恢复并启动旧程序。原程序目录必须可写；这不等于 Authenticode 签名或绕过 SmartScreen。

Debian/Ubuntu 验证包名 `dengshell`、架构 `amd64`，固定执行 `pkexec <dpkg路径> --install <已校验文件>`。Arch 只将 `.PKGINFO` 读到有大小和时间限制的缓冲区，校验包名 `dengshell`、架构 `x86_64` 和与构建号一致的 pkgrel，然后固定执行 `pkexec <pacman路径> --upgrade --noconfirm --needed <已校验文件>`。参数通过独立 argv 传递，不经 Shell。root 直接调用包管理器。Arch 包声明 pacman、libarchive 和 polkit 依赖；普通桌面需有正常工作的系统授权代理。

pacman 保留依赖、数据库锁和本机签名策略检查，不执行系统整体升级，不删除锁，也不关闭验证。`--noconfirm` 用于用户已在软件内确认后的无终端安装。授权取消或包管理器忙时记录失败并尝试重开旧程序，不清除用户数据。已授权的包管理事务不会被下载计时器强制终止。完整 Linux 包事务的回退需要旧安装包，不能仅复制 ELF 冒充回退。

Linux 安装目标为 `/opt/dengshell/dengshell`。安装后验证目标程序哈希，并传入原绝对 `--config` 路径启动。数据目录内的 update-receipt.json 保存构建高水位，update-result.json 与私有更新目录内 installer.log 记录结果；收据通过私有临时文件、fsync 和原子重命名保存。

## 网站部署与验证边界

先上传安装文件，最后上传对应 JSON，推荐原子切换整个目录；不要把不匹配的新旧文件混合。JSON 应禁止长时间缓存；CDN 对六个更新文件使用缓存绕过规则，覆盖后清除旧缓存。公开下载位于 downloads/，更新描述中的地址固定在站点根目录。GitHub Release 上传不会部署官网。

签名描述默认 90 天有效，最长 180 天；到期前需重新签发，即使程序版本不变。过期只停止提供更新。

本轮实际验证结果与边界见 [FUNCTIONAL-AUDIT-r40.md](FUNCTIONAL-AUDIT-r40.md)。容器中以 root 完成包安装不能代替真实桌面 polkit 授权窗口或 Windows 实机更新验证。
