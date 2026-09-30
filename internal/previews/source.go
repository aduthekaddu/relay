package previews

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// socket is a listening socket attributed to a process.
type socket struct {
	IP   net.IP
	Port int
	PID  int    // 0 when the owner could not be determined
	Key  uint64 // stable identity while the socket lives (inode on Linux)
}

// source enumerates the user's listening sockets.
type source interface {
	sockets(ctx context.Context) ([]socket, error)
	info(ctx context.Context, pid int) procInfo
}

// procSource reads /proc (Linux).
type procSource struct{ fs *procFS }

func (s *procSource) sockets(ctx context.Context) ([]socket, error) {
	ls, err := s.fs.listeners()
	if err != nil {
		return nil, err
	}
	want := make(map[uint64]bool, len(ls))
	for _, l := range ls {
		want[l.Inode] = true
	}
	pids := s.fs.resolve(want)
	out := make([]socket, 0, len(ls))
	for _, l := range ls {
		out = append(out, socket{IP: l.IP, Port: l.Port, PID: pids[l.Inode], Key: l.Inode})
	}
	return out, nil
}

func (s *procSource) info(_ context.Context, pid int) procInfo { return s.fs.info(pid) }

// lsofSource uses lsof(8) where /proc is unavailable (macOS).
type lsofSource struct {
	uid int
	bin string
}

func (s *lsofSource) sockets(ctx context.Context) ([]socket, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, s.bin, "-nP", "-a", "-u", strconv.Itoa(s.uid), "-iTCP", "-sTCP:LISTEN", "-F", "pn").Output()
	if err != nil && len(out) == 0 {
		// lsof exits 1 when nothing matches.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("lsof: %w", err)
	}
	return parseLsof(out), nil
}

// parseLsof parses `lsof -F pn` output: "p<pid>" starts a process,
// "n<addr>:<port>" names a socket ("*:5173", "127.0.0.1:3000", "[::1]:8000").
func parseLsof(out []byte) []socket {
	var res []socket
	pid := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			host, portStr, err := net.SplitHostPort(line[1:])
			if err != nil {
				continue
			}
			port, err := strconv.Atoi(portStr)
			if err != nil || port <= 0 || port > 65535 {
				continue
			}
			var ip net.IP
			if host == "*" {
				ip = net.IPv4zero
			} else if ip = net.ParseIP(host); ip == nil {
				continue
			}
			res = append(res, socket{IP: ip, Port: port, PID: pid, Key: uint64(pid)<<16 | uint64(port)})
		}
	}
	return res
}

func (s *lsofSource) info(ctx context.Context, pid int) procInfo {
	pi := procInfo{PID: pid}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "ps", "-o", "comm=", "-o", "args=", "-p", strconv.Itoa(pid)).Output(); err == nil {
		line := strings.TrimSpace(string(out))
		comm, args, _ := strings.Cut(line, " ")
		pi.Exe = lastPathElem(comm)
		pi.Cmdline = capString(strings.TrimSpace(args), 512)
	}
	if out, err := exec.CommandContext(ctx, s.bin, "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-F", "n").Output(); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(l, "n") {
				pi.Cwd = l[1:]
			}
		}
	}
	return pi
}

func lastPathElem(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// emptySource is used when neither /proc nor lsof is available.
type emptySource struct{}

func (emptySource) sockets(context.Context) ([]socket, error) { return nil, nil }
func (emptySource) info(_ context.Context, pid int) procInfo  { return procInfo{PID: pid} }
