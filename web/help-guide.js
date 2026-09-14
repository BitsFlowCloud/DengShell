/* Offline reference and first-use tour. The tour changes no connections or commands. */
(() => {
  'use strict';
  const sections = [
    { id: 'finalshell-import', title: '从 FinalShell 导入', icon: 'folder', html: `<p>在 FinalShell 中导出当前或全部 SSH 连接，将导出文件放进 <strong>finalshell_oot_pot</strong> 文件夹，再将此文件夹放在 DengShell 可执行程序旁。设置中点击“从 FinalShell 导入”，自动扫描并导入；密码自动解析并加密保存，无需 Python 插件。将私钥放进其中的 <strong>key</strong> 子文件夹，以连接 JSON 的 secret_key_id 命名（可带 .pem 后缀），或使用包含 ID 和私钥内容的密钥 JSON。程序自动导入密钥管理器并关联连接；同一私钥只保存一份。重复导入可补齐缺失的密钥关联，并保留已配置的凭据。仍缺失的密钥在连接时提示重新配置。</p>` },
    { id: 'ui-fonts', title: '界面字体与文字颜色', icon: 'type', html: `<p>设置 → 界面字体与文字颜色。内置 Noto 黑体、Noto 宋体、更纱 UI、霞鹜文楷屏幕版、Maple Mono CN，包含简繁中文与英文，附字体许可证。内置字体立即切换，导入的 TTF / OTF / WOFF / WOFF2 界面字体需要完整退出后重新启动才能生效。浅色、深色模式的界面文字颜色分别保存；界面字体与终端字体分别管理。</p>` },
    { id: 'common-apps', title: '常用应用：优化、清理、测试与 SWAP', icon: 'layout', html: `<p>文件区的<strong>常用应用</strong>位于传输任务左侧，提供六个胶囊按钮。命令会发送到当前 SSH 终端，进度与权限提示直接在终端显示。弹窗打开后换到其他会话，需要关闭并重新选择应用，防止误发。</p><ul><li><strong>BBR优化：</strong>选择保守、平衡或激进，检查通过后执行。三档使用现有内核的 BBR，分别偏向排队延迟、均衡、吞吐；无法保证所有链路上最低延迟或最低重传。应用前备份参数，不安装内核或自动重启。</li><li><strong>清理垃圾：</strong>先列明清理包缓存与早于14天的归档journal，确认后执行；不卸载软件，不删除用户、网站、数据库或Docker数据。</li><li><strong>NQ / YABS：</strong>先显示第三方脚本的完整调用命令，确认后在终端运行。</li><li><strong>国际测速：</strong>先确认第三方脚本，再确认大流量消耗。20～50GB只是粗略提示，实际可能更多。</li><li><strong>SWAP：</strong>先读取活动交换区和开机配置；已有时可关闭/删除或查看重新设置要求。无SWAP时提供512MB/1GB/2GB/4GB及64MB～1024GB的自定义大小。按本工具流程，删除后先重启再重新设置，不自动重启。</li></ul><p>仅删除确认身份的交换文件，分区/zram只关闭；失败、文件系统不适用或空间不足会说明。通用创建适用于ext系列/XFS，Btrfs需专用方案。zram等服务可能在重启后重新创建交换区。执行需要root或sudo，程序不会在打开应用面板时自动修改系统。Bash/Zsh/Fish集成就绪时会阻止向忙碌终端发送；未检测到集成时会提示先确保终端空闲，仍保留用户确认后直接发送的方式。</p>` },
    { id: 'start', title: '从第一台服务器开始', icon: 'terminal', html: `<p>DengShell 把 SSH 终端、远程文件和服务器状态放在同一工作台。顶部切换会话，左侧查看状态，右侧使用终端，下方管理文件或快捷命令。</p><ol><li>点击顶部文件夹图标，进入<strong>服务器管理</strong>，选择“新建连接”。</li><li>填写名称、服务器地址、端口和用户名，选择所属分组及认证方式。</li><li>按需要选择密钥、保存凭据或配置代理，再点击“保存并连接”。</li><li>连接成功后即可使用终端和文件面板；以后双击已保存的服务器可再次连接。</li></ol><p>未连接时，监控显示“—”，文件列表为空，这是等待连接的状态。手册和引导在未连接时也可以查看。</p>` },
    { id: 'groups', title: '服务器与多级分组', icon: 'folder', html: `<p>服务器管理采用左侧目录、右侧连接的分栏布局。目录通过缩进、连线和级别区分，选中后整行突出显示。拖动中间分隔线可调整宽度，深层目录可横向滚动；右侧可点击路径返回上级，或切换“包含子分组”。栏宽、所选目录与子分组选项会保存。搜索框按名称、地址、用户名或分组路径搜索全部连接。单击选择，双击打开连接。</p><ul><li><strong>新建分组：</strong>可选择顶层或任意上级分组；分组右侧“⋯”可以添加服务器、新建子分组，或修改名称、图标和层级。</li><li><strong>展开与收起：</strong>点击分组左侧箭头或使用左右方向键，收起状态会保留；点击目录名称在右侧查看连接。搜索会临时展开相关祖先，不改变已保存的收起状态。</li><li><strong>图标：</strong>可搜索常用表情、国家或地区名称以及英文缩写，或自行输入表情。内置国旗使用离线图像显示。</li><li><strong>排序：</strong>忽略开头的表情和标点，数字在前，之后是英文、中文拼音；编号 2 排在 10 前面。</li><li><strong>编辑连接：</strong>右键连接或点击“⋯”，可编辑、删除、定位分组或复制地址。编辑可更换分组、地址、认证和代理；保存后在下次连接时使用新的配置。</li><li><strong>批量连接：</strong>按住 <kbd>Ctrl</kbd>（或 <kbd>⌘</kbd>）单击服务器进行多选，再点击“打开所选”或按 <kbd>Enter</kbd>。所有选中连接同时发起，标签立即出现；各台独立连接、失败和重试，快服务器无需等待慢服务器。再次按住 Ctrl 单击可取消该项，Esc 可清空选择。</li></ul><p>有子分组、服务器，或“已删除”中仍有连接引用的分组不能直接删除。</p>` },
    { id: 'connection-history', title: '历史连接与已删除', icon: 'history', html: `<p><strong>历史连接</strong>展示成功连接的时间和累计次数，便于找回最近使用的服务器。它与终端里的命令历史是两个列表。</p><ul><li>删除服务器会先移入<strong>已删除</strong>；若仍有会话，会先确认是否关闭。</li><li>在“已删除”右键连接，选择<strong>恢复</strong>，可恢复原连接、分组归属和保存的认证信息。</li><li><strong>永久删除</strong>只出现在“已删除”中，确认后移除该连接及相关历史，无法从回收区恢复。</li><li>历史中已删除的连接不能直接连接，可先转到“已删除”进行恢复。</li></ul><p>这里删除的是本机连接配置，不会删除远程服务器。远程文件的删除属于文件面板中的另一项操作。</p>` },
    { id: 'keys', title: '认证与密钥管理器', icon: 'key', html: `<p>连接支持密码、私钥和 SSH Agent。设置中的<strong>密钥管理器</strong>可导入、命名、生成 Ed25519 密钥，并查看、复制公钥和指纹；同一把密钥可以绑定多台服务器。</p><ul><li>新建或编辑连接，选择“私钥”认证，再选择密钥库中的密钥；也可以填写本机私钥路径。</li><li>密钥管理器导入或生成加密私钥时，会将口令一起保存在本机加密配置中；所有引用这把密钥的连接自动使用，重启后仍有效。旧版导入的密钥可在管理器中点“编辑”补填口令。直接填写外部私钥路径时，口令仍由连接配置管理；没有勾选保存的密码或口令只用于本次运行。</li><li>使用生成的密钥前，需要将公钥配置到远程账号，例如该账号的 <code>authorized_keys</code>；生成密钥本身不会自动修改服务器。</li><li>SSH Agent 使用本机可用的代理密钥。Agent 的启动和加载由本机环境提供；连接、密钥读取或签名停滞会超时，关闭应用也会取消等待。</li><li>首次连接或主机指纹变化时，会在发送登录凭据前要求确认。请通过服务器控制台或可信管理员核对界面展示的新旧指纹；确认一致后才选择“已核实，信任并连接”。认证被拒绝时会显示中文原因，并允许重新输入一次密码。</li></ul><p>密钥库中的副本跟随配置目录；手动填写的外部私钥路径需要自行保留，换电脑时可能要更新路径。</p>` },
    { id: 'proxies', title: '连接代理', icon: 'network', html: `<p>设置 → <strong>代理管理器</strong>可保存常用 SOCKS5 或 HTTP CONNECT 代理，包括名称、地址、端口和可选的用户名、密码。</p><ol><li>添加代理后，打开服务器的连接编辑器。</li><li>展开“连接代理”，选择已保存的代理；也可为这条连接填写自定义代理。</li><li>保存后重新连接，新的会话使用所选路径。</li></ol><p><strong>直连</strong>遵循系统路由。如果本机开启了 TUN，直连流量仍可能被系统 TUN 接管。SOCKS5 / HTTP 代理由代理端解析服务器域名。ICMP 探测不等同于 SSH 代理链路的响应时间。</p>` },
    { id: 'terminal', title: '终端、选择与剪贴板', icon: 'terminal', html: `<p>连接后直接在终端输入，与普通 SSH 终端一样使用交互式 Shell、编辑器和命令行程序。每个SSH使用一个独立标签，在顶部点击切换；选中的已连接标签右侧标记“当前连接”。当前窗口至少有两个SSH标签时才显示“独立窗口”按钮，关闭到一个或没有标签时立即隐藏；当前连接就绪后可点击拆分。Ctrl / ⌘ + 单击多选服务器后，“打开所选”在同一窗口建立多个标签，所有选中连接同时发起、立即显示各自标签，快的先可用、慢的独立等待，完成后不抢正在使用的标签。点击终端右上角的“独立窗口”，或将已连接的标签拖出标签栏后松开（预览卡显示目标，Esc 可取消），可把原 SSH 会话移到独立窗口；右键标签入口仍保留。拆分先显示较小的毛玻璃终端预览，再打开新窗口。将标签拖向另一个可见的DengShell窗口，提示“合并到”后松开，标签会加入目标窗口；右键标签也可直接选择合并目标。Windows与Linux X11支持拖动命中；Wayland请使用合并菜单。只能在同一次启动共享后台的窗口之间移动。空独立窗口会自动关闭，主窗口保留。远程 PTY 不重建，运行中的程序、终端内容、当前目录与网络曲线保留；取消成功时恢复源窗口；交接状态暂无法确认时保留窗口并锁定相关操作。新窗口以可移动的普通窗口打开，仍有单独 WebView 的资源开销。若提示存在待传文件，先等待上传完成再拆分。终端工具栏可断开、重新连接、调整字号和清除滚屏。断开位于重连左侧，保留标签和滚屏内容；重新连接会创建新会话。</p><ul><li>用鼠标拖动选择终端文字，再按 <kbd>Ctrl</kbd> + <kbd>Shift</kbd> + <kbd>C</kbd> 复制；右键菜单也有“复制”。</li><li>按 <kbd>Ctrl</kbd> + <kbd>Shift</kbd> + <kbd>V</kbd> 或右键“粘贴”，将剪贴板文字送入当前终端。</li><li><kbd>Ctrl</kbd> + <kbd>C</kbd> 始终向远程终端发送中断，不用作复制。</li><li>清除滚屏只清理本地终端显示；不会撤销已经执行的命令。</li></ul><p>粘贴的内容会交给当前远程程序。带换行的文本如何处理取决于远程程序及其粘贴模式；执行前请确认当前会话和文本内容。</p>` },
    { id: 'chart-styles', title: '网速与延迟曲线样式', icon: 'network', html: `<p>设置 → <strong>曲线样式</strong>可独立调整上行、下行的实线或虚线、粗细和颜色，以及延迟曲线的粗细和颜色。粗细范围为 0.5–6 px。</p><p>调整时可以预览；点击“保存设置”后持久保存，重启后仍有效。颜色可自定义或跟随明暗主题，每条曲线都可单独恢复默认。点击取消会恢复保存前的样式。</p>` },
    { id: 'command-history', title: '命令输入条与历史', icon: 'history', html: `<p>终端底部的输入条适合编写或修改命令。按 <kbd>Enter</kbd> 发送并执行，<kbd>Shift</kbd> + <kbd>Enter</kbd> 换行，<kbd>↑</kbd> / <kbd>↓</kbd> 浏览该服务器的历史命令。</p><p>点击输入条右侧的历史图标，可搜索命令。点击命令正文会填入输入条，便于先编辑；旁边的<strong>复制</strong>复制完整内容，<strong>执行</strong>会立即发往对应的会话。也可清空历史列表。</p><p>命令历史列表汇总所有 VPS 中执行过的命令，最多保留最近 2000 条（合计 2 MiB），切换服务器或未连接时仍可查看和复制，兼容的远程 Shell 会在实际提交命令后记录。密码提示、终端应用内输入，以及尚未提交的文字不会被当作命令历史。Shell 不支持集成时，记录范围可能受到限制。</p>` },
    { id: 'quick-commands', title: '快捷命令胶囊', icon: 'code', html: `<p>下方面板选择<strong>命令</strong>页签，可以创建常用命令。每条命令可设置名称、分组、颜色和多行内容。</p><ul><li>在命令区域右键选择<strong>新建分组</strong>；空组也会保存，删除组内最后一条命令不会删除分组，旧版命令分组自动迁移。</li><li>点击“新建命令”填写内容，使用主题下拉选择已有分组。分组旁“＋”可创建新组，并保留正在编辑的名称和命令内容；使用不同颜色区分用途。</li><li>右键胶囊可编辑、删除或选择“调用前编辑”。拖动命令和分组可排序，拖到其他分组可移动命令。</li><li>每条命令独立保存<strong>末尾添加回车 CR</strong>；默认不勾选，仅填入终端，勾选后发送并执行。带参数的命令先打开命令编辑区。</li><li>可插入 1–5 号参数，使用 [p#1 参数名] 占位符；编辑区填写参数并查看实际命令预览，内容保留以便反复使用。发送目标独立显示，切换服务器标签不会暗中改发到其他服务器。</li></ul><p>命令内容由你自行维护。发送之前先看清顶部会话，确认当前目标服务器。</p>` },
    { id: 'files', title: '远程文件与上传', icon: 'upload', html: `<p>下方<strong>文件</strong>面板使用 SFTP。左边显示远程根目录下的真实目录，右边显示所选目录中的文件；两个区域可以独立滚动。</p><ul><li>双击目录进入，使用路径栏跳转、返回上级或刷新；名称搜索用于过滤当前目录。</li><li>将本机文件拖入文件区域即可上传到当前远程目录；“上传”菜单也可选择文件或文件夹。</li><li><strong>传输任务</strong>页签显示进度和结果，支持取消、重试；同名目标会提供覆盖相关处理。最多 4 个文件同时上传，超出的等待，完成后自动补位；每个大文件最多并行写入 32 个 SFTP 分块，进度只统计服务器已确认的数据。重试会从头开始，不支持断点续传。</li><li>选中文件后可以下载、重命名或删除，文件区“＋”提供新建文件、新建文件夹。远程文件删除无法通过“已删除连接”恢复，目录删除会包含子项。</li><li>“跟随终端”依赖远程 Shell 的目录报告，支持时可随终端切换目录。</li></ul><p>上传前可先确认路径栏。权限不足或连接中断时，查看传输任务中的失败原因。</p>` },
    { id: 'monitor', title: '系统、进程与磁盘', icon: 'cpu', html: `<p>左侧上方以对应操作系统图标和完整名称突出显示发行版，并显示当前服务器的运行时间、CPU、内存、交换空间和负载；活动进程可按内存、CPU 或命令列排序。进程列表仅在展开且窗口可见时按需采集；收起后保留最近采样缓存和时间，不再扫描进程，不代表当前实时数量。收起后显示“点击显示进程列表”，最近采样时间保留在提示中。</p><p>网络流量每<strong>1秒</strong>独立采样，负载、CPU、内存和磁盘速率每<strong>5秒</strong>采样；网卡元信息和磁盘容量约30秒刷新。每条已连接SSH的网卡流量在后台持续记录，切换标签或最小化也保留最近30秒真实历史；回到标签时立即显示。负载、CPU、内存和磁盘等其它采样仍按可见状态请求。网络使用远端实际时间差计算，SSH或TUN返回延迟不会被当成远端采样间隔。读数明确标注“上行”和“下行”，例如上行4.0 KiB/s、下行819 B/s。默认上行橙色实线、下行蓝色虚线，可在“设置 → 曲线样式”中分别调整颜色、粗细和线型；刻度按最近<strong>30秒</strong>数据自动切换B/s、KiB/s、MiB/s、GiB/s刻度（1024进位），新峰值立即扩大范围，较小范围稳定5秒后缩小。缺测保留断点，不填入假零值。下方磁盘区域显示挂载路径、可用和总容量，以及读写速度。</p><p>IPv4/IPv6面板只显示可确认的公网地址，点击复制，悬停查看来源；可能显示SSH入口或网卡地址。NAT入口不等于服务器出口，无法确认时显示“—”。真实内网地址仍保留在网卡菜单提示中。</p><p>这些数值来自远程服务器的周期采样，不是本机硬件数据。刚连接时需要等待首轮采样；部分权限或系统接口不可用时可能显示“—”。较窄窗口可通过顶部芯片图标打开侧栏。</p>` },
    { id: 'network', title: 'ICMP 与 MTR 网络诊断', icon: 'network', html: `<p>ICMP 区域展示本机到服务器的当前延迟、最近 1 分钟丢包率和延迟连续曲线。曲线从左到右由旧到新，经过真实采样并保留峰值；丢包和缺测位置断开。刻度与曲线使用同一坐标系；左侧毫秒刻度按当前样本调整，非零起点有断轴标记。成功回包使用同一种颜色，红叉代表超时或不可达（延迟未知），灰点代表待回包或本机探测不可用。悬停可查看时间、读数和状态，也可聚焦图表后用左右方向键逐项查看。</p><p>点击网络区域可打开 MTR 诊断。可以从本机探测服务器，也可以从远程服务器探测你指定的目标，查看每一跳的响应和可用的 ASN 信息。</p><ul><li>缺少 <code>mtr</code> 时，界面会显示安装方案和命令，只有明确确认后才开始安装。</li><li>需要交互验证时，按提示在对应终端完成；安装后可以重新检测。</li><li>中间路由器可能不响应或限制探测包，单个中间节点不回复不能直接判断业务流量丢包。</li><li>代理、TUN、路由和目标是否允许 ICMP，都会影响结果。ICMP 数值不是 SSH 会话的按键响应计时。</li></ul>` },
    { id: 'fonts', title: '字体、加粗与文字颜色', icon: 'type', html: `<p>顶部 <strong>T</strong> 按钮打开字体管理器。内置 20 款英文等宽字体组合，配合随程序携带的中文回退字体显示中英文；也可导入 TTF、OTF、WOFF、WOFF2 字体。</p><ul><li>点击字体卡片立即切换终端字体；字号控件与终端 A− / A+ 均按 <strong>0.5 px</strong> 调整，范围为 8–40 px。可输入例如 14.5，选择会保存，重启后保留。</li><li>每张卡片都有独立的<strong>加粗</strong>和<strong>颜色</strong>控件，颜色按钮打开可拖动的配色窗口。可用预设色、颜色选择器、RGB 数值或 HEX 颜色。</li><li>当前字体的修改立即预览；修改其他卡片只保存那款字体的样式，切换过去后再应用。</li><li>字体管理器可拖动，不遮暗终端，方便观察真实显示效果。可以删除自行导入的字体。</li></ul><p>设置中的<strong>提示符颜色</strong>可分别调整 @ 前的用户名和 @ 后的主机名，支持预设色、RGB、HEX 和拖动预览；Bash / Zsh / Fish 在下次提示符出现时生效，两项选择“保持原样”恢复原提示符。终端默认文字颜色可自定义，远程程序主动设置的 ANSI 颜色仍保留其含义。自定义字体的符号覆盖和等宽效果取决于字体本身。</p>` },
    { id: 'appearance', title: '背景、主题、布局与缩放', icon: 'image', html: `<p>设置 → <strong>背景管理器</strong>可选择内置背景或导入 PNG、JPG、WebP 图片，并调整显示强度。选择后立即应用；面板可拖动，自定义背景可删除。</p><ul><li>设置中的<strong>明暗跟随系统</strong>让Windows和Linux随桌面偏好切换主题，选择会保存。顶部太阳 / 月亮按钮手动切换浅色和深色时会退出跟随；重新打开设置开关即可恢复。系统变化和手动变化都使用开灯/关灯渐变。</li><li>拖动左侧与终端之间的分隔条调整侧栏宽度，拖动文件面板上方的分隔条调整终端和文件区高度。</li><li>设置中的<strong>界面缩放</strong>调整整个应用；终端的字号按钮只改变终端文字。</li><li>窗口较小时会适当限制实际缩放，避免控件被挤出窗口；放大窗口后恢复所选比例。</li><li>顶部布局按钮恢复默认布局和终端字号。</li></ul>` },
    { id: 'config', title: '配置、备份与换电脑', icon: 'folder', html: `<p>设置 → <strong>连接配置文档</strong>可查看实际保存位置并复制路径。便携版默认使用程序旁 data 目录；deb 版使用用户配置目录。</p><ol><li><strong>升级：</strong>在同一目录中替换可执行程序，保留配置文件和资源。</li><li><strong>备份或换电脑：</strong>一起复制 <code>dengshell.config.enc</code>、<code>dengshell.config.key</code>、<code>assets/</code> 和 <code>keys/</code>，不要只复制加密配置文件。</li><li><strong>恢复：</strong>退出应用，将整套备份放回实际配置目录，再启动。手动指定的外部私钥需要另行复制并检查路径。</li></ol><p>多个窗口共享同一配置服务，保存时只合并实际修改的设置、字体样式键和历史键，避免旧快照覆盖其他窗口的新设置；同一项同时修改时以最后成功保存为准。程序使用旁边的密钥文件自动解锁配置；加密不防止文件被删除。新程序目录没有配置时不会自动读取旧的用户目录，请按“连接配置文档”中的迁移说明处理。具体位置以应用显示的实际路径为准。</p>` },
    { id: 'startup', title: '启动、版本与再次查看帮助', icon: 'settings', html: `<p>设置中的启动动画选项控制开场显示。首次功能引导会在动画和版本提示结束后出现；“暂时结束”、关闭按钮或 <kbd>Esc</kbd> 都可以提前结束，并记录为已查看。</p><p>启动时可检查版本信息。发现新版本时显示当前版本、新版本和更新说明，可以选择“稍后再说”。Windows、Debian/Ubuntu 和 Arch pacman 版确认在线升级后会下载、验证发行签名并安装更新；Linux 安装需要系统授权。旧 Arch 客户端须先手动安装一次 R28 或更新版。其他 Linux 系列可到官网下载 RPM 或通用安装包。替换主程序需要重新启动并断开会话；可备份配置后在原目录手动替换程序。</p><p>设置按钮左侧的书本图标始终是本手册入口。手册支持关键词搜索和章节跳转，下方“重新查看引导”会从第一步重新开始；查看说明不会创建连接或执行命令。</p>` },
    { id: 'file-editor', title: '文件右键菜单与文本编辑器', icon: 'file', html: `<p>双击文件默认进入内置文本编辑器。左侧目录树不响应右键。右侧右键文件或目录可以打开、选择打开方式、删除、快速删除、重命名、打包下载、复制路径及修改权限；空白处右键可以刷新、新建文件或文件夹。</p><p>文本编辑器支持 UTF-8、带 BOM 的 UTF-8 / UTF-16、GB18030、GBK 和 Big5，保留原有 BOM、空格及混合换行。粘贴按剪贴板交付的 Unicode 原样插入；操作系统已经转换的原始字节无法从剪贴板恢复。编码不明确时需手动选择，不猜测后静默保存。单文件最大 8 MiB。</p><p>每个文件拥有独立标签、文本和撤销记录，可同时打开不同服务器的文件。标签显示服务器、文件名及“当前查看”；可移到新的可拖动编辑窗口，每个窗口都有自己的标签栏。收起后通过文件工具栏“文本编辑器”恢复。关闭未保存文件会询问，连接断开后保留草稿供复制。Ctrl+Tab 切换文件，Ctrl+W 关闭当前文件。</p><p>按 Ctrl+S 保存到文件原所属服务器；保存前检查远程文件是否被其他程序改动，原子替换并保留权限与所有者。系统关联在本地打开下载的副本，可指定编辑器可执行文件；外部修改后需要手动上传。右键菜单的<strong>删除</strong>和<strong>快速删除</strong>都先弹出确认框，列出服务器和完整路径；目录删除包含子项且无法撤销。切换页签不会改变已确认的目标；原连接若已断开，本次删除不会执行。打包在本机进行，不占用远程压缩进程。</p>` },
    { id: 'tray-prompt', title: '托盘、窗口和提示符颜色', icon: 'settings', html: `<p>首次最小化可选择普通最小化或缩小到托盘，并记住选择；设置中可以修改。Linux 需要桌面提供托盘服务，不可用时保留普通最小化。窗口隐藏时请求退出，会先恢复窗口再显示确认框。</p><p>首次窗口宽高默认为屏幕的 75%，使用系统 DPI；之后恢复已保存尺寸。设置可把信息栏放在终端左侧或右侧、文件区放在上方或下方。</p><p>提示符颜色设置可分别配置用户名和主机名，支持预设、RGB 色盘和当前会话预览。Bash / Zsh / Fish 在下一次显示提示符时生效，不向正在运行的程序注入按键；其他 Shell 保留原提示符。字体颜色则在每张字体卡片中独立设置。</p>` },
  ];
  const steps = [
    { section: 'start', title: '先认识你的 SSH 工作台', target: '.terminal-panel', fallback: '#help-button', location: '顶部会话 · 左侧状态 · 右侧终端 · 下方文件', text: '把常用操作集中在一个窗口。接下来用 12 步走过主要功能；未连接服务器也可以完整查看。', bullets: ['每一步只介绍操作入口，不会替你连接或执行命令。', '随时点击“暂时结束”或按 Esc，之后可从使用手册重新查看。'] },
    { section: 'groups', title: '整理服务器，也能找回删除的连接', target: '#connection-button', location: '顶部文件夹 → 服务器管理', text: '新建连接时填写地址、账号和认证。用任意层级的分组整理服务器，再用搜索、折叠和图标快速定位。', bullets: ['Ctrl / ⌘ + 单击可多选，点击“打开所选”立即显示全部标签，同时发起各台连接。“历史连接”记录成功连接。', '删除先进入“已删除”，可以恢复；永久删除需要单独确认。'] },
    { section: 'keys', title: '为连接选择密钥和代理', target: '#settings-button', location: '设置 → 密钥管理器 / 代理管理器', text: '密钥可以导入、命名、生成和查看公钥；代理支持 SOCKS5 与 HTTP CONNECT。保存后在服务器连接编辑器中选用。', bullets: ['支持密码、私钥文件、加密私钥与 SSH Agent。', '代理按连接选择；系统 TUN 仍可能影响所谓“直连”的实际路径。'] },
    { section: 'terminal', title: '在终端输入，正确使用复制和历史', target: '.terminal-command-bar', fallback: '.terminal-panel', location: '右侧终端与底部命令输入条', text: '选中文字后用 Ctrl + Shift + C 复制，用 Ctrl + Shift + V 粘贴，也可以使用右键菜单。Ctrl + C 用于中断远程程序。', bullets: ['输入条 Enter 执行，Shift + Enter 换行，↑ / ↓ 浏览历史。', '历史按钮支持搜索、填入、复制或直接执行已有命令。'] },
    { section: 'quick-commands', title: '把常用操作保存为快捷命令', target: '[data-pane="commands"]', location: '下方面板 → 命令', text: '命令区域右键可新建分组，空组也会保留。为命令设置名称、分组、颜色和内容，每条命令显示为彩色胶囊。', bullets: ['新建或编辑命令时可选择已有分组，分组旁“＋”可创建新组。命令默认不追加回车，勾选回车后才自动执行；带参数时先进入编辑区。', '发送前确认顶部会话，避免选错服务器。'] },
    { section: 'common-apps', title: '选择常用工具，在终端中执行', target: '[data-pane="common-apps"]', location: '下方面板 → 常用应用', text: 'BBR三档、缓存清理、第三方测试与SWAP管理放在六个胶囊中。选择前确认当前SSH会话，执行输出在终端显示。', bullets: ['清理和第三方测试先显示说明，国际测速另有一次流量确认。', 'SWAP先检查真实状态；已有SWAP时提示删除并重启后再设置，不自动重启。'] },
    { section: 'files', title: '查看目录，拖入文件即可上传', target: '.files-panel', location: '下方面板 → 文件 / 传输任务', text: '左侧是远程实际目录，右侧是所选目录内容，两个区域独立滚动。支持路径跳转、上传、下载和重命名；文件区“＋”可新建文件或文件夹。', bullets: ['拖入文件，或从上传菜单选择文件 / 文件夹；最多 4 个文件同时上传，额外排队；进度在“传输任务”中查看。', '删除远程文件不可撤销；“已删除连接”不负责恢复远程文件。'] },
    { section: 'monitor', title: '查看系统资源和磁盘空间', target: '.sidebar', fallback: '#toggle-monitor', location: '左侧状态栏；窄窗口用顶部芯片按钮打开', text: 'CPU、内存、负载、运行时间、活动进程、网卡流量和磁盘容量来自当前远程服务器的周期采样。', bullets: ['选择网卡查看流量，点击进程列标题切换排序。', '未连接或尚未采样时显示“—”，连接成功后会刷新。'] },
    { section: 'network', title: '用 ICMP 与 MTR 观察网络', target: '#ping-status', fallback: '#toggle-monitor', location: '左侧 ICMP 区域；点击网络图打开诊断', text: '查看当前延迟、最近 1 分钟丢包率和延迟变化，也可以从本机或远程服务器进行 MTR 路由诊断。', bullets: ['ICMP 不等于 SSH 代理会话的按键响应时间。', '缺少 mtr 时先展示安装方案，确认之后才开始安装。'] },
    { section: 'fonts', title: '每款字体都有自己的加粗和颜色', target: '#manage-fonts', location: '顶部 T → 字体管理器；设置 → 背景管理器', text: '内置 20 款中英文显示组合，可导入字体和背景。每张字体卡片单独保存加粗与文字颜色，支持预设、RGB 和 HEX。', bullets: ['选择当前字体立即预览；字号按 0.5 px 调整，面板可拖动。', '背景可以自定义并调整强度，文字的 ANSI 颜色仍由远程程序决定。'] },
    { section: 'appearance', title: '调整主题、布局和整体大小', target: '#reset-layout', location: '顶部布局 / 主题按钮；设置 → 界面缩放', text: '以开关灯效果切换浅色或深色主题，拖动分隔条调整终端、侧栏和文件区比例。界面缩放与终端字号可以分别调整。', bullets: ['小窗口会适当限制实际缩放，保证控件仍然可用。', '布局按钮恢复默认布局和终端字号；启动动画也可在设置中调整。'] },
    { section: 'config', title: '保留配置，随时回来查手册', target: '#help-button', location: '设置 → 连接配置文档；顶部书本 → 使用手册', text: '升级时在同一目录替换程序。备份或换电脑时，一起保留加密配置、解锁密钥和 assets / keys 资源目录。', bullets: ['启动检查最多 3 秒；按 SHA-256 比较固定更新包，确认后下载、校验并重启安装。', '本手册逐项说明功能并支持搜索，也可以重新播放这份引导。'] },
  ];
  let initialized = false, configSeen = false, completed = false, autoStarted = false, stepIndex = 0, persistence = null, guideReturn = null, manualReturn = null;
  let resolveConfig;
  const configReady = new Promise(resolve => { resolveConfig = resolve; });
  const $h = selector => document.querySelector(selector);
  function acceptConfig(config) {
    if (!config?.appearance) return;
    completed = !!config.appearance.onboardingCompleted;
    configSeen = true; resolveConfig();
  }
  function make(tag, className = '', text = '') { const element = document.createElement(tag); element.className = className; element.textContent = text; return element; }
  function closeButton(label, id) { const button = make('button', 'icon-button'); button.type = 'button'; button.id = id; button.setAttribute('aria-label', label); button.innerHTML = '<svg aria-hidden="true"><use href="#i-close"/></svg>'; return button; }
  function visible(element) {
    if (!element || !element.getClientRects().length || element.closest('[hidden]')) return false;
    const rect = element.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0 && rect.bottom > 0 && rect.right > 0 && rect.left < innerWidth && rect.top < innerHeight;
  }
  function restoreFocus(element) { if (visible(element) && !element.disabled) element.focus({ preventScroll: true }); }
  function keepModalTab(event) {
    if (event.key !== 'Tab') return;
    const dialog = event.currentTarget;
    const controls = [...dialog.querySelectorAll('button:not(:disabled),input:not(:disabled),[tabindex="0"]')].filter(visible);
    if (!controls.length) return;
    const first = controls[0], last = controls.at(-1);
    if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    else if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
  }
  function positionTarget() {
    const ring = $h('#help-guide-target'), dialog = $h('#help-guide-dialog');
    if (!dialog?.open || $h('#help-manual-dialog')?.open) { if (ring) ring.hidden = true; return; }
    const step = steps[stepIndex], primary = $h(step.target), target = visible(primary) ? primary : $h(step.fallback || '#help-button');
    if (!visible(target)) { ring.hidden = true; return; }
    const rect = target.getBoundingClientRect(), scale = typeof effectiveScale === 'number' ? effectiveScale : 1;
    const left = Math.max(3, rect.left - 4), top = Math.max(3, rect.top - 4), right = Math.min(innerWidth - 3, rect.right + 4), bottom = Math.min(innerHeight - 3, rect.bottom + 4);
    ring.style.left = `${left / scale}px`; ring.style.top = `${top / scale}px`; ring.style.width = `${(right - left) / scale}px`; ring.style.height = `${(bottom - top) / scale}px`; ring.hidden = false;
    ring.dataset.target = target.id || step.target;
  }
  function renderStep(focus = true) {
    const step = steps[stepIndex];
    $h('#help-guide-title').textContent = step.title;
    $h('#help-guide-location').textContent = step.location;
    $h('#help-guide-text').textContent = step.text;
    $h('#help-guide-points').replaceChildren(...step.bullets.map(text => make('li', '', text)));
    $h('#help-guide-step').textContent = `${stepIndex + 1} / ${steps.length}`;
    const progress = $h('#help-guide-progress'); progress.value = stepIndex + 1; progress.max = steps.length; progress.setAttribute('aria-label', `功能引导，第 ${stepIndex + 1} 步，共 ${steps.length} 步`);
    $h('#help-guide-prev').disabled = stepIndex === 0;
    $h('#help-guide-next').textContent = stepIndex === steps.length - 1 ? '开始使用' : '下一步';
    if (focus) $h('#help-guide-title').focus({ preventScroll: true });
    requestAnimationFrame(positionTarget);
  }
  function saveCompletion() {
    if (completed || persistence) return persistence || Promise.resolve();
    const previous = appearance.onboardingCompleted;
    persistence = persistAppearance({ onboardingCompleted: true }).then(() => { completed = true; }).catch(error => {
      appearance.onboardingCompleted = previous;
      toast(`引导已关闭，但查看状态未能保存：${error.message || error}`);
    }).finally(() => { persistence = null; });
    return persistence;
  }
  function closeGuide() {
    const dialog = $h('#help-guide-dialog'); if (!dialog?.open) return;
    autoStarted = true; dialog.close(); $h('#help-guide-target').hidden = true;
    restoreFocus(guideReturn); saveCompletion();
  }
  function openGuide() {
    if (!initialized) return;
    autoStarted = true; stepIndex = 0;
    const manual = $h('#help-manual-dialog'); if (manual.open) manual.close();
    const dialog = $h('#help-guide-dialog');
    if (!dialog.open) { guideReturn = document.activeElement; dialog.showModal(); }
    renderStep();
  }
  async function replayGuide() {
    await configReady;
    if (window.DengShellStartupReady) await window.DengShellStartupReady;
    openGuide();
  }
  function renderManual() {
    const query = $h('#help-manual-search').value.trim().toLocaleLowerCase();
    const visibleSections = sections.filter(section => `${section.title} ${section.html.replace(/<[^>]+>/g, ' ')}`.toLocaleLowerCase().includes(query));
    const wanted = new Set(visibleSections.map(section => section.id));
    for (const article of $h('#help-manual-content').querySelectorAll('article')) article.hidden = !wanted.has(article.dataset.section);
    for (const button of $h('#help-manual-nav').querySelectorAll('button')) button.hidden = !wanted.has(button.dataset.section);
    $h('#help-manual-empty').hidden = !!visibleSections.length;
    $h('#help-manual-results').textContent = query ? `${visibleSections.length} 个相关章节` : `${sections.length} 个功能章节`;
    $h('#help-manual-content').scrollTop = 0;
  }
  function openManual(sectionID = '') {
    if (!initialized) return;
    const dialog = $h('#help-manual-dialog');
    $h('#help-manual-search').value = ''; renderManual();
    if (!dialog.open) { manualReturn = document.activeElement; dialog.showModal(); }
    $h('#help-guide-target').hidden = true;
    $h('#help-manual-search').focus({ preventScroll: true });
    if (sectionID) requestAnimationFrame(() => jumpManual(sectionID));
  }
  function jumpManual(id) {
    const section = document.getElementById(`help-section-${id}`);
    if (!section || section.hidden) return;
    const scroller = $h('#help-manual-content'), scale = typeof effectiveScale === 'number' ? effectiveScale : 1;
    scroller.scrollTop += (section.getBoundingClientRect().top - scroller.getBoundingClientRect().top) / scale;
    section.querySelector('h3').focus({ preventScroll: true });
  }
  function closeManual() {
    const dialog = $h('#help-manual-dialog'); if (!dialog?.open) return;
    dialog.close(); restoreFocus(manualReturn); positionTarget();
  }
  async function startFirstGuide() {
    if (window.CLOUDSHELL?.detachedNonce) return;
    await configReady;
    // The coordinator owns splash/update ordering; never race a startup modal.
    const startup = window.DengShellStartupReady;
    if (!startup || typeof startup.then !== 'function') return;
    try { await startup; } catch { return; }
    if (completed || autoStarted || !configSeen) return;
    const maybeOpen = () => {
      if (completed || autoStarted) { document.removeEventListener('close', maybeOpen, true); return; }
      if (document.querySelector('dialog[open]')) return;
      document.removeEventListener('close', maybeOpen, true); openGuide();
    };
    document.addEventListener('close', maybeOpen, true); maybeOpen();
  }
  function initializeHelp() {
    if (initialized) return; initialized = true;
    const guide = make('dialog', 'help-guide-dialog'); guide.id = 'help-guide-dialog'; guide.setAttribute('aria-labelledby', 'help-guide-title'); guide.setAttribute('aria-describedby', 'help-guide-text');
    guide.innerHTML = `<div class="help-guide-top"><span class="help-eyebrow">功能引导</span><span id="help-guide-step" aria-live="polite"></span></div><progress id="help-guide-progress" value="1" max="12"></progress><div class="help-guide-body"><p id="help-guide-location"></p><h2 id="help-guide-title" tabindex="-1"></h2><p id="help-guide-text"></p><ul id="help-guide-points"></ul></div><div class="help-guide-footer"><button type="button" id="help-guide-manual">查看本节手册</button><button type="button" id="help-guide-skip">暂时结束</button><div><button type="button" id="help-guide-prev">上一步</button><button type="button" id="help-guide-next" class="primary-button">下一步</button></div></div>`;
    guide.querySelector('.help-guide-top').append(closeButton('结束功能引导', 'help-guide-close'));
    const ring = make('div', 'help-guide-target'); ring.id = 'help-guide-target'; ring.setAttribute('aria-hidden', 'true'); ring.hidden = true;
    const manual = make('dialog', 'help-manual-dialog'); manual.id = 'help-manual-dialog'; manual.setAttribute('aria-labelledby', 'help-manual-title');
    manual.innerHTML = `<div class="help-manual-heading"><div><span class="help-eyebrow">DENGSHELL</span><h2 id="help-manual-title">使用手册</h2></div></div><label class="help-manual-search"><svg aria-hidden="true"><use href="#i-search"/></svg><input type="search" id="help-manual-search" aria-label="搜索使用手册" placeholder="搜索功能、操作或快捷键"><span id="help-manual-results" role="status" aria-live="polite"></span></label><div class="help-manual-layout"><nav id="help-manual-nav" aria-label="手册章节"></nav><div id="help-manual-content" tabindex="0" aria-label="功能说明"><p id="help-manual-empty" hidden>没有匹配的章节，试试“代理”“字体”或“上传”。</p></div></div><div class="help-manual-footer"><button type="button" id="help-manual-replay">重新查看引导</button><span>离线可用 · Esc 关闭</span><button type="button" id="help-manual-done" class="primary-button">关闭手册</button></div>`;
    manual.querySelector('.help-manual-heading').append(closeButton('关闭使用手册', 'help-manual-close'));
    for (const section of sections) {
      const button = make('button', '', section.title); button.type = 'button'; button.dataset.section = section.id; button.onclick = () => jumpManual(section.id); manual.querySelector('#help-manual-nav').append(button);
      const article = make('article', 'help-manual-section'); article.id = `help-section-${section.id}`; article.dataset.section = section.id;
      const heading = make('h3', '', section.title); heading.tabIndex = -1; article.append(heading);
      const content = make('div'); content.innerHTML = section.html; article.append(content); manual.querySelector('#help-manual-content').append(article);
    }
    document.body.append(ring, guide, manual);
    guide.addEventListener('keydown', keepModalTab); manual.addEventListener('keydown', keepModalTab);
    guide.addEventListener('keydown', event => { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); closeGuide(); } });
    manual.addEventListener('keydown', event => { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); closeManual(); } });
    $h('#help-button').onclick = () => openManual();
    $h('#help-guide-close').onclick = closeGuide; $h('#help-guide-skip').onclick = closeGuide;
    $h('#help-guide-prev').onclick = () => { if (stepIndex > 0) { stepIndex--; renderStep(); } };
    $h('#help-guide-next').onclick = () => { if (stepIndex === steps.length - 1) closeGuide(); else { stepIndex++; renderStep(); } };
    $h('#help-guide-manual').onclick = () => openManual(steps[stepIndex].section);
    guide.oncancel = event => { event.preventDefault(); closeGuide(); };
    guide.addEventListener('close', () => { ring.hidden = true; saveCompletion(); });
    $h('#help-manual-search').oninput = renderManual;
    $h('#help-manual-close').onclick = closeManual; $h('#help-manual-done').onclick = closeManual;
    manual.oncancel = event => { event.preventDefault(); closeManual(); };
    manual.addEventListener('close', positionTarget);
    $h('#help-manual-replay').onclick = () => { replayGuide().catch(error => toast(error.message || String(error))); };
    window.addEventListener('resize', () => requestAnimationFrame(positionTarget));
    document.addEventListener('scroll', () => { if (guide.open) requestAnimationFrame(positionTarget); }, true);
    new ResizeObserver(() => { if (guide.open) requestAnimationFrame(positionTarget); }).observe(document.body);
    startFirstGuide();
  }
  window.DengShellHelp = Object.freeze({ acceptConfig, openManual, replayGuide, get isTourOpen() { return !!$h('#help-guide-dialog')?.open; } });
  document.addEventListener('DOMContentLoaded', initializeHelp, { once: true });
})();
