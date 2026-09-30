package previews

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// listener is one listening TCP socket from /proc/net/tcp{,6}.
type listener struct {
	IP    net.IP
	Port  int
	UID   int
	Inode uint64
}

// procInfo describes the process owning a socket.
type procInfo struct {
	PID     int
	Exe     string // basename of /proc/<pid>/exe
	Cmdline string // argv joined with spaces, capped
	Cwd     string
}

// tcpListen is the /proc/net/tcp state code for LISTEN.
const tcpListen = "0A"

// parseProcNet reads /proc/net/tcp or /proc/net/tcp6 and returns the
// listening sockets. Malformed lines are skipped.
func parseProcNet(r io.Reader, v6 bool) ([]listener, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	var out []listener
	first := true
	for sc.Scan() {
		if first { // header
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != tcpListen {
			continue
		}
		ip, port, err := parseHexAddr(f[1], v6)
		if err != nil {
			continue
		}
		uid, err := strconv.Atoi(f[7])
		if err != nil {
			continue
		}
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil || inode == 0 {
			continue
		}
		out = append(out, listener{IP: ip, Port: port, UID: uid, Inode: inode})
	}
	return out, sc.Err()
}

// parseHexAddr decodes "0100007F:1F90" (IPv4, little-endian word) or the
// 32-hex-digit IPv6 form (four little-endian 32-bit words).
func parseHexAddr(s string, v6 bool) (net.IP, int, error) {
	hexIP, hexPort, ok := strings.Cut(s, ":")
	if !ok {
		return nil, 0, errors.New("no port")
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return nil, 0, fmt.Errorf("port: %w", err)
	}
	b, err := hex.DecodeString(hexIP)
	if err != nil {
		return nil, 0, fmt.Errorf("address: %w", err)
	}
	want := 4
	if v6 {
		want = 16
	}
	if len(b) != want {
		return nil, 0, fmt.Errorf("address length %d", len(b))
	}
	ip := make(net.IP, len(b))
	for w := 0; w < len(b); w += 4 {
		ip[w], ip[w+1], ip[w+2], ip[w+3] = b[w+3], b[w+2], b[w+1], b[w]
	}
	if v4 := ip.To4(); v4 != nil && !v6 {
		ip = v4
	}
	return ip, int(port), nil
}

// procFS reads process information below a /proc root (a fixture
// directory in tests).
type procFS struct {
	root string
	uid  int
	// inodes caches socket inode → pid for sockets seen before.
	inodes map[uint64]int
}

func newProcFS(root string, uid int) *procFS {
	return &procFS{root: root, uid: uid, inodes: map[uint64]int{}}
}

// available reports whether this root exposes /proc/net/tcp.
func (p *procFS) available() bool {
	_, err := os.Stat(filepath.Join(p.root, "net", "tcp"))
	return err == nil
}

// listeners returns every listening socket owned by the user (IPv4 and
// IPv6).
func (p *procFS) listeners() ([]listener, error) {
	var all []listener
	for _, n := range []struct {
		file string
		v6   bool
	}{{"tcp", false}, {"tcp6", true}} {
		f, err := os.Open(filepath.Join(p.root, "net", n.file))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		ls, err := parseProcNet(f, n.v6)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", n.file, err)
		}
		for _, l := range ls {
			if l.UID == p.uid {
				all = append(all, l)
			}
		}
	}
	return all, nil
}

// resolve maps socket inodes to pids. Cached inodes are answered without
// touching /proc; unknown ones trigger one scan of the user's processes.
// Inodes of sockets that no longer exist are dropped from the cache.
func (p *procFS) resolve(want map[uint64]bool) map[uint64]int {
	out := make(map[uint64]int, len(want))
	missing := false
	for ino := range want {
		if pid, ok := p.inodes[ino]; ok && p.pidAlive(pid) {
			out[ino] = pid
		} else {
			missing = true
		}
	}
	for ino := range p.inodes {
		if !want[ino] {
			delete(p.inodes, ino)
		}
	}
	if !missing {
		return out
	}
	entries, err := os.ReadDir(p.root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 || !p.ownedByUser(pid) {
			continue
		}
		fdDir := filepath.Join(p.root, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			ino, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]"), 10, 64)
			if err != nil || !want[ino] {
				continue
			}
			if _, done := out[ino]; !done {
				out[ino] = pid
				p.inodes[ino] = pid
			}
		}
	}
	return out
}

func (p *procFS) pidAlive(pid int) bool {
	_, err := os.Stat(filepath.Join(p.root, strconv.Itoa(pid)))
	return err == nil
}

func (p *procFS) ownedByUser(pid int) bool {
	fi, err := os.Stat(filepath.Join(p.root, strconv.Itoa(pid)))
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == p.uid
}

// info reads exe, cmdline and cwd of pid. Missing pieces stay empty.
func (p *procFS) info(pid int) procInfo {
	dir := filepath.Join(p.root, strconv.Itoa(pid))
	pi := procInfo{PID: pid}
	if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
		pi.Exe = filepath.Base(strings.TrimSuffix(exe, " (deleted)"))
	}
	if b, err := readCapped(filepath.Join(dir, "cmdline"), 4096); err == nil {
		args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
		pi.Cmdline = capString(strings.Join(args, " "), 512)
		if pi.Exe == "" && len(args) > 0 {
			pi.Exe = filepath.Base(args[0])
		}
	}
	if cwd, err := os.Readlink(filepath.Join(dir, "cwd")); err == nil {
		pi.Cwd = cwd
	}
	return pi
}

func readCapped(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, n))
}

func capString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
