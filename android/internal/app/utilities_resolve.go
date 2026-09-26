package app

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Resolve fstab tags and path aliases with a second read-only probe. No source
// is interpolated as code. blkid's multiple matches remain unresolved instead
// of silently choosing an arbitrary device with a duplicated UUID or LABEL.
func utilitySwapResolveProbe(entries []UtilitySwap) string {
	var s strings.Builder
	s.WriteString("export LC_ALL=C\n")
	for n, entry := range entries {
		fmt.Fprintf(&s, "printf '\\n__DS_RESOLVE_%d__\\n'\nds_source=%s\nds_target=''\nds_note=''\n", n, terminalQuote(entry.Path))
		s.WriteString(`case "$ds_source" in
  /*) ds_target=$ds_source ;;
  UUID=*|LABEL=*|PARTUUID=*|PARTLABEL=*)
    if command -v blkid >/dev/null 2>&1; then
      ds_matches=$(blkid -c /dev/null -t "$ds_source" -o device 2>/dev/null || :)
      ds_count=$(printf '%s\n' "$ds_matches" | awk 'NF {n++} END {print n+0}')
      if [ "$ds_count" = 1 ]; then ds_target=$ds_matches
      elif [ "$ds_count" -gt 1 ]; then ds_note='duplicate'
      fi
    fi
    if [ -z "$ds_target" ] && [ -z "$ds_note" ] && command -v findfs >/dev/null 2>&1; then ds_target=$(findfs "$ds_source" 2>/dev/null || :); fi ;;
esac
if [ -n "$ds_target" ] && command -v readlink >/dev/null 2>&1; then
  ds_target=$(readlink -f -- "$ds_target" 2>/dev/null || :)
fi
case "$ds_target" in /*) ;; *) ds_target='';; esac
printf '%s\n' "$ds_target"
if [ -n "$ds_target" ] && [ -b "$ds_target" ]; then
  case "${ds_target##*/}" in zram[0-9]*) printf 'zram\n';; *) printf 'partition\n';; esac
elif [ -n "$ds_target" ] && [ -f "$ds_target" ]; then printf 'file\n'
else printf 'unknown\n'; fi
printf '%s\n' "$ds_note"
`)
	}
	s.WriteString("printf '\\n__DS_RESOLVE_END__\\n'\n")
	return s.String()
}

func applyUtilitySwapResolution(i *utilityInspection, output string) error {
	if !strings.Contains(output, "\n__DS_RESOLVE_END__") {
		return errors.New("交换设备解析结果不完整，请重试。")
	}
	// Canonical path is only used to merge aliases. Active /proc/swaps paths
	// remain intact for snapshot comparison, swapoff and inode validation.
	canonical := make([]string, len(i.swaps))
	for n := range i.swaps {
		marker := fmt.Sprintf("\n__DS_RESOLVE_%d__\n", n)
		_, rest, ok := strings.Cut(output, marker)
		if !ok {
			return errors.New("交换设备解析缺少项目，请重试。")
		}
		lines := strings.Split(rest, "\n")
		if len(lines) < 3 {
			return errors.New("交换设备解析信息不完整。")
		}
		name, kind, note := lines[0], lines[1], lines[2]
		if name != "" && (!path.IsAbs(name) || path.Clean(name) != name || name == "/" || strings.ContainsAny(name, "\x00\r\n")) {
			return errors.New("交换设备解析路径无效。")
		}
		entry := &i.swaps[n]
		canonical[n] = name
		if note == "duplicate" {
			entry.Note = "UUID/LABEL 对应多个设备，无法唯一识别；仅移除交换配置，不处理候选设备。"
		}
		if !entry.Active {
			if name != "" && (kind == "file" || kind == "partition" || kind == "zram") {
				entry.Path, entry.Type = name, kind
			} else if entry.Note == "" {
				entry.Note = "配置尚未启用或设备无法解析；仅移除对应交换配置，不删除未确认的文件或设备。"
			}
		}
	}
	merged := make([]UtilitySwap, 0, len(i.swaps))
	indices := map[string]int{}
	for n, entry := range i.swaps {
		key := canonical[n]
		if key == "" {
			key = entry.Path
		}
		if at, ok := indices[key]; ok {
			previous := &merged[at]
			previous.Configured = previous.Configured || entry.Configured
			previous.Sources = append(previous.Sources, entry.Sources...)
			continue
		}
		indices[key] = len(merged)
		merged = append(merged, entry)
	}
	i.swaps = merged
	return nil
}
