//go:build !linux

package system

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// platformMetrics is the minimal fallback for systems without /proc
// (macOS, BSD): core count, memory size, load, uptime, the root and home
// filesystems and host identity from sysctl(8). CPU %, IO and network
// rates are not reported.
func platformMetrics(ctx context.Context, c *Collector) api.Metrics {
	if c.Available() {
		return c.Sample(ctx)
	}
	m := api.Metrics{At: time.Now().UTC()}
	m.CPU.Cores = runtime.NumCPU()
	m.CPU.PerCore = make([]float64, m.CPU.Cores)
	m.CPU.Model = sysctl(ctx, "machdep.cpu.brand_string")
	m.Memory.Total, _ = strconv.ParseUint(sysctl(ctx, "hw.memsize"), 10, 64)
	// vm.loadavg prints "{ 1.23 1.10 0.98 }".
	m.Load = parseLoadavg([]byte(strings.Trim(sysctl(ctx, "vm.loadavg"), "{} ")))
	// kern.boottime prints "{ sec = 1700000000, usec = 0 } ...".
	if bt := sysctl(ctx, "kern.boottime"); bt != "" {
		if _, rest, ok := strings.Cut(bt, "sec = "); ok {
			if sec, err := strconv.ParseInt(strings.TrimRight(strings.Fields(rest)[0], ","), 10, 64); err == nil {
				m.Uptime = int64(time.Since(time.Unix(sec, 0)).Seconds())
			}
		}
	}
	seen := map[string]bool{}
	for _, mnt := range []string{"/", os.Getenv("HOME")} {
		if mnt == "" || seen[mnt] {
			continue
		}
		seen[mnt] = true
		if total, free, avail, err := c.statfsFn(mnt); err == nil && total > 0 {
			m.Disks = append(m.Disks, api.DiskStats{Mount: mnt, Total: total, Used: total - free, Free: avail})
		}
	}
	h := api.HostInfo{Arch: runtime.GOARCH, OS: runtime.GOOS}
	h.Hostname, _ = os.Hostname()
	if v := sysctl(ctx, "kern.osproductversion"); v != "" {
		h.OS = "macOS " + v
	}
	h.Kernel = sysctl(ctx, "kern.osrelease")
	m.Host = h
	return m
}

func sysctl(ctx context.Context, name string) string {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sysctl", "-n", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
