# DengShell 更新器约定

当前协议统一维护于 [在线更新协议](ONLINE-UPDATE-DESIGN.md)，签名细节见 [发行签名](SIGNED-UPDATES.md)，避免两份协议出现不一致。

- Windows：固定 `/up.exe.json` 与 `/up.exe`。
- Debian/Ubuntu：固定 `/up.deb.json` 与 `/up.deb`。
- Arch pacman（R28 起）：固定 `/up.pkg.tar.zst.json` 与 `/up.pkg.tar.zst`，签名中的平台为 `linux-amd64-pacman`。
- 来源固定为 `https://dengshell.com`，不接受跨域更新下载或描述文件指定的命令。
- 已发布的旧客户端仍固定访问 `https://ds.free-vps.org`。两个域名必须在相同路径提供同一套已签名的 `up.*` 文件；旧域名的更新请求不能跳转到新域名，否则旧客户端会拒绝。
- schema 2；新版强制验证 Ed25519 发行签名、有效期、SHA-256、大小与构建高水位；Arch 独立验证包格式、名称、架构和 pkgrel。
- 启动检查，用户确认后下载和安装；Linux 使用系统授权和对应包管理器。安装后继续使用原配置目录，重启程序，SSH 会话断开。
- 旧 Arch 版须先手动安装 R28 或更新版；不添加 pacman 软件仓库。GitHub 发布不会自动部署官网。
