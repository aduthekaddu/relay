package agents

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
)

// historyReader reads one agent's transcripts. Implementations must be
// read-only: they never create, modify or lock the agent's files.
type historyReader interface {
	// sources lists every transcript source currently on disk.
	sources(ctx context.Context, e *env) ([]source, error)
	// parse reads src. For append-only sources (src.Append) parsing starts
	// at offset and parsed.Consumed reports the new offset (always at a
	// line boundary); otherwise offset is ignored and the whole source is
	// read.
	parse(ctx context.Context, e *env, src source, offset int64) (*parsed, error)
}

// env is what readers and hook installers may touch: the user's home
// (a temp dir in tests) and helpers.
type env struct {
	home string
	// workspaces returns known project directories (for agents that keep
	// history inside each project, like Aider and Crush).
	workspaces func(ctx context.Context) []string
	// tmp is a private scratch dir (SQLite snapshots).
	tmp string
	// chunk caps how many bytes of an append-only source one parse call
	// reads (0 = no cap). The indexer uses it to bound memory; parse then
	// reports how far it got in parsed.Consumed.
	chunk int64
	// dbs caches read-only handles on agents' SQLite databases.
	dbs *agentDBs
}

func (e *env) path(parts ...string) string {
	return filepath.Join(append([]string{e.home}, parts...)...)
}

// source is one transcript unit: usually a file, or a row in an agent's
// own SQLite database.
type source struct {
	Key     string // unique within the agent: file path, or "<db>#<session id>"
	Path    string // file to read (the database for SQLite sources)
	Size    int64
	ModTime time.Time
	Append  bool   // append-only JSONL: parse incrementally from an offset
	Version string // change marker for non-file sources (row updated time)
	// NativeID is known up front for some layouts (dir name, DB row).
	NativeID string
	// UsageOnly sources add token usage to NativeID's session but are not
	// sessions themselves (Claude sub-agent transcripts).
	UsageOnly bool
	// Start/End bound the byte range of a source that is one part of a
	// larger file (Aider runs); End 0 means "to the end".
	Start, End int64
}

// parsed is what a reader extracted from a source (or a chunk of it).
type parsed struct {
	NativeID  string
	Title     string // explicit title (custom/ai title); wins over prompts
	FirstUser string // first real user prompt (fallback title)
	Summary   string
	Cwd       string
	GitBranch string
	Model     string
	StartedAt time.Time
	UpdatedAt time.Time
	Messages  []api.AgentMessage
	Usage     []usageRec
	Consumed  int64 // new offset for append-only sources
	// UsageOnly sources contribute token usage to NativeID but are not
	// listed as sessions (Claude sub-agent transcripts).
	UsageOnly bool
	// Hidden sessions are indexed for usage but not listed (e.g. empty).
	Hidden    bool
	Resumable bool

	lastAPIID string // parser state: API message id of the last assistant message
}

// usageRec is one billed model call.
type usageRec struct {
	Key        string // dedupe key, unique within the session
	At         time.Time
	Model      string
	Input      int64 // non-cached input
	Output     int64 // includes reasoning
	CacheRead  int64
	CacheWrite int64
	Reasoning  int64
	NativeCost float64 // cost the agent itself recorded (0 = unknown)
	Credits    float64 // plan credits the agent metered (Kiro), informational
}

// touch widens the session time range.
func (p *parsed) touch(t time.Time) {
	if t.IsZero() {
		return
	}
	if p.StartedAt.IsZero() || t.Before(p.StartedAt) {
		p.StartedAt = t
	}
	if t.After(p.UpdatedAt) {
		p.UpdatedAt = t
	}
}

// addUser records a user prompt as the fallback title when it is the first.
func (p *parsed) addUser(text string) {
	if p.FirstUser == "" {
		if t := cleanTitle(text); t != "" {
			p.FirstUser = t
		}
	}
}

// Limits for normalised transcripts.
const (
	maxToolText  = 4 << 10  // tool input/output kept per part
	maxPartText  = 64 << 10 // text/thinking kept per part
	maxLineBytes = 16 << 20 // JSONL lines longer than this are skipped
	maxTitle     = 80
)

// truncate cuts s to at most n bytes on a rune boundary, adding "…".
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

var (
	wsRe       = regexp.MustCompile(`\s+`)
	xmlBlockRe = regexp.MustCompile(`(?s)<([a-zA-Z][a-zA-Z0-9_-]*)(?:\s[^>]*)?>.*?</([a-zA-Z][a-zA-Z0-9_-]*)>`)
	ansiRe     = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
)

// cleanTitle turns a raw prompt into a one-line title (≤ 80 chars).
// Injected context blocks (<system-reminder>…</system-reminder>,
// <environment_context>, command wrappers) are dropped.
func cleanTitle(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	s = xmlBlockRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := xmlBlockRe.FindStringSubmatch(m)
		if len(sub) == 3 && sub[1] == sub[2] {
			switch sub[1] {
			case "command-name", "command-args", "command-message":
				// Slash-command wrappers: keep the visible command text.
				inner := strings.TrimSuffix(strings.TrimPrefix(m, "<"+sub[1]+">"), "</"+sub[2]+">")
				return " " + inner + " "
			}
			return " "
		}
		return m
	})
	s = strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
	if strings.HasPrefix(s, "Caveat:") || strings.HasPrefix(s, "[Request interrupted") {
		return ""
	}
	return truncate(s, maxTitle)
}

// isInjectedPrompt reports whether a "user" text block is context the CLI
// injected rather than something the person typed.
func isInjectedPrompt(s string) bool {
	t := strings.TrimSpace(s)
	for _, p := range []string{
		"<environment_context>", "<user_instructions>", "# AGENTS.md instructions",
		"<permissions instructions>", "<system-reminder>", "<local-command-stdout>",
		"<recommended_plugins>", "Caveat: The messages below", "<turn_aborted>",
	} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// textPart / toolCall / toolResult build normalised parts with caps applied.
func textPart(kind, text string) api.Part {
	return api.Part{Type: kind, Text: truncate(text, maxPartText)}
}

func toolCallPart(tool, input string) api.Part {
	return api.Part{Type: "tool_call", Tool: tool, Input: truncate(input, maxToolText)}
}

func toolResultPart(tool, output string, isErr bool) api.Part {
	return api.Part{Type: "tool_result", Tool: tool, Output: truncate(output, maxToolText), IsError: isErr}
}

// rawString renders a JSON value compactly for tool inputs.
func rawString(r json.RawMessage) string {
	if len(r) == 0 || string(r) == "null" {
		return ""
	}
	var s string
	if r[0] == '"' && json.Unmarshal(r, &s) == nil {
		return s
	}
	var buf bytes.Buffer
	if json.Compact(&buf, r) == nil {
		return buf.String()
	}
	return string(r)
}

// parseTime accepts RFC 3339 strings, unix seconds or unix milliseconds.
func parseTime(v any) time.Time {
	switch t := v.(type) {
	case string:
		if t == "" {
			return time.Time{}
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
			if ts, err := time.Parse(layout, t); err == nil {
				return ts.UTC()
			}
		}
		if n, err := strconv.ParseFloat(t, 64); err == nil {
			return unixAny(n)
		}
	case float64:
		return unixAny(t)
	case int64:
		return unixAny(float64(t))
	case int:
		return unixAny(float64(t))
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return unixAny(f)
		}
	}
	return time.Time{}
}

// unixAny interprets n as seconds, milliseconds or microseconds.
func unixAny(n float64) time.Time {
	switch {
	case n <= 0:
		return time.Time{}
	case n > 1e15:
		return time.UnixMicro(int64(n)).UTC()
	case n > 1e11:
		return time.UnixMilli(int64(n)).UTC()
	default:
		sec := int64(n)
		return time.Unix(sec, int64((n-float64(sec))*1e9)).UTC()
	}
}

// scanJSONL reads complete lines of path starting at offset and calls fn
// with each line and the byte offset where it starts (a stable id for the
// line). It returns the offset just past the last complete line, so a line
// still being written is re-read next time. Lines longer than maxLineBytes
// are skipped. With limit > 0 it stops after the first line boundary at or
// beyond offset+limit.
func scanJSONL(ctx context.Context, path string, offset, limit int64, fn func(line []byte, off int64) error) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return offset, err
		}
	}
	r := bufio.NewReaderSize(f, 64<<10)
	pos := offset   // bytes consumed so far
	start := offset // where the current line began
	var long []byte
	skipping := false
	for n := 0; ; n++ {
		if n%512 == 0 && ctx.Err() != nil {
			return start, ctx.Err()
		}
		chunk, err := r.ReadSlice('\n')
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			if !skipping {
				long = append(long, chunk...)
				if len(long) > maxLineBytes {
					long, skipping = nil, true
				}
			}
			pos += int64(len(chunk))
			continue
		case errors.Is(err, io.EOF):
			// A final line without a newline is used only when it is a
			// complete JSON value; otherwise it is still being written:
			// rewind to its start so it is read again next time.
			if !skipping {
				tail := append(long, chunk...)
				if t := bytes.TrimSpace(tail); len(t) > 0 && json.Valid(t) {
					if ferr := fn(t, start); ferr != nil {
						return start, ferr
					}
					return pos + int64(len(chunk)), nil
				}
			}
			return start, nil
		case err != nil:
			return start, err
		}
		pos += int64(len(chunk))
		line := chunk
		if skipping {
			skipping = false
			start = pos
			continue
		}
		if len(long) > 0 {
			long = append(long, chunk...)
			line = long
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if ferr := fn(line, start); ferr != nil {
				return start, ferr
			}
		}
		long = long[:0]
		start = pos
		if limit > 0 && pos-offset >= limit {
			return pos, nil
		}
	}
}

// readJSONFile decodes a whole JSON file, refusing files over limit bytes.
func readJSONFile(path string, limit int64, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > limit {
		return fmt.Errorf("%s: %d bytes exceeds %d", filepath.Base(path), st.Size(), limit)
	}
	return json.NewDecoder(f).Decode(v)
}

// fileSource stats path into a source.
func fileSource(path string, appendOnly bool) (source, bool) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return source{}, false
	}
	return source{Key: path, Path: path, Size: st.Size(), ModTime: st.ModTime(), Append: appendOnly}, true
}

// globSources returns file sources matching pattern (filepath.Glob syntax).
func globSources(pattern string, appendOnly bool) []source {
	matches, _ := filepath.Glob(pattern)
	out := make([]source, 0, len(matches))
	for _, m := range matches {
		if s, ok := fileSource(m, appendOnly); ok {
			out = append(out, s)
		}
	}
	return out
}

// walkSources walks root (bounded depth) collecting files accepted by match.
func walkSources(ctx context.Context, root string, maxDepth int, appendOnly bool, match func(name string) bool) []source {
	var out []source
	base := strings.Count(filepath.Clean(root), string(filepath.Separator))
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if strings.Count(p, string(filepath.Separator))-base >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && match(d.Name()) {
			if s, ok := fileSource(p, appendOnly); ok {
				out = append(out, s)
			}
		}
		return nil
	})
	return out
}

// lineID is the message id for a JSONL line without a native id.
func lineID(off int64) string { return "o" + strconv.FormatInt(off, 10) }
