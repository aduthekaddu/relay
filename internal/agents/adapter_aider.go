package agents

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Aider keeps a Markdown log per project: <repo>/.aider.chat.history.md.
// Each run starts with "# aider chat started at YYYY-MM-DD HH:MM:SS";
// user prompts are "#### " lines, tool output is quoted with "> ", and
// everything else is the assistant's reply. Every run is one session. Aider
// cannot resume a specific run and records no token usage.
func newAider() *Adapter {
	return &Adapter{
		ID: "aider", Name: "Aider", Vendor: "Aider", Color: "#14B014",
		Binaries: []string{"aider"}, InstallHint: "aider",
		Caps:     api.AgentCapabilities{Headless: true, History: true, Worktrees: true},
		Interactive: func(bin, _, model string) []string {
			return withFlag([]string{bin}, "--model", model)
		},
		Headless: func(bin, prompt, model string) []string {
			return withFlag([]string{bin, "--no-auto-commits", "--message", safeArg(prompt)}, "--model", model)
		},
		History: aiderReader{},
	}
}

const aiderHistory = ".aider.chat.history.md"

var aiderStart = []byte("# aider chat started at ")

type aiderReader struct{}

func (aiderReader) sources(ctx context.Context, e *env) ([]source, error) {
	if e.workspaces == nil {
		return nil, nil
	}
	var out []source
	for _, ws := range e.workspaces(ctx) {
		path := filepath.Join(ws, aiderHistory)
		fs, ok := fileSource(path, false)
		if !ok {
			continue
		}
		segs, err := aiderSegments(path)
		if err != nil {
			continue
		}
		sum := sha256.Sum256([]byte(path))
		prefix := hex.EncodeToString(sum[:4])
		for _, sg := range segs {
			s := fs
			s.Key = path + "#" + strconv.FormatInt(sg.start, 10)
			s.NativeID = prefix + "-" + sg.at.Format("20060102T150405")
			s.Start, s.End = sg.start, sg.end
			s.Version = strconv.FormatInt(sg.end-sg.start, 10)
			out = append(out, s)
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
	}
	return out, nil
}

type aiderSeg struct {
	start, end int64
	at         time.Time
}

// aiderSegments finds the byte range of every run in an Aider log.
func aiderSegments(path string) ([]aiderSeg, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	var segs []aiderSeg
	var pos int64
	for {
		line, err := r.ReadBytes('\n')
		if bytes.HasPrefix(line, aiderStart) {
			if n := len(segs); n > 0 {
				segs[n-1].end = pos
			}
			ts := strings.TrimSpace(string(line[len(aiderStart):]))
			at, _ := time.ParseInLocation("2006-01-02 15:04:05", ts, time.Local)
			segs = append(segs, aiderSeg{start: pos, at: at.UTC()})
		}
		pos += int64(len(line))
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if n := len(segs); n > 0 {
		segs[n-1].end = pos
	}
	return segs, nil
}

func (aiderReader) parse(ctx context.Context, e *env, src source, _ int64) (*parsed, error) {
	f, err := os.Open(src.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(src.Start, io.SeekStart); err != nil {
		return nil, err
	}
	limit := src.End - src.Start
	if limit <= 0 || limit > 64<<20 {
		limit = 64 << 20
	}
	body, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}
	p := &parsed{NativeID: src.NativeID, Cwd: filepath.Dir(src.Path)}
	lines := strings.Split(string(body), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], string(aiderStart)) {
		ts := strings.TrimSpace(strings.TrimPrefix(lines[0], string(aiderStart)))
		if at, err := time.ParseInLocation("2006-01-02 15:04:05", ts, time.Local); err == nil {
			p.touch(at.UTC())
		}
		lines = lines[1:]
	}
	p.touch(src.ModTime)
	var role string
	var buf []string
	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		buf = buf[:0]
		if text == "" || role == "" {
			return
		}
		id := "m" + strconv.Itoa(len(p.Messages))
		switch role {
		case "user":
			p.addUser(text)
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "user", Parts: []api.Part{textPart("text", text)}})
		case "tool":
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "tool", Parts: []api.Part{toolResultPart("aider", text, false)}})
		default:
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "assistant", Parts: []api.Part{textPart("text", text)}})
		}
	}
	for _, l := range lines {
		next := "assistant"
		content := l
		switch {
		case strings.HasPrefix(l, "#### "):
			next, content = "user", strings.TrimPrefix(l, "#### ")
		case strings.HasPrefix(l, "> ") || l == ">":
			next, content = "tool", strings.TrimPrefix(strings.TrimPrefix(l, ">"), " ")
		case strings.TrimSpace(l) == "" && role != "":
			next = role
		}
		if next != role {
			flush()
			role = next
		}
		buf = append(buf, content)
	}
	flush()
	return p, ctx.Err()
}
