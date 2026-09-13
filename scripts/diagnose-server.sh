#!/bin/sh
# DengShell server diagnostics. POSIX sh; no installs, sysctl writes or cleanup utilities.
# Generated read-only application probes are appended below; reports omit raw probe stdout.
set -u
umask 077
export LC_ALL=C
DS_VERSION='1.0 (DengShell r20/r21 probe snapshot)'
if [ "${1-}" = --help ]; then
    printf '%s\n' 'Usage: sh DengShell-diagnose.sh [existing-output-directory]' 'Run on the affected Linux VPS as the SAME account used by DengShell. No sudo needed.'
    exit 0
fi
for ds_tool in mktemp cat rm sed grep tr head wc date timeout sh; do
    if ! command -v "$ds_tool" >/dev/null 2>&1; then
        printf 'Missing prerequisite: %s. No diagnostic commands were run.\n' "$ds_tool" >&2
        printf '%s\n' 'A working timeout command is required so a stuck df or shell cannot hang this script.' >&2
        exit 2
    fi
done
DS_SH=/bin/sh
[ -x "$DS_SH" ] || DS_SH=$(command -v sh)
timeout -k 1 2 "$DS_SH" -c : >/dev/null 2>&1 || { printf '%s\n' 'timeout -k is unavailable; stopped before running probes.' >&2; exit 2; }
directory=${1:-.}
case "$directory" in -*) printf '%s\n' 'Use ./ before a relative output directory starting with -.' >&2; exit 2;; esac
[ -d "$directory" ] || { printf '%s\n' 'Output directory does not exist.' >&2; exit 2; }
report_dir=$(mktemp -d "$directory/DengShell-diagnose.XXXXXX") || exit 2
work=$(mktemp -d "$report_dir/.work.XXXXXX") || exit 2
report=$report_dir/report.txt
summary=$report_dir/summary.txt
cleanup() {
    case "$work" in "$report_dir"/.work.*) rm -rf -- "$work";; esac
}
trap cleanup 0
trap 'exit 130' INT
trap 'exit 143' TERM HUP
: > "$summary"
exec 3>&1
exec > "$report" 2>&1
printf 'DengShell read-only diagnosis %s\n' "$DS_VERSION"
printf 'Time (UTC): '; date -u '+%Y-%m-%dT%H:%M:%SZ'
printf '%s\n' 'Run as the same VPS account as DengShell. This does NOT test the Windows GUI or the existing SSH transport.'
printf '%s\n' 'Raw probe stdout is temporary and removed. No private keys, passwords, command history, environment dumps or full process command lines are collected.'

finding() { printf '%s\n' "$*" >> "$summary"; }
clean_error() {
    # Best-effort redaction, not a claim of complete anonymization. No stdout dumps.
    head -c 1536 "$1" | tr -cd '\11\12\15\40-\176' |
        sed -E 's/([0-9]{1,3}\.){3}[0-9]{1,3}/[IPv4]/g; s/([[:xdigit:]]{0,4}:){2,}[[:xdigit:]:.%a-zA-Z0-9]*/[IPv6]/g' | head -n 12
}
now_ms() {
    ds_now=$(date '+%s%N' 2>/dev/null)
    # Some BusyBox builds silently omit %N instead of printing a literal N.
    case "$ds_now" in ''|*[!0-9]*) ds_now='';; esac
    if [ "${#ds_now}" -ge 18 ]; then printf '%s\n' "$((ds_now / 1000000))"
    else ds_now=$(date '+%s'); printf '%s\n' "$((ds_now * 1000))"; fi
}
run_probe() {
    ds_id=$1; ds_budget=$2; ds_shell=$3; ds_script=$4
    printf '\n--- %s (budget %ss) ---\n' "$ds_id" "$ds_budget"
    ds_start=$(now_ms)
    # Only this subshell has a file-size limit; the VPS shell remains unchanged.
    (ulimit -f 8192 2>/dev/null || :; timeout -k 2 "$ds_budget" "$ds_shell" -c "$(cat "$ds_script")" < /dev/null) > "$work/$ds_id.out" 2> "$work/$ds_id.err"
    ds_rc=$?; ds_elapsed=$(( $(now_ms) - ds_start ))
    printf '%s\n' "$ds_rc" > "$work/$ds_id.rc"
    printf '%s\n' "$ds_elapsed" > "$work/$ds_id.ms"
    ds_timed_out=0
    # GNU timeout uses 124; BusyBox can return the terminating signal (143).
    # Require an elapsed deadline for signal exits, not just an arbitrary 143.
    case "$ds_rc" in
      124) ds_timed_out=1;;
      137|143) [ "$ds_elapsed" -lt "$((ds_budget * 1000 - 1000))" ] || ds_timed_out=1;;
    esac
    printf '%s\n' "$ds_timed_out" > "$work/$ds_id.timeout"
    ds_bytes=$(wc -c < "$work/$ds_id.out" | tr -d ' ')
    ds_errors=$(wc -c < "$work/$ds_id.err" | tr -d ' ')
    printf 'exit=%s elapsed_ms=%s stdout_bytes=%s stderr_bytes=%s\n' "$ds_rc" "$ds_elapsed" "$ds_bytes" "$ds_errors"
    if [ "$ds_errors" -gt 0 ]; then printf 'stderr excerpt (ASCII, address-redacted):\n'; clean_error "$work/$ds_id.err"; fi
    if [ "$ds_timed_out" = 1 ]; then
        finding "[TIMEOUT] $ds_id 超过检查时限。应用的系统信息/常用应用检查总时限约 12 秒，还包含 SSH 往返时间。"
    elif [ "$ds_rc" != 0 ]; then finding "[COMMAND] $ds_id 退出码为 $ds_rc，请查看报告中该项的错误摘要。"; fi
    [ "$ds_bytes" -le 4194304 ] || finding "[OUTPUT] $ds_id 输出超过应用监控的 4 MiB 上限。"
}
marker() { grep -q -x "$2" "$work/$1.out"; }
section_has_data() {
    # Independent of awk, with a fixed number of processes even on large hosts.
    tr -d '\r' < "$work/$1.out" |
        sed 's/^[[:space:]]*//;s/[[:space:]]*$//' |
        sed -n "/^$2\$/,/^__[A-Z][A-Z]_.*__\$/p" |
        grep -E -v '^__|^[[:space:]]*$' >/dev/null
}
assess() {
    ds_id=$1; ds_kind=$2
    case "$ds_kind" in
      utility)
        if marker "$ds_id" __DS_END__; then
            printf 'utility_end_marker=exact\n'
        elif tr -d '\r' < "$work/$ds_id.out" | grep -q -x '__DS_END__'; then
            printf 'utility_end_marker=CRLF_only\n'
            finding "[FORMAT] $ds_id 的结束标记带 CR 回车。r20/r21 常用应用解析未去掉该字符，可导致截图中的检查不完整。"
        else
            printf 'utility_end_marker=MISSING\n'
            finding "[INCOMPLETE] $ds_id 没有输出准确的 __DS_END__，已复现红色提示的触发条件。重点检查 Shell、启动脚本拦截、超时及输出截断。"
        fi
        ds_total=$(( $(wc -c < "$work/$ds_id.out") + $(wc -c < "$work/$ds_id.err") ))
        [ "$ds_total" -le 131072 ] || finding "[OUTPUT] $ds_id 的标准输出和错误合计超过常用应用检查的 128 KiB 上限，启动横幅/错误可能导致结束标记被截断。"
        for ds_section in OS UID TOOLS; do
            if section_has_data "$ds_id" "__DS_${ds_section}__"; then printf '%s=present\n' "$ds_section"; else printf '%s=empty\n' "$ds_section"; fi
        done;;
      core|full)
        for ds_section in STAT MEM UPTIME LOAD NET; do
            if section_has_data "$ds_id" "__CS_${ds_section}__"; then printf '%s=present\n' "$ds_section"; else printf '%s=EMPTY\n' "$ds_section"; finding "[PROC] $ds_id 没有可用的 $ds_section 数据段，系统信息可能显示为空。"; fi
        done
        if marker "$ds_id" __CS_END__; then printf 'monitor_end_marker=present\n'; else printf 'monitor_end_marker=missing\n'; fi;;
      network)
        for ds_section in UPTIME NET; do
            if section_has_data "$ds_id" "__CS_${ds_section}__"; then printf '%s=present\n' "$ds_section"; else printf '%s=EMPTY\n' "$ds_section"; fi
        done;;
      static)
        for ds_section in OS CPUINFO DF; do
            if section_has_data "$ds_id" "__CS_${ds_section}__"; then printf '%s=present\n' "$ds_section"; else printf '%s=EMPTY\n' "$ds_section"; finding "[STATIC] $ds_id 缺少 $ds_section 静态信息，检查对应命令、读取权限或挂载点。"; fi
        done;;
      process)
        ds_process_count=$(grep -c '^P' "$work/$ds_id.out" || :)
        printf 'process_record_count=%s\n' "$ds_process_count"
        [ "$ds_process_count" -gt 0 ] || finding "[PROCESS] $ds_id 没有可用进程记录，请检查 /proc 可见性与进程采样错误。"
        if grep -q '^M' "$work/$ds_id.out"; then printf 'process_metadata=present\n'; else printf 'process_metadata=missing\n'; finding "[PROCESS] $ds_id 未输出进程元数据，请检查 awk/getconf 和 /proc 读取权限。"; fi
        if grep -q '^S[[:space:]]' "$work/$ds_id.out"; then printf 'process_summary=present\n'; else printf 'process_summary=missing\n'; finding "[PROCESS] $ds_id 缺少进程采样汇总，即使已有部分 P 记录也不是完整采样。"; fi;;
    esac
}

# Exact read-only commands exported from DengShell source, not utility action scripts.
cat > "$work/core.sh" <<'DENGSHELL_DIAG_CORE_PROBE'
export LC_ALL=C

printf '\n__CS_BLOCKS__\n'
for dengshell_block in /sys/block/*; do
    [ -e "$dengshell_block" ] || continue
    dengshell_layer=leaf
    for dengshell_slave in "$dengshell_block"/slaves/*; do
        if [ -e "$dengshell_slave" ]; then dengshell_layer=stacked; break; fi
    done
    printf '%s %s\n' "${dengshell_block##*/}" "$dengshell_layer"
done
awk '
function emit(section, path, line) {
    printf "\n__CS_%s__\n", section
    while ((getline line < path) > 0) {
        if (section == "STAT" && line !~ /^cpu /) continue
        if (section == "DISKIO") { split(line, parts, " "); if (parts[3] ~ /^(loop|ram|dm-)/) continue }
        print line
        if (section == "STAT") break
    }
    close(path)
}
BEGIN {
    emit("STAT", "/proc/stat")
    emit("MEM", "/proc/meminfo")
    emit("UPTIME", "/proc/uptime")
    emit("LOAD", "/proc/loadavg")
    emit("NET", "/proc/net/dev")
    emit("DISKIO", "/proc/diskstats")
}' 2>/dev/null

printf '\n__CS_END__\n'

DENGSHELL_DIAG_CORE_PROBE
cat > "$work/full.sh" <<'DENGSHELL_DIAG_FULL_PROBE'
export LC_ALL=C

awk '
BEGIN {
    print "\n__CS_OS__"
    while ((getline line < "/etc/os-release") > 0) print line
    close("/etc/os-release")
    print "\n__CS_CPUINFO__"
    while ((getline line < "/proc/cpuinfo") > 0) {
        if (line ~ /^processor[ \t]*:/) print line
        if (!model && line ~ /^(model name|Hardware)[ \t]*:/) { print line; model = 1 }
    }
    close("/proc/cpuinfo")
}' 2>/dev/null
printf '\n__CS_LINK__\n'; ip -o link show 2>/dev/null
printf '\n__CS_ADDR__\n'; ip -o addr show 2>/dev/null
printf '\n__CS_ROUTES__\n'; ip -4 route show default 2>/dev/null; ip -6 route show default 2>/dev/null
printf '\n__CS_CONNECTION__\n'; printf '%s\n' "$SSH_CONNECTION"
printf '\n__CS_DF__\n'; df -Pk 2>/dev/null

printf '\n__CS_BLOCKS__\n'
for dengshell_block in /sys/block/*; do
    [ -e "$dengshell_block" ] || continue
    dengshell_layer=leaf
    for dengshell_slave in "$dengshell_block"/slaves/*; do
        if [ -e "$dengshell_slave" ]; then dengshell_layer=stacked; break; fi
    done
    printf '%s %s\n' "${dengshell_block##*/}" "$dengshell_layer"
done
awk '
function emit(section, path, line) {
    printf "\n__CS_%s__\n", section
    while ((getline line < path) > 0) {
        if (section == "STAT" && line !~ /^cpu /) continue
        if (section == "DISKIO") { split(line, parts, " "); if (parts[3] ~ /^(loop|ram|dm-)/) continue }
        print line
        if (section == "STAT") break
    }
    close(path)
}
BEGIN {
    emit("STAT", "/proc/stat")
    emit("MEM", "/proc/meminfo")
    emit("UPTIME", "/proc/uptime")
    emit("LOAD", "/proc/loadavg")
    emit("NET", "/proc/net/dev")
    emit("DISKIO", "/proc/diskstats")
}' 2>/dev/null

printf '\n__CS_PROCESS__\n'
dengshell_hz=$(getconf CLK_TCK 2>/dev/null)
dengshell_pagesize=$(getconf PAGESIZE 2>/dev/null)
for dengshell_proc in /proc/[0-9]*/stat; do printf '%s\n' "$dengshell_proc"; done |
awk -v hz="$dengshell_hz" -v pages="$dengshell_pagesize" '
function now( text, parts, value) {
    value = -1
    if ((getline text < "/proc/uptime") > 0) {
        split(text, parts, " ")
        value = parts[1]
    }
    close("/proc/uptime")
    return value
}
function readstat(path, text, line) {
    text = ""
    while ((getline line < path) > 0) {
        if (text != "") text = text "\n"
        text = text line
    }
    close(path)
    return text
}
function fields(text, out, left, right, i, tail) {
    split("", out)
    left = index(text, "(")
    right = 0
    for (i = length(text); i > left; i--) {
        if (substr(text, i, 1) == ")") { right = i; break }
    }
    if (left < 2 || right <= left) return 0
    tail = substr(text, right + 2)
    if (split(tail, out, " ") < 22) return 0
    if (out[12] !~ /^[0-9]+$/ || out[13] !~ /^[0-9]+$/ || out[20] !~ /^[0-9]+$/) return 0
    out[0] = substr(text, left + 1, right - left - 1)
    return 1
}
function encode(text, i, result) {
    result = ""
    for (i = 1; i <= length(text); i++) result = result sprintf("%02x", ord[substr(text, i, 1)])
    return result
}
BEGIN {
    for (i = 1; i < 256; i++) ord[sprintf("%c", i)] = i
    boot = ""
    getline boot < "/proc/sys/kernel/random/boot_id"
    close("/proc/sys/kernel/random/boot_id")
    if (boot == "") {
        while ((getline line < "/proc/stat") > 0) {
            if (line ~ /^btime /) { boot = line; break }
        }
        close("/proc/stat")
    }
    start = now()
    printf "M\t%s\t%s\t%s\t%s\n", hz, pages, boot, start
}
/^\/proc\/[0-9]+\/stat$/ {
    path = $0
    split(path, components, "/")
    pid = components[3]
    visible++
    if (!fields(readstat(path), first)) {
        unavailable++
        printf "U\t%s\n", pid
        next
    }
    memory = first[22]
    source = "stat"
    rollup = "/proc/" pid "/smaps_rollup"
    while ((getline line < rollup) > 0) {
        if (line ~ /^Rss:[ \t]+[0-9]+[ \t]+kB/) {
            split(line, values, " ")
            memory = sprintf("%.0f", values[2] * 1024)
            source = "smaps_rollup"
            break
        }
    }
    close(rollup)
    if (source == "stat") {
        status = "/proc/" pid "/status"
        while ((getline line < status) > 0) {
            if (line ~ /^VmRSS:[ \t]+[0-9]+[ \t]+kB/) {
                split(line, values, " ")
                memory = sprintf("%.0f", values[2] * 1024)
                source = "status"
                break
            }
        }
        close(status)
    }
    if (!fields(readstat(path), last) || first[20] != last[20]) {
        unavailable++
        printf "U\t%s\n", pid
        next
    }
    if (source == "stat") memory = last[22]
    readable++
    printf "P\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", pid, last[12], last[13], last[20], now(), memory, source, last[7], last[1], encode(last[0])
}
END {
    printf "S\t%d\t%d\t%d\t%s\n", visible, readable, unavailable, now()
}
' 2>/dev/null

printf '\n__CS_END__\n'

DENGSHELL_DIAG_FULL_PROBE
cat > "$work/network.sh" <<'DENGSHELL_DIAG_NETWORK_PROBE'
export LC_ALL=C
awk 'BEGIN {
    if ((getline stamp < "/proc/uptime") <= 0) exit 1
    close("/proc/uptime")
    print "__CS_UPTIME__"; print stamp
    print "__CS_NET__"
    while ((getline line < "/proc/net/dev") > 0) print line
    close("/proc/net/dev")
}' 2>/dev/null

DENGSHELL_DIAG_NETWORK_PROBE
cat > "$work/process.sh" <<'DENGSHELL_DIAG_PROCESS_PROBE'
export LC_ALL=C

printf '\n__CS_PROCESS__\n'
dengshell_hz=$(getconf CLK_TCK 2>/dev/null)
dengshell_pagesize=$(getconf PAGESIZE 2>/dev/null)
for dengshell_proc in /proc/[0-9]*/stat; do printf '%s\n' "$dengshell_proc"; done |
awk -v hz="$dengshell_hz" -v pages="$dengshell_pagesize" '
function now( text, parts, value) {
    value = -1
    if ((getline text < "/proc/uptime") > 0) {
        split(text, parts, " ")
        value = parts[1]
    }
    close("/proc/uptime")
    return value
}
function readstat(path, text, line) {
    text = ""
    while ((getline line < path) > 0) {
        if (text != "") text = text "\n"
        text = text line
    }
    close(path)
    return text
}
function fields(text, out, left, right, i, tail) {
    split("", out)
    left = index(text, "(")
    right = 0
    for (i = length(text); i > left; i--) {
        if (substr(text, i, 1) == ")") { right = i; break }
    }
    if (left < 2 || right <= left) return 0
    tail = substr(text, right + 2)
    if (split(tail, out, " ") < 22) return 0
    if (out[12] !~ /^[0-9]+$/ || out[13] !~ /^[0-9]+$/ || out[20] !~ /^[0-9]+$/) return 0
    out[0] = substr(text, left + 1, right - left - 1)
    return 1
}
function encode(text, i, result) {
    result = ""
    for (i = 1; i <= length(text); i++) result = result sprintf("%02x", ord[substr(text, i, 1)])
    return result
}
BEGIN {
    for (i = 1; i < 256; i++) ord[sprintf("%c", i)] = i
    boot = ""
    getline boot < "/proc/sys/kernel/random/boot_id"
    close("/proc/sys/kernel/random/boot_id")
    if (boot == "") {
        while ((getline line < "/proc/stat") > 0) {
            if (line ~ /^btime /) { boot = line; break }
        }
        close("/proc/stat")
    }
    start = now()
    printf "M\t%s\t%s\t%s\t%s\n", hz, pages, boot, start
}
/^\/proc\/[0-9]+\/stat$/ {
    path = $0
    split(path, components, "/")
    pid = components[3]
    visible++
    if (!fields(readstat(path), first)) {
        unavailable++
        printf "U\t%s\n", pid
        next
    }
    memory = first[22]
    source = "stat"
    rollup = "/proc/" pid "/smaps_rollup"
    while ((getline line < rollup) > 0) {
        if (line ~ /^Rss:[ \t]+[0-9]+[ \t]+kB/) {
            split(line, values, " ")
            memory = sprintf("%.0f", values[2] * 1024)
            source = "smaps_rollup"
            break
        }
    }
    close(rollup)
    if (source == "stat") {
        status = "/proc/" pid "/status"
        while ((getline line < status) > 0) {
            if (line ~ /^VmRSS:[ \t]+[0-9]+[ \t]+kB/) {
                split(line, values, " ")
                memory = sprintf("%.0f", values[2] * 1024)
                source = "status"
                break
            }
        }
        close(status)
    }
    if (!fields(readstat(path), last) || first[20] != last[20]) {
        unavailable++
        printf "U\t%s\n", pid
        next
    }
    if (source == "stat") memory = last[22]
    readable++
    printf "P\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", pid, last[12], last[13], last[20], now(), memory, source, last[7], last[1], encode(last[0])
}
END {
    printf "S\t%d\t%d\t%d\t%s\n", visible, readable, unavailable, now()
}
' 2>/dev/null

DENGSHELL_DIAG_PROCESS_PROBE
cat > "$work/static.sh" <<'DENGSHELL_DIAG_STATIC_PROBE'
export LC_ALL=C

awk '
BEGIN {
    print "\n__CS_OS__"
    while ((getline line < "/etc/os-release") > 0) print line
    close("/etc/os-release")
    print "\n__CS_CPUINFO__"
    while ((getline line < "/proc/cpuinfo") > 0) {
        if (line ~ /^processor[ \t]*:/) print line
        if (!model && line ~ /^(model name|Hardware)[ \t]*:/) { print line; model = 1 }
    }
    close("/proc/cpuinfo")
}' 2>/dev/null
printf '\n__CS_LINK__\n'; ip -o link show 2>/dev/null
printf '\n__CS_ADDR__\n'; ip -o addr show 2>/dev/null
printf '\n__CS_ROUTES__\n'; ip -4 route show default 2>/dev/null; ip -6 route show default 2>/dev/null
printf '\n__CS_CONNECTION__\n'; printf '%s\n' "$SSH_CONNECTION"
printf '\n__CS_DF__\n'; df -Pk 2>/dev/null

DENGSHELL_DIAG_STATIC_PROBE
cat > "$work/utility.sh" <<'DENGSHELL_DIAG_UTILITY_PROBE'
export LC_ALL=C
printf '\n__DS_OS__\n'; uname -s
printf '\n__DS_UID__\n'; id -u
printf '\n__DS_TOOLS__\n'; for ds_tool in sudo base64 sysctl modprobe modinfo swapon swapoff mkswap blkid findfs readlink btrfs fallocate chattr lsattr ln dd stat df awk sort cmp cp mv mkdir rm rmdir mktemp chmod cat date apt-get dnf yum zypper apk paccache journalctl; do command -v "$ds_tool" >/dev/null 2>&1 && printf '%s\n' "$ds_tool"; done
printf '\n__DS_BBR__\n'; sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null || :
printf '\n__DS_BBR_MODULE__\n'; modinfo -n tcp_bbr 2>/dev/null || :
printf '\n__DS_SWAPS__\n'; cat '/proc/swaps' 2>/dev/null || :
printf '\n__DS_FSTAB__\n'; if [ -r '/etc/fstab' ]; then awk '$1 !~ /^#/ && $3 == "swap" {print $1}' '/etc/fstab'; fi
printf '\n__DS_FS__\n'; stat -f -c %T '/' 2>/dev/null || :
printf '\n__DS_BTRFS_CREATE__\n'; if command -v btrfs >/dev/null 2>&1 && btrfs filesystem mkswapfile --help >/dev/null 2>&1; then printf 'yes\n'; fi
printf '\n__DS_BTRFS_MAP__\n'; if command -v btrfs >/dev/null 2>&1 && btrfs inspect-internal map-swapfile --help >/dev/null 2>&1; then printf 'yes\n'; fi
printf '\n__DS_FREE__\n'; df -Pk '/' 2>/dev/null | awk 'NR==2 {print $4}'
printf '\n__DS_EXISTS__\n'; if [ -e '/swapfile.dengshell' ] || [ -L '/swapfile.dengshell' ]; then printf 'yes\n'; fi
printf '\n__DS_BOOT__\n'; cat '/proc/sys/kernel/random/boot_id' 2>/dev/null || :
printf '\n__DS_REMOVED_BOOT__\n'; cat '/var/lib/dengshell/swap-removed-boot-id' 2>/dev/null || :
printf '\n__DS_END__\n'

DENGSHELL_DIAG_UTILITY_PROBE

printf '\n=== Environment / prerequisites ===\n'
printf 'Kernel: '; uname -srm 2>/dev/null || :
grep -E '^(ID|VERSION_ID|PRETTY_NAME)=' /etc/os-release 2>/dev/null || :
printf 'UID: '; id -u 2>/dev/null || :
ds_declared=${SHELL:-unknown}
printf 'Declared shell basename: %s\n' "${ds_declared##*/}"
printf 'SSH_CONNECTION_present='; if [ -n "${SSH_CONNECTION-}" ]; then printf 'yes\n'; else printf 'no (local invocation or variable removed)\n'; fi
for ds_path in /proc/stat /proc/meminfo /proc/uptime /proc/loadavg /proc/net/dev /proc/diskstats /proc/swaps /proc/self/stat /proc/self/status /etc/os-release /proc/cpuinfo; do
    if [ -r "$ds_path" ]; then printf 'READABLE %s\n' "$ds_path"; else printf 'UNREADABLE %s\n' "$ds_path"; finding "[ACCESS] 当前账号不能读取 $ds_path。"; fi
done
for ds_tool in awk getconf df stat ip uname id base64 sysctl modinfo bash zsh fish sudo apt-get dnf yum zypper apk paccache journalctl; do
    ds_location=$(command -v "$ds_tool" 2>/dev/null || :)
    if [ -n "$ds_location" ]; then printf 'TOOL %s=%s\n' "$ds_tool" "$ds_location"; else printf 'MISSING_TOOL %s\n' "$ds_tool"; fi
done
for ds_tool in awk df stat getconf; do
    command -v "$ds_tool" >/dev/null 2>&1 || finding "[DEPENDENCY] 找不到 $ds_tool，可能影响系统信息、进程读取或常用应用检查。"
done
printf 'tmp_directory_writable='; if [ -w /tmp ]; then printf 'yes (permission check only)\n'; else printf 'no\n'; finding '[INTEGRATION] /tmp 不可写，Shell 集成的 SFTP 暂存步骤可能失败。'; fi
# Do not print hostnames, client/server addresses or passwd entries.
ds_default=${SHELL:-$DS_SH}
if command -v getent >/dev/null 2>&1; then
    ds_passwd=$(timeout -k 1 3 getent passwd "$(id -u)" 2>/dev/null || :)
    case "$ds_passwd" in *:*:*:*:*:*:*) ds_default=${ds_passwd##*:};; esac
    unset ds_passwd
fi
printf 'Login shell basename: %s\n' "${ds_default##*/}"
case "${ds_default##*/}" in bash|zsh) :;; *) finding "[INTEGRATION] 登录 Shell 为 ${ds_default##*/}，不属于应用集成支持的 Bash/Zsh。未检测到提示符集成本身不等于 BBR 故障。";; esac

cat > "$work/basic.sh" <<'DS_DIAG_BASIC'
export LC_ALL=C
printf '__DS_BASIC_BEGIN__\n'
awk 'BEGIN {print "awk_basic_ok"; if ((getline x < "/proc/meminfo")>0) print "awk_getline_ok"; close("/proc/meminfo")}'
printf 'clock_ticks='; getconf CLK_TCK
printf 'page_size='; getconf PAGESIZE
printf '__DS_BASIC_END__\n'
DS_DIAG_BASIC
run_probe sh_basic 5 "$DS_SH" "$work/basic.sh"
if ! grep -q -x awk_getline_ok "$work/sh_basic.out"; then finding '[AWK] awk 的 /proc getline 基础测试失败。检查 awk 是否可用及 /proc/meminfo 是否可读、非空。'; fi
for ds_metric in clock_ticks page_size; do
    ds_value=$(sed -n "s/^$ds_metric=//p" "$work/sh_basic.out")
    case "$ds_value" in ''|*[!0-9]*|0) finding "[GETCONF] $ds_metric 不是有效正整数，进程统计可能不可用。";; *) printf '%s=%s\n' "$ds_metric" "$ds_value";; esac
done
run_probe sh_network 3 "$DS_SH" "$work/network.sh"; assess sh_network network
run_probe sh_core 12 "$DS_SH" "$work/core.sh"; assess sh_core core
run_probe sh_static 12 "$DS_SH" "$work/static.sh"; assess sh_static static
run_probe sh_process 12 "$DS_SH" "$work/process.sh"; assess sh_process process
run_probe sh_utility 12 "$DS_SH" "$work/utility.sh"; assess sh_utility utility
run_probe sh_full 12 "$DS_SH" "$work/full.sh"; assess sh_full full

# A -c subprocess is an approximation. It does NOT prove the actual SSH exec
# request follows the same startup path or that MaxSessions permits new channels.
case "${ds_default##*/}" in sh|bash|dash|zsh|fish|ash|ksh|mksh|csh|tcsh)
    if [ -x "$ds_default" ]; then
        run_probe default_network 3 "$ds_default" "$work/network.sh"; assess default_network network
        run_probe default_full 12 "$ds_default" "$work/full.sh"; assess default_full full
        run_probe default_utility 12 "$ds_default" "$work/utility.sh"; assess default_utility utility
        if marker sh_utility __DS_END__ && ! marker default_utility __DS_END__; then
            finding '[SHELL] 同一常用应用检查在 sh 下完整，在登录 Shell 的 -c 模式下不完整。Shell 语法或启动逻辑是明确线索，需结合单独 SSH exec 检查确认。'
        fi
        if section_has_data sh_full __CS_STAT__ && ! section_has_data default_full __CS_STAT__; then
            finding '[SHELL] 完整监控在 sh 下能读到 CPU 数据，登录 Shell 的 -c 模式却没有。重点检查 Shell 兼容性和启动脚本。'
        fi
    else finding '[SHELL] 登录 Shell 路径不可执行，已跳过默认 Shell 对比。'; fi;;
    *) finding '[SHELL] 检测到自定义或受限 Shell，未执行未知包装程序，需要单独检查 SSH exec。';;
esac

# Unmask errors only for failed original probes; these contain read-only commands.
for ds_kind in core static process utility; do
    ds_retry=0
    [ "$(cat "$work/sh_$ds_kind.rc")" = 0 ] || ds_retry=1
    case "$ds_kind" in
      core) section_has_data sh_core __CS_STAT__ && section_has_data sh_core __CS_MEM__ || ds_retry=1;;
      static) section_has_data sh_static __CS_DF__ || ds_retry=1;;
      process) grep -q '^M' "$work/sh_process.out" && grep -q '^S[[:space:]]' "$work/sh_process.out" || ds_retry=1;;
      utility) marker sh_utility __DS_END__ || ds_retry=1;;
    esac
    if [ "$ds_retry" = 1 ]; then
        sed 's/2>\/dev\/null//g' "$work/$ds_kind.sh" > "$work/visible-$ds_kind.sh"
        run_probe "stderr_$ds_kind" 8 "$DS_SH" "$work/visible-$ds_kind.sh"
        if [ "$ds_kind" = process ] && grep -q 'awk:.*read error' "$work/stderr_process.err"; then
            finding '[AWK_PROC_READ] awk 读取 /proc 时中止。mawk 读取内核线程的 smaps_rollup 或进程消失时可能触发；需要核对具体路径，不能仅据 No such process 断言进程刚退出。'
        fi
    fi
done

cat > "$work/df-root.sh" <<'DS_DIAG_DF'
df -Pk /
DS_DIAG_DF
run_probe df_root 5 "$DS_SH" "$work/df-root.sh"
if [ "$(cat "$work/sh_static.timeout")" = 1 ] && [ "$(cat "$work/df_root.rc")" = 0 ]; then
    finding '[MOUNT] 完整静态采样超时，但 df / 正常，其他挂载点可能卡住；尚需结合 ip/df 分项排查，不能直接断言是磁盘故障。'
fi
printf '\n=== BBR / cleanup eligibility (read-only; separate from missing END) ===\n'
if [ "$(id -u 2>/dev/null)" != 0 ]; then finding '[PRIVILEGE] 当前不是 root。只读监控通常仍应可用；实际修改 BBR/清理操作才需要 root 或 sudo。'; fi
if [ -r /proc/self/status ]; then
    grep -E '^(CapEff|NoNewPrivs):' /proc/self/status || :
fi
for ds_key in net.ipv4.tcp_available_congestion_control net.ipv4.tcp_congestion_control net.core.default_qdisc; do
    printf '%s=' "$ds_key"
    timeout -k 1 3 sysctl -n "$ds_key" 2>/dev/null || printf 'unavailable\n'
done
if [ -r /proc/sys/net/ipv4/tcp_available_congestion_control ]; then
    if ! grep -q -w bbr /proc/sys/net/ipv4/tcp_available_congestion_control; then
        finding '[BBR] 当前可用拥塞控制列表没有 bbr，不代表没有可加载模块，也不能解释结束标记缺失。本脚本没有加载模块。'
    fi
fi
printf '\n=== Startup/config hints (counts only, contents not copied) ===\n'
for ds_file in /etc/profile /etc/bash.bashrc /etc/bashrc "${HOME:-/nonexistent}/.profile" "${HOME:-/nonexistent}/.bashrc" "${HOME:-/nonexistent}/.bash_profile" "${HOME:-/nonexistent}/.zshenv" "${HOME:-/nonexistent}/.zshrc"; do
    [ -r "$ds_file" ] || continue
    ds_count=$(grep -E -c '(^|[;[:space:]])(exec|read|stty|tput|neofetch|fastfetch|screen|tmux)([[:space:];]|$)' "$ds_file" || :)
    printf '%s startup_interception_hint_lines=%s (not a fault by itself)\n' "${ds_file##*/}" "$ds_count"
done
for ds_var in BASH_ENV ENV ZDOTDIR; do
    case "$ds_var" in BASH_ENV) ds_set=${BASH_ENV+x};; ENV) ds_set=${ENV+x};; ZDOTDIR) ds_set=${ZDOTDIR+x};; esac
    printf '%s_present=%s\n' "$ds_var" "${ds_set:-no}"
done
for ds_file in /etc/ssh/sshd_config /etc/ssh/sshd_config.d/*.conf; do
    [ -r "$ds_file" ] || continue
    for ds_directive in Match ForceCommand MaxSessions PermitTTY ChrootDirectory; do
        ds_count=$(grep -i -E -c "^[[:space:]]*$ds_directive[[:space:]]" "$ds_file" || :)
        [ "$ds_count" = 0 ] || printf '%s: %s entries=%s (values not collected)\n' "${ds_file##*/}" "$ds_directive" "$ds_count"
    done
done
# Configuration validation prints no keys and does not start/reload the daemon.
ds_sshd=$(command -v sshd 2>/dev/null || :)
[ -n "$ds_sshd" ] || { [ ! -x /usr/sbin/sshd ] || ds_sshd=/usr/sbin/sshd; }
if [ -n "$ds_sshd" ] && [ "$(id -u 2>/dev/null)" = 0 ]; then
    timeout -k 1 5 "$ds_sshd" -T > "$work/sshd.out" 2> "$work/sshd.err"
    ds_rc=$?
    printf 'sshd_global_config_test_exit=%s (Match context/authorized_keys/provider policy not evaluated)\n' "$ds_rc"
    if [ "$ds_rc" = 0 ]; then
        grep -E '^(maxsessions|permittty|usepam) ' "$work/sshd.out" || :
        if grep -q '^forcecommand none$' "$work/sshd.out"; then printf 'global_forcecommand=none\n'; else printf 'global_forcecommand=set (value hidden)\n'; finding '[SSH_POLICY] 全局 ForceCommand 已设置，可能替换监控/常用应用的 SSH exec 命令。'; fi
        if grep -q -E '^maxsessions [012]$' "$work/sshd.out"; then finding '[SSH_POLICY] 全局 MaxSessions 不大于 2，终端/SFTP/监控可能争抢通道。仍需核对该账号实际命中的 Match 配置。'; fi
    else clean_error "$work/sshd.err"; fi
else printf 'Effective SSH server settings not inspected (non-root or sshd unavailable).\n'; fi

printf '\n=== Conclusion / 排查结论 ===\n'
if section_has_data sh_full __CS_STAT__ && section_has_data sh_full __CS_MEM__ && marker sh_utility __DS_END__ && [ "$(cat "$work/sh_full.rc")" = 0 ]; then
    finding '[SH_OK] 本次 sh 模式能返回基础 CPU/内存数据与常用应用结束标记；进程采样须单独查看 PROCESS/COMMAND 项，此项不代表所有功能通过。'
fi
if [ ! -s "$summary" ]; then finding '[LOCAL_PASS] 未发现本次本地探测故障；尚不能排除实际 SSH 连接、通道限制、启动逻辑及 Windows 客户端问题。'; fi
finding '[NEXT] 请同时发回 report.txt 和 summary.txt，并告知 DengShell 的 r 版本及同账号在其他 SSH 客户端是否正常。'
finding '[BOUNDARY] 终端内脚本不能完整复现 SSH 通道准入/ForceCommand/PTY 行为。若探测通过但界面仍失败，请补做说明中的 ssh -T 标记检查。'
cat "$summary"
printf '\n%s\n' 'No BBR/SWAP/cleanup action was run. Only this private report directory and its temporary files were created.'
printf '\n排查完成。请发回这两个文件（不是脚本本身）：\n%s\n%s\n' "$report" "$summary" >&3
printf '报告可能含系统版本、命令路径及错误摘要；未采集密钥、口令和命令历史。\n' >&3
exit 0
