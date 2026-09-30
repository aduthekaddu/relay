package system

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Collector samples machine metrics from /proc and /sys. Roots are
// injectable so tests run against fixture trees. It is safe for
// concurrent use; rates are computed against the previous sample.
type Collector struct {
	Proc string // "/proc"
	Sys  string // "/sys"
	Etc  string // "/etc"

	// Exec runs helper tools (nvidia-smi, systemd-detect-virt). nil = os/exec.
	Exec func(ctx context.Context, name string, args ...string) ([]byte, error)

	mu       sync.Mutex
	prevAt   time.Time
	prevCPU  cpuTimes
	prevCore []cpuTimes
	prevNet  map[string]netCounters
	prevDisk map[string]diskIO

	host     *api.HostInfo
	model    string
	gpuAt    time.Time
	gpu      []api.GPUStats
	gpuTool  string
	thermal  string // chosen thermal zone temp file ("" = none, "-" = not probed)
	statfsFn func(path string) (total, free, avail uint64, err error)
}

// NewCollector returns a collector for the real machine.
func NewCollector() *Collector {
	c := &Collector{Proc: "/proc", Sys: "/sys", Etc: "/etc", thermal: "-", statfsFn: statfs}
	if p, err := exec.LookPath("nvidia-smi"); err == nil {
		c.gpuTool = p
	}
	return c
}

func (c *Collector) read(root, rel string) []byte {
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return nil
	}
	return b
}

func (c *Collector) run(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if c.Exec != nil {
		return c.Exec(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	return cmd.Output()
}

// Available reports whether /proc looks usable (Linux).
func (c *Collector) Available() bool {
	_, err := os.Stat(filepath.Join(c.Proc, "stat"))
	return err == nil
}

// Sample returns current metrics. Rates (CPU %, IO, network) are relative
// to the previous call; the very first call primes the counters with a
// short 250 ms interval.
func (c *Collector) Sample(ctx context.Context) api.Metrics {
	c.mu.Lock()
	primed := !c.prevAt.IsZero()
	c.mu.Unlock()
	if !primed {
		c.sample(ctx)
		t := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
		case <-t.C:
		}
		t.Stop()
	}
	return c.sample(ctx)
}

func (c *Collector) sample(ctx context.Context) api.Metrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	m := api.Metrics{At: now.UTC()}
	dt := now.Sub(c.prevAt).Seconds()
	if c.prevAt.IsZero() || dt <= 0 {
		dt = 0
	}

	all, cores, _ := parseProcStat(c.read(c.Proc, "stat"))
	m.CPU.Cores = len(cores)
	if m.CPU.Cores == 0 {
		m.CPU.Cores = runtime.NumCPU()
	}
	m.CPU.PerCore = make([]float64, len(cores))
	if dt > 0 {
		m.CPU.Percent = round1(cpuPercent(c.prevCPU, all))
		for i := range cores {
			if i < len(c.prevCore) {
				m.CPU.PerCore[i] = round1(cpuPercent(c.prevCore[i], cores[i]))
			}
		}
	}
	c.prevCPU, c.prevCore = all, cores
	if c.model == "" {
		c.model = parseCPUModel(c.read(c.Proc, "cpuinfo"))
	}
	m.CPU.Model = c.model
	m.CPU.TempC = c.temperature()

	mi := parseMeminfo(c.read(c.Proc, "meminfo"))
	m.Memory = memStats(mi)

	m.Load = parseLoadavg(c.read(c.Proc, "loadavg"))
	m.Uptime = parseUptime(c.read(c.Proc, "uptime"))
	m.Procs = c.countProcs()
	m.Net = c.netStats(dt)
	m.Disks = c.diskStats(dt)
	m.GPU = c.gpuStats(ctx, now)
	m.Host = c.hostInfo(ctx)
	c.prevAt = now
	return m
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func memStats(mi map[string]uint64) api.MemStats {
	ms := api.MemStats{
		Total:     mi["MemTotal"],
		Available: mi["MemAvailable"],
		Cached:    mi["Cached"] + mi["SReclaimable"] + mi["Buffers"],
		SwapTotal: mi["SwapTotal"],
	}
	if ms.Available == 0 { // kernels < 3.14
		ms.Available = mi["MemFree"] + ms.Cached
	}
	if ms.Total > ms.Available {
		ms.Used = ms.Total - ms.Available
	}
	if ms.SwapTotal > mi["SwapFree"] {
		ms.SwapUsed = ms.SwapTotal - mi["SwapFree"]
	}
	return ms
}

func (c *Collector) countProcs() int {
	des, err := os.ReadDir(c.Proc)
	if err != nil {
		return 0
	}
	n := 0
	for _, de := range des {
		if name := de.Name(); name != "" && name[0] >= '0' && name[0] <= '9' {
			n++
		}
	}
	return n
}

func (c *Collector) netStats(dt float64) api.NetStats {
	ifs := parseNetDev(c.read(c.Proc, "net/dev"))
	var phys, other []netCounters
	for _, n := range ifs {
		if n.name == "lo" {
			continue
		}
		if isVirtualIface(c.Sys, n.name) {
			other = append(other, n)
		} else {
			phys = append(phys, n)
		}
	}
	use := phys
	if len(use) == 0 { // containers: only veth/virtual interfaces exist
		use = other
	}
	var ns api.NetStats
	next := make(map[string]netCounters, len(use))
	for _, n := range use {
		ns.RxTotal += n.rx
		ns.TxTotal += n.tx
		next[n.name] = n
		if p, ok := c.prevNet[n.name]; ok && dt > 0 && n.rx >= p.rx && n.tx >= p.tx {
			ns.RxBps += float64(n.rx-p.rx) / dt
			ns.TxBps += float64(n.tx-p.tx) / dt
		}
	}
	ns.RxBps, ns.TxBps = float64(int64(ns.RxBps)), float64(int64(ns.TxBps))
	c.prevNet = next
	return ns
}

// deviceName maps a mount source (/dev/sda1, /dev/mapper/x) to its
// /proc/diskstats name (sda1, dm-0).
func deviceName(dev string) string {
	if real, err := filepath.EvalSymlinks(dev); err == nil {
		dev = real
	}
	return filepath.Base(dev)
}

func (c *Collector) diskStats(dt float64) []api.DiskStats {
	mounts := parseMounts(c.read(c.Proc, "self/mounts"))
	io := parseDiskstats(c.read(c.Proc, "diskstats"))
	out := make([]api.DiskStats, 0, len(mounts))
	next := make(map[string]diskIO, len(mounts))
	for _, mt := range mounts {
		total, free, avail, err := c.statfsFn(mt.mount)
		if err != nil || total == 0 {
			continue
		}
		d := api.DiskStats{Mount: mt.mount, FS: mt.fs, Total: total, Free: avail}
		if total > free {
			d.Used = total - free
		}
		name := deviceName(mt.device)
		if cur, ok := io[name]; ok {
			next[name] = cur
			if p, ok := c.prevDisk[name]; ok && dt > 0 && cur.read >= p.read && cur.written >= p.written {
				d.ReadBps = float64(int64(float64(cur.read-p.read) / dt))
				d.WriteBps = float64(int64(float64(cur.written-p.written) / dt))
			}
		}
		out = append(out, d)
	}
	c.prevDisk = next
	sort.SliceStable(out, func(i, j int) bool { return out[i].Mount < out[j].Mount })
	return out
}

// temperature reads the best CPU thermal zone in °C, or 0.
func (c *Collector) temperature() float64 {
	if c.thermal == "-" {
		c.thermal = ""
		zones, _ := filepath.Glob(filepath.Join(c.Sys, "class/thermal/thermal_zone*"))
		best := 99
		for _, z := range zones {
			typ := strings.TrimSpace(string(c.read(z, "type")))
			if r := thermalRank(typ); r < best {
				if _, err := os.Stat(filepath.Join(z, "temp")); err == nil {
					best, c.thermal = r, filepath.Join(z, "temp")
				}
			}
		}
	}
	if c.thermal == "" {
		return 0
	}
	b, err := os.ReadFile(c.thermal)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil || v <= 0 {
		return 0
	}
	return round1(v / 1000)
}

// gpuStats queries nvidia-smi at most every 5 s.
func (c *Collector) gpuStats(ctx context.Context, now time.Time) []api.GPUStats {
	if c.gpuTool == "" {
		return nil
	}
	if now.Sub(c.gpuAt) < 5*time.Second {
		return c.gpu
	}
	c.gpuAt = now
	out, err := c.run(ctx, 2*time.Second, c.gpuTool,
		"--query-gpu=name,utilization.gpu,memory.used,memory.total,temperature.gpu",
		"--format=csv,noheader,nounits")
	if err != nil {
		c.gpu = nil
		return nil
	}
	c.gpu = parseNvidiaSMI(out)
	return c.gpu
}

// parseNvidiaSMI parses csv,noheader,nounits rows (memory in MiB).
func parseNvidiaSMI(b []byte) []api.GPUStats {
	var out []api.GPUStats
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ",")
		if len(f) < 5 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		num := func(s string) float64 {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return 0
			}
			return v
		}
		out = append(out, api.GPUStats{
			Name:     f[0],
			Util:     num(f[1]),
			MemUsed:  uint64(num(f[2])) << 20,
			MemTotal: uint64(num(f[3])) << 20,
			TempC:    num(f[4]),
		})
	}
	return out
}

// hostInfo is computed once.
func (c *Collector) hostInfo(ctx context.Context) api.HostInfo {
	if c.host != nil {
		return *c.host
	}
	h := api.HostInfo{Arch: runtime.GOARCH}
	h.Hostname, _ = os.Hostname()
	if b := c.read(c.Etc, "os-release"); b != nil {
		h.OS = parseOSRelease(b)
	} else if b := c.read("/usr/lib", "os-release"); b != nil && c.Etc == "/etc" {
		h.OS = parseOSRelease(b)
	}
	if h.OS == "" {
		h.OS = runtime.GOOS
	}
	h.Kernel = strings.TrimSpace(string(c.read(c.Proc, "sys/kernel/osrelease")))
	if out, err := c.run(ctx, 2*time.Second, "systemd-detect-virt"); err == nil {
		if v := strings.TrimSpace(string(out)); v != "none" {
			h.Virt = v
		}
	}
	c.host = &h
	return h
}
