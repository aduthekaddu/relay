package system

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Synthetic /proc and /sys fixtures.

const fixStat1 = `cpu  1000 0 500 8000 500 0 0 0 0 0
cpu0 500 0 250 4000 250 0 0 0 0 0
cpu1 500 0 250 4000 250 0 0 0 0 0
intr 12345
ctxt 999
btime 1700000000
processes 4242
procs_running 2
`

// 1000 more jiffies per core: core0 fully busy, core1 half busy.
const fixStat2 = `cpu  2500 0 500 8500 500 0 0 0 0 0
cpu0 1500 0 250 4000 250 0 0 0 0 0
cpu1 1000 0 250 4500 250 0 0 0 0 0
btime 1700000000
`

const fixMeminfo = `MemTotal:       16000000 kB
MemFree:         2000000 kB
MemAvailable:    8000000 kB
Buffers:          500000 kB
Cached:          3000000 kB
SwapCached:            0 kB
SReclaimable:     500000 kB
SwapTotal:       4000000 kB
SwapFree:        3000000 kB
`

const fixCPUInfo = `processor	: 0
vendor_id	: SyntheticVendor
model name	: Synthetic CPU 9000 @ 3.00GHz
processor	: 1
model name	: Synthetic CPU 9000 @ 3.00GHz
`

const fixNetDev1 = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  999999     100    0    0    0     0          0         0   999999     100    0    0    0     0       0          0
  eth0: 1000000    1000    0    0    0     0          0         0   500000     500    0    0    0     0       0          0
docker0:  200000     10    0    0    0     0          0         0   200000      10    0    0    0     0       0          0
`

const fixNetDev2 = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1999999     100    0    0    0     0          0         0  1999999     100    0    0    0     0       0          0
  eth0: 1100000    1000    0    0    0     0          0         0   550000     500    0    0    0     0       0          0
docker0:  900000     10    0    0    0     0          0         0   900000      10    0    0    0     0       0          0
`

const fixMounts = `sysfs /sys sysfs rw,nosuid 0 0
proc /proc proc rw 0 0
/dev/sda1 / ext4 rw,relatime 0 0
/dev/sda1 /var/lib/docker/overlay ext4 rw 0 0
/dev/sda1 /srv/bind ext4 rw 0 0
/dev/sdb1 /mnt/my\040data xfs rw 0 0
tmpfs /run tmpfs rw 0 0
/dev/loop3 /snap/core/1 squashfs ro 0 0
/dev/sdc1 /run/media/usb vfat rw 0 0
server:/export /mnt/nfs nfs4 rw 0 0
overlay /var/lib/docker/overlay2/x/merged overlay rw 0 0
`

const fixDiskstats1 = `   8       0 sda 100 0 2000 0 50 0 4000 0 0 0 0
   8       1 sda1 100 0 2000 0 50 0 4000 0 0 0 0
   8      17 sdb1 10 0 100 0 5 0 100 0 0 0 0
`

const fixDiskstats2 = `   8       0 sda 100 0 4048 0 50 0 8096 0 0 0 0
   8       1 sda1 100 0 4048 0 50 0 8096 0 0 0 0
   8      17 sdb1 10 0 100 0 5 0 100 0 0 0 0
`

type fixture struct {
	proc, sys, etc string
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{proc: filepath.Join(root, "proc"), sys: filepath.Join(root, "sys"), etc: filepath.Join(root, "etc")}
	writeFile(t, filepath.Join(f.proc, "stat"), fixStat1)
	writeFile(t, filepath.Join(f.proc, "meminfo"), fixMeminfo)
	writeFile(t, filepath.Join(f.proc, "cpuinfo"), fixCPUInfo)
	writeFile(t, filepath.Join(f.proc, "loadavg"), "0.50 1.25 2.00 2/345 6789\n")
	writeFile(t, filepath.Join(f.proc, "uptime"), "12345.67 20000.00\n")
	writeFile(t, filepath.Join(f.proc, "net/dev"), fixNetDev1)
	writeFile(t, filepath.Join(f.proc, "self/mounts"), fixMounts)
	writeFile(t, filepath.Join(f.proc, "diskstats"), fixDiskstats1)
	writeFile(t, filepath.Join(f.proc, "sys/kernel/osrelease"), "6.1.0-synthetic\n")
	writeFile(t, filepath.Join(f.etc, "os-release"), "NAME=\"Synthix\"\nPRETTY_NAME=\"Synthix 1.0 (Test)\"\n")
	writeFile(t, filepath.Join(f.etc, "passwd"), "root:x:0:0::/root:/bin/sh\ndev:x:"+strconv.Itoa(os.Getuid())+":1000::/home/dev:/bin/sh\n")
	// /sys: eth0 is a physical NIC, docker0 is virtual.
	writeFile(t, filepath.Join(f.sys, "class/net/eth0/device/uevent"), "")
	writeFile(t, filepath.Join(f.sys, "class/net/docker0/type"), "1\n")
	writeFile(t, filepath.Join(f.sys, "class/thermal/thermal_zone0/type"), "acpitz\n")
	writeFile(t, filepath.Join(f.sys, "class/thermal/thermal_zone0/temp"), "40000\n")
	writeFile(t, filepath.Join(f.sys, "class/thermal/thermal_zone1/type"), "x86_pkg_temp\n")
	writeFile(t, filepath.Join(f.sys, "class/thermal/thermal_zone1/temp"), "55500\n")
	// Two processes with pids unlikely to collide with anything real
	// (the lister only reads the fixture tree).
	writeProc(t, f.proc, 4001, "4001 (bash) S 1 4001 4001 0 -1 0 0 0 0 0 100 50 0 0 20 0 1 0 5000 1000000 256 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n", "bash\x00-l\x00")
	writeProc(t, f.proc, 4002, "4002 (my (weird) proc) R 4001 4001 4001 0 -1 0 0 0 0 0 300 100 0 0 20 0 4 0 6000 2000000 1024 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n", "python3\x00-m\x00http.server\x00")
	return f
}

func writeProc(t *testing.T, proc string, pid int, stat, cmd string) {
	t.Helper()
	dir := filepath.Join(proc, strconv.Itoa(pid))
	writeFile(t, filepath.Join(dir, "stat"), stat)
	writeFile(t, filepath.Join(dir, "cmdline"), cmd)
}

func (f fixture) collector() *Collector {
	return &Collector{
		Proc: f.proc, Sys: f.sys, Etc: f.etc, thermal: "-",
		Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte("kvm\n"), nil
		},
		statfsFn: func(path string) (uint64, uint64, uint64, error) {
			return 100 << 30, 40 << 30, 35 << 30, nil
		},
	}
}

// primeAt sets the previous-sample time so rates use a known interval.
func (c *Collector) primeAt(t time.Time) {
	c.mu.Lock()
	c.prevAt = t
	c.mu.Unlock()
}
