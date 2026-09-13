# DengShell 功能异常排查脚本

用于排查：系统信息/CPU/内存/磁盘为空、活跃进程读取失败，以及 BBR/清理垃圾提示“服务器检查结果不完整，请重试”。

**脚本应在出现问题的 Linux VPS 上运行，不是在 Windows 电脑上双击。使用与 DengShell 连接相同的账号，不要为了排查临时改成 sudo/root。**

## 最简单的操作

1. 把 `DengShell-diagnose.sh` 发给朋友，让他用 SFTP 上传到出现问题的 VPS 的 `/tmp/`。
2. 先结束正在运行的 ping、YABS 等前台命令，回到空闲提示符。在该 VPS 终端运行：

```sh
sh /tmp/DengShell-diagnose.sh /tmp
```

3. 等待完成。正常通常几秒；遇到多个卡住的检查，可能需要约 2 分钟。脚本会显示类似：

```text
/tmp/DengShell-diagnose.ABC123/report.txt
/tmp/DengShell-diagnose.ABC123/summary.txt
```

4. 通过 SFTP 下载并发回这两个文件，再补充 DengShell 的 **r 版本**。不要发回整个个人 `data` 目录、密码或私钥。

不知道 r 版本时，可在 Windows 的 EXE 所在目录打开 PowerShell，执行以下命令，把 SHA-256 一并发回，方便核对实际程序版本：

```powershell
Get-FileHash .\DengShell.exe -Algorithm SHA256
```

如果无法上传到 `/tmp`，放在该账号有权限的目录，把命令中的路径替换成实际位置即可。脚本不需要 `chmod +x`，也不会要求输入 sudo 密码。尽量直接传输原文件，避免编辑器改成 Windows CRLF 换行。

## 为什么需要专门检查

截图中的网速由独立的轻量命令采样，只读取 `/proc/uptime` 和 `/proc/net/dev`；完整系统信息还包含其他 `/proc` 文件、awk 函数、Shell 循环、ip、df、getconf 和进程扫描。因此网速正常不代表完整监控命令正常。

“服务器检查结果不完整”来自检查输出里缺少可识别的 `__DS_END__` 标记。它不是“不支持 BBR”的同义词。Shell 不兼容、启动逻辑替换了命令、回车/输出格式变化、输出被截断等都值得排查。截图中的“未检测到 Shell 提示符集成”是另外一条提示，不能仅凭它确定故障原因。

## 脚本检查内容

- Linux 版本、登录 Shell、基础命令是否存在和 `/proc` 可读性。
- 与 r20/r21 源码一致的只读网速、核心监控、静态信息、进程和常用应用检查命令。
- 对比 `sh -c` 与账号登录 Shell 的 `-c` 执行结果，记录退出码、耗时、输出大小和协议标记。
- 识别 CRLF 结束标记及超过常用应用 128 KiB 输出上限的情况；失败时补采原代码隐藏的错误输出。
- 比较完整静态采样与 `df /`，寻找卡住的挂载点等线索。
- 只读检查当前 BBR 状态、清理所需工具；不会实际调优、加载内核模块或清理文件。
- 仅统计启动文件中的可疑命令关键字，不复制文件内容。条件允许时检查 sshd 全局 `MaxSessions`/`ForceCommand` 等，**不代表已经检查该账号所有 Match、authorized_keys 或服务商限制**。

`summary.txt` 提供中文线索，`report.txt` 提供退出码与错误证据。`[SH_OK]` 表示本次 sh 模式的探测通过；`[SHELL]`、`[INCOMPLETE]`、`[FORMAT]`、`[TIMEOUT]`、`[AWK]` 是重点。`[BBR]` 未列出 bbr、`[PRIVILEGE]` 不是 root 等提示是适用条件，不能单独解释结束标记缺失。

## 若脚本通过，界面仍失败：补做 SSH exec 检查

在朋友的 Windows PowerShell 中，使用系统 OpenSSH 执行下面命令，替换端口、账号和 VPS 地址：

```powershell
ssh -T -o ConnectTimeout=8 -p 22 用户名@VPS地址 'echo __DS_EXEC_BEGIN__; uname -s; echo __DS_EXEC_END__'
```

预期包含以下三行：

```text
__DS_EXEC_BEGIN__
Linux
__DS_EXEC_END__
```

正常的 SSH 身份验证照常完成；不要关闭主机指纹校验，也不用导出 DengShell 的配置或密钥。如果只出现菜单/欢迎界面、没有结束标记、报 `administratively prohibited` 等错误，请把该输出发回。超过约 15 秒还不退出，可按 Ctrl+C 并说明卡住。

还可以让独立 SSH exec 直接启动已上传的诊断脚本，对比它与终端内运行是否相同：

```powershell
ssh -T -o ConnectTimeout=8 -p 22 用户名@VPS地址 'sh /tmp/DengShell-diagnose.sh /tmp'
```

该命令通常几秒完成，遇到卡住的探测可能约 2 分钟；诊断报告仍保存在 VPS。默认 Shell 与 sh 的详细采样对比已包含在脚本中。

这个独立 SSH 连接并不与 DengShell 共用同一传输连接，不能单独证明 `MaxSessions` 是否足够；朋友若使用跳板机、代理或密钥认证，也需要按其正常 OpenSSH 用法配置同一路径。只有 DengShell 能登录而系统 OpenSSH 无法认证时，先发回 VPS 脚本报告即可。

## 只读范围和隐私

脚本不会安装软件、修改 sysctl/SSH/启动配置、加载 BBR、开关 SWAP、执行清理、测速或上传报告；没有主动连接外部测试站点。它创建一个权限受限的报告目录，并在结束时删除自己的临时采样文件。

默认 Shell 的 `-c` 启动仍会遵循该 Shell 自身的启动规则；已有启动文件如果自动执行命令，其行为属于正在检查的服务器环境。脚本不主动 source 启动配置。

报告不保存原始采样标准输出，不收集口令、私钥、完整进程命令行、命令历史或完整环境变量。错误摘要做了有限地址脱敏，但可能含系统版本、用户名/路径等；发回前可检查。运行需要常见基础命令及支持 `-k` 的 `timeout`；缺少时会明确停止，不自动安装。

本脚本只能帮助收集证据。终端内运行不能完整模拟 SSH exec 请求、PTY、通道限制或 Windows 客户端状态，不能在朋友尚未运行前断言具体根因。
