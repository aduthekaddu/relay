package previews

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const tcp4Fixture = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1435 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 111 1 0000000000000000 100 0 0 10 0
   1: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 222 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1435 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1000        0 333 1 0000000000000000 100 0 0 10 0
   3: 00000000:1538 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 444 1 0000000000000000 100 0 0 10 0
   4: garbage line
   5: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 0 1 0000000000000000 100 0 0 10 0
`

const tcp6Fixture = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:1F40 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 555 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000000000000:22B8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 666 1 0000000000000000 100 0 0 10 0
`

func TestParseProcNet(t *testing.T) {
	tests := []struct {
		name string
		in   string
		v6   bool
		want []listener
	}{
		{"ipv4", tcp4Fixture, false, []listener{
			{IP: net.IPv4(127, 0, 0, 1).To4(), Port: 5173, UID: 1000, Inode: 111},
			{IP: net.IPv4zero.To4(), Port: 3000, UID: 1000, Inode: 222},
			{IP: net.IPv4zero.To4(), Port: 5432, UID: 0, Inode: 444},
		}},
		{"ipv6", tcp6Fixture, true, []listener{
			{IP: net.IPv6loopback, Port: 8000, UID: 1000, Inode: 555},
			{IP: net.IPv6unspecified, Port: 8888, UID: 1000, Inode: 666},
		}},
		{"empty", "  sl  local_address\n", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProcNet(strings.NewReader(tt.in), tt.v6)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d listeners %+v, want %d", len(got), got, len(tt.want))
			}
			for i := range got {
				g, w := got[i], tt.want[i]
				if !g.IP.Equal(w.IP) || g.Port != w.Port || g.UID != w.UID || g.Inode != w.Inode {
					t.Errorf("[%d] = %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

func TestParseHexAddr(t *testing.T) {
	tests := []struct {
		in     string
		v6     bool
		ip     string
		port   int
		hasErr bool
	}{
		{"0100007F:1435", false, "127.0.0.1", 5173, false},
		{"0101A8C0:0050", false, "192.168.1.1", 80, false},
		{"00000000000000000000000001000000:1F40", true, "::1", 8000, false},
		{"B80D0120000000000000000001000000:01BB", true, "2001:db8::1", 443, false},
		{"0100007F", false, "", 0, true},
		{"0100007F:ZZZZ", false, "", 0, true},
		{"01007F:1435", false, "", 0, true},
		{"0100007F:1435", true, "", 0, true},
	}
	for _, tt := range tests {
		ip, port, err := parseHexAddr(tt.in, tt.v6)
		if (err != nil) != tt.hasErr {
			t.Errorf("%s: err = %v", tt.in, err)
			continue
		}
		if tt.hasErr {
			continue
		}
		if ip.String() != tt.ip || port != tt.port {
			t.Errorf("%s = %s:%d, want %s:%d", tt.in, ip, port, tt.ip, tt.port)
		}
	}
}

// writeProcFixture builds a fake /proc with one process (pid 4242) that
// owns socket inode 111 and runs vite in /work/app.
func writeProcFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "net"), 0o755))
	uid := strconv.Itoa(os.Getuid())
	must(os.WriteFile(filepath.Join(root, "net", "tcp"), []byte(strings.ReplaceAll(tcp4Fixture, " 1000 ", " "+uid+" ")), 0o644))
	must(os.WriteFile(filepath.Join(root, "net", "tcp6"), []byte(strings.ReplaceAll(tcp6Fixture, " 1000 ", " "+uid+" ")), 0o644))
	pd := filepath.Join(root, "4242")
	must(os.MkdirAll(filepath.Join(pd, "fd"), 0o755))
	must(os.Symlink("socket:[111]", filepath.Join(pd, "fd", "3")))
	must(os.Symlink("/dev/null", filepath.Join(pd, "fd", "0")))
	must(os.Symlink("socket:[999]", filepath.Join(pd, "fd", "4")))
	must(os.Symlink("/usr/bin/node", filepath.Join(pd, "exe")))
	must(os.Symlink("/work/app", filepath.Join(pd, "cwd")))
	must(os.WriteFile(filepath.Join(pd, "cmdline"), []byte("node\x00/work/app/node_modules/.bin/vite\x00--port\x005173\x00"), 0o644))
	// A non-numeric entry and a process without fds must be ignored.
	must(os.MkdirAll(filepath.Join(root, "self"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "77"), 0o755))
	return root
}

func TestProcFS(t *testing.T) {
	root := writeProcFixture(t)
	fs := newProcFS(root, os.Getuid())
	if !fs.available() {
		t.Fatal("fixture not available")
	}
	ls, err := fs.listeners()
	if err != nil {
		t.Fatal(err)
	}
	// uid filter drops the root-owned 5432 socket; 4 remain (2 v4, 2 v6).
	if len(ls) != 4 {
		t.Fatalf("listeners = %+v", ls)
	}
	want := map[uint64]bool{111: true, 222: true}
	pids := fs.resolve(want)
	if pids[111] != 4242 {
		t.Errorf("inode 111 → pid %d, want 4242", pids[111])
	}
	if _, ok := pids[222]; ok {
		t.Errorf("inode 222 should be unresolved")
	}
	// Second call is served from the cache.
	if again := fs.resolve(map[uint64]bool{111: true}); again[111] != 4242 {
		t.Errorf("cached resolve = %v", again)
	}
	if _, ok := fs.inodes[222]; ok {
		t.Errorf("unwanted inode cached")
	}
	pi := fs.info(4242)
	if pi.Exe != "node" || pi.Cwd != "/work/app" || !strings.Contains(pi.Cmdline, "vite --port 5173") {
		t.Errorf("info = %+v", pi)
	}
	src := &procSource{fs: fs}
	socks, err := src.sockets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range socks {
		if s.Port == 5173 && s.PID == 4242 && s.Key == 111 {
			found = true
		}
	}
	if !found {
		t.Errorf("sockets = %+v", socks)
	}
}

func TestParseLsof(t *testing.T) {
	out := []byte("p501\nn*:5173\nn127.0.0.1:3000\np777\nn[::1]:8000\nnbogus\nn*:99999\n")
	got := parseLsof(out)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].PID != 501 || got[0].Port != 5173 || !got[0].IP.IsUnspecified() {
		t.Errorf("[0] = %+v", got[0])
	}
	if got[2].PID != 777 || got[2].Port != 8000 || !got[2].IP.Equal(net.IPv6loopback) {
		t.Errorf("[2] = %+v", got[2])
	}
}

func TestCapString(t *testing.T) {
	if got := capString("héllo wörld", 2); got != "h…" {
		t.Errorf("capString = %q", got)
	}
	if got := capString("abc", 5); got != "abc" {
		t.Errorf("capString = %q", got)
	}
}
