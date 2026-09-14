DengShell v0.01 r40 · Windows 10/11 x64

安装版：运行 DengShell-Setup-x64.exe。默认安装到当前用户的 LocalAppData\Programs\DengShell，建立开始菜单／桌面快捷方式，并注册“控制面板 → 程序和功能”与 Windows 设置中的卸载入口。升级前退出程序；卸载仅移除发行文件与快捷方式，保留个人 data 配置、SSH 密钥、字体和背景。

绿色版：解压 DengShell-windows-x64.zip，运行 DengShell.exe。无需安装，数据在旁边 data/ 中。整个目录可搬移；不要只保留程序而丢弃 data/。

需要 Microsoft Edge WebView2，缺少时应用提供安装指引。EXE 采用 UPX 最佳档位压缩；发行验证使用 GitHub Windows 运行器，检查原生窗口、安装／卸载登记与升级后重新显示主窗口；不代表所有实体设备均已验证。

R30～R40 累计更新：IBM Plex Sans SC 界面字体与五款 Shell 字体、20/30 款在线字体库、分组背景色与三次确认删除、编辑器与列表精简、SSH 初始化与提示符修复、默认网卡选择改进。完整更新见 CHANGELOG.md。

保留原有功能：管理器保存密钥口令；所有 VPS 命令收集到本地历史；上行／下行／延迟独立样式；文件用户／组名称及大小／修改时间排序；此前审查修复。国际测速脚本按用户约定保留。

配置使用本地加密文件和解锁密钥，退出后备份整个 data/。应用代码 MIT，第三方许可见 data/licenses/；完整说明见 data/docs/。
官网：https://ds.free-vps.org
