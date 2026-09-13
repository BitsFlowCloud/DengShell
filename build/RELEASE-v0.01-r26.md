# DengShell v0.01 r26

构建号：`20260914026`。发布日期：2026-09-14。

本次将 R20 以来的改动合并为一次完整发行，详情见 [累计更新日志](../CHANGELOG.md)；无需逐版升级。

- Windows：`DengShell-windows-x64.zip`、`DengShell-Setup-x64.exe`。
- Linux x86-64：`DengShell-linux-x64.deb`、`.rpm`、`.tar.gz`、`.pkg.tar.zst`。
- 源码：`DengShell-source.zip`。校验：`SHA256SUMS.txt`。

包含 FinalShell 导入、命令参数与可选回车、命令／分组拖动、跨服务器多标签文本编辑、五套界面字体与文字颜色、Bash / Zsh / Fish 兼容及 Ed25519 更新签名。

功能审查和发行验证见 [FUNCTIONAL-AUDIT-r26.md](FUNCTIONAL-AUDIT-r26.md)。Windows 本次进行了交叉编译、压缩完整性与安装包静态检查，尚未进行 Windows 实机安装／卸载回归。

升级前退出旧程序，保留完整用户数据目录。配置、密钥、字体和背景不会作为发行资源上传或打包。应用内在线更新依赖官网部署匹配的更新文件；上传 GitHub 不等于更新官网服务。
