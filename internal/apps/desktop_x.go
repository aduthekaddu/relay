package apps

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aduthekaddu/relay/internal/httpx"
)

// Launch starts a catalog app on the desktop (starting the desktop first
// when needed). The app runs detached in its own process group and ends
// with the X server at the latest.
func (k *desktop) Launch(ctx context.Context, id string) error {
	var app *deskApp
	for i := range k.catalog {
		if k.catalog[i].ID == id {
			app = &k.catalog[i]
		}
	}
	if app == nil {
		return httpx.NotFound(fmt.Sprintf("unknown desktop app %q", id))
	}
	if err := k.Start(ctx); err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(k.dir, "bin", app.ID))
	cmd.Env = append(k.sessionEnv(), k.sessionBus()...)
	cmd.Dir = k.d.Paths.Home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return httpx.Unavailable(fmt.Sprintf("Launching %s failed: %v", app.Name, err))
	}
	go func() { _ = cmd.Wait() }() // reap
	k.touch()
	k.mu.Lock()
	k.scanner.Reset()
	k.mu.Unlock()
	k.log.Info("desktop app launched", "app", app.ID, "pid", cmd.Process.Pid)
	k.changed()
	return nil
}

// sessionBus returns the session's D-Bus address (written by xstartup)
// as an environment entry, so API-launched apps share the session bus.
func (k *desktop) sessionBus() []string {
	b, err := os.ReadFile(filepath.Join(k.dir, "dbus-address"))
	if err != nil {
		return nil
	}
	addr, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if !strings.HasPrefix(addr, "unix:") || strings.ContainsAny(addr, "\x00") {
		return nil
	}
	return []string{"DBUS_SESSION_BUS_ADDRESS=" + addr}
}

// requireRunning returns a 409 unless the desktop is running.
func (k *desktop) requireRunning() error {
	if st, _ := k.status(); st != stateRunning {
		return httpx.Conflict("the desktop is not running")
	}
	return nil
}

// xtool runs an X client against the desktop display with a timeout.
// Its output is captured up to limit bytes; limit < 0 discards output
// (for tools that fork a background child holding stdout open).
func (k *desktop) xtool(ctx context.Context, stdin io.Reader, limit int64, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = k.sessionEnv()
	cmd.Stdin = stdin
	var out, errb limitedBuffer
	out.limit, errb.limit = limit, 4096
	if limit >= 0 {
		cmd.Stdout, cmd.Stderr = &out, &errb
	}
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return out.Bytes(), fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return out.Bytes(), fmt.Errorf("%s: %w", name, err)
	}
	if out.over {
		return out.Bytes(), errTooLarge
	}
	return out.Bytes(), nil
}

var errTooLarge = fmt.Errorf("output too large")

// limitedBuffer keeps the first limit bytes and notes overflow.
type limitedBuffer struct {
	bytes.Buffer
	limit int64
	over  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - int64(b.Len())
	if int64(len(p)) > room {
		b.over = true
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// Clipboard returns the desktop's CLIPBOARD selection as text ("" when
// empty or not text).
func (k *desktop) Clipboard(ctx context.Context) (string, error) {
	if err := k.requireRunning(); err != nil {
		return "", err
	}
	out, err := k.xtool(ctx, nil, maxClipboard, "xclip", "-selection", "clipboard", "-o", "-t", "UTF8_STRING")
	if err == errTooLarge {
		return "", httpx.BadRequest("the desktop clipboard holds more than 1 MiB of text")
	}
	if err != nil {
		if _, lerr := exec.LookPath("xclip"); lerr != nil {
			return "", httpx.Unavailable("xclip is not installed")
		}
		return "", nil // empty selection or a non-text target
	}
	return strings.ToValidUTF8(string(out), ""), nil
}

// SetClipboard puts text into the desktop's CLIPBOARD and PRIMARY
// selections. xclip keeps serving it in the background until replaced.
func (k *desktop) SetClipboard(ctx context.Context, text string) error {
	if len(text) > maxClipboard {
		return httpx.BadRequest("clipboard text is larger than 1 MiB")
	}
	if err := k.requireRunning(); err != nil {
		return err
	}
	if _, err := exec.LookPath("xclip"); err != nil {
		return httpx.Unavailable("xclip is not installed")
	}
	for _, sel := range []string{"clipboard", "primary"} {
		// -silent: xclip forks and the parent returns once it owns the
		// selection; nothing holds our pipes open.
		if _, err := k.xtool(ctx, strings.NewReader(text), -1, "xclip", "-selection", sel, "-i", "-silent"); err != nil {
			return httpx.Unavailable("Setting the desktop clipboard failed: " + err.Error())
		}
	}
	k.touch()
	return nil
}

// Resize changes the desktop resolution with RandR, creating the mode on
// the fly (TigerVNC accepts any size).
func (k *desktop) Resize(ctx context.Context, w, h int) error {
	if w < minW || h < minH || w > maxW || h > maxH {
		return httpx.BadRequest(fmt.Sprintf("size must be between %dx%d and %dx%d", minW, minH, maxW, maxH))
	}
	if err := k.requireRunning(); err != nil {
		return err
	}
	out, err := k.xtool(ctx, nil, 256<<10, "xrandr", "-q")
	if err != nil {
		return httpx.Unavailable("Resizing the desktop failed: " + err.Error())
	}
	output, modes := parseXrandr(out)
	if output == "" {
		return httpx.Unavailable("Resizing the desktop failed: no connected output")
	}
	mode := fmt.Sprintf("%dx%d", w, h)
	if !modes[mode] {
		ws, hs := strconv.Itoa(w), strconv.Itoa(h)
		if _, err := k.xtool(ctx, nil, 4096, "xrandr", "--newmode", mode, "60", ws, ws, ws, ws, hs, hs, hs, hs); err != nil {
			return httpx.Unavailable("Resizing the desktop failed: " + err.Error())
		}
		if _, err := k.xtool(ctx, nil, 4096, "xrandr", "--addmode", output, mode); err != nil {
			return httpx.Unavailable("Resizing the desktop failed: " + err.Error())
		}
	}
	if _, err := k.xtool(ctx, nil, 4096, "xrandr", "--output", output, "--mode", mode); err != nil {
		return httpx.Unavailable("Resizing the desktop failed: " + err.Error())
	}
	k.mu.Lock()
	k.width, k.height = w, h
	k.mu.Unlock()
	k.touch()
	k.changed()
	return nil
}

// parseXrandr returns the first connected output and the mode names it
// lists from `xrandr -q` output.
func parseXrandr(out []byte) (string, map[string]bool) {
	modes := map[string]bool{}
	output := ""
	inOutput := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, " ") {
			f := strings.Fields(line)
			inOutput = len(f) >= 2 && f[1] == "connected" && output == ""
			if inOutput {
				output = f[0]
			}
			continue
		}
		if inOutput {
			if f := strings.Fields(line); len(f) > 0 {
				modes[f[0]] = true
			}
		}
	}
	return output, modes
}
