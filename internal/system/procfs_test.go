package system

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

func TestParseProcStat(t *testing.T) {
	all, cores, bt := parseProcStat([]byte(fixStat1))
	if all.total != 10000 || all.idle != 8500 {
		t.Fatalf("aggregate = %+v", all)
	}
	if len(cores) != 2 || cores[0].total != 5000 || cores[0].idle != 4250 {
		t.Fatalf("cores = %+v", cores)
	}
	if bt != 1700000000 {
		t.Fatalf("btime = %d", bt)
	}
}

func TestCPUPercent(t *testing.T) {
	tests := []struct {
		name      string
		prev, cur cpuTimes
		want      float64
	}{
		{"idle", cpuTimes{idle: 100, total: 100}, cpuTimes{idle: 200, total: 200}, 0},
		{"busy", cpuTimes{idle: 100, total: 100}, cpuTimes{idle: 100, total: 200}, 100},
		{"half", cpuTimes{idle: 0, total: 0}, cpuTimes{idle: 50, total: 100}, 50},
		{"no time", cpuTimes{idle: 5, total: 5}, cpuTimes{idle: 5, total: 5}, 0},
		{"counter reset", cpuTimes{idle: 500, total: 1000}, cpuTimes{idle: 1, total: 2}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cpuPercent(tt.prev, tt.cur); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestParseMeminfoAndStats(t *testing.T) {
	ms := memStats(parseMeminfo([]byte(fixMeminfo)))
	want := api.MemStats{
		Total: 16000000 * 1024, Available: 8000000 * 1024, Used: 8000000 * 1024,
		Cached: 4000000 * 1024, SwapTotal: 4000000 * 1024, SwapUsed: 1000000 * 1024,
	}
	if ms != want {
		t.Fatalf("got %+v\nwant %+v", ms, want)
	}
	// Old kernels without MemAvailable.
	old := memStats(parseMeminfo([]byte("MemTotal: 1000 kB\nMemFree: 200 kB\nCached: 300 kB\n")))
	if old.Available != 500*1024 || old.Used != 500*1024 {
		t.Fatalf("fallback available = %+v", old)
	}
}

func TestParseCPUModel(t *testing.T) {
	tests := map[string]string{
		fixCPUInfo: "Synthetic CPU 9000 @ 3.00GHz",
		"processor : 0\nBogoMIPS : 50\nHardware : Synthetic Board\n": "Synthetic Board",
		"": "",
	}
	for in, want := range tests {
		if got := parseCPUModel([]byte(in)); got != want {
			t.Errorf("parseCPUModel = %q want %q", got, want)
		}
	}
}

func TestParseSmallFiles(t *testing.T) {
	if got := parseLoadavg([]byte("0.50 1.25 2.00 2/345 6789\n")); got != [3]float64{0.5, 1.25, 2} {
		t.Errorf("loadavg = %v", got)
	}
	if got := parseUptime([]byte("12345.67 20000.00\n")); got != 12345 {
		t.Errorf("uptime = %v", got)
	}
	if got := parseOSRelease([]byte("NAME=Foo\nVERSION=\"2 (x)\"\n")); got != "Foo 2 (x)" {
		t.Errorf("os-release = %q", got)
	}
}

func TestParseNetDevAndVirtual(t *testing.T) {
	ifs := parseNetDev([]byte(fixNetDev1))
	if len(ifs) != 3 || ifs[1].name != "eth0" || ifs[1].rx != 1000000 || ifs[1].tx != 500000 {
		t.Fatalf("ifs = %+v", ifs)
	}
	f := newFixture(t)
	tests := []struct {
		sys, name string
		want      bool
	}{
		{f.sys, "lo", true},
		{f.sys, "eth0", false},   // has device/
		{f.sys, "docker0", true}, // in /sys without device/
		{"", "veth12ab", true},   // prefix fallback
		{"", "enp3s0", false},    // prefix fallback
		{"", "tailscale0", true}, // prefix fallback
		{f.sys, "wlan0", false},  // not in fixture /sys: prefix fallback
	}
	for _, tt := range tests {
		if got := isVirtualIface(tt.sys, tt.name); got != tt.want {
			t.Errorf("isVirtualIface(%q) = %v want %v", tt.name, got, tt.want)
		}
	}
}

func TestParseMounts(t *testing.T) {
	got := parseMounts([]byte(fixMounts))
	want := []mountInfo{
		{"/dev/sda1", "/", "ext4"},
		{"/dev/sdb1", "/mnt/my data", "xfs"},
		{"/dev/sdc1", "/run/media/usb", "vfat"},
		{"server:/export", "/mnt/nfs", "nfs4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseDiskstats(t *testing.T) {
	io := parseDiskstats([]byte(fixDiskstats1))
	if io["sda1"] != (diskIO{read: 2000 * 512, written: 4000 * 512}) {
		t.Fatalf("sda1 = %+v", io["sda1"])
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	got := parseNvidiaSMI([]byte("Synthetic GPU A, 37, 1024, 8192, 61\nbad line\nSynthetic GPU B, [N/A], 0, 4096, [N/A]\n"))
	want := []api.GPUStats{
		{Name: "Synthetic GPU A", Util: 37, MemUsed: 1024 << 20, MemTotal: 8192 << 20, TempC: 61},
		{Name: "Synthetic GPU B", MemTotal: 4096 << 20},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestCollectorSample(t *testing.T) {
	f := newFixture(t)
	c := f.collector()
	first := c.sample(context.Background())
	if first.CPU.Percent != 0 || first.Net.RxBps != 0 {
		t.Fatalf("first sample must not report rates: %+v", first.CPU)
	}
	writeFile(t, filepath.Join(f.proc, "stat"), fixStat2)
	writeFile(t, filepath.Join(f.proc, "net/dev"), fixNetDev2)
	writeFile(t, filepath.Join(f.proc, "diskstats"), fixDiskstats2)
	c.primeAt(time.Now().Add(-2 * time.Second))
	m := c.sample(context.Background())

	if m.CPU.Percent != 75 || !reflect.DeepEqual(m.CPU.PerCore, []float64{100, 50}) || m.CPU.Cores != 2 {
		t.Errorf("cpu = %+v", m.CPU)
	}
	if m.CPU.Model != "Synthetic CPU 9000 @ 3.00GHz" || m.CPU.TempC != 55.5 {
		t.Errorf("model/temp = %q %v", m.CPU.Model, m.CPU.TempC)
	}
	near := func(got, want float64) bool { return math.Abs(got-want) <= want*0.05 }
	// Only eth0 counts: lo is skipped and docker0 is virtual.
	if m.Net.RxTotal != 1100000 || m.Net.TxTotal != 550000 || !near(m.Net.RxBps, 50000) || !near(m.Net.TxBps, 25000) {
		t.Errorf("net = %+v", m.Net)
	}
	if len(m.Disks) != 4 || m.Disks[0].Mount != "/" || m.Disks[0].Used != 60<<30 || m.Disks[0].Free != 35<<30 {
		t.Fatalf("disks = %+v", m.Disks)
	}
	if !near(m.Disks[0].ReadBps, 1<<19) || !near(m.Disks[0].WriteBps, 1<<20) {
		t.Errorf("disk io = %v %v", m.Disks[0].ReadBps, m.Disks[0].WriteBps)
	}
	if m.Load != [3]float64{0.5, 1.25, 2} || m.Uptime != 12345 || m.Procs != 2 {
		t.Errorf("load/uptime/procs = %v %v %v", m.Load, m.Uptime, m.Procs)
	}
	if m.Memory.Total != 16000000*1024 {
		t.Errorf("memory = %+v", m.Memory)
	}
	if m.Host.OS != "Synthix 1.0 (Test)" || m.Host.Kernel != "6.1.0-synthetic" || m.Host.Virt != "kvm" || m.Host.Arch == "" {
		t.Errorf("host = %+v", m.Host)
	}
}

func TestCollectorGPUCached(t *testing.T) {
	f := newFixture(t)
	c := f.collector()
	c.gpuTool = "nvidia-smi"
	calls := 0
	c.Exec = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "nvidia-smi" {
			calls++
			return []byte("Synthetic GPU, 10, 1, 2, 30\n"), nil
		}
		return nil, os.ErrNotExist
	}
	now := time.Now()
	for i := 0; i < 3; i++ {
		if g := c.gpuStats(context.Background(), now.Add(time.Duration(i)*time.Second)); len(g) != 1 {
			t.Fatalf("gpu = %+v", g)
		}
	}
	if calls != 1 {
		t.Fatalf("nvidia-smi ran %d times within 5 s, want 1", calls)
	}
	c.gpuStats(context.Background(), now.Add(6*time.Second))
	if calls != 2 {
		t.Fatalf("nvidia-smi not re-run after 5 s")
	}
}

func TestUnescapeMount(t *testing.T) {
	for in, want := range map[string]string{`/a\040b`: "/a b", `/plain`: "/plain", `/x\011y`: "/x\ty", `/bad\9`: `/bad\9`} {
		if got := unescapeMount(in); got != want {
			t.Errorf("unescapeMount(%q) = %q", in, got)
		}
	}
}

func TestRing(t *testing.T) {
	r := ring{buf: make([]api.Metrics, 3)}
	base := time.Now()
	for i := 0; i < 5; i++ {
		r.push(api.Metrics{At: base.Add(time.Duration(i) * time.Minute), Procs: i})
	}
	got := r.since(time.Time{})
	if len(got) != 3 || got[0].Procs != 2 || got[2].Procs != 4 {
		t.Fatalf("ring = %+v", got)
	}
	if got := r.since(base.Add(4 * time.Minute)); len(got) != 1 || got[0].Procs != 4 {
		t.Fatalf("since = %+v", got)
	}
}
