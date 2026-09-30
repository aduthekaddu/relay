package workspaces

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Git plumbing. Every call is an argv slice (never a shell string), runs
// with a timeout, with prompts disabled and optional locks off, and with
// output capped so a pathological repository cannot exhaust memory.
const (
	readTimeout  = 1500 * time.Millisecond // per read-only git call
	writeTimeout = 20 * time.Second        // stage/unstage/discard
	hookTimeout  = 2 * time.Minute         // commit (pre-commit hooks may run)
	maxGitOutput = 8 << 20                 // bytes kept from one git call
	maxDiff      = 1 << 20                 // GitDiff.Diff cap
	maxFiles     = 5000                    // files listed in a status
	maxMessage   = 64 << 10                // commit message cap
)

// gitRunner runs git; tests may replace the binary.
type gitRunner struct {
	bin string
}

// gitError is a failed git call: the exit code and (trimmed) stderr.
type gitError struct {
	Args   []string
	Code   int
	Stderr string
}

func (e *gitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = "exit status " + strconv.Itoa(e.Code)
	}
	return "git " + strings.Join(e.Args, " ") + ": " + firstLines(msg, 3)
}

// firstLines keeps the first n lines of s.
func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// capWriter keeps at most max bytes and records overflow.
type capWriter struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) > room {
			w.buf.Write(p[:room])
			w.truncated = true
		} else {
			w.buf.Write(p)
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	return len(p), nil
}

// gitResult is the output of one call.
type gitResult struct {
	Out       []byte
	Truncated bool
	Code      int
}

// run executes git in dir. okCodes lists non-zero exit codes that are not
// errors for this call (git diff --no-index exits 1 on differences).
func (g gitRunner) run(ctx context.Context, timeout time.Duration, dir string, stdin io.Reader, args []string, okCodes ...int) (gitResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"-c", "core.quotepath=off", "-c", "color.ui=false", "-c", "core.fsmonitor=false",
		"-c", "core.pager=cat", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, g.bin, full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C",
		"GIT_PAGER=cat", "GIT_ASKPASS=", "SSH_ASKPASS=", "GCM_INTERACTIVE=never", "GIT_EDITOR=true")
	cmd.Stdin = stdin
	cmd.WaitDelay = time.Second
	out := &capWriter{max: maxGitOutput}
	errw := &capWriter{max: 16 << 10}
	cmd.Stdout, cmd.Stderr = out, errw
	err := cmd.Run()
	res := gitResult{Out: out.buf.Bytes(), Truncated: out.truncated}
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil {
		return res, fmt.Errorf("git %s: timed out", args[0])
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.Code = ee.ExitCode()
		for _, c := range okCodes {
			if c == res.Code {
				return res, nil
			}
		}
		return res, &gitError{Args: args, Code: res.Code, Stderr: errw.buf.String()}
	}
	return res, fmt.Errorf("run git: %w", err)
}

// out runs a read-only call and returns trimmed stdout.
func (g gitRunner) out(ctx context.Context, dir string, args ...string) (string, error) {
	r, err := g.run(ctx, readTimeout, dir, nil, args)
	return strings.TrimSpace(string(r.Out)), err
}

// ---------------------------------------------------------------------------
// Porcelain v2 status

// statusInfo is a parsed `git status --porcelain=v2 --branch -z`.
type statusInfo struct {
	Branch    string
	Head      string
	Upstream  string
	Ahead     int
	Behind    int
	Files     []api.GitFile
	Truncated bool
}

// parseStatusV2 parses NUL-separated porcelain v2 output with --branch.
func parseStatusV2(out []byte) statusInfo {
	var st statusInfo
	recs := bytes.Split(out, []byte{0})
	for i := 0; i < len(recs); i++ {
		rec := string(recs[i])
		if rec == "" {
			continue
		}
		switch rec[0] {
		case '#':
			parseBranchHeader(&st, rec)
		case '1', '2', 'u', '?':
			if len(st.Files) >= maxFiles {
				st.Truncated = true
				if rec[0] == '2' {
					i++
				}
				continue
			}
			f, ok := parseEntry(rec)
			if !ok {
				continue
			}
			if rec[0] == '2' && i+1 < len(recs) {
				i++
				f.OrigPath = string(recs[i])
			}
			st.Files = append(st.Files, f)
		}
	}
	return st
}

func parseBranchHeader(st *statusInfo, rec string) {
	f := strings.Fields(rec)
	if len(f) < 3 {
		return
	}
	switch f[1] {
	case "branch.oid":
		st.Head = f[2]
	case "branch.head":
		if f[2] != "(detached)" {
			st.Branch = f[2]
		}
	case "branch.upstream":
		st.Upstream = f[2]
	case "branch.ab":
		if len(f) >= 4 {
			st.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[2], "+"))
			b, _ := strconv.Atoi(strings.TrimPrefix(f[3], "-"))
			st.Behind = b
		}
	}
}

// parseEntry parses one changed/renamed/unmerged/untracked record.
//
//	1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>
//	2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <X><score> <path>   (origPath in the next record)
//	u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>
//	? <path>
func parseEntry(rec string) (api.GitFile, bool) {
	if rec[0] == '?' {
		if len(rec) < 3 {
			return api.GitFile{}, false
		}
		return api.GitFile{Path: rec[2:], Index: "?", Work: "?"}, true
	}
	fields := map[byte]int{'1': 8, '2': 9, 'u': 10}[rec[0]]
	parts := strings.SplitN(rec, " ", fields+1)
	if len(parts) != fields+1 || len(parts[1]) != 2 {
		return api.GitFile{}, false
	}
	f := api.GitFile{Path: parts[fields], Index: parts[1][:1], Work: parts[1][1:]}
	if rec[0] == 'u' {
		f.Index, f.Work = "U", "U"
	}
	f.Staged = f.Index != "." && f.Index != "?" && f.Index != "U"
	return f, true
}

// numstat is +/− per path from `git diff --numstat -z`.
type numstat struct {
	Added, Removed int
	Binary         bool
}

// parseNumstat parses `git diff --numstat -z` (renames use two extra
// NUL-terminated paths after an empty path field).
func parseNumstat(out []byte) map[string]numstat {
	m := map[string]numstat{}
	recs := bytes.Split(out, []byte{0})
	for i := 0; i < len(recs); i++ {
		rec := string(recs[i])
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		ns := numstat{}
		if parts[0] == "-" && parts[1] == "-" {
			ns.Binary = true
		} else {
			ns.Added, _ = strconv.Atoi(parts[0])
			ns.Removed, _ = strconv.Atoi(parts[1])
		}
		path := parts[2]
		if path == "" && i+2 < len(recs) {
			// rename: <orig>\0<new>\0
			path = string(recs[i+2])
			i += 2
		}
		m[path] = ns
	}
	return m
}

// ---------------------------------------------------------------------------
// Log

const logFormat = "%H%x1f%h%x1f%s%x1f%an%x1f%aI%x1e"

func parseLog(out []byte) []api.Commit {
	commits := []api.Commit{}
	for _, rec := range bytes.Split(out, []byte{0x1e}) {
		f := strings.Split(strings.TrimSpace(string(rec)), "\x1f")
		if len(f) != 5 || f[0] == "" {
			continue
		}
		at, _ := time.Parse(time.RFC3339, f[4])
		commits = append(commits, api.Commit{Hash: f[0], Short: f[1], Subject: f[2], Author: f[3], At: at.UTC()})
	}
	return commits
}

// ---------------------------------------------------------------------------
// Worktrees

// parseWorktrees parses `git worktree list --porcelain -z`.
func parseWorktrees(out []byte) []api.Worktree {
	var list []api.Worktree
	var cur *api.Worktree
	flush := func() {
		if cur != nil && cur.Path != "" {
			list = append(list, *cur)
		}
		cur = nil
	}
	for _, rec := range bytes.Split(out, []byte{0}) {
		line := string(rec)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &api.Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "bare":
			cur.Branch = "(bare)"
		}
	}
	flush()
	if len(list) > 0 {
		list[0].Main = true
	}
	return list
}

// ---------------------------------------------------------------------------
// Remotes

// stripCredentials removes user:password@ from URL-style remotes. SCP-like
// remotes (git@host:path) carry no secret and are returned unchanged.
func stripCredentials(remote string) string {
	remote = strings.TrimSpace(remote)
	if !strings.Contains(remote, "://") {
		return remote
	}
	u, err := url.Parse(remote)
	if err != nil {
		return ""
	}
	if u.User != nil {
		if _, hasPass := u.User.Password(); hasPass || u.Scheme == "https" || u.Scheme == "http" {
			u.User = nil
		}
	}
	return u.String()
}
