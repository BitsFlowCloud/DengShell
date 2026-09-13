package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

type UtilityRequest struct {
	Kind    string `json:"kind"`
	Mode    string `json:"mode,omitempty"`
	SizeMiB int64  `json:"sizeMiB,omitempty"`
}

type UtilitySwap struct {
	Type       string   `json:"type"`
	Path       string   `json:"path"`
	SizeMiB    float64  `json:"sizeMiB"`
	UsedMiB    float64  `json:"usedMiB"`
	Priority   int      `json:"priority"`
	Active     bool     `json:"active"`
	Configured bool     `json:"configured"`
	Sources    []string `json:"sources,omitempty"`
	Note       string   `json:"note,omitempty"`
	token      string
	procType   string
}

type UtilityPlan struct {
	Kind           string        `json:"kind"`
	SessionID      string        `json:"sessionId"`
	ServerName     string        `json:"serverName"`
	Host           string        `json:"host"`
	Title          string        `json:"title"`
	Summary        string        `json:"summary"`
	Command        string        `json:"command"`
	Details        []string      `json:"details"`
	BlockedReason  string        `json:"blockedReason,omitempty"`
	HasSwap        bool          `json:"hasSwap"`
	RebootRequired bool          `json:"rebootRequired"`
	SwapEntries    []UtilitySwap `json:"swapEntries"`
}

type utilityInspection struct {
	sections  map[string]string
	swaps     []UtilitySwap
	tools     map[string]bool
	swapError string
}

// These paths are never supplied by the HTTP caller. Tests substitute only
// temporary fixture paths and mock privileged commands; no host sysctl/swap is
// changed by a test or by requesting a plan.
type utilityPaths struct {
	state, swaps, fstab, boot, memory, swapfile, filesystem, sysctlConfig string
}

var linuxUtilityPaths = utilityPaths{
	state: "/var/lib/dengshell", swaps: "/proc/swaps", fstab: "/etc/fstab",
	boot: "/proc/sys/kernel/random/boot_id", memory: "/proc/meminfo",
	swapfile: "/swapfile.dengshell", filesystem: "/", sysctlConfig: "/etc/sysctl.d/99-dengshell.conf",
}

func (a *App) registerUtilitiesHTTP(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/{id}/utilities/plan", func(w http.ResponseWriter, r *http.Request) {
		var input UtilityRequest
		if !decode(w, r, &input) {
			return
		}
		if err := validateUtilityRequest(input); err != nil {
			writeError(w, 400, err)
			return
		}
		s, err := a.session(r.PathValue("id"))
		if err != nil {
			writeError(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		output, err := runMTRScript(ctx, s, utilityProbe(linuxUtilityPaths), 128*1024)
		if err != nil {
			writeError(w, 400, errors.New("无法完成服务器只读检查，请确认连接正常、目标为 Linux 且允许读取系统信息。"))
			return
		}
		inspection, err := parseUtilityInspection(output)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		if strings.HasPrefix(input.Kind, "swap-") && inspection.swapError == "" && inspection.sections["OS"] == "Linux" && len(inspection.swaps) > 0 {
			if len(inspection.swaps) > 128 {
				inspection.swapError = "交换配置超过单次检查上限 128 项，请用系统工具处理。"
			} else {
				resolved, resolveErr := runMTRScript(ctx, s, utilitySwapResolveProbe(inspection.swaps), 128*1024)
				if resolveErr != nil {
					writeError(w, 400, errors.New("无法完成交换设备与 UUID/LABEL 的只读核对，请重试。"))
					return
				}
				if err := applyUtilitySwapResolution(&inspection, resolved); err != nil {
					writeError(w, 400, err)
					return
				}
			}
		}
		plan := buildUtilityPlan(input, inspection, linuxUtilityPaths)
		plan.SessionID = s.ID
		if profile, err := a.store.Get(s.ProfileID); err == nil {
			plan.ServerName, plan.Host = profile.Name, profile.Host
		}
		writeJSON(w, plan)
	})
}

func validateUtilityRequest(input UtilityRequest) error {
	switch input.Kind {
	case "bbr":
		if input.Mode != "conservative" && input.Mode != "balanced" && input.Mode != "aggressive" {
			return errors.New("请选择保守、平衡或激进 BBR 策略")
		}
	case "clean", "swap-status", "swap-remove":
	case "swap-create":
		if input.SizeMiB < 64 || input.SizeMiB > 1048576 {
			return errors.New("SWAP 大小应为 64 MiB 至 1 TiB 之间的整数 MiB")
		}
	default:
		return errors.New("不支持的常用应用操作")
	}
	return nil
}

func utilityProbe(p utilityPaths) string {
	return `export LC_ALL=C
printf '\n__DS_OS__\n'; uname -s
printf '\n__DS_UID__\n'; id -u
printf '\n__DS_TOOLS__\n'; for ds_tool in sudo base64 sysctl modprobe modinfo swapon swapoff mkswap blkid findfs readlink btrfs fallocate chattr lsattr ln dd stat df awk sort cmp cp mv mkdir rm rmdir mktemp chmod cat date apt-get dnf yum zypper apk paccache journalctl; do command -v "$ds_tool" >/dev/null 2>&1 && printf '%s\n' "$ds_tool"; done
printf '\n__DS_BBR__\n'; sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null || :
printf '\n__DS_BBR_MODULE__\n'; modinfo -n tcp_bbr 2>/dev/null || :
printf '\n__DS_SWAPS__\n'; cat ` + terminalQuote(p.swaps) + ` 2>/dev/null || :
printf '\n__DS_FSTAB__\n'; if [ -r ` + terminalQuote(p.fstab) + ` ]; then awk '$1 !~ /^#/ && $3 == "swap" {print $1}' ` + terminalQuote(p.fstab) + `; fi
printf '\n__DS_FS__\n'; stat -f -c %T ` + terminalQuote(p.filesystem) + ` 2>/dev/null || :
printf '\n__DS_BTRFS_CREATE__\n'; if command -v btrfs >/dev/null 2>&1 && btrfs filesystem mkswapfile --help >/dev/null 2>&1; then printf 'yes\n'; fi
printf '\n__DS_BTRFS_MAP__\n'; if command -v btrfs >/dev/null 2>&1 && btrfs inspect-internal map-swapfile --help >/dev/null 2>&1; then printf 'yes\n'; fi
printf '\n__DS_FREE__\n'; df -Pk ` + terminalQuote(p.filesystem) + ` 2>/dev/null | awk 'NR==2 {print $4}'
printf '\n__DS_EXISTS__\n'; if [ -e ` + terminalQuote(p.swapfile) + ` ] || [ -L ` + terminalQuote(p.swapfile) + ` ]; then printf 'yes\n'; fi
printf '\n__DS_BOOT__\n'; cat ` + terminalQuote(p.boot) + ` 2>/dev/null || :
printf '\n__DS_REMOVED_BOOT__\n'; cat ` + terminalQuote(path.Join(p.state, "swap-removed-boot-id")) + ` 2>/dev/null || :
printf '\n__DS_END__\n'
`
}

func parseUtilityInspection(output string) (utilityInspection, error) {
	i := utilityInspection{sections: map[string]string{}, tools: map[string]bool{}, swaps: []UtilitySwap{}}
	section := ""
	complete := false
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "__DS_") && strings.HasSuffix(line, "__") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "__DS_"), "__")
			if section == "END" {
				complete = true
			}
			continue
		}
		if section != "" {
			i.sections[section] += line + "\n"
		}
	}
	if !complete {
		return i, errors.New("服务器检查结果不完整，请重试")
	}
	for key, value := range i.sections {
		i.sections[key] = strings.TrimSpace(value)
	}
	for _, tool := range strings.Fields(i.sections["TOOLS"]) {
		i.tools[tool] = true
	}
	if i.sections["OS"] != "Linux" {
		return i, nil
	}
	lines := strings.Split(i.sections["SWAPS"], "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "Filename") {
		i.swapError = "无法读取 /proc/swaps，未生成会修改 SWAP 的命令"
		return i, nil
	}
	seen := map[string]bool{}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) != 5 {
			i.swapError = "SWAP 信息格式无法可靠识别，请使用系统工具检查"
			return i, nil
		}
		name, err := decodeSwapPath(fields[0])
		size, e1 := strconv.ParseUint(fields[2], 10, 64)
		used, e2 := strconv.ParseUint(fields[3], 10, 64)
		priority, e3 := strconv.Atoi(fields[4])
		if err != nil || e1 != nil || e2 != nil || e3 != nil || (fields[1] != "file" && fields[1] != "partition") || seen[name] {
			i.swapError = "SWAP 路径或类型无法可靠识别，已停止生成命令"
			return i, nil
		}
		seen[name] = true
		kind := fields[1]
		if kind == "partition" && strings.HasPrefix(path.Base(name), "zram") {
			kind = "zram"
		}
		i.swaps = append(i.swaps, UtilitySwap{Type: kind, Path: name, SizeMiB: float64(size) / 1024, UsedMiB: float64(used) / 1024, Priority: priority, Active: true, token: fields[0], procType: fields[1]})
	}
	for _, token := range strings.Fields(i.sections["FSTAB"]) {
		name, err := decodeSwapField(token)
		if err != nil {
			i.swapError = "fstab 交换项含无法识别的转义，请先检查配置。"
			return i, nil
		}
		if seen[name] {
			for n := range i.swaps {
				if i.swaps[n].Path == name {
					i.swaps[n].Configured = true
					i.swaps[n].Sources = append(i.swaps[n].Sources, name)
					break
				}
			}
		} else {
			i.swaps = append(i.swaps, UtilitySwap{Type: "configured", Path: name, Configured: true, Sources: []string{name}})
			seen[name] = true
		}
	}
	return i, nil
}

func decodeSwapField(token string) (string, error) {
	var result strings.Builder
	for j := 0; j < len(token); j++ {
		if token[j] == '\\' {
			if j+3 >= len(token) {
				return "", errors.New("invalid escape")
			}
			n, err := strconv.ParseUint(token[j+1:j+4], 8, 8)
			if err != nil {
				return "", err
			}
			result.WriteByte(byte(n))
			j += 3
		} else {
			result.WriteByte(token[j])
		}
	}
	value := result.String()
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("invalid swap path")
	}
	return value, nil
}

func decodeSwapPath(token string) (string, error) {
	value, err := decodeSwapField(token)
	if err != nil || !path.IsAbs(value) || path.Clean(value) != value || value == "/" {
		return "", errors.New("invalid swap path")
	}
	return value, nil
}

func buildUtilityPlan(input UtilityRequest, i utilityInspection, paths utilityPaths) UtilityPlan {
	p := UtilityPlan{Kind: input.Kind, SwapEntries: i.swaps, HasSwap: len(i.swaps) > 0, Details: []string{}}
	p.RebootRequired = i.sections["REMOVED_BOOT"] != "" && (i.sections["BOOT"] == "" || i.sections["REMOVED_BOOT"] == i.sections["BOOT"])
	if i.sections["OS"] != "Linux" {
		p.BlockedReason = "常用应用目前仅支持 Linux 服务器"
		return p
	}
	if strings.HasPrefix(input.Kind, "swap-") && i.swapError != "" {
		p.Title = "SWAP 检查"
		p.BlockedReason = i.swapError
		return p
	}
	var script string
	switch input.Kind {
	case "bbr":
		names := map[string]string{"conservative": "保守", "balanced": "平衡", "aggressive": "激进"}
		p.Title, p.Summary = "BBR 优化 · "+names[input.Mode], "使用当前内核提供的 BBR，按策略调整 TCP 自动缓冲及发送队列上限。"
		p.Details = []string{"保守侧重抑制本机排队；平衡兼顾传输与排队；激进允许更大的吞吐缓冲。实际延迟、重传和速度取决于链路，不能保证最低延迟或零重传。", "不安装或更换内核，不修改 TIME_WAIT、重传恢复、安全保护或网络接口现有队列；BBR 对新 TCP 连接生效。", "应用前备份当前值；持久配置只写入 /etc/sysctl.d/99-dengshell.conf。失败时尝试回滚，备份位置会在终端显示。"}
		if !containsWord(i.sections["BBR"], "bbr") && !(i.sections["BBR_MODULE"] != "" && i.tools["modprobe"]) {
			p.BlockedReason = "当前内核未提供可加载的 BBR；请使用发行版支持的内核方案，本工具不会安装内核或重启服务器。"
		}
		script = bbrUtilityScript(input.Mode, paths)
	case "clean":
		p.Title, p.Summary = "清理系统缓存", "清理包管理器的下载缓存，并清理早于 14 天的归档 journal。"
		p.Details = []string{"APT/DNF/YUM/Zypper/Apk 按其缓存命令清理；Arch 如有 paccache 则保留最近两个缓存版本。未提供对应工具时跳过。", "journal 会先轮转，再按 14 天期限清理归档日志；近期日志保留。", "不卸载软件，不删除用户文件、网站、数据库、Docker 数据或整个 /tmp。"}
		script = cleanUtilityScript(paths)
	case "swap-status":
		p.Title, p.Summary = "SWAP 检查", "已读取当前交换文件、交换分区、ZRAM 和开机交换配置。"
		if i.sections["FS"] == "btrfs" {
			p.Details = []string{"Btrfs：会使用专用交换文件流程并核对区段；交换文件启用期间，其所在子卷不能创建快照。"}
		}
		if i.sections["FS"] == "f2fs" {
			p.Details = []string{"F2FS：创建未压缩的交换文件；是否能启用取决于内核与挂载模式，内核拒绝时会清理本次文件。"}
		}
		return p
	case "swap-create":
		p.Title, p.Summary = "设置 SWAP", fmt.Sprintf("创建 %d MiB 的交换文件 %s。", input.SizeMiB, paths.swapfile)
		p.Details = []string{"执行前重新核对交换区、开机配置、文件系统、目标路径与可用空间；不覆盖已有路径。", "ext 系列、XFS、F2FS 实际分配无空洞文件；Btrfs 优先用专用 mkswapfile，旧工具使用 NOCOW 后分配。只有 swapon 成功才写入 fstab。", "Btrfs 需内核支持、单设备数据区段、无压缩及 NOCOW；活动交换文件所在子卷无法创建快照。F2FS 取决于内核与挂载模式；内核拒绝时自动清理本次文件。"}
		switch {
		case p.HasSwap || p.RebootRequired:
			p.BlockedReason = "需要先删除当前 SWAP 并重启之后才能正确设置。程序不会自动重启；重启后请再次检查。"
		case i.sections["EXISTS"] == "yes":
			p.BlockedReason = "交换文件目标路径已存在，不能覆盖：" + paths.swapfile
		case !utilitySwapFilesystemSupported(i.sections["FS"]):
			p.BlockedReason = "当前文件系统（" + i.sections["FS"] + "）没有可验证的自动交换文件方案；不支持在网络、内存或 Overlay/FUSE 文件系统上自动创建。已有活动交换区仍可识别和关闭。"
		case i.sections["FS"] == "btrfs" && !utilityBtrfsToolsSupported(i):
			p.BlockedReason = "Btrfs 需要 btrfs-progs；优先使用 filesystem mkswapfile，旧工具还需 chattr、lsattr 和 fallocate。请先安装发行版对应工具。"
		case i.sections["FS"] == "f2fs" && !(i.tools["chattr"] && i.tools["lsattr"]):
			p.BlockedReason = "F2FS 需要 chattr 和 lsattr 来核对交换文件未启用压缩，请先安装发行版文件属性工具。"
		default:
			free, err := strconv.ParseInt(i.sections["FREE"], 10, 64)
			if err != nil || free < input.SizeMiB*1024+262144 {
				p.BlockedReason = "可用磁盘空间不足，创建后需额外保留至少 256 MiB。"
			}
		}
		script = createSwapUtilityScript(input.SizeMiB, paths)
	case "swap-remove":
		p.Title, p.Summary = "关闭 / 删除 SWAP", "逐个关闭活动交换区，移除 fstab 中的交换项；仅删除确认过的交换文件。"
		p.Details = []string{"所有 swapoff 均成功后才改 fstab 或删除文件；内存不足等失败会停止并保留文件。", "交换分区与 ZRAM 只关闭，不删除设备；其他 fstab 项保留并备份。仅配置但未启用的普通文件需 blkid 确认交换签名才删除；无法确认则保留并提示人工处理。", "ZRAM 或自定义服务可能在重启时重新创建交换区，需要在发行版对应服务中调整。关闭后如需重新设置，请先重启再检查。"}
		if !p.HasSwap {
			p.BlockedReason = "当前没有活动交换区或 fstab 交换配置。"
		}
		script = removeSwapUtilityScript(i.swaps, paths, i.sections["FSTAB"])
	}
	if p.BlockedReason == "" {
		if !i.tools["base64"] || (i.sections["UID"] != "0" && !i.tools["sudo"]) {
			p.BlockedReason = "执行需要 base64 工具以及 root 或 sudo 权限；请先安装发行版基础工具或改用 root 连接。"
		} else {
			p.Command = wrapUtilityScript(script)
		}
	}
	return p
}

func containsWord(list, word string) bool {
	for _, value := range strings.Fields(list) {
		if value == word {
			return true
		}
	}
	return false
}

func utilityBtrfsToolsSupported(i utilityInspection) bool {
	if !i.tools["btrfs"] || !i.tools["ln"] || !i.tools["lsattr"] {
		return false
	}
	if i.sections["BTRFS_CREATE"] == "yes" {
		return i.tools["blkid"]
	}
	return i.tools["chattr"] && i.tools["fallocate"]
}

func utilitySwapFilesystemSupported(fs string) bool {
	return fs == "ext2/ext3" || fs == "ext2" || fs == "ext3" || fs == "ext4" || fs == "xfs" || fs == "btrfs" || fs == "f2fs"
}

func wrapUtilityScript(script string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	return "( dengshell_utility_script=$(printf '%s' " + terminalQuote(encoded) + " | base64 -d) || exit $?; if [ \"$(id -u)\" = 0 ]; then exec sh -c \"$dengshell_utility_script\"; elif command -v sudo >/dev/null 2>&1; then exec sudo sh -c \"$dengshell_utility_script\"; else printf '%s\\n' '需要 root 或 sudo 权限。' >&2; exit 1; fi )"
}
