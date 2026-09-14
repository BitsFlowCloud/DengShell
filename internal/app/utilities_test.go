package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const utilityInspectionFixture = `
__DS_OS__
Linux
__DS_UID__
0
__DS_TOOLS__
base64
sysctl
modprobe
blkid
__DS_BBR__
reno cubic bbr
__DS_SWAPS__
Filename Type Size Used Priority
__DS_FSTAB__
__DS_FS__
ext2/ext3
__DS_FREE__
99999999
__DS_BOOT__
boot-one
__DS_END__
`

func TestUtilityPlansAreReadOnlyAndValidateRequests(t *testing.T) {
	i, err := parseUtilityInspection(utilityInspectionFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []UtilityRequest{{Kind: "bbr", Mode: "conservative"}, {Kind: "bbr", Mode: "balanced"}, {Kind: "bbr", Mode: "aggressive"}, {Kind: "clean"}, {Kind: "swap-status"}, {Kind: "swap-create", SizeMiB: 512}} {
		if err := validateUtilityRequest(request); err != nil {
			t.Fatal(err)
		}
		p := buildUtilityPlan(request, i, linuxUtilityPaths)
		if p.BlockedReason != "" {
			t.Fatalf("unexpected blocked plan: %+v", p)
		}
		if request.Kind == "swap-status" && p.Command != "" {
			t.Fatal("status inspection returned a mutating command")
		}
		if request.Kind != "swap-status" && !strings.Contains(p.Command, "base64 -d") {
			t.Fatal("script was not safely encoded")
		}
	}
	for _, request := range []UtilityRequest{{Kind: "arbitrary"}, {Kind: "bbr", Mode: "$(touch /tmp/unsafe)"}, {Kind: "swap-create", SizeMiB: -1}, {Kind: "swap-create", SizeMiB: 1048577}} {
		if validateUtilityRequest(request) == nil {
			t.Fatal("invalid utility request accepted", request)
		}
	}
	probe := utilityProbe(linuxUtilityPaths)
	for _, forbidden := range []string{"sysctl -w", "sysctl -p", "modprobe tcp_bbr", "\nswapoff ", "\nswapon ", "\nmkswap ", "rm -", "apt-get clean"} {
		if strings.Contains(probe, forbidden) {
			t.Fatal("read-only probe contains mutation", forbidden)
		}
	}
	if strings.Contains(wrapUtilityScript("echo '$unsafe'; # 中文"), "echo '$unsafe'") {
		t.Fatal("raw script interpolated into terminal wrapper")
	}
	if !strings.Contains(wrapUtilityScript("echo test"), base64.StdEncoding.EncodeToString([]byte("echo test"))) {
		t.Fatal("unexpected script encoding")
	}
}

func TestUtilitySwapInspectionAndBlockedPlans(t *testing.T) {
	output := strings.Replace(utilityInspectionFixture, "Filename Type Size Used Priority", "Filename Type Size Used Priority\n/swap\\040file file 524288 1024 -2\n/dev/sda2 partition 1048576 0 -3\n/dev/zram0 partition 262144 2048 100", 1)
	i, err := parseUtilityInspection(output)
	if err != nil || len(i.swaps) != 3 || i.swaps[0].Path != "/swap file" || i.swaps[0].UsedMiB != 1 || i.swaps[1].Type != "partition" || i.swaps[2].Type != "zram" {
		t.Fatalf("swap parsing incorrect: %+v %v", i, err)
	}
	p := buildUtilityPlan(UtilityRequest{Kind: "swap-create", SizeMiB: 512}, i, linuxUtilityPaths)
	if !p.HasSwap || p.Command != "" || !strings.Contains(p.BlockedReason, "重启") {
		t.Fatal("existing swap not blocked", p)
	}
	i, _ = parseUtilityInspection(strings.Replace(utilityInspectionFixture, "Filename Type Size Used Priority", "unavailable", 1))
	if p = buildUtilityPlan(UtilityRequest{Kind: "clean"}, i, linuxUtilityPaths); p.Command == "" {
		t.Fatal("missing proc/swaps incorrectly blocked cache cleaning")
	}
	if p = buildUtilityPlan(UtilityRequest{Kind: "swap-status"}, i, linuxUtilityPaths); p.BlockedReason == "" {
		t.Fatal("missing proc/swaps incorrectly accepted")
	}
	i, _ = parseUtilityInspection(utilityInspectionFixture)
	i.sections["FS"] = "btrfs"
	if p = buildUtilityPlan(UtilityRequest{Kind: "swap-create", SizeMiB: 512}, i, linuxUtilityPaths); !strings.Contains(p.BlockedReason, "Btrfs") {
		t.Fatal("unsafe filesystem accepted")
	}
	i.sections["FS"] = "ext2/ext3"
	i.sections["REMOVED_BOOT"] = i.sections["BOOT"]
	if p = buildUtilityPlan(UtilityRequest{Kind: "swap-create", SizeMiB: 512}, i, linuxUtilityPaths); !p.RebootRequired || p.Command != "" {
		t.Fatal("same-boot recreation not blocked")
	}
	i.sections["BBR"] = "reno cubic"
	if p = buildUtilityPlan(UtilityRequest{Kind: "bbr", Mode: "balanced"}, i, linuxUtilityPaths); p.BlockedReason == "" {
		t.Fatal("missing BBR accepted")
	}
}

// The only privileged command implementations on PATH are these mocks. Every
// path in a generated script is replaced by a t.TempDir() fixture. Real dd may
// write at most the 1 MiB test file; actual sysctl/swapon/swapoff/mkswap and
// package manager operations are never invoked.
const utilityMock = `#!/usr/bin/python3
import sys,os,json,pathlib,subprocess
root=pathlib.Path(os.environ['DENGSHELL_UTILITY_TEST_ROOT'])
tool=pathlib.Path(sys.argv[0]).name
args=sys.argv[1:]
with (root/'calls').open('a') as f:f.write(json.dumps([tool]+args)+'\n')
if tool=='id': print('0');sys.exit()
if tool=='stat':
 if args[0]=='-f':print((root/'filesystem').read_text());sys.exit()
 if args[-1]=='/proc/self/fd/3':
  info=os.fstat(3);print(str(info.st_dev)+':'+str(info.st_ino));sys.exit()
 assert str(pathlib.Path(args[-1])).startswith(str(root)),args
 sys.exit(subprocess.call(['/usr/bin/stat']+args))
if tool=='df':print('Filesystem 1024-blocks Used Available Capacity Mounted on\nfixture 99999999 0 99999999 0% '+str(root));sys.exit()
if tool=='sysctl':
 file=root/'sysctl.json';values=json.loads(file.read_text())
 if args[0]=='-n': print(values[args[1]]);sys.exit()
 if args[0]=='-p':
  for line in pathlib.Path(args[1]).read_text().splitlines():
   if '=' not in line:continue
   key,value=[p.strip() for p in line.split('=',1)]
   fail=root/'fail-sysctl'
   if fail.exists() and fail.read_text()==key and args[1].endswith('new.conf'):sys.exit(1)
   values[key]=value;file.write_text(json.dumps(values))
  sys.exit()
 for key in args:print(key+' = '+values[key])
 sys.exit()
if tool=='modprobe':sys.exit()
if tool=='findfs':
 mappings=json.loads((root/'aliases.json').read_text()) if (root/'aliases.json').exists() else {}
 values=mappings.get(args[0],[])
 if len(values)==1:print(values[0]);sys.exit()
 sys.exit(1)
if tool=='blkid':
 if '-t' in args:
  mappings=json.loads((root/'aliases.json').read_text()) if (root/'aliases.json').exists() else {}
  for value in mappings.get(args[args.index('-t')+1],[]):print(value)
  sys.exit()
 file=pathlib.Path(args[-1]);assert str(file).startswith(str(root))
 if not file.exists():sys.exit(2)
 if file.read_bytes().startswith(b'SWAPFIXTURE'):print('swap');sys.exit()
 sys.exit(2)
if tool=='btrfs':
 if '--help' in args:
  if args[0]=='filesystem' and (root/'legacy-btrfs').exists():sys.exit(1)
  if args[0]=='inspect-internal' and (root/'no-map-btrfs').exists():sys.exit(1)
  sys.exit()
 if args[:2]==['filesystem','show']:
  print('Total devices '+('2' if (root/'multi-device').exists() else '1')+' FS bytes used 1048576');sys.exit()
 if args[:2]==['filesystem','df']:
  print('Data, '+('RAID1:' if (root/'raid-data').exists() else 'single:')+' total=1GiB, used=1MiB');print('Metadata, DUP: total=1GiB, used=1MiB');sys.exit()
 target=pathlib.Path(args[-1]);assert str(target).startswith(str(root))
 if args[:2]==['filesystem','mkswapfile']:
  size=int(args[args.index('-s')+1]);assert size<=1048576
  fd=os.open(target,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
  if not (root/'sparse-btrfs').exists():os.write(fd,b'SWAPFIXTURE'+b'0'*(size-11))
  else:os.ftruncate(fd,size)
  os.close(fd)
  if (root/'publish-collision').exists():(root/'swapfile').write_bytes(b'keep other file')
  if (root/'fail-btrfs-create').exists():sys.exit(1)
  sys.exit()
 if args[:2]==['inspect-internal','map-swapfile']:
  sys.exit(1 if (root/'fail-map').exists() else 0)
 raise RuntimeError('unexpected btrfs args '+str(args))
if tool in ('chattr','lsattr'):
 target=pathlib.Path(args[-1]);assert str(target).startswith(str(root))
 if tool=='lsattr':
  flags='---------------' if (root/'filesystem').read_text()=='f2fs' else '--------C------'
  if (root/'compressed').exists():flags+='c'
  if (root/'no-nocow').exists():flags='---------------'
  print(flags+' '+str(target))
 sys.exit()
if tool=='fallocate':
 assert args[-1]=='/proc/self/fd/3',args
 size=int(args[args.index('-l')+1]);assert size<=1048576
 os.write(3,b'0'*size);os.fsync(3);sys.exit()
if tool in ('mkswap','swapon','swapoff'):
 file=args[-1]; assert file.startswith(str(root)) or file.startswith('/dev/fixture-') or file=='/dev/zram0',file
 if (root/('fail-'+tool)).exists():sys.exit(2)
 if tool=='mkswap':sys.exit()
 proc=root/'proc-swaps';lines=proc.read_text().splitlines();token=file.replace('\\','\\134').replace(' ','\\040')
 if tool=='swapon':lines.append(token+' file 1024 0 -2')
 if tool=='swapoff':lines=[line for line in lines if line.split()[0]!=token]
 proc.write_text('\n'.join(lines)+'\n');sys.exit()
if tool in ('apt-get','dnf','yum','zypper','apk','paccache','journalctl'):sys.exit()
raise RuntimeError('unexpected mock tool '+tool)
`

type utilityTestFixture struct {
	root  string
	paths utilityPaths
	env   []string
}

func newUtilityTestFixture(t *testing.T) utilityTestFixture {
	requireLinuxShellFixture(t)
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.Mkdir(bin, 0700)
	for _, tool := range []string{"id", "stat", "df", "sysctl", "modprobe", "blkid", "mkswap", "swapon", "swapoff", "apt-get", "dnf", "yum", "zypper", "apk", "paccache", "journalctl", "findfs", "btrfs", "chattr", "lsattr", "fallocate"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(utilityMock), 0700); err != nil {
			t.Fatal(err)
		}
	}
	p := utilityPaths{state: filepath.Join(root, "state"), swaps: filepath.Join(root, "proc-swaps"), fstab: filepath.Join(root, "fstab"), boot: filepath.Join(root, "boot"), memory: filepath.Join(root, "meminfo"), swapfile: filepath.Join(root, "swapfile"), filesystem: root, sysctlConfig: filepath.Join(root, "sysctl.d", "99-dengshell.conf")}
	os.WriteFile(p.swaps, []byte("Filename Type Size Used Priority\n"), 0600)
	os.WriteFile(p.fstab, []byte("# keep comment\nUUID=root / ext4 defaults 0 1\n"), 0644)
	os.WriteFile(p.boot, []byte("fixture-boot\n"), 0600)
	os.WriteFile(p.memory, []byte("MemTotal: 1048576 kB\n"), 0600)
	os.WriteFile(filepath.Join(root, "filesystem"), []byte("ext2/ext3"), 0600)
	values := map[string]string{"net.ipv4.tcp_available_congestion_control": "reno cubic bbr", "net.core.default_qdisc": "fq_codel", "net.ipv4.tcp_congestion_control": "cubic", "net.ipv4.tcp_rmem": "4096 131072 6291456", "net.ipv4.tcp_wmem": "4096 16384 4194304", "net.ipv4.tcp_limit_output_bytes": "1048576", "net.ipv4.tcp_moderate_rcvbuf": "1"}
	encoded, _ := json.Marshal(values)
	os.WriteFile(filepath.Join(root, "sysctl.json"), encoded, 0600)
	return utilityTestFixture{root, p, append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "DENGSHELL_UTILITY_TEST_ROOT="+root)}
}

func (f utilityTestFixture) run(t *testing.T, script string, success bool) string {
	t.Helper()
	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script syntax: %s %v", out, err)
	}
	cmd = exec.Command("sh", "-c", script)
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if (err == nil) != success {
		t.Fatalf("unexpected script result (want success=%v): %v\n%s", success, err, out)
	}
	return string(out)
}

func TestUtilityBBRProfilesAndFailureRollbackInFixture(t *testing.T) {
	for _, mode := range []string{"conservative", "balanced", "aggressive"} {
		t.Run(mode, func(t *testing.T) {
			f := newUtilityTestFixture(t)
			f.run(t, bbrUtilityScript(mode, f.paths), true)
			data, _ := os.ReadFile(f.paths.sysctlConfig)
			if !strings.Contains(string(data), "tcp_congestion_control = bbr") || strings.Contains(string(data), "tcp_tw") {
				t.Fatal("wrong or excessive BBR settings", string(data))
			}
		})
	}
	f := newUtilityTestFixture(t)
	original, _ := os.ReadFile(filepath.Join(f.root, "sysctl.json"))
	os.Mkdir(filepath.Dir(f.paths.sysctlConfig), 0700)
	os.WriteFile(f.paths.sysctlConfig, []byte("# previous custom profile\n"), 0600)
	os.WriteFile(filepath.Join(f.root, "fail-sysctl"), []byte("net.ipv4.tcp_rmem"), 0600)
	f.run(t, bbrUtilityScript("aggressive", f.paths), false)
	after, _ := os.ReadFile(filepath.Join(f.root, "sysctl.json"))
	var beforeValues, afterValues map[string]string
	json.Unmarshal(original, &beforeValues)
	json.Unmarshal(after, &afterValues)
	for key, value := range beforeValues {
		if afterValues[key] != value {
			t.Fatal("partial sysctl application not rolled back", key)
		}
	}
	config, _ := os.ReadFile(f.paths.sysctlConfig)
	if string(config) != "# previous custom profile\n" {
		t.Fatal("failed BBR overwrote persistent config")
	}
}

func TestUtilityCacheCleanupScopeInFixture(t *testing.T) {
	f := newUtilityTestFixture(t)
	userData := filepath.Join(f.root, "user-data")
	os.WriteFile(userData, []byte("keep"), 0600)
	f.run(t, cleanUtilityScript(f.paths), true)
	calls, _ := os.ReadFile(filepath.Join(f.root, "calls"))
	if !strings.Contains(string(calls), `["apt-get", "clean"]`) || !strings.Contains(string(calls), `["journalctl", "--vacuum-time=14d"]`) {
		t.Fatal("expected bounded cache cleanup absent", string(calls))
	}
	if data, _ := os.ReadFile(userData); string(data) != "keep" {
		t.Fatal("cache cleanup touched user data")
	}
}

func TestUtilitySwapCreateRechecksAndRollsBackInFixture(t *testing.T) {
	f := newUtilityTestFixture(t)
	f.run(t, createSwapUtilityScript(1, f.paths), true)
	info, err := os.Stat(f.paths.swapfile)
	if err != nil || info.Size() != 1048576 || info.Mode().Perm() != 0600 {
		t.Fatal("incorrect created fixture file", err)
	}
	if data, _ := os.ReadFile(f.paths.fstab); !strings.Contains(string(data), f.paths.swapfile+" none swap sw 0 0") {
		t.Fatal("successful swap not persisted")
	}
	for _, failure := range []string{"existing", "overlay", "swapon", "same-boot", "fstab-configured"} {
		t.Run(failure, func(t *testing.T) {
			f := newUtilityTestFixture(t)
			fstab, _ := os.ReadFile(f.paths.fstab)
			switch failure {
			case "existing":
				os.WriteFile(f.paths.swapfile, []byte("never overwrite"), 0600)
			case "overlay":
				os.WriteFile(filepath.Join(f.root, "filesystem"), []byte("overlayfs"), 0600)
			case "swapon":
				os.WriteFile(filepath.Join(f.root, "fail-swapon"), []byte("yes"), 0600)
			case "same-boot":
				os.Mkdir(f.paths.state, 0700)
				os.WriteFile(filepath.Join(f.paths.state, "swap-removed-boot-id"), []byte("fixture-boot"), 0600)
			case "fstab-configured":
				fstab = append(fstab, []byte("UUID=swap none swap sw 0 0\n")...)
				os.WriteFile(f.paths.fstab, fstab, 0644)
			}
			f.run(t, createSwapUtilityScript(1, f.paths), false)
			if after, _ := os.ReadFile(f.paths.fstab); string(after) != string(fstab) {
				t.Fatal("failed create changed fstab")
			}
			if failure == "existing" {
				if after, _ := os.ReadFile(f.paths.swapfile); string(after) != "never overwrite" {
					t.Fatal("existing path overwritten")
				}
			} else if _, err := os.Stat(f.paths.swapfile); !os.IsNotExist(err) {
				t.Fatal("failed create left unused swap file")
			}
		})
	}
}

func TestUtilitySwapRemovalNeverDeletesDevicesOrUnconfirmedFiles(t *testing.T) {
	f := newUtilityTestFixture(t)
	active := filepath.Join(f.root, "swap quote' $(not-executed)")
	valid := filepath.Join(f.root, "inactive-swap")
	ordinary := filepath.Join(f.root, "ordinary-file")
	os.WriteFile(active, []byte("active swap fixture"), 0600)
	os.WriteFile(valid, []byte("SWAPFIXTURE inactive"), 0600)
	os.WriteFile(ordinary, []byte("user file keep"), 0600)
	token := strings.ReplaceAll(active, " ", `\040`)
	os.WriteFile(f.paths.swaps, []byte("Filename Type Size Used Priority\n"+token+" file 1024 0 -2\n/dev/fixture-partition partition 2048 0 -3\n/dev/zram0 partition 1024 0 100\n"), 0600)
	baseFstab, _ := os.ReadFile(f.paths.fstab)
	os.WriteFile(f.paths.fstab, append(baseFstab, []byte(token+" none swap sw 0 0\nUUID=swap none swap defaults 0 0\n"+valid+" none swap sw 0 0\n"+ordinary+" none swap sw 0 0\n")...), 0644)
	entries := []UtilitySwap{{Type: "file", Path: active, Active: true, token: token, procType: "file"}, {Type: "partition", Path: "/dev/fixture-partition", Active: true, token: "/dev/fixture-partition", procType: "partition"}, {Type: "zram", Path: "/dev/zram0", Active: true, token: "/dev/zram0", procType: "partition"}, {Type: "configured", Path: valid}, {Type: "configured", Path: ordinary}}
	output := f.run(t, removeSwapUtilityScript(entries, f.paths), true)
	for _, file := range []string{active, valid} {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatal("verified swap file not removed", file)
		}
	}
	if data, _ := os.ReadFile(ordinary); string(data) != "user file keep" || !strings.Contains(output, "人工检查") {
		t.Fatal("unverified regular file not preserved/not explained")
	}
	if data, _ := os.ReadFile(f.paths.fstab); string(data) != string(baseFstab) {
		t.Fatal("non-swap fstab content changed", string(data))
	}
	script := removeSwapUtilityScript(entries, f.paths)
	if strings.Contains(script, "rm -f -- '/dev/") {
		t.Fatal("device removal command generated")
	}
	if data, _ := os.ReadFile(filepath.Join(f.paths.state, "swap-removed-boot-id")); strings.TrimSpace(string(data)) != "fixture-boot" {
		t.Fatal("restart requirement marker not saved")
	}
}

func TestUtilitySwapOffFailurePreservesFileAndFstab(t *testing.T) {
	f := newUtilityTestFixture(t)
	active := filepath.Join(f.root, "swapfile")
	os.WriteFile(active, []byte("keep original swap"), 0600)
	os.WriteFile(f.paths.swaps, []byte("Filename Type Size Used Priority\n"+active+" file 1024 100 -2\n"), 0600)
	os.WriteFile(filepath.Join(f.root, "fail-swapoff"), []byte("yes"), 0600)
	original, _ := os.ReadFile(f.paths.fstab)
	f.run(t, removeSwapUtilityScript([]UtilitySwap{{Type: "file", Path: active, Active: true, token: active, procType: "file"}}, f.paths), false)
	if data, _ := os.ReadFile(active); string(data) != "keep original swap" {
		t.Fatal("swapoff failure deleted file")
	}
	if data, _ := os.ReadFile(f.paths.fstab); string(data) != string(original) {
		t.Fatal("swapoff failure modified fstab")
	}
}

func TestUtilitySwapCreateDoesNotDeleteAReplacedPath(t *testing.T) {
	f := newUtilityTestFixture(t)
	mock := `#!/usr/bin/python3
import os,pathlib,sys
root=pathlib.Path(os.environ['DENGSHELL_UTILITY_TEST_ROOT'])
target=root/'swapfile'
target.rename(root/'original-inode')
target.write_bytes(b'new unrelated file must survive')
os.write(1,b'writes go to the held original descriptor')
sys.exit(1)
`
	if err := os.WriteFile(filepath.Join(f.root, "bin", "dd"), []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}
	output := f.run(t, createSwapUtilityScript(1, f.paths), false)
	data, _ := os.ReadFile(f.paths.swapfile)
	if string(data) != "new unrelated file must survive" || !strings.Contains(output, "身份已变化") {
		t.Fatal("changed path was touched or unexplained", output)
	}
	old, _ := os.ReadFile(filepath.Join(f.root, "original-inode"))
	if string(old) != "writes go to the held original descriptor" {
		t.Fatal("dd reopened the pathname instead of the exclusive descriptor")
	}
}

func TestUtilityWrapperPreservesExitCodesAndShellVariables(t *testing.T) {
	f := newUtilityTestFixture(t)
	run := func(script string, expected int) {
		t.Helper()
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = f.env
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			var status *exec.ExitError
			if !errors.As(err, &status) {
				t.Fatal(err)
			}
			code = status.ExitCode()
		}
		if code != expected {
			t.Fatalf("wrapper exit=%d want=%d output=%s", code, expected, out)
		}
	}
	run(wrapUtilityScript("exit 7"), 7)
	run("dengshell_utility_script=keep; "+wrapUtilityScript("exit 0")+`; [ "$dengshell_utility_script" = keep ]`, 0)
	os.WriteFile(filepath.Join(f.root, "bin", "id"), []byte("#!/bin/sh\nprintf '1000\\n'\n"), 0700)
	os.WriteFile(filepath.Join(f.root, "bin", "sudo"), []byte("#!/bin/sh\nexit 31\n"), 0700)
	run(wrapUtilityScript("exit 0"), 31)
}

func TestUtilityProbeOnlyReadsFixture(t *testing.T) {
	f := newUtilityTestFixture(t)
	before, _ := os.ReadFile(filepath.Join(f.root, "sysctl.json"))
	cmd := exec.Command("sh", "-c", utilityProbe(f.paths))
	cmd.Env = f.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	i, err := parseUtilityInspection(string(output))
	if err != nil || i.sections["OS"] != "Linux" || i.sections["BBR"] != "reno cubic bbr" || i.swapError != "" {
		t.Fatal("read-only probe parse failed", err, string(output))
	}
	after, _ := os.ReadFile(filepath.Join(f.root, "sysctl.json"))
	if string(before) != string(after) {
		t.Fatal("plan probe modified sysctl")
	}
	if _, err := os.Stat(f.paths.state); !os.IsNotExist(err) {
		t.Fatal("plan probe created operation state")
	}
}
