package app

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

func utilityPrelude(p utilityPaths) string {
	return `set -eu
export LC_ALL=C
umask 077
ds_fail() { printf '\n%s\n' "$*" >&2; exit 1; }
[ "$(uname -s)" = Linux ] || ds_fail '此操作仅支持 Linux。'
[ "$(id -u)" = 0 ] || ds_fail '需要 root 权限，请使用 sudo 或 root 连接。'
ds_state=` + terminalQuote(p.state) + `
ds_swaps=` + terminalQuote(p.swaps) + `
ds_fstab=` + terminalQuote(p.fstab) + `
ds_boot=` + terminalQuote(p.boot) + `
ds_swapfile=` + terminalQuote(p.swapfile) + `
ds_fs=` + terminalQuote(p.filesystem) + `
ds_memory=` + terminalQuote(p.memory) + `
[ ! -L "$ds_state" ] || ds_fail 'DengShell 状态目录是符号链接，已停止。'
mkdir -p "$ds_state"
ds_lock="$ds_state/utility.lock"
mkdir "$ds_lock" 2>/dev/null || ds_fail '另一个常用应用正在运行；若上次异常中断，请先核实再清理 utility.lock。'
ds_cleanup() { :; }
trap 'ds_result=$?; trap - 0; ds_cleanup "$ds_result" || :; rmdir "$ds_lock" 2>/dev/null || :; exit "$ds_result"' 0
trap 'exit 130' INT
trap 'exit 143' TERM HUP
ds_require() { for ds_tool in "$@"; do command -v "$ds_tool" >/dev/null 2>&1 || ds_fail "缺少基础工具：$ds_tool。请先用发行版包管理器安装。"; done; }
ds_require awk stat cp mv cmp mktemp cat chmod
ds_backup=$(mktemp -d "$ds_state/backup-XXXXXXXX")
printf '操作备份：%s\n' "$ds_backup"
`
}

func bbrUtilityScript(mode string, p utilityPaths) string {
	buffer, queue := 8*1024*1024, 1024*1024
	switch mode {
	case "conservative":
		buffer, queue = 4*1024*1024, 256*1024
	case "aggressive":
		buffer, queue = 32*1024*1024, 4*1024*1024
	}
	return utilityPrelude(p) + fmt.Sprintf(`ds_require sysctl
ds_config=%s
ds_profile="$ds_backup/new.conf"
ds_old="$ds_backup/runtime-before.conf"
ds_committed=0
ds_applied=0
ds_tmp=''
[ ! -L "$ds_config" ] || ds_fail 'BBR 配置是符号链接，已停止。'
if [ -e "$ds_config" ]; then [ -f "$ds_config" ] || ds_fail 'BBR 配置不是普通文件。'; cp -p "$ds_config" "$ds_backup/config-before.conf"; fi
ds_available=$(sysctl -n net.ipv4.tcp_available_congestion_control)
case " $ds_available " in *' bbr '*) ;; *) command -v modprobe >/dev/null 2>&1 && modprobe tcp_bbr || ds_fail '当前内核无法加载 BBR；不会安装内核或重启。';; esac
ds_available=$(sysctl -n net.ipv4.tcp_available_congestion_control)
case " $ds_available " in *' bbr '*) ;; *) ds_fail '当前内核没有可用的 BBR。';; esac
if command -v modprobe >/dev/null 2>&1; then modprobe sch_fq 2>/dev/null || :; fi
for ds_key in net.core.default_qdisc net.ipv4.tcp_congestion_control net.ipv4.tcp_rmem net.ipv4.tcp_wmem net.ipv4.tcp_limit_output_bytes net.ipv4.tcp_moderate_rcvbuf; do
  ds_value=$(sysctl -n "$ds_key") || ds_fail "无法读取参数 $ds_key，未修改设置。"
  printf '%%s = %%s\n' "$ds_key" "$ds_value" >> "$ds_old"
done
ds_ram=$(awk '$1 == "MemTotal:" {print $2}' "$ds_memory")
case "$ds_ram" in ''|*[!0-9]*) ds_fail '无法读取真实内存容量。';; esac
ds_limit=%d
ds_cap=$((ds_ram * 1024 / 16))
[ "$ds_cap" -ge 1048576 ] || ds_cap=1048576
[ "$ds_limit" -le "$ds_cap" ] || ds_limit=$ds_cap
printf 'net.core.default_qdisc = fq\nnet.ipv4.tcp_congestion_control = bbr\nnet.ipv4.tcp_moderate_rcvbuf = 1\nnet.ipv4.tcp_limit_output_bytes = %d\n' > "$ds_profile"
for ds_key in net.ipv4.tcp_rmem net.ipv4.tcp_wmem; do
  ds_vector=$(sysctl -n "$ds_key")
  ds_line=$(printf '%%s\n' "$ds_vector" | awk -v key="$ds_key" -v cap="$ds_limit" 'NF==3 && $1 ~ /^[0-9]+$/ && $2 ~ /^[0-9]+$/ && $3 ~ /^[0-9]+$/ {if(cap<$1)cap=$1; if(cap<$2)cap=$2; printf "%%s = %%s %%s %%.0f\n",key,$1,$2,cap}')
  [ -n "$ds_line" ] || ds_fail 'TCP 缓冲参数格式异常，未修改设置。'
  printf '%%s\n' "$ds_line" >> "$ds_profile"
done
ds_cleanup() {
  if [ "$ds_committed" = 0 ] && [ "$ds_applied" = 1 ]; then
    printf '设置未完成，尝试恢复先前运行参数…\n' >&2
    sysctl -p "$ds_old" || printf '自动恢复失败，请使用备份文件手动恢复：%%s\n' "$ds_old" >&2
  fi
  if [ -n "$ds_tmp" ]; then rm -f -- "$ds_tmp"; fi
}
ds_applied=1
sysctl -p "$ds_profile" || ds_fail '内核拒绝部分参数，已停止并尝试恢复。'
mkdir -p %s
ds_tmp=$(mktemp %s)
cat "$ds_profile" > "$ds_tmp"
chmod 644 "$ds_tmp"
mv -f "$ds_tmp" "$ds_config" || ds_fail '无法保存独立持久配置，已停止并尝试恢复。'
ds_committed=1
printf '\nBBR 策略已应用。新 TCP 连接使用 BBR；未替换现有网卡队列。\n'
printf '运行参数恢复命令：sysctl -p %%s\n' "$ds_old"
printf '持久配置：%%s；重启时其他系统配置仍可能覆盖它。\n' "$ds_config"
sysctl net.ipv4.tcp_congestion_control net.ipv4.tcp_rmem net.ipv4.tcp_wmem net.ipv4.tcp_limit_output_bytes
`, terminalQuote(p.sysctlConfig), buffer, queue, terminalQuote(path.Dir(p.sysctlConfig)), terminalQuote(path.Join(path.Dir(p.sysctlConfig), ".dengshell-XXXXXXXX")))
}

func cleanUtilityScript(p utilityPaths) string {
	return utilityPrelude(p) + `ds_failed=0
if command -v apt-get >/dev/null 2>&1; then apt-get clean || ds_failed=1
elif command -v dnf >/dev/null 2>&1; then dnf clean packages || ds_failed=1
elif command -v yum >/dev/null 2>&1; then yum clean packages || ds_failed=1
elif command -v zypper >/dev/null 2>&1; then zypper --non-interactive clean --all || ds_failed=1
elif command -v apk >/dev/null 2>&1; then apk cache clean || ds_failed=1
elif command -v paccache >/dev/null 2>&1; then paccache -rk2 || ds_failed=1
else printf '未找到受支持的缓存清理工具，跳过包缓存。\n'; fi
if command -v journalctl >/dev/null 2>&1; then
  if journalctl --rotate; then journalctl --vacuum-time=14d || ds_failed=1; else ds_failed=1; fi
else printf '未使用 systemd journal，跳过归档日志。\n'; fi
[ "$ds_failed" = 0 ] || ds_fail '部分清理未完成，请查看上方包管理器或 journal 提示。'
printf '\n清理完成：未卸载软件，未删除用户、网站、数据库或 Docker 数据。\n'
`
}

func utilitySwapChecks() string {
	return `ds_require swapoff sort dirname
[ -r "$ds_swaps" ] || ds_fail '无法读取 /proc/swaps。'
[ ! -L "$ds_fstab" ] || ds_fail 'fstab 是符号链接，请由系统工具处理。'
if [ -e "$ds_fstab" ]; then [ -f "$ds_fstab" ] || ds_fail 'fstab 不是普通文件。'; fi
ds_has_swap() { awk 'NR>1 {found=1} END {exit !found}' "$ds_swaps"; }
ds_configured_swap() { [ -f "$ds_fstab" ] && awk '$1 !~ /^#/ && $3=="swap" {found=1} END {exit !found}' "$ds_fstab"; }
ds_save_fstab() {
  if [ -f "$ds_fstab" ]; then cp -p "$ds_fstab" "$ds_backup/fstab.before"; else : > "$ds_backup/fstab.absent"; : > "$ds_backup/fstab.before"; chmod 644 "$ds_backup/fstab.before"; fi
  ds_fstab_tmp=$(mktemp "$(dirname "$ds_fstab")/.dengshell-fstab-XXXXXXXX")
  cp -p "$ds_backup/fstab.before" "$ds_fstab_tmp"
}
ds_commit_fstab() {
  if [ -f "$ds_backup/fstab.absent" ]; then
    [ ! -e "$ds_fstab" ] && [ ! -L "$ds_fstab" ] || ds_fail 'fstab 在操作期间被其他程序创建，已停止。'
  else cmp -s "$ds_fstab" "$ds_backup/fstab.before" || ds_fail 'fstab 在操作期间被其他程序修改，已停止。'; fi
  mv -f "$ds_fstab_tmp" "$ds_fstab"
}
`
}

func createSwapUtilityScript(sizeMiB int64, p utilityPaths) string {
	return utilityPrelude(p) + utilitySwapChecks() + fmt.Sprintf(`ds_require swapon mkswap dd df
ds_created=0
ds_committed=0
ds_fstab_tmp=''
ds_identity=''
ds_stage=''
ds_stagefile=''
ds_modern=0
ds_map=0
ds_same_file() { [ -n "$ds_identity" ] && [ ! -L "$ds_swapfile" ] && [ -f "$ds_swapfile" ] && [ "$(stat -c '%%d:%%i' "$ds_swapfile")" = "$ds_identity" ]; }
ds_cleanup() {
  if [ "$ds_committed" = 0 ] && [ "$ds_created" = 1 ]; then
    if ! ds_same_file; then printf '目标路径身份已变化，保留当前路径，不关闭或删除其他文件：%%s\n' "$ds_swapfile" >&2; return; fi
    if awk -v name="$ds_swapfile" 'NR>1 && $1==name {found=1} END {exit !found}' "$ds_swaps"; then
      swapoff "$ds_swapfile" || { printf '无法停用新交换文件，已保留文件，请人工核实：%%s\n' "$ds_swapfile" >&2; return; }
    fi
    ds_same_file && rm -f -- "$ds_swapfile"
  fi
  if [ -n "$ds_stagefile" ] && [ -n "$ds_identity" ] && [ ! -L "$ds_stagefile" ] && [ -f "$ds_stagefile" ] && [ "$(stat -c '%%d:%%i' "$ds_stagefile")" = "$ds_identity" ]; then rm -f -- "$ds_stagefile"; fi
  if [ -n "$ds_stage" ]; then rmdir "$ds_stage" 2>/dev/null || :; fi
  if [ -n "$ds_fstab_tmp" ]; then rm -f -- "$ds_fstab_tmp"; fi
}
if ds_has_swap || ds_configured_swap; then ds_fail '需要先删除当前 SWAP 并重启之后才能正确设置；未执行创建。'; fi
ds_current_boot=$(cat "$ds_boot" 2>/dev/null || :)
ds_removed_boot=$(cat "$ds_state/swap-removed-boot-id" 2>/dev/null || :)
if [ -n "$ds_removed_boot" ] && { [ -z "$ds_current_boot" ] || [ "$ds_current_boot" = "$ds_removed_boot" ]; }; then ds_fail '本次开机已关闭/删除过 SWAP，请先重启再设置；不会自动重启。'; fi
[ ! -e "$ds_swapfile" ] && [ ! -L "$ds_swapfile" ] || ds_fail '目标路径已存在，绝不覆盖。'
ds_filesystem=$(stat -f -c %%T "$ds_fs")
case "$ds_filesystem" in
  ext2/ext3|ext2|ext3|ext4|xfs) ;;
  f2fs) ds_require chattr lsattr ;;
  btrfs)
    ds_require btrfs ln
    if btrfs filesystem mkswapfile --help >/dev/null 2>&1; then ds_modern=1; else ds_require chattr lsattr fallocate; fi
    if btrfs inspect-internal map-swapfile --help >/dev/null 2>&1; then ds_map=1; fi
    # Older tools cannot inspect swap extents. Only accept one device and a
    # single data profile in that case; metadata DUP is harmless.
    if [ "$ds_map" = 0 ]; then
      ds_devices=$(btrfs filesystem show --raw "$ds_fs" | awk '/Total devices/ {for(i=1;i<=NF;i++)if($i=="devices")print $(i+1)}')
      [ "$ds_devices" = 1 ] || ds_fail '旧版 Btrfs 工具仅支持单设备文件系统；请升级 btrfs-progs 后检查交换区段。'
      ds_profiles=$(btrfs filesystem df "$ds_fs" | awk '/^Data,/ {print $2}')
      [ "$ds_profiles" = single: ] || ds_fail '旧版 Btrfs 工具仅支持 single 数据配置，未创建文件。'
    fi
    printf 'Btrfs：会验证 NOCOW、无空洞及交换区段；活动交换文件所在子卷无法创建快照。\n'
    ;;
  *) ds_fail "当前文件系统 $ds_filesystem 没有受支持的自动交换文件方案。";;
esac
ds_size=%d
ds_free=$(df -Pk "$ds_fs" | awk 'NR==2 {print $4}')
case "$ds_free" in ''|*[!0-9]*) ds_fail '无法可靠读取可用磁盘空间。';; esac
[ "$ds_free" -ge "$((ds_size * 1024 + 262144))" ] || ds_fail '可用空间不足；需额外保留 256 MiB。'
ds_save_fstab
if [ "$ds_modern" = 1 ]; then
  # The Btrfs utility creates its own O_EXCL file. Use a private directory,
  # verify its result, then publish by a no-replace hard link on the same FS.
  ds_stage=$(mktemp -d "$(dirname "$ds_swapfile")/.dengshell-swap-XXXXXXXX")
  ds_stagefile="$ds_stage/swapfile"
  ds_create_result=0
  btrfs filesystem mkswapfile -s "$((ds_size * 1048576))" "$ds_stagefile" || ds_create_result=$?
  [ ! -L "$ds_stagefile" ] && [ -f "$ds_stagefile" ] || ds_fail 'Btrfs 未创建有效交换文件。'
  ds_identity=$(stat -c '%%d:%%i' "$ds_stagefile")
  [ "$ds_create_result" = 0 ] || ds_fail 'Btrfs 专用交换文件创建失败。'
  ds_require blkid lsattr
  [ "$(blkid -p -s TYPE -o value "$ds_stagefile" 2>/dev/null || :)" = swap ] || ds_fail 'Btrfs 交换文件未生成有效交换签名。'
  ln -- "$ds_stagefile" "$ds_swapfile" || ds_fail '无法发布交换文件；目标可能已存在，不会覆盖。'
  ds_created=1
  ds_same_file || ds_fail '交换文件发布时身份发生变化。'
  rm -- "$ds_stagefile"
  ds_stagefile=''
  rmdir "$ds_stage"
  ds_stage=''
else
  set -C
  if exec 3> "$ds_swapfile"; then :; else set +C; ds_fail '无法独占创建交换文件。'; fi
  set +C
  ds_created=1
  ds_identity=$(stat -Lc '%%d:%%i' /proc/self/fd/3) || ds_fail '无法核对独占文件句柄，停止创建并保留路径供核查。'
  ds_same_file || ds_fail '交换文件路径在创建后被替换，已停止。'
  if [ "$ds_filesystem" = btrfs ]; then
    chattr +C "$ds_swapfile" || ds_fail '无法在空文件上启用 NOCOW，未分配交换空间。'
    ds_flags=$(lsattr -d "$ds_swapfile" | awk '{print $1}')
    case "$ds_flags" in *C*) ;; *) ds_fail '无法确认 NOCOW 属性，未分配交换空间。';; esac
    ds_same_file || ds_fail '交换文件属性设置期间路径已变化。'
    fallocate -l "$((ds_size * 1048576))" /proc/self/fd/3 || ds_fail 'Btrfs 预分配失败。'
  else
    if [ "$ds_filesystem" = f2fs ]; then
      chattr -c "$ds_swapfile" || ds_fail 'F2FS 无法关闭新交换文件的压缩属性。'
      ds_flags=$(lsattr -d "$ds_swapfile" | awk '{print $1}')
      case "$ds_flags" in ''|*c*) ds_fail 'F2FS 文件仍启用压缩或属性不可读，未分配空间。';; esac
    fi
    ds_same_file || ds_fail '交换文件属性设置期间路径已变化。'
    printf '正在实际分配 %%s MiB 交换文件…\n' "$ds_size"
    dd if=/dev/zero bs=1048576 count="$ds_size" conv=fsync >&3 || ds_fail '实际分配失败，未启用交换文件。'
  fi
  ds_same_file || ds_fail '交换文件路径在分配期间被替换，已停止。'
fi
if [ "$ds_filesystem" = btrfs ]; then
  ds_flags=$(lsattr -d "$ds_swapfile" | awk '{print $1}')
  case "$ds_flags" in *c*) ds_fail 'Btrfs 交换文件仍有压缩属性。';; *C*) ;; *) ds_fail '无法确认 Btrfs NOCOW 属性。';; esac
  if [ "$ds_map" = 1 ]; then
    btrfs inspect-internal map-swapfile "$ds_swapfile" || ds_fail 'Btrfs 区段不符合交换要求（单设备、无空洞等），未启用。'
  fi
fi
[ ! -L "$ds_swapfile" ] && [ -f "$ds_swapfile" ] && [ "$(stat -c %%h "$ds_swapfile")" = 1 ] || ds_fail '交换文件类型或链接数变化，已停止。'
[ "$(stat -c %%s "$ds_swapfile")" -eq "$((ds_size * 1048576))" ] || ds_fail '交换文件大小不正确。'
ds_blocks=$(stat -c %%b "$ds_swapfile")
[ "$((ds_blocks * 512))" -ge "$((ds_size * 1048576))" ] || ds_fail '交换文件包含未分配区域，已停止。'
if ds_has_swap || ds_configured_swap; then ds_fail '分配期间其他程序添加了 SWAP 或开机配置，已停止启用新文件。'; fi
ds_same_file || ds_fail '交换文件路径已变化，未初始化。'
if [ "$ds_modern" = 0 ]; then mkswap "$ds_swapfile" || ds_fail 'mkswap 失败，未启用交换文件。'; fi
ds_same_file || ds_fail '交换文件路径已变化，未启用。'
swapon "$ds_swapfile" || ds_fail '内核拒绝启用交换文件（可能是内核、文件系统模式或区段限制），未写入开机配置；会清理本次文件。'
printf '\n%%s none swap sw 0 0\n' "$ds_swapfile" >> "$ds_fstab_tmp"
ds_commit_fstab
ds_committed=1
printf '\nSWAP 已创建并启用：%%s MiB；路径：%%s\n' "$ds_size" "$ds_swapfile"
cat "$ds_swaps"
`, sizeMiB)
}

func removeSwapUtilityScript(entries []UtilitySwap, p utilityPaths, expectedConfig ...string) string {
	var snapshot []string
	var before, disable, remove strings.Builder
	for index, entry := range entries {
		if entry.Note != "" {
			fmt.Fprintf(&before, "printf '%%s\\n' %s\n", terminalQuote(entry.Path+"："+entry.Note))
		}
		if !entry.Active {
			if entry.Type == "partition" || entry.Type == "zram" {
				continue
			}
			if path.IsAbs(entry.Path) && path.Clean(entry.Path) == entry.Path && entry.Path != "/" && !strings.ContainsAny(entry.Path, "\x00\r\n") {
				q := terminalQuote(entry.Path)
				fmt.Fprintf(&before, "ds_remove_%d=0\nif [ ! -L %s ] && [ -f %s ] && [ \"$(stat -c %%h %s)\" = 1 ]; then\n  if command -v blkid >/dev/null 2>&1 && [ \"$(blkid -p -s TYPE -o value %s 2>/dev/null || :)\" = swap ]; then\n    ds_identity_%d=$(stat -c '%%d:%%i' %s); ds_remove_%d=1\n  else printf '保留未能确认交换签名的文件，请人工检查：%%s\\n' %s; fi\nfi\n", index, q, q, q, q, index, q, index, q)
				fmt.Fprintf(&remove, "if [ \"$ds_remove_%d\" = 1 ]; then\n  [ ! -L %s ] && [ -f %s ] && [ \"$(stat -c '%%d:%%i' %s)\" = \"$ds_identity_%d\" ] || ds_fail '停用交换文件的路径发生变化，未删除。'\n  rm -f -- %s\nfi\n", index, q, q, q, index, q)
			}
			continue
		}
		snapshot = append(snapshot, entry.token+" "+entry.procType)
		q := terminalQuote(entry.Path)
		if entry.Type == "file" {
			fmt.Fprintf(&before, "[ ! -L %s ] && [ -f %s ] && [ \"$(stat -c %%h %s)\" = 1 ] || ds_fail '交换文件类型或链接数异常，未删除。'\nds_identity_%d=$(stat -c '%%d:%%i' %s)\n", q, q, q, index, q)
			fmt.Fprintf(&remove, "[ ! -L %s ] && [ -f %s ] && [ \"$(stat -c '%%d:%%i' %s)\" = \"$ds_identity_%d\" ] || ds_fail '交换文件路径在关闭后发生变化，未删除。'\nrm -f -- %s\n", q, q, q, index, q)
		}
		fmt.Fprintf(&disable, "printf '关闭交换区：%%s\\n' %s\nswapoff %s || ds_fail 'swapoff 失败（可能内存不足），已停止。没有删除任何交换文件或修改 fstab；前面已关闭的交换区可按需手动重新启用。'\n", q, q)
	}
	sort.Strings(snapshot)
	expected := strings.Join(snapshot, "\n")
	if expected != "" {
		expected += "\n"
	}
	configCheck := ""
	if len(expectedConfig) > 0 {
		tokens := strings.Fields(expectedConfig[0])
		sort.Strings(tokens)
		expected := strings.Join(tokens, "\n")
		if expected != "" {
			expected += "\n"
		}
		configCheck = "printf '%s' " + terminalQuote(expected) + " > \"$ds_backup/expected-fstab-swaps\"\n" + `awk '$1 !~ /^#/ && $3=="swap" {print $1}' "$ds_backup/fstab.before" | sort > "$ds_backup/current-fstab-swaps"
cmp -s "$ds_backup/expected-fstab-swaps" "$ds_backup/current-fstab-swaps" || ds_fail '开机交换配置在检查后发生变化，请重新检查；未关闭交换区。'
`
	}
	return utilityPrelude(p) + utilitySwapChecks() + `ds_fstab_tmp=''
ds_cleanup() { if [ -n "$ds_fstab_tmp" ]; then rm -f -- "$ds_fstab_tmp"; fi; }
printf '%s' ` + terminalQuote(expected) + ` > "$ds_backup/expected-swaps"
awk 'NR>1 {print $1 " " $2}' "$ds_swaps" | sort > "$ds_backup/current-swaps"
cmp -s "$ds_backup/expected-swaps" "$ds_backup/current-swaps" || ds_fail 'SWAP 在检查后发生变化，请重新打开常用应用检查，未关闭任何交换区。'
ds_save_fstab
` + configCheck + before.String() + disable.String() + `if ds_has_swap; then ds_fail '仍有活动交换区或服务重新启用了 SWAP，未修改 fstab 或删除文件。'; fi
ds_current_boot=$(cat "$ds_boot" 2>/dev/null || :)
[ -n "$ds_current_boot" ] || ds_current_boot=unknown-boot
printf '%s\n' "$ds_current_boot" > "$ds_state/swap-removed-boot-id"
awk '!($1 !~ /^#/ && $3=="swap") {print}' "$ds_backup/fstab.before" > "$ds_fstab_tmp"
ds_commit_fstab
` + remove.String() + `printf '\nSWAP 已关闭；交换分区/ZRAM 设备不会删除。重新设置前请先重启。\n'
printf '若系统的 ZRAM 或自定义服务会自动创建交换区，还需在对应服务中调整；本工具不会自动重启。\n'
cat "$ds_swaps"
`
}
