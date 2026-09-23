package app

import (
	"sort"
	"strconv"
	"strings"
)

// mountinfo identifies filesystems even when df uses different device aliases
// (e.g. /dev/dm-0 and /dev/mapper/vg-root). Its escaped fields must be decoded
// after splitting, so mount points containing spaces still match df.
var mountFieldUnescaper = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

type monitorMount struct {
	device, root, kind, source string
}

func parseMonitorDisks(lines, mounts []string) []Disk {
	metadata := make(map[string]monitorMount, len(mounts))
	for _, line := range mounts {
		f := strings.Fields(line)
		if len(f) == 5 {
			metadata[mountFieldUnescaper.Replace(f[2])] = monitorMount{
				device: f[0], root: mountFieldUnescaper.Replace(f[1]),
				kind: f[3], source: mountFieldUnescaper.Replace(f[4]),
			}
		}
	}
	type candidate struct {
		disk   Disk
		source string
	}
	candidates := make([]candidate, 0, len(lines))
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 6 || f[0] == "Filesystem" {
			continue
		}
		mount := strings.Join(f[5:], " ")
		if !strings.HasPrefix(mount, "/") {
			continue
		}
		total, totalOK := diskKilobytes(f[1])
		used, usedOK := diskKilobytes(f[2])
		available, availableOK := diskKilobytes(f[3])
		if !totalOK || !usedOK || !availableOK || total == 0 {
			continue
		}
		candidates = append(candidates, candidate{Disk{mount, total, used, available}, f[0]})
	}
	// Prefer / and original, short mount paths over aliases in the tooltip.
	sort.SliceStable(candidates, func(i, j int) bool {
		return len(candidates[i].disk.Path) < len(candidates[j].disk.Path)
	})
	disks := []Disk{}
	seenDevice, seenSource, seenPath := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var rootFallback *Disk
	for _, c := range candidates {
		path, source := c.disk.Path, c.source
		m, hasMetadata := metadata[path]
		if hasMetadata {
			source = m.source
		}
		if excludedDiskFilesystem(m.kind) || excludedDiskFilesystem(source) ||
			strings.HasPrefix(source, "/dev/loop") || strings.HasPrefix(source, "/dev/ram") || strings.HasPrefix(source, "/dev/zram") ||
			strings.HasPrefix(m.device, "7:") || strings.HasPrefix(path, "/snap/") {
			continue
		}
		// A container's overlay root (or a pooled root without a block-device
		// source) remains useful as a fallback. Never sum container overlays,
		// network mounts or other virtual volumes into host disk capacity.
		local := strings.HasPrefix(source, "/dev/") || strings.HasPrefix(source, "UUID=") || strings.HasPrefix(source, "LABEL=")
		if !local {
			if path == "/" && (source == "overlay" || source == "rootfs" || m.kind == "zfs") {
				disk := c.disk
				rootFallback = &disk
			}
			continue
		}
		if seenPath[path] || seenSource[source] || m.device != "" && seenDevice[m.device] {
			continue
		}
		seenPath[path], seenSource[source] = true, true
		if m.device != "" {
			seenDevice[m.device] = true
		}
		disks = append(disks, c.disk)
	}
	if rootFallback != nil {
		// Preserve the root view for container/pooled roots: df alone cannot
		// tell whether additional block mounts share that root's backing
		// storage. In particular, never replace a ZFS root with only its EFI
		// partition or expose host totals through a container's /etc binds.
		return []Disk{*rootFallback}
	}
	return disks
}

func excludedDiskFilesystem(kind string) bool {
	switch kind {
	case "tmpfs", "devtmpfs", "ramfs", "proc", "sysfs", "devpts", "squashfs", "iso9660", "udf", "swap",
		"efivarfs", "cgroup", "cgroup2", "debugfs", "tracefs", "securityfs", "pstore", "hugetlbfs",
		"nfs", "nfs4", "cifs", "smb3", "9p", "ceph", "fuse.sshfs", "fuse.rclone", "fuse.glusterfs":
		return true
	}
	return false
}

func diskKilobytes(value string) (uint64, bool) {
	// df can report negative available space when reserved blocks are in use.
	// Keep the volume in the summary, with zero space available to users.
	if strings.HasPrefix(value, "-") {
		_, err := strconv.ParseInt(value, 10, 64)
		return 0, err == nil
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n > ^uint64(0)/1024 {
		return 0, false
	}
	return n * 1024, true
}
