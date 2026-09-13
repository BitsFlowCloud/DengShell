# 常用应用

入口位于文件区的“常用应用”，在“传输任务”左侧。六个按钮分别为 BBR优化、清理垃圾、NQ测试脚本、YABS测试脚本、国际测速、开启/关闭SWAP。

所有执行命令都交给用户当前选定的 SSH 终端；权限提示、进度和结果直接在终端显示。弹窗打开后切换会话、连接断开、关闭弹窗时，不会把待确认命令发到另一会话。对于已就绪的 Bash/Zsh 提示符集成，检测到忙碌或未提交输入时会阻止执行；不支持集成的 Shell 会明确提示先确认终端空闲，保留用户确认后直接发送的兼容路径，不能自动保证其前台程序状态。系统工具的方案接口只读取检查信息，不在后台执行优化、清理或交换区修改。

## BBR 三档

三个档位使用服务器现有内核的 BBR，不安装新内核，不升级 BBR 版本，也不自动重启。选择方案即开始只读检查，通过后发送到同一 SSH 终端。需要 root 或 sudo；sudo 的验证在终端中进行。

| 方案 | TCP 自动缓冲目标上限 | TCP 本地发送排队上限 | 设计取向 |
| --- | ---: | ---: | --- |
| 保守 | 4 MiB | 256 KiB | 减少本机发送积压，偏向排队延迟 |
| 平衡 | 8 MiB | 1 MiB | 兼顾吞吐量和发送排队 |
| 激进 | 32 MiB | 4 MiB | 允许更大缓冲，偏向高带宽/长距离吞吐 |

TCP 缓冲按真实总内存的 1/16 调整预算，最低预算 1 MiB，并保留内核原有最低值和默认值。上限不是立即分配的内存量。全部方案保留内核的重传恢复机制；不通过减少重试次数等方式伪装低重传。实际重传、延迟和吞吐受链路、拥塞、内核版本和对端影响，不能保证某一组参数在所有服务器上最优。

设置 `tcp_congestion_control=bbr`、`tcp_moderate_rcvbuf=1`、`default_qdisc=fq` 和上述缓冲/发送队列预算；现有网卡的运行队列不会强行替换。新 TCP 连接使用新的拥塞控制设置，现有 SSH 连接继续使用原状态。

运行参数和原有 DengShell 配置在 `/var/lib/dengshell/backup-*` 备份；持久配置写入 `/etc/sysctl.d/99-dengshell.conf`。应用失败尝试恢复运行参数，终端显示备份位置和恢复命令。其他系统配置仍可能在启动时覆盖本工具配置；切换档位同样先备份。

## 清理垃圾

先展示将清理的内容，经用户确认才执行：

- APT/DNF/YUM/Zypper/Apk 的包缓存，使用对应包管理器命令。
- Arch 若存在 `paccache`，保留最近两个缓存版本。
- 若使用 systemd journal，轮转后清理早于 14 天的归档日志。

不卸载软件，不删除用户文件、网站、数据库、Docker 数据或整个 `/tmp`。没有相应工具则跳过，部分清理失败会在终端明确报告。

## 第三方测试

只保留调用入口，软件不内置或改写第三方脚本。确认前显示以下原始命令：

```sh
bash <(curl -sL https://run.NodeQuality.com)
curl -sL https://yabs.sh | bash
curl -sL nws.sh | bash
```

NQ 和 YABS 各确认一次；国际测速先确认脚本，再确认大流量提示。界面提示大约 20～50 GB，但此数值不是上限，实际用量依带宽和测试节点变化，可能明显更高。命令使用脚本当时在线提供的版本，具体测试和交互由第三方维护。

## SWAP

检查 `/proc/swaps` 与 `/etc/fstab` 的交换项，解析文件路径、设备路径、符号链接及 `UUID=`、`LABEL=`、`PARTUUID=`、`PARTLABEL=`。同一设备的不同标识合并展示，保留开机配置来源；重复标识对应多个设备时不猜测目标。单次检查最多 128 项，超出会提示使用系统工具。

- 已有活动 SWAP 或开机交换配置时可选“关闭/删除SWAP”或“设置SWAP”；选择设置会提示先删除并重启。
- 没有 SWAP 时可选 512 MB、1 GB、2 GB、4 GB，或自定义大小；输入和单位位于同一行。按 1 GB = 1024 MB 换算，支持 64 MB～1024 GB 之间的整数 MB。
- 新建目标是 `/swapfile.dengshell`，不覆盖已有文件。执行时重新检查活动交换区、开机配置和可用空间，创建后至少保留 256 MiB。核对文件身份、大小与已分配块；只有内核 `swapon` 成功才写入 fstab。

| 类型 | 识别、关闭及配置 | 自动创建 |
| --- | --- | --- |
| ext2/ext3/ext4、XFS 的交换文件 | 支持，关闭后核对文件身份再删除 | 通过实际写零分配无空洞文件，再初始化并启用 |
| Btrfs 的交换文件 | 支持；不会修改其他子卷、数据或快照 | 优先使用 `btrfs filesystem mkswapfile`；旧工具在空文件上设置 NOCOW 后预分配。工具可用时必须通过 `map-swapfile` 区段验证；旧工具无法验证区段时仅接受单设备且 data 为 single 的文件系统 |
| F2FS 的交换文件 | 支持 | 先关闭并核对压缩属性，再实际分配；仍需内核、挂载模式与区段布局支持，内核拒绝即停止并清理本次文件 |
| 块设备、交换分区、LVM/dm-crypt 映射 | 识别活动项及可解析的开机标识；仅 `swapoff` 和移除 fstab 交换项 | 不分区、不格式化设备，也不创建或销毁加密映射 |
| ZRAM | 识别并关闭当前交换用途；移除其 fstab 交换项 | 不重设设备容量、不卸载模块、不自动更改发行版 zram 服务 |
| 其他文件系统上的已有交换区 | 以内核实际活动状态识别；活动文件可关闭并核对后删除 | 网络文件系统、tmpfs、Overlay/FUSE 及未验证类型不自动创建，不承诺所有文件系统都可使用 `swapon` |

Btrfs 交换文件需支持该功能的内核（上游从 Linux 5.0 开始），数据区段位于一个设备上、NOCOW、无压缩及无空洞。多设备 Btrfs 只有新工具能够验证该文件的实际区段符合条件时才继续；最终仍以内核启用结果为准。**活动交换文件所在的 Btrfs 子卷不能创建快照**，工具不会自行改变已有快照方案。现代专用工具输出仍会检查签名、文件大小、已分配块和属性，避免只相信工具退出码。

关闭时先核对活动列表与检查时一致，再逐个执行 `swapoff`。内存不足等失败会停止，不删除任何交换文件或改写 fstab；已经关闭的前几项可由用户按需恢复。分区、块设备及 ZRAM 永不删除。停用但仍有配置的普通文件需 `blkid` 确認交换签名、无符号链接并核对文件身份才删除；未解析标识或未能确认的文件只移除交换配置，并提示保留文件供人工核实。

fstab 会先备份，仅移除交换项，其他挂载配置保留；写入前发现并发修改会停止。关闭后记录本次开机标识，重新设置前要求重启；程序不会自动重启。ZRAM、自定义 systemd `.swap` 单元、crypttab 或其他交换管理服务可能再次创建或启用交换区，需通过发行版对应服务管理方式调整；本工具不会擅自禁用它们。

## 参考资料

参数含义以 [Linux 内核 IP sysctl 文档](https://docs.kernel.org/networking/ip-sysctl.html) 为准。[util-linux swapon](https://man7.org/linux/man-pages/man8/swapon.8.html) 说明交换文件的文件系统限制；[systemd journalctl](https://www.freedesktop.org/software/systemd/man/latest/journalctl.html) 说明日志轮转与期限清理。

第三方入口：[NodeQuality](https://github.com/LloydAsp/NodeQuality)、[YABS](https://github.com/masonr/yet-another-bench-script)、[nws.sh](https://nws.sh/)。nws.sh 官方页面给出不同带宽下的流量样本，20～50 GB 不能视作保证值。

交换区兼容参考：[Btrfs Swapfile](https://btrfs.readthedocs.io/en/latest/Swapfile.html)、[Btrfs mkswapfile](https://btrfs.readthedocs.io/en/latest/btrfs-filesystem.html#mkswapfile)、[Btrfs map-swapfile](https://btrfs.readthedocs.io/en/latest/btrfs-inspect-internal.html)、[Linux F2FS 交换区实现](https://github.com/torvalds/linux/blob/master/fs/f2fs/data.c)、[util-linux findfs](https://man7.org/linux/man-pages/man8/findfs.8.html)。
