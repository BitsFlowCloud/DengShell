package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUtilitySwapFilesystemSpecificCreate(t *testing.T) {
	cases := []struct {
		name, fs string
		flags    []string
		success  bool
	}{
		{"btrfs modern", "btrfs", nil, true},
		{"btrfs legacy", "btrfs", []string{"legacy-btrfs", "no-map-btrfs"}, true},
		{"btrfs legacy with map", "btrfs", []string{"legacy-btrfs"}, true},
		{"btrfs extents rejected", "btrfs", []string{"fail-map"}, false},
		{"btrfs sparse output", "btrfs", []string{"sparse-btrfs"}, false},
		{"btrfs create failure", "btrfs", []string{"fail-btrfs-create"}, false},
		{"btrfs kernel rejects", "btrfs", []string{"fail-swapon"}, false},
		{"btrfs no nocow", "btrfs", []string{"no-nocow"}, false},
		{"btrfs compressed", "btrfs", []string{"compressed"}, false},
		{"btrfs old multi device", "btrfs", []string{"legacy-btrfs", "no-map-btrfs", "multi-device"}, false},
		{"btrfs old raid data", "btrfs", []string{"legacy-btrfs", "no-map-btrfs", "raid-data"}, false},
		{"btrfs new multi device validated by map", "btrfs", []string{"multi-device"}, true},
		{"f2fs", "f2fs", nil, true},
		{"f2fs compressed", "f2fs", []string{"compressed"}, false},
		{"f2fs kernel mode rejects", "f2fs", []string{"fail-swapon"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newUtilityTestFixture(t)
			os.WriteFile(filepath.Join(f.root, "filesystem"), []byte(tc.fs), 0600)
			for _, flag := range tc.flags {
				os.WriteFile(filepath.Join(f.root, flag), []byte("yes"), 0600)
			}
			original, _ := os.ReadFile(f.paths.fstab)
			f.run(t, createSwapUtilityScript(1, f.paths), tc.success)
			file, err := os.Stat(f.paths.swapfile)
			if tc.success {
				if err != nil || file.Size() != 1048576 || file.Mode().Perm() != 0600 {
					t.Fatalf("bad created file %v %v", file, err)
				}
			} else {
				if !os.IsNotExist(err) {
					t.Fatal("failed create left file", err)
				}
				after, _ := os.ReadFile(f.paths.fstab)
				if string(after) != string(original) {
					t.Fatal("failed create changed fstab")
				}
			}
			dirs, _ := filepath.Glob(filepath.Join(f.root, ".dengshell-swap-*"))
			if len(dirs) != 0 {
				t.Fatal("private staging was not cleaned", dirs)
			}
			calls, _ := os.ReadFile(filepath.Join(f.root, "calls"))
			if tc.name == "btrfs legacy" {
				c := string(calls)
				nocow := strings.Index(c, `["chattr", "+C"`)
				allocate := strings.Index(c, `["fallocate"`)
				initialize := strings.Index(c, `["mkswap"`)
				if !(nocow >= 0 && allocate > nocow && initialize > allocate) {
					t.Fatal("NOCOW must precede allocation and initialization", c)
				}
			}
		})
	}
}

func TestUtilityBtrfsPublishDoesNotReplaceExistingFile(t *testing.T) {
	f := newUtilityTestFixture(t)
	os.WriteFile(filepath.Join(f.root, "filesystem"), []byte("btrfs"), 0600)
	os.WriteFile(filepath.Join(f.root, "publish-collision"), nil, 0600)
	before, _ := os.ReadFile(f.paths.fstab)
	f.run(t, createSwapUtilityScript(1, f.paths), false)
	data, _ := os.ReadFile(f.paths.swapfile)
	if string(data) != "keep other file" {
		t.Fatal("publication overwrote another file")
	}
	after, _ := os.ReadFile(f.paths.fstab)
	if string(after) != string(before) {
		t.Fatal("failed publication changed fstab")
	}
	dirs, _ := filepath.Glob(filepath.Join(f.root, ".dengshell-swap-*"))
	if len(dirs) != 0 {
		t.Fatal("staging file not removed")
	}
}

func TestUtilitySwapAliasResolutionAndMerge(t *testing.T) {
	raw := strings.Replace(utilityInspectionFixture, "Filename Type Size Used Priority", "Filename Type Size Used Priority\n/dev/mapper/swap partition 2048 0 -2\n/dev/zram0 partition 1024 0 100", 1)
	raw = strings.Replace(raw, "__DS_FSTAB__", "__DS_FSTAB__\nUUID=swap-id\nPARTUUID=partition-id\nLABEL=swap\\040name\nPARTLABEL=zram-name\nUUID=duplicate", 1)
	i, err := parseUtilityInspection(raw)
	if err != nil || len(i.swaps) != 7 {
		t.Fatalf("parse %v %+v", err, i.swaps)
	}
	values := [][3]string{{"/dev/dm-0", "partition", ""}, {"/dev/zram0", "zram", ""}, {"/dev/dm-0", "partition", ""}, {"/dev/dm-0", "partition", ""}, {"/inactive/swap file", "file", ""}, {"/dev/zram0", "zram", ""}, {"", "unknown", "duplicate"}}
	var out strings.Builder
	for n, v := range values {
		fmt.Fprintf(&out, "\n__DS_RESOLVE_%d__\n%s\n%s\n%s\n", n, v[0], v[1], v[2])
	}
	out.WriteString("\n__DS_RESOLVE_END__\n")
	if err = applyUtilitySwapResolution(&i, out.String()); err != nil {
		t.Fatal(err)
	}
	if len(i.swaps) != 4 || i.swaps[0].Path != "/dev/mapper/swap" || !i.swaps[0].Active || !i.swaps[0].Configured || len(i.swaps[0].Sources) != 2 {
		t.Fatalf("active device aliases not merged safely: %+v", i.swaps)
	}
	if i.swaps[1].Type != "zram" || !i.swaps[1].Configured || i.swaps[2].Type != "file" || i.swaps[2].Sources[0] != "LABEL=swap name" || !strings.Contains(i.swaps[3].Note, "多个设备") {
		t.Fatal("wrong classification", i.swaps)
	}
	if applyUtilitySwapResolution(&i, "\n__DS_RESOLVE_END__\n") == nil {
		t.Fatal("truncated resolution accepted")
	}
}

func TestUtilitySwapResolveProbeOnlyReadsAndQuotesSources(t *testing.T) {
	f := newUtilityTestFixture(t)
	target := filepath.Join(f.root, "swap quote' $(not-executed)")
	os.WriteFile(target, []byte("SWAPFIXTURE"), 0600)
	alias := filepath.Join(f.root, "alias")
	os.Symlink(target, alias)
	mappings := map[string][]string{"UUID=fixture": {target}, "LABEL=two": {target, alias}, "PARTUUID=fixture": {target}, "PARTLABEL=fixture": {target}}
	b, _ := json.Marshal(mappings)
	os.WriteFile(filepath.Join(f.root, "aliases.json"), b, 0600)
	i := utilityInspection{swaps: []UtilitySwap{{Path: target, Type: "file", Active: true}, {Path: alias, Configured: true, Sources: []string{alias}}, {Path: "UUID=fixture", Configured: true, Sources: []string{"UUID=fixture"}}, {Path: "LABEL=two", Configured: true}, {Path: "PARTUUID=fixture", Configured: true}, {Path: "PARTLABEL=fixture", Configured: true}}}
	cmd := exec.Command("sh", "-c", utilitySwapResolveProbe(i.swaps))
	cmd.Env = f.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	if err := applyUtilitySwapResolution(&i, string(output)); err != nil {
		t.Fatal(err, string(output))
	}
	if len(i.swaps) != 2 || len(i.swaps[0].Sources) != 2 || !strings.Contains(i.swaps[1].Note, "多个设备") {
		t.Fatalf("alias merge result: %+v\n%s", i.swaps, output)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "SWAPFIXTURE" {
		t.Fatal("read-only resolution modified file")
	}
	if _, err := os.Stat(f.paths.state); !os.IsNotExist(err) {
		t.Fatal("read-only resolution wrote operation state")
	}
}

func TestUtilitySwapResolvedInactiveDeviceNeverGetsRemovalCommand(t *testing.T) {
	entries := []UtilitySwap{
		{Type: "partition", Path: "/dev/fixture-partition", Configured: true, Sources: []string{"UUID=fixture"}},
		{Type: "zram", Path: "/dev/zram0", Configured: true, Sources: []string{"LABEL=zram"}},
	}
	script := removeSwapUtilityScript(entries, linuxUtilityPaths)
	if strings.Contains(script, "rm -f -- '/dev/") || strings.Contains(script, "mkswap '/dev/") {
		t.Fatal("inactive device received a file removal or formatting command")
	}
}

func TestUtilitySwapCreationPlansReflectFilesystemCapabilities(t *testing.T) {
	i, _ := parseUtilityInspection(utilityInspectionFixture)
	request := UtilityRequest{Kind: "swap-create", SizeMiB: 512}
	for _, fs := range []string{"ext2/ext3", "ext4", "xfs"} {
		i.sections["FS"] = fs
		if p := buildUtilityPlan(request, i, linuxUtilityPaths); p.Command == "" {
			t.Fatal(fs, p.BlockedReason)
		}
	}
	i.sections["FS"] = "btrfs"
	if p := buildUtilityPlan(request, i, linuxUtilityPaths); !strings.Contains(p.BlockedReason, "Btrfs") {
		t.Fatal("missing btrfs tools not explained")
	}
	for _, tool := range []string{"btrfs", "lsattr", "ln", "chattr", "fallocate"} {
		i.tools[tool] = true
	}
	if p := buildUtilityPlan(request, i, linuxUtilityPaths); p.Command == "" {
		t.Fatal("legacy Btrfs plan blocked", p.BlockedReason)
	}
	i.sections["BTRFS_CREATE"] = "yes"
	if p := buildUtilityPlan(request, i, linuxUtilityPaths); p.Command == "" {
		t.Fatal("modern Btrfs plan blocked", p.BlockedReason)
	}
	i.sections["FS"] = "f2fs"
	if p := buildUtilityPlan(request, i, linuxUtilityPaths); p.Command == "" {
		t.Fatal("F2FS plan blocked", p.BlockedReason)
	}
	for _, fs := range []string{"tmpfs", "nfs", "cifs", "overlayfs", "fuseblk", "unknown"} {
		i.sections["FS"] = fs
		if p := buildUtilityPlan(request, i, linuxUtilityPaths); p.Command != "" || p.BlockedReason == "" {
			t.Fatal("unverified filesystem accepted", fs)
		}
	}
}

func TestUtilitySwapRemovalRechecksFstabAliases(t *testing.T) {
	f := newUtilityTestFixture(t)
	original, _ := os.ReadFile(f.paths.fstab)
	configured := string(original) + "UUID=old none swap sw 0 0\nLABEL=new none swap defaults 0 0\n"
	os.WriteFile(f.paths.fstab, []byte(configured), 0644)
	out := f.run(t, removeSwapUtilityScript([]UtilitySwap{{Type: "configured", Path: "UUID=old", Note: "无法唯一解析，保留设备。"}}, f.paths, "UUID=old"), false)
	after, _ := os.ReadFile(f.paths.fstab)
	if string(after) != configured || !strings.Contains(out, "检查后发生变化") {
		t.Fatal("changed fstab was modified or unexplained", out)
	}
	calls, _ := os.ReadFile(filepath.Join(f.root, "calls"))
	if strings.Contains(string(calls), `["swapoff"`) {
		t.Fatal("swapoff ran despite changed configuration")
	}
	out = f.run(t, removeSwapUtilityScript([]UtilitySwap{{Type: "configured", Path: "UUID=old", Note: "无法唯一解析，保留设备。"}}, f.paths, "UUID=old\nLABEL=new"), true)
	if !strings.Contains(out, "无法唯一解析") {
		t.Fatal("unresolved source preservation was not explained", out)
	}
	after, _ = os.ReadFile(f.paths.fstab)
	if string(after) != string(original) {
		t.Fatal("unrelated mounts changed")
	}
}
