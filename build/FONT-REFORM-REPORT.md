# DengShell 字体改革与功能复核

> 本文保留字体改革首轮记录。官网字体库现已部署，后续修复、当前增量包信息与复测结论见 [官网部署与功能复核](FUNCTIONAL-RECHECK-20260914.md)。下文“尚未上传”等表述仅对应首轮结束时的状态。

日期：2026-09-14。范围为当前 R34 源码的字体改革与官网字体库增量文件，未增加发行版本号。

## 已实现

| 范围 | 结果 |
| --- | --- |
| 内置界面字体 | 仅 IBM Plex Sans SC，保留上游原生 WOFF2 |
| 内置 Shell 字体 | JetBrains Mono、Fira Code、Source Code Pro、IBM Plex Mono、Maple Mono CN |
| 中文回退 | Shell 复用 Maple Mono CN；界面复用 IBM Plex Sans SC，无额外第六套 Shell 字体 |
| 设置 | “界面字体与文字颜色”与“Shell 字体”两个入口，面板可互相切换，各自保存选择 |
| 官网在线库 | 20 款界面字体、30 款 Shell 字体，数量包含对应的内置款式 |
| 按需获取 | 实际字体渲染为小型 PNG 样张；确认后才下载完整字库，下载完成保存到本机并使用 |
| 手动导入 | 保留 TTF/OTF/WOFF/WOFF2 导入；界面字体仍需完整重启，Shell 字体可直接切换 |
| 旧设置迁移 | 已移除的内置 UI 字体回退到 IBM，已移除的 Shell 字体回退到 JetBrains；用户导入的字体及其他设置保留 |

在线字体下载支持进度、取消、失败重试、已下载副本复用和删除。“检查或重新下载”会验证本地副本，缺失或损坏时下载新副本。损坏的旧副本保留供用户自行删除，不擅自删除自定义资源。删除当前字体后回退到默认款式。

字体目录与许可随客户端内嵌，读取目录和启动时不会访问字体库。只有浏览样张、主动下载时访问官网。应用固定 HTTPS 来源、允许路径、文件大小及 SHA-256，并校验字体或 PNG 格式；网站不能用动态目录替换客户端信任的字库。在线库中新增或更换字库需要同步构建并复核客户端目录。

下载完成后会检查当前窗口及共享配置中的较新选择，避免较早的下载覆盖用户后选的字体。在线 UI 字体经验证后注册到当前进程，支持即时切换；手动导入的重启边界保持不变。

## 资源与许可

- 改革前 `web/assets/fonts/` 与 `web/assets/ui-fonts/` 共 59,198,132 字节，约 56.5 MiB。
- 改革后共 9,493,009 字节，约 9.1 MiB，减少约 84%。其中字体二进制 9,452,864 字节，剩余为目录和许可。
- 内置共 6 个字体家族、7 个字体文件：IBM Plex Mono 的独立粗体仍属于同一家族；Maple 的中英文主字体与中文回退共用一个物理文件。
- 官网 50 个字体选项对应 49 份完整字体文件，UI/Shell 的 Maple 复用相同文件。网站不额外存储重复的字体 ZIP：浏览器在确认后校验字体并生成包含字体、OFL 和说明的 ZIP。
- 50 款均保留 SIL Open Font License 1.1 的版权及许可；上游版本、下载归档、输入文件和许可哈希见 `font-licenses/library-sources.json`。转换格式的字体使用独立内部家族名，原生 WOFF2 保持原文件。没有把字体的许可改成软件的 MIT 许可。
- 部分字体以繁体或展示风格为主，目录明确说明缺字回退；朱雀仿宋标注上游技术预览版本。不把每款字体都描述为独立覆盖所有简繁汉字。

以上是资源体积，**不是重新生成后的 EXE 体积**。本次没有打包，因此不以此推算最终安装包大小。

## 复核与修复

| 验证 | 结果与边界 |
| --- | --- |
| 完整 Go 测试 | `go test -buildvcs=false ./...` 通过 |
| 并发检查 | 字体下载、取消、复用、损坏副本、运行期注册、删除及迁移相关 `go test -race` 通过 |
| 前端检查 | `npm run check` 与现有 14 项前端回归脚本通过 |
| Windows 代码 | Windows amd64 交叉编译检查通过；仅编译测试代码，没有运行 Windows 实机程序 |
| 全库字体加载 | 50 款在 Chromium 152 实际加载并渲染 PNG；30 款 Shell 的可打印英文字符等宽验证通过 |
| 格式转换 | 49 份不同文件的全部字符映射、字宽及可变轴与上游输入一致 |
| 内置中文 | IBM 和 Maple 的简体、繁体、英文样本完整；IBM 29,285 个 Unicode 映射，Maple 22,138 个 |
| Chromium 软件流程 | 分类切换、20/30 目录、确认下载、即时使用、许可证持久化、取消、离线、删除、跨窗口较新选择保护通过 |
| xterm | 五款内置字体的中文两格、ANSI 粗体和列位置检查通过 |
| Linux WebKit | 本机 WebKitGTK 2.52.6 的 20/30 目录、样张、在线 UI 切换、五款 Shell 中英字宽与 Iosevka 中文回退通过 |
| 官网 | 桌面/390px 手机布局、严格 CSP、选择前不下载完整字体、确认后生成 ZIP 通过；Python ZIP CRC 校验与 OFL 内容复核通过 |

复查修复了下载/预览 API 的路由冲突；窄字宽字体的中文回退改为匹配两格；损坏副本支持重新获取；补充取消缓存复用任务的检查；下载完成不覆盖较新选择。旧全量字体生成脚本移除，新增最小资源校验和官网字体构建流程，避免以后构建时把已删除字体带回来。源码打包清单已纳入网站字体页源码，发行许可来源改为实际保留的字体目录。

字体浏览器测试使用独立配置、临时下载目录和本地静态字体服务，未连接用户服务器，也未修改实际连接、密钥、自定义字体和背景。

浏览器服务夹具在验收完成后继续等待收尾，达到 15 分钟上限后以超时失败退出；这是测试服务的生命周期超时。上表全量 Go 测试、Chromium 与 WebKit 功能断言有各自独立的通过结果。后续复现应在验收后及时创建夹具目录中的 `stop-browser` 文件结束服务。

## 文件与使用边界

- 网站增量包：`/home/bitsflow/share/DengShell-官网字体库补丁.zip`，152,560,231 字节，约 145.49 MiB。
- 上传说明：`/home/bitsflow/share/DengShell-字体库上传说明.txt`。
- 字体库页面源码：`build/font-library-site/`；构建说明：`build/font-licenses/README.md`。
- 原网站的安装文件和演示资源不在增量包中。首页按本地现有 R30 官网 ZIP 添加入口；如果线上首页已另行修改，只合并字体库导航，不覆盖较新的页面内容。
- 本次未上传线上网站或 GitHub，未生成安装包。线上 HTTP 读取返回 403，因此未验证当前线上字体路径、CDN 缓存及实际公网下载速度。
- 当前客户端源码重新构建、网站字体文件上传后，软件内在线入口才可投入使用；原 EXE 不会因上传网站而自动获得新功能。旧客户端可以先在网站下载并手动导入。
- Windows 实机新构建及其他 Linux WebKit 版本未覆盖。较旧内核不支持窄字体的 `size-adjust` 时会提示更新内核并保留当前字体。罕见汉字、Emoji、所有 ANSI/Unicode 宽度规则及任意用户导入字体不在“全部字形一致”的保证范围内。
- `/home/bitsflow/share/DengShell.exe` 保持原文件，SHA-256：`540e880fc288ff5f085d51db6498079d178e98c4eb154f93300fbd2eb72d98ca`。

## 字体清单

界面（20 款）：

Noto 黑体、Noto 宋体、更纱 UI、霞鹜文楷屏幕版、Maple Mono CN、IBM Plex Sans SC、未来荧黑、寒蝉全圆体、霞鹜漫黑、悠哉字体、LXGW Bright、昭源黑体、昭源宋体、昭源环方、jf open 粉圆、霞鹜文楷 TC、站酷小薇体、站酷庆科黄油体、朱雀仿宋、得意黑。

Shell（30 款）：

JetBrains Mono、Fira Code、Source Code Pro、IBM Plex Mono、Roboto Mono、Inconsolata、Fira Mono、Space Mono、Cutive Mono、PT Mono、Anonymous Pro、Overpass Mono、Red Hat Mono、DM Mono、Spline Sans Mono、Geist Mono、Azeret Mono、Martian Mono、Chivo Mono、Fragment Mono、Maple Mono CN、Noto Sans Mono CJK SC、Intel One Mono、Victor Mono、Cascadia Code、B612 Mono、CommitMono、Iosevka、Monaspace Neon、Lilex。

每款的来源、版本、完整许可证与资源哈希可在 `internal/app/font_library_catalog.json` 和 `build/font-licenses/library-sources.json` 中核对。
