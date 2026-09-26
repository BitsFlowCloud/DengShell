export LC_ALL=C
printf '\n__CS_OS__\n'; uname -s; cat /etc/os-release 2>/dev/null
printf '\n__CS_CPUINFO__\n'; cat /proc/cpuinfo 2>/dev/null
printf '\n__CS_STAT__\n'; cat /proc/stat 2>/dev/null
printf '\n__CS_MEM__\n'; cat /proc/meminfo 2>/dev/null
printf '\n__CS_UPTIME__\n'; cat /proc/uptime 2>/dev/null
printf '\n__CS_LOAD__\n'; cat /proc/loadavg 2>/dev/null
printf '\n__CS_NET__\n'; cat /proc/net/dev 2>/dev/null
printf '\n__CS_LINK__\n'; ip -o link show 2>/dev/null
printf '\n__CS_ADDR__\n'; ip -o addr show scope global 2>/dev/null
printf '\n__CS_ROUTES__\n'; ip -4 route show default 2>/dev/null; ip -6 route show default 2>/dev/null
printf '\n__CS_CONNECTION__\n'; printf '%s\n' "$SSH_CONNECTION"
printf '\n__CS_BLOCKS__\n'; ls /sys/block 2>/dev/null
printf '\n__CS_DISKIO__\n'; cat /proc/diskstats 2>/dev/null
printf '\n__CS_DF__\n'; df -Pk 2>/dev/null

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
