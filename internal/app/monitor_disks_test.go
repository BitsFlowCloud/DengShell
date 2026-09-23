package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMonitorDiskCapacity(t *testing.T) {
	const gib = uint64(1 << 30)
	cases := []struct {
		name, df, mounts string
		want             []Disk
	}{
		{
			name: "separate NVMe root home and EFI",
			df: `/dev/nvme0n1p2 97517568 9646899 87464345 10% /
/dev/nvme0n1p4 3758096384 1073741824 2684354560 29% /home
/dev/nvme0n1p1 999424 6144 993280 1% /boot/efi
tmpfs 33554432 100 33554332 1% /dev/shm`,
			mounts: `259:2 / / ext4 /dev/nvme0n1p2
259:4 / /home ext4 /dev/nvme0n1p4
259:1 / /boot/efi vfat /dev/nvme0n1p1
0:29 / /dev/shm tmpfs tmpfs`,
			want: []Disk{{"/", 97517568 * 1024, 9646899 * 1024, 87464345 * 1024}, {"/home", 3584 * gib, 1024 * gib, 2560 * gib}, {"/boot/efi", 976 << 20, 6 << 20, 970 << 20}},
		},
		{
			name: "LVM aliases binds independent disks and equal size volumes",
			df: `/dev/mapper/vg-root 104857600 20971520 78643200 21% /
/dev/dm-0 104857600 20971520 78643200 21% /mnt/root-copy
/dev/mapper/vg-root 104857600 20971520 78643200 21% /srv/bind
/dev/sdb1 104857600 10485760 94371840 10% /data
/dev/sdc1 104857600 10485760 94371840 10% /backup`,
			mounts: `253:0 / / ext4 /dev/mapper/vg-root
253:0 / /mnt/root-copy ext4 /dev/dm-0
253:0 /var/lib /srv/bind ext4 /dev/mapper/vg-root
8:17 / /data xfs /dev/sdb1
8:33 / /backup xfs /dev/sdc1`,
			want: []Disk{{"/", 100 * gib, 20 * gib, 75 * gib}, {"/data", 100 * gib, 10 * gib, 90 * gib}, {"/backup", 100 * gib, 10 * gib, 90 * gib}},
		},
		{
			name: "Btrfs subvolumes share device capacity",
			df: `/dev/sda2 104857600 20971520 78643200 21% /
/dev/sda2 104857600 20971520 78643200 21% /home
/dev/sda2 104857600 20971520 78643200 21% /.snapshots`,
			mounts: `0:30 /@ / btrfs /dev/sda2
0:31 /@home /home btrfs /dev/sda2
0:32 /@snapshots /.snapshots btrfs /dev/sda2`,
			want: []Disk{{"/", 100 * gib, 20 * gib, 75 * gib}},
		},
		{
			name: "virtual remote and container mounts do not inflate capacity",
			df: `/dev/sda1 104857600 20971520 78643200 21% /
overlay 104857600 20971520 78643200 21% /custom/overlay/merged
tmpfs 104857600 20971520 78643200 21% /run
/dev/loop0 104857600 20971520 78643200 21% /snap/test/1
/dev/zram0 104857600 20971520 78643200 21% /zram
remote:/data 104857600 20971520 78643200 21% /nfs
//host/share 104857600 20971520 78643200 21% /smb
none 104857600 20971520 78643200 21% /dev
/dev/sr0 104857600 20971520 78643200 21% /media/cd
efivarfs 192 100 92 52% /sys/firmware/efi/efivars`,
			mounts: `8:1 / / ext4 /dev/sda1
0:31 / /custom/overlay/merged overlay overlay
11:0 / /media/cd iso9660 /dev/sr0`,
			want: []Disk{{"/", 100 * gib, 20 * gib, 75 * gib}},
		},
		{
			name: "mountinfo unavailable still filters and deduplicates df sources",
			df: `/dev/sda1 104857600 20971520 78643200 21% /
/dev/sda1 104857600 20971520 78643200 21% /other
/dev/sdb1 104857600 10485760 94371840 10% /data
tmpfs 104857600 20971520 78643200 21% /run`,
			want: []Disk{{"/", 100 * gib, 20 * gib, 75 * gib}, {"/data", 100 * gib, 10 * gib, 90 * gib}},
		},
		{
			name: "mount paths with spaces match escaped metadata",
			df: `/dev/dm-0 104857600 20971520 78643200 21% /data disk
/dev/mapper/data 104857600 20971520 78643200 21% /other disk`,
			mounts: `253:0 / /data\040disk ext4 /dev/dm-0
253:0 / /other\040disk ext4 /dev/mapper/data`,
			want: []Disk{{"/data disk", 100 * gib, 20 * gib, 75 * gib}},
		},
		{
			name: "container root fallback excludes host file binds",
			df: `overlay 104857600 20971520 78643200 21% /
/dev/sda1 104857600 20971520 78643200 21% /etc/hosts
tmpfs 104857600 20971520 78643200 21% /dev`,
			mounts: `0:31 / / overlay overlay
8:1 /var/lib/docker/containers/id/hosts /etc/hosts ext4 /dev/sda1`,
			want: []Disk{{"/", 100 * gib, 20 * gib, 75 * gib}},
		},
		{
			name: "pooled root capacity is not replaced with only EFI",
			df: `rpool/ROOT/default 104857600 20971520 78643200 21% /
/dev/sda1 999424 6144 993280 1% /boot/efi`,
			mounts: `0:30 / / zfs rpool/ROOT/default
8:1 / /boot/efi vfat /dev/sda1`,
			want: []Disk{{"/", 100 * gib, 20 * gib, 75 * gib}},
		},
		{
			name: "negative available after consuming reserved space",
			df: `/dev/sda1 1000 950 -10 101% /
/dev/sdb1 invalid 10 10 50% /data
/dev/sdc1 18446744073709551615 10 10 50% /overflow`,
			want: []Disk{{"/", 1000 * 1024, 950 * 1024, 0}},
		},
		{name: "empty capacity stays unavailable", df: "Filesystem 1024-blocks Used Available Capacity Mounted on\nnone 0 0 0 0% /zero", want: []Disk{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseStats([]byte("__CS_STAT__\ncpu 100 0 0 100 0 0 0 0\n__CS_MEM__\nMemTotal: 1024 kB\n__CS_MOUNTS__\n"+tc.mounts+"\n__CS_DF__\n"+tc.df), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r.Disks, tc.want) {
				t.Fatalf("got %+v; want %+v", r.Disks, tc.want)
			}
			// Make the same parsed snapshot available to the actual browser test.
			if dir := os.Getenv("DENG_DISK_QA_DIR"); dir != "" && tc.name == cases[0].name {
				data, err := json.Marshal(r.Stats)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "disk-fixture.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMonitorDiskThousandsOfBindMounts(t *testing.T) {
	df := []string{"/dev/sda1 1000 200 750 20% /"}
	mounts := []string{"8:1 / / ext4 /dev/sda1"}
	for i := 0; i < 5000; i++ {
		df = append(df, fmt.Sprintf("/dev/disk/by-id/root 1000 200 750 20%% /mnt/bind-%d", i))
		mounts = append(mounts, fmt.Sprintf("8:1 / /mnt/bind-%d ext4 /dev/disk/by-id/root", i))
	}
	got := parseMonitorDisks(df, mounts)
	if !reflect.DeepEqual(got, []Disk{{"/", 1000 * 1024, 200 * 1024, 750 * 1024}}) {
		t.Fatalf("bind mounts counted more than once: %+v", got)
	}
}

func TestMonitorDiskLiveCollector(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux procfs collector")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "sh", "-c", monitorCommandFor(true, false)).Output()
	if err != nil {
		t.Fatal(err)
	}
	r, err := parseStats(data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "__CS_MOUNTS__\n") || len(r.Disks) == 0 {
		t.Fatal("collector lost mounted filesystem metadata/capacity")
	}
	for _, disk := range r.Disks {
		output, err := exec.CommandContext(ctx, "env", "LC_ALL=C", "df", "-Pk", disk.Path).Output()
		if err != nil {
			t.Fatal(err)
		}
		f := strings.Fields(strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[1]))
		if disk.Total != number(f[1])*1024 {
			t.Fatalf("collector disagrees with df for %s: %+v, %s", disk.Path, disk, output)
		}
	}
}
