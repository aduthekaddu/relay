package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

func TestParseJournalLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want api.LogLine
		ok   bool
	}{
		{
			name: "string message",
			in:   `{"MESSAGE":"started","PRIORITY":"6","_SYSTEMD_USER_UNIT":"relay.service","__REALTIME_TIMESTAMP":"1700000000000000"}`,
			want: api.LogLine{At: time.Unix(1700000000, 0).UTC(), Unit: "relay.service", Prio: 6, Text: "started"},
			ok:   true,
		},
		{
			name: "byte array message with colour",
			in:   `{"MESSAGE":[27,91,51,49,109,101,114,114,27,91,48,109],"PRIORITY":"3","__REALTIME_TIMESTAMP":"1700000000000000"}`,
			want: api.LogLine{At: time.Unix(1700000000, 0).UTC(), Unit: "x.service", Prio: 3, Text: "err"},
			ok:   true,
		},
		{
			name: "null message, bad prio",
			in:   `{"MESSAGE":null,"PRIORITY":"99","__REALTIME_TIMESTAMP":"1700000000000000"}`,
			want: api.LogLine{At: time.Unix(1700000000, 0).UTC(), Unit: "x.service", Prio: 6},
			ok:   true,
		},
		{name: "not json", in: `-- No entries --`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseJournalLine([]byte(tt.in), "x.service")
			if ok != tt.ok || (ok && got != tt.want) {
				t.Fatalf("got %+v %v", got, ok)
			}
		})
	}
}

func TestCleanLogText(t *testing.T) {
	if got := cleanLogText("a\tb\x07c\x1b[1;32md\r\n"); got != "a\tbcd" {
		t.Errorf("clean = %q", got)
	}
	if got := cleanLogText("bad \xff utf8"); got != "bad \uFFFD utf8" {
		t.Errorf("utf8 = %q", got)
	}
	long := strings.Repeat("é", logLineMax) // 2 bytes each
	got := cleanLogText(long)
	if len(got) > logLineMax || strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("cap: len %d, replacement %v", len(got), strings.ContainsRune(got, '\uFFFD'))
	}
}

func TestLastLinesOffset(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		content string
		n       int
		want    string
	}{
		{"a\nb\nc\n", 2, "b\nc\n"},
		{"a\nb\nc", 2, "b\nc"},
		{"a\nb\nc\n", 10, "a\nb\nc\n"},
		{"a\nb\nc\n", 0, ""},
		{"", 5, ""},
	}
	for i, tt := range tests {
		p := filepath.Join(dir, "f")
		writeFile(t, p, tt.content)
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		off, err := lastLinesOffset(f, int64(len(tt.content)), tt.n)
		_ = f.Close()
		if err != nil || tt.content[off:] != tt.want {
			t.Errorf("%d: offset %d -> %q want %q (%v)", i, off, tt.content[off:], tt.want, err)
		}
	}
}

// collect is a concurrency-safe log sink.
type collect struct {
	mu    sync.Mutex
	lines []string
}

func (c *collect) sink(ll api.LogLine) error {
	c.mu.Lock()
	c.lines = append(c.lines, ll.Text)
	c.mu.Unlock()
	return nil
}

func (c *collect) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := append([]string(nil), c.lines...)
		c.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Fatalf("timed out waiting for %d lines, have %q", n, c.lines)
	return nil
}

func appendFile(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestTailFileFollowTruncateRotate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log")
	writeFile(t, p, "one\ntwo\nthree\n")
	ctx, cancel := context.WithCancel(context.Background())
	var c collect
	done := make(chan error, 1)
	go func() { done <- tailFile(ctx, p, 2, c.sink) }()

	c.waitFor(t, 2)
	appendFile(t, p, "four\nfi")
	c.waitFor(t, 3)
	appendFile(t, p, "ve\n") // a line written in two parts arrives whole
	c.waitFor(t, 4)

	writeFile(t, p, "") // truncate
	time.Sleep(2 * logPoll)
	appendFile(t, p, "after-truncate\n")
	c.waitFor(t, 5)

	// Rotate: rename away and create a new file.
	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, "rotated\n")
	got := c.waitFor(t, 6)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	want := []string{"two", "three", "four", "five", "after-truncate", "rotated"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTailFileSinkErrorStops(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.log")
	writeFile(t, p, "a\nb\n")
	stop := errors.New("client gone")
	err := tailFile(context.Background(), p, 10, func(api.LogLine) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenLogRefusesSpecial(t *testing.T) {
	dir := t.TempDir()
	if _, err := openLog(dir); err == nil {
		t.Error("directory accepted")
	}
	target := filepath.Join(dir, "real.log")
	writeFile(t, target, "x\n")
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openLog(link); err == nil {
		t.Error("symlink followed")
	}
	f, err := openLog(target)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestReadJournalStream(t *testing.T) {
	in := strings.Join([]string{
		`{"MESSAGE":"a","PRIORITY":"6","__REALTIME_TIMESTAMP":"1"}`,
		`garbage`,
		`{"MESSAGE":"b","PRIORITY":"4","__REALTIME_TIMESTAMP":"2"}`,
	}, "\n")
	var c collect
	if err := readJournal(strings.NewReader(in), "u.service", c.sink); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.lines, ",") != "a,b" {
		t.Fatalf("lines = %v", c.lines)
	}
}
