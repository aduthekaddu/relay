package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// ErrAborted is returned when the user cancels a prompt (Ctrl-C / EOF).
var ErrAborted = errors.New("setup cancelled")

// Choice is one option in a selection prompt.
type Choice struct {
	Label string
	Hint  string // one line, dimmed
}

// UI is a small, dependency-free terminal prompter: arrow keys and
// numbers on a real TTY, numbered line input otherwise, and defaults
// only when not interactive (--yes).
type UI struct {
	in          *bufio.Reader
	out         io.Writer
	tty         *os.File // raw-mode capable input; nil for line mode
	Color       bool
	Interactive bool
}

// NewUI prepares prompts. When stdin is not a terminal (curl | bash) it
// reads from /dev/tty instead. The returned func releases resources.
func NewUI(interactive bool, out io.Writer) (*UI, func()) {
	u := &UI{out: out, Interactive: interactive, Color: colorEnabled(out)}
	closeFn := func() {}
	if !interactive {
		u.in = bufio.NewReader(strings.NewReader(""))
		return u, closeFn
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		u.tty = os.Stdin
	} else if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		u.tty = f
		closeFn = func() { _ = f.Close() }
	}
	if u.tty != nil {
		u.in = bufio.NewReader(u.tty)
	} else {
		u.in = bufio.NewReader(os.Stdin)
	}
	return u, closeFn
}

// NewLineUI returns an interactive UI that reads numbered answers from in
// (tests, dumb terminals).
func NewLineUI(in io.Reader, out io.Writer) *UI {
	return &UI{in: bufio.NewReader(in), out: out, Interactive: true}
}

func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// ANSI styles.
const (
	sBold   = "1"
	sDim    = "2"
	sAccent = "1;38;5;141" // violet
	sOK     = "1;32"
	sWarn   = "1;33"
	sErr    = "1;31"
	sCyan   = "36"
)

func (u *UI) style(code, s string) string {
	if !u.Color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Printf writes formatted text.
func (u *UI) Printf(format string, a ...any) { fmt.Fprintf(u.out, format, a...) }

// Banner prints the welcome header.
func (u *UI) Banner(title, subtitle string) {
	u.Printf("\n  %s %s\n  %s\n\n", u.style(sAccent, "▲"), u.style(sBold, title), u.style(sDim, subtitle))
}

// Step prints a numbered section header.
func (u *UI) Step(n, total int, title string) {
	u.Printf("\n%s %s\n", u.style(sAccent, fmt.Sprintf("[%d/%d]", n, total)), u.style(sBold, title))
}

// Info prints an indented note.
func (u *UI) Info(format string, a ...any) { u.Printf("  %s\n", fmt.Sprintf(format, a...)) }

// Dim prints a dimmed note.
func (u *UI) Dim(format string, a ...any) {
	u.Printf("  %s\n", u.style(sDim, fmt.Sprintf(format, a...)))
}

// OK prints a success line.
func (u *UI) OK(format string, a ...any) {
	u.Printf("  %s %s\n", u.style(sOK, "✓"), fmt.Sprintf(format, a...))
}

// Warn prints a warning line.
func (u *UI) Warn(format string, a ...any) {
	u.Printf("  %s %s\n", u.style(sWarn, "!"), fmt.Sprintf(format, a...))
}

// Fail prints an error line.
func (u *UI) Fail(format string, a ...any) {
	u.Printf("  %s %s\n", u.style(sErr, "✗"), fmt.Sprintf(format, a...))
}

// Code prints a command the user may run.
func (u *UI) Code(cmd string) { u.Printf("    %s\n", u.style(sCyan, cmd)) }

func (u *UI) question(q string) string { return u.style(sAccent, "?") + " " + u.style(sBold, q) }

func (u *UI) raw() bool { return u.tty != nil && term.IsTerminal(int(u.tty.Fd())) }

// Choose asks for one option and returns its index.
func (u *UI) Choose(q string, opts []Choice, def int) (int, error) {
	if def < 0 || def >= len(opts) {
		def = 0
	}
	if !u.Interactive {
		return def, nil
	}
	if u.raw() {
		sel := []bool{}
		return u.rawSelect(q, opts, def, sel, false)
	}
	u.Printf("%s\n", u.question(q))
	u.printNumbered(opts, nil, def)
	for attempt := 0; attempt < 5; attempt++ {
		u.Printf("  Enter a number [%d]: ", def+1)
		line, err := u.readLine()
		if err != nil {
			return 0, err
		}
		if line == "" {
			return def, nil
		}
		if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(opts) {
			return n - 1, nil
		}
		u.Warn("please enter a number between 1 and %d", len(opts))
	}
	return 0, fmt.Errorf("%w: no valid answer", ErrAborted)
}

// MultiChoose asks for any subset of opts; defs are the preselected ones.
func (u *UI) MultiChoose(q string, opts []Choice, defs []bool) ([]bool, error) {
	sel := make([]bool, len(opts))
	copy(sel, defs)
	if !u.Interactive {
		return sel, nil
	}
	if u.raw() {
		_, err := u.rawSelect(q, opts, 0, sel, true)
		return sel, err
	}
	u.Printf("%s\n", u.question(q))
	u.printNumbered(opts, sel, -1)
	for attempt := 0; attempt < 5; attempt++ {
		u.Printf("  Numbers separated by commas (Enter keeps the ticked ones, 0 for none): ")
		line, err := u.readLine()
		if err != nil {
			return nil, err
		}
		if line == "" {
			return sel, nil
		}
		next := make([]bool, len(opts))
		ok := true
		for _, f := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' }) {
			n, err := strconv.Atoi(f)
			switch {
			case err == nil && n == 0:
			case err == nil && n >= 1 && n <= len(opts):
				next[n-1] = true
			default:
				ok = false
			}
		}
		if ok {
			return next, nil
		}
		u.Warn("please use numbers between 1 and %d", len(opts))
	}
	return nil, fmt.Errorf("%w: no valid answer", ErrAborted)
}

func (u *UI) printNumbered(opts []Choice, sel []bool, def int) {
	w := labelWidth(opts)
	for i, o := range opts {
		mark := "  "
		if sel != nil {
			mark = "[ ]"
			if sel[i] {
				mark = "[" + u.style(sOK, "x") + "]"
			}
		} else if i == def {
			mark = u.style(sAccent, "❯ ")
		}
		u.Printf("  %s %d. %s  %s\n", mark, i+1, pad(o.Label, w), u.style(sDim, o.Hint))
	}
}

// rawSelect drives an arrow-key list. multi toggles sel with space.
func (u *UI) rawSelect(q string, opts []Choice, cur int, sel []bool, multi bool) (int, error) {
	fd := int(u.tty.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		u.tty = nil // fall back to line mode
		if multi {
			r, err := u.MultiChoose(q, opts, sel)
			copy(sel, r)
			return 0, err
		}
		return u.Choose(q, opts, cur)
	}
	defer term.Restore(fd, state)
	hint := "↑/↓ to move, Enter to confirm"
	if multi {
		hint = "↑/↓ to move, Space to toggle, Enter to confirm"
	}
	w := labelWidth(opts)
	draw := func(first bool) {
		if !first {
			fmt.Fprintf(u.out, "\x1b[%dA", len(opts))
		}
		for i, o := range opts {
			pointer := "  "
			if i == cur {
				pointer = u.style(sAccent, "❯ ")
			}
			box := ""
			if multi {
				box = "◯ "
				if sel[i] {
					box = u.style(sOK, "◉ ")
				}
			}
			label := pad(o.Label, w)
			if i == cur {
				label = u.style(sBold, label)
			}
			fmt.Fprintf(u.out, "\r\x1b[2K  %s%s%d. %s  %s\r\n", pointer, box, i+1, label, u.style(sDim, o.Hint))
		}
	}
	fmt.Fprintf(u.out, "%s  %s\r\n", u.question(q), u.style(sDim, hint))
	draw(true)
	buf := make([]byte, 16)
	for {
		n, err := u.tty.Read(buf)
		if err != nil || n == 0 {
			return 0, ErrAborted
		}
		k := string(buf[:n])
		switch {
		case k == "\x03" || k == "\x04": // Ctrl-C, Ctrl-D
			fmt.Fprint(u.out, "\r\n")
			return 0, ErrAborted
		case k == "\r" || k == "\n":
			u.collapse(opts, cur, sel, multi)
			return cur, nil
		case k == "\x1b[A" || k == "\x1bOA" || k == "k":
			cur = (cur - 1 + len(opts)) % len(opts)
		case k == "\x1b[B" || k == "\x1bOB" || k == "j" || k == "\t":
			cur = (cur + 1) % len(opts)
		case k == " " && multi:
			sel[cur] = !sel[cur]
		case len(k) == 1 && k[0] >= '1' && k[0] <= '9':
			i := int(k[0] - '1')
			if i < len(opts) {
				cur = i
				if multi {
					sel[i] = !sel[i]
				} else {
					draw(false)
					u.collapse(opts, cur, sel, multi)
					return cur, nil
				}
			}
		}
		draw(false)
	}
}

// collapse replaces the drawn list with a one-line summary of the answer.
func (u *UI) collapse(opts []Choice, cur int, sel []bool, multi bool) {
	fmt.Fprintf(u.out, "\x1b[%dA\r\x1b[J", len(opts))
	answer := opts[cur].Label
	if multi {
		var picked []string
		for i, s := range sel {
			if s {
				picked = append(picked, opts[i].Label)
			}
		}
		answer = strings.Join(picked, ", ")
		if answer == "" {
			answer = "none"
		}
	}
	fmt.Fprintf(u.out, "  %s %s\r\n", u.style(sOK, "✓"), answer)
}

// Ask reads a line of text. validate may be nil.
func (u *UI) Ask(q, def string, validate func(string) error) (string, error) {
	if !u.Interactive {
		if validate != nil {
			if err := validate(def); err != nil {
				return "", fmt.Errorf("%s: %w", q, err)
			}
		}
		return def, nil
	}
	for attempt := 0; attempt < 5; attempt++ {
		if def != "" {
			u.Printf("%s %s ", u.question(q), u.style(sDim, "["+def+"]"))
		} else {
			u.Printf("%s ", u.question(q))
		}
		line, err := u.readLine()
		if err != nil {
			return "", err
		}
		if line == "" {
			line = def
		}
		if validate != nil {
			if err := validate(line); err != nil {
				u.Warn("%v", err)
				continue
			}
		}
		return line, nil
	}
	return "", fmt.Errorf("%w: no valid answer", ErrAborted)
}

// Secret reads a line without echo on a TTY.
func (u *UI) Secret(q string) (string, error) {
	if !u.Interactive {
		return "", fmt.Errorf("%s: needs an interactive terminal (or --password-stdin)", q)
	}
	u.Printf("%s ", u.question(q))
	if u.tty != nil && term.IsTerminal(int(u.tty.Fd())) {
		b, err := term.ReadPassword(int(u.tty.Fd()))
		u.Printf("\n")
		if err != nil {
			return "", ErrAborted
		}
		return string(b), nil
	}
	return u.readLine()
}

// Confirm asks a yes/no question.
func (u *UI) Confirm(q string, def bool) (bool, error) {
	if !u.Interactive {
		return def, nil
	}
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for attempt := 0; attempt < 5; attempt++ {
		u.Printf("%s %s ", u.question(q), u.style(sDim, hint))
		line, err := u.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(line) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		u.Warn("please answer y or n")
	}
	return false, fmt.Errorf("%w: no valid answer", ErrAborted)
}

func (u *UI) readLine() (string, error) {
	line, err := u.in.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", ErrAborted
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func labelWidth(opts []Choice) int {
	w := 0
	for _, o := range opts {
		if n := utf8.RuneCountInString(o.Label); n > w {
			w = n
		}
	}
	return w
}

func pad(s string, w int) string {
	if n := utf8.RuneCountInString(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
