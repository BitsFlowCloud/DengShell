# DengShell v0.03 功能审查与发布验证

v0.03 包含 2026-10-03 功能审查确认的九项修复，共用后端与界面同步至 Android。问题触发条件、修改与针对性回归见 [详细审查记录](FUNCTIONAL-AUDIT-2026-10-03.md)。

## 功能回归

发布前审查的 Go 全套测试有 760 项通过、49 项条件跳过；完整 race 检查和 go vet 通过。另行启用真实 SSH/PTTY 条件测试，23 个顶层测试、59 项结果全部通过；Bash/Zsh/Fish SSH/SFTP 矩阵通过。前端语法与 26 份脚本、25 套浏览器回归通过，涵盖真实 SFTP 操作和双实例加密同步。Android 共用后端与 Linux 启动脚本通过。

这些结果属于本次修复的功能审查；平台安装包还需要各自的原生发布验证。发布结果与验证范围以对应 Release 说明和 [Windows](https://github.com/BitsFlowCloud/DengShell/actions/workflows/windows-release-validation.yml)、[macOS](https://github.com/BitsFlowCloud/DengShell/actions/workflows/macos-build.yml) 工作流为准。源码中的历史审查记录保留当时的版本和测试边界。

## 平台与兼容边界

- Windows 使用 UPX 压缩，并在生成哈希及更新签名前修复、验证 PE 头；原生验证应使用最终发布的安装器与绿色包，覆盖启动、安装、卸载和旧版本升级。
- Linux x64 使用 Ubuntu 22.04 / glibc 2.35 构建基线，依赖 GTK3 / WebKitGTK 4.1；不提供 Linux ARM 或 musl 包。
- macOS 13+ 通用包包含 Apple 芯片和 Intel，仍为临时签名、未公证预览版。
- Android 保持原调试签名，版本 0.3.0，覆盖安装保留数据；分架构构建和签名核验不等同于每种实体设备验证。
- RDP 仍为 Windows x64 测试功能；没有声称覆盖全部服务器、输入法、显卡和网络环境。
- 安全锁拒绝旧实例覆盖新的磁盘状态；另一实例变更安全设置后，旧实例需要重启。本次未实现多实例实时同步锁定策略。
