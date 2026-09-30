package system

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Parsers for /proc and /sys files. They take byte slices so tests feed
// synthetic fixtures; collectors read from an injectable root directory.

// cpuTimes are the jiffy counters of one /proc/stat "cpu" line.
type cpuTimes struct {
	idle, total uint64
}

// parseProcStat returns the aggregate CPU line, per-core lines, the boot
// time (seconds since the epoch) and the number of running processes.
func parseProcStat(b []byte) (all cpuTimes, cores []cpuTimes, btime int64) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "cpu "):
			all = parseCPULine(line)
		case strings.HasPrefix(line, "cpu"):
			cores = append(cores, parseCPULine(line))
		case strings.HasPrefix(line, "btime "):
			btime, _ = strconv.ParseInt(strings.TrimSpace(line[6:]), 10, 64)
		}
	}
	return all, cores, btime
}

func parseCPULine(line string) cpuTimes {
	f := strings.Fields(line)
	var t cpuTimes
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		// guest and guest_nice (8, 9) are already included in user/nice.
		if i >= 8 {
			break
		}
		t.total += n
		if i == 3 || i == 4 { // idle, iowait
			t.idle += n
		}
	}
	return t
}

// cpuPercent is the busy share between two samples, 0–100.
func cpuPercent(prev, cur cpuTimes) float64 {
	dt := float64(cur.total) - float64(prev.total)
	if dt <= 0 {
		return 0
	}
	di := float64(cur.idle) - float64(prev.idle)
	p := (1 - di/dt) * 100
	return clamp(p, 0, 100)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// parseMeminfo returns /proc/meminfo values in bytes keyed by field name.
func parseMeminfo(b []byte) map[string]uint64 {
	out := map[string]uint64{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && f[1] == "kB" {
			n *= 1024
		}
		out[k] = n
	}
	return out
}

// parseCPUModel returns the first model name (x86 "model name", ARM
// "Hardware"/"Model", or the CPU implementer/part fallback).
func parseCPUModel(b []byte) string {
	var model, hardware string
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "model name", "Processor", "cpu model":
			if model == "" {
				model = v
			}
		case "Hardware", "Model":
			if hardware == "" {
				hardware = v
			}
		}
	}
	if model != "" {
		return model
	}
	if hardware != "" {
		return hardware
	}
	return armModel(b)
}

// armParts names common Arm cores by "CPU implementer" and "CPU part"
// (arm64 /proc/cpuinfo has no model name).
var armParts = map[string]string{
	"0x41/0xd03": "Cortex-A53", "0x41/0xd04": "Cortex-A35", "0x41/0xd05": "Cortex-A55",
	"0x41/0xd07": "Cortex-A57", "0x41/0xd08": "Cortex-A72", "0x41/0xd09": "Cortex-A73",
	"0x41/0xd0a": "Cortex-A75", "0x41/0xd0b": "Cortex-A76", "0x41/0xd0c": "Neoverse-N1",
	"0x41/0xd0d": "Cortex-A77", "0x41/0xd40": "Neoverse-V1", "0x41/0xd41": "Cortex-A78",
	"0x41/0xd44": "Cortex-X1", "0x41/0xd46": "Cortex-A510", "0x41/0xd47": "Cortex-A710",
	"0x41/0xd48": "Cortex-X2", "0x41/0xd49": "Neoverse-N2", "0x41/0xd4d": "Cortex-A715",
	"0x41/0xd4e": "Cortex-X3", "0x41/0xd4f": "Neoverse-V2", "0x41/0xd80": "Cortex-A520",
	"0x41/0xd81": "Cortex-A720", "0x41/0xd82": "Cortex-X4", "0x41/0xd84": "Neoverse-V3",
	"0x41/0xd8e": "Neoverse-N3", "0xc0/0xac3": "Ampere-1", "0xc0/0xac4": "Ampere-1a",
}

// armModel names the first core listed in an arm64 cpuinfo.
func armModel(b []byte) string {
	var impl, part string
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "CPU implementer":
			if impl == "" {
				impl = strings.ToLower(strings.TrimSpace(v))
			}
		case "CPU part":
			if part == "" {
				part = strings.ToLower(strings.TrimSpace(v))
			}
		}
	}
	if impl == "" || part == "" {
		return ""
	}
	if name, ok := armParts[impl+"/"+part]; ok {
		return name
	}
	return "Arm " + impl + "/" + part
}

// parseLoadavg returns the three load averages.
func parseLoadavg(b []byte) [3]float64 {
	var out [3]float64
	f := strings.Fields(string(b))
	for i := 0; i < 3 && i < len(f); i++ {
		out[i], _ = strconv.ParseFloat(f[i], 64)
	}
	return out
}

// parseUptime returns whole seconds since boot.
func parseUptime(b []byte) int64 {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return int64(v)
}

type netCounters struct {
	name   string
	rx, tx uint64
}

// parseNetDev parses /proc/net/dev (header lines skipped).
func parseNetDev(b []byte) []netCounters {
	var out []netCounters
	for _, line := range strings.Split(string(b), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		out = append(out, netCounters{name: strings.TrimSpace(name), rx: rx, tx: tx})
	}
	return out
}

// virtualIfacePrefixes are interfaces created by container runtimes,
// bridges and VPNs. Their traffic is also counted on a physical
// interface, so they are left out of totals when a physical one exists.
var virtualIfacePrefixes = []string{
	"lo", "docker", "br-", "veth", "virbr", "vnet", "tun", "tap", "wg", "tailscale",
	"zt", "cni", "flannel", "cali", "vxlan", "kube", "lxc", "lxd", "podman", "dummy", "bond", "ifb",
}

// isVirtualIface reports whether name is a virtual interface. sysRoot is
// consulted when available: physical NICs have a /sys/class/net/<if>/device.
func isVirtualIface(sysRoot, name string) bool {
	if name == "lo" {
		return true
	}
	if sysRoot != "" {
		if _, err := os.Stat(filepath.Join(sysRoot, "class/net", name)); err == nil {
			_, err := os.Stat(filepath.Join(sysRoot, "class/net", name, "device"))
			return err != nil
		}
	}
	for _, p := range virtualIfacePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

type mountInfo struct {
	device, mount, fs string
}

// realFilesystems are filesystem types shown as disks even when their
// source is not a /dev node.
var realFilesystems = map[string]bool{
	"zfs": true, "nfs": true, "nfs4": true, "cifs": true, "smb3": true, "fuseblk": true,
	"virtiofs": true, "9p": true, "ceph": true, "glusterfs": true, "fuse.sshfs": true,
}

// skipFilesystems are never real disks even with a /dev source.
var skipFilesystems = map[string]bool{"squashfs": true, "iso9660": true, "devtmpfs": true, "tmpfs": true, "overlay": true}

// parseMounts returns real filesystems from /proc/self/mounts, one per
// device (bind mounts and btrfs subvolumes collapse to the shortest
// mount point).
func parseMounts(b []byte) []mountInfo {
	byDev := map[string]mountInfo{}
	var order []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		dev, mnt, fs := unescapeMount(f[0]), unescapeMount(f[1]), f[2]
		if skipFilesystems[fs] {
			continue
		}
		if !strings.HasPrefix(dev, "/dev/") && !realFilesystems[fs] {
			continue
		}
		if strings.HasPrefix(dev, "/dev/loop") {
			continue // snaps and images
		}
		if strings.HasPrefix(mnt, "/snap/") || strings.HasPrefix(mnt, "/var/lib/docker/") ||
			(strings.HasPrefix(mnt, "/run/") && !strings.HasPrefix(mnt, "/run/media/")) || strings.HasPrefix(mnt, "/proc") || strings.HasPrefix(mnt, "/sys") {
			continue
		}
		cur, seen := byDev[dev]
		if !seen {
			order = append(order, dev)
		}
		if !seen || len(mnt) < len(cur.mount) {
			byDev[dev] = mountInfo{device: dev, mount: mnt, fs: fs}
		}
	}
	out := make([]mountInfo, 0, len(order))
	for _, d := range order {
		out = append(out, byDev[d])
	}
	return out
}

// unescapeMount decodes the octal escapes (\040 for space) in mounts.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

type diskIO struct {
	read, written uint64 // bytes
}

// parseDiskstats maps device name → cumulative bytes read/written.
func parseDiskstats(b []byte) map[string]diskIO {
	out := map[string]diskIO{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		r, _ := strconv.ParseUint(f[5], 10, 64)
		w, _ := strconv.ParseUint(f[9], 10, 64)
		out[f[2]] = diskIO{read: r * 512, written: w * 512}
	}
	return out
}

// parseOSRelease returns PRETTY_NAME (or NAME VERSION) from os-release.
func parseOSRelease(b []byte) string {
	vals := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		vals[k] = strings.Trim(v, `"'`)
	}
	if v := vals["PRETTY_NAME"]; v != "" {
		return v
	}
	return strings.TrimSpace(vals["NAME"] + " " + vals["VERSION"])
}

// procStat is the subset of /proc/<pid>/stat Relay uses.
type procStat struct {
	pid     int
	comm    string
	state   string
	ppid    int
	ticks   uint64 // utime + stime
	threads int
	start   uint64 // clock ticks after boot
	rss     uint64 // pages
}

var errShortStat = errors.New("system: short stat line")

// parseProcPidStat parses /proc/<pid>/stat. comm may contain spaces and
// parentheses, so fields are split after the last ')'.
func parseProcPidStat(b []byte) (procStat, error) {
	s := string(b)
	open := strings.IndexByte(s, '(')
	end := strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return procStat{}, errShortStat
	}
	var p procStat
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return procStat{}, errShortStat
	}
	p.pid = pid
	p.comm = s[open+1 : end]
	f := strings.Fields(s[end+1:])
	// f[0] is field 3 (state); field n is f[n-3].
	if len(f) < 22 {
		return procStat{}, errShortStat
	}
	p.state = f[0]
	p.ppid, _ = strconv.Atoi(f[1])
	ut, _ := strconv.ParseUint(f[11], 10, 64)
	st, _ := strconv.ParseUint(f[12], 10, 64)
	p.ticks = ut + st
	p.threads, _ = strconv.Atoi(f[17])
	p.start, _ = strconv.ParseUint(f[19], 10, 64)
	p.rss, _ = strconv.ParseUint(f[21], 10, 64)
	return p, nil
}

// parsePasswd maps uid → user name.
func parsePasswd(b []byte) map[int]string {
	out := map[int]string{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 3 || strings.HasPrefix(line, "#") {
			continue
		}
		if uid, err := strconv.Atoi(f[2]); err == nil {
			if _, dup := out[uid]; !dup {
				out[uid] = f[0]
			}
		}
	}
	return out
}

// cmdline turns a NUL-separated /proc/<pid>/cmdline into a display string.
func cmdline(b []byte, max int) string {
	b = bytes.TrimRight(b, "\x00")
	s := strings.ReplaceAll(string(b), "\x00", " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// thermalRank ranks /sys/class/thermal zone types; lower is better.
func thermalRank(typ string) int {
	t := strings.ToLower(typ)
	switch {
	case strings.Contains(t, "x86_pkg_temp"), strings.Contains(t, "cpu"), strings.Contains(t, "soc"):
		return 0
	case strings.Contains(t, "acpitz"), strings.Contains(t, "package"):
		return 1
	}
	return 2
}
