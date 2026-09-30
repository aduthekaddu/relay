package apps

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	deskassets "github.com/aduthekaddu/relay/deploy/desktop"
)

// safePathRe limits the session directory to characters that need no
// quoting in openbox commands, .desktop Exec lines or XML.
var safePathRe = regexp.MustCompile(`^[A-Za-z0-9._/+@-]+$`)

// renderSession writes the session files into dir (0700): the xstartup
// script, openbox config, menu and theme, the tint2 panel, one launcher
// script per catalog app (dir/bin/<id>) and its .desktop entry.
func renderSession(dir string, catalog []deskApp) error {
	if !safePathRe.MatchString(dir) {
		return fmt.Errorf("desktop directory %q contains unsupported characters", dir)
	}
	bin := filepath.Join(dir, "bin")
	appsDir := filepath.Join(dir, "applications")
	// Regenerated on every start: drop stale launchers first.
	for _, sub := range []string{bin, appsDir} {
		if err := os.RemoveAll(sub); err != nil {
			return err
		}
	}
	for _, sub := range []string{dir, bin, appsDir, filepath.Join(dir, "openbox"), filepath.Join(dir, "share", "themes", "Relay", "openbox-3")} {
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return err
		}
	}
	asset := func(name string) (string, error) {
		b, err := fs.ReadFile(deskassets.Files, name)
		return string(b), err
	}
	files := []struct {
		src, dst string
		mode     os.FileMode
		edit     func(string) string
	}{
		{"xstartup", "xstartup", 0o700, nil},
		{"theme/Relay/openbox-3/themerc", "share/themes/Relay/openbox-3/themerc", 0o600, nil},
		{"openbox/rc.xml", "openbox/rc.xml", 0o600, func(s string) string {
			s = strings.ReplaceAll(s, "@BIN@", bin)
			return strings.ReplaceAll(s, "@MENU@", filepath.Join(dir, "openbox", "menu.xml"))
		}},
		{"tint2/tint2rc", "tint2rc", 0o600, func(s string) string {
			var b strings.Builder
			b.WriteString(s)
			for _, a := range catalog {
				b.WriteString("launcher_item_app = " + filepath.Join(appsDir, "relay-"+a.ID+".desktop") + "\n")
			}
			return b.String()
		}},
	}
	for _, f := range files {
		s, err := asset(f.src)
		if err != nil {
			return fmt.Errorf("desktop asset %s: %w", f.src, err)
		}
		if f.edit != nil {
			s = f.edit(s)
		}
		if err := writeFileAtomic(filepath.Join(dir, f.dst), []byte(s), f.mode); err != nil {
			return err
		}
	}
	for _, a := range catalog {
		if err := writeFileAtomic(filepath.Join(bin, a.ID), []byte(launcherScript(a)), 0o700); err != nil {
			return err
		}
		entry := desktopEntry(a, filepath.Join(bin, a.ID))
		if err := writeFileAtomic(filepath.Join(appsDir, "relay-"+a.ID+".desktop"), []byte(entry), 0o600); err != nil {
			return err
		}
	}
	return writeFileAtomic(filepath.Join(dir, "openbox", "menu.xml"), []byte(openboxMenu(catalog, bin)), 0o600)
}

// oneLine strips control characters so config values cannot inject
// extra keys into .desktop files.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func desktopEntry(a deskApp, exec string) string {
	return "[Desktop Entry]\nType=Application\nVersion=1.0\n" +
		"Name=" + oneLine(a.Name) + "\n" +
		"Exec=" + exec + "\n" +
		"Icon=" + oneLine(a.Icon) + "\n" +
		"Terminal=false\nStartupNotify=true\n"
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(oneLine(s)))
	return strings.ReplaceAll(b.String(), `"`, "&#34;")
}

// openboxMenu renders the right-click root menu.
func openboxMenu(catalog []deskApp, bin string) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<openbox_menu xmlns=\"http://openbox.org/3.4/menu\">\n")
	b.WriteString("<menu id=\"root-menu\" label=\"Relay\">\n")
	for _, a := range catalog {
		fmt.Fprintf(&b, "  <item label=\"%s\"><action name=\"Execute\"><command>%s</command></action></item>\n",
			xmlEscape(a.Name), filepath.Join(bin, a.ID))
	}
	if len(catalog) > 0 {
		b.WriteString("  <separator/>\n")
	}
	b.WriteString("  <menu id=\"client-list-combined-menu\"/>\n")
	b.WriteString("  <item label=\"Reload desktop config\"><action name=\"Reconfigure\"/></item>\n")
	b.WriteString("</menu>\n</openbox_menu>\n")
	return b.String()
}

// writeFileAtomic writes data to a temp file in the same directory and
// renames it into place.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, mode)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, werr)
	}
	return nil
}

// writeXauthority writes an Xauthority file with one fresh
// MIT-MAGIC-COOKIE-1 for display num (FamilyWild: any local address).
func writeXauthority(path string, num int) error {
	cookie := make([]byte, 16)
	if _, err := rand.Read(cookie); err != nil {
		return err
	}
	var b []byte
	b = binary.BigEndian.AppendUint16(b, 0xffff) // FamilyWild
	for _, field := range [][]byte{nil, []byte(strconv.Itoa(num)), []byte("MIT-MAGIC-COOKIE-1"), cookie} {
		b = binary.BigEndian.AppendUint16(b, uint16(len(field)))
		b = append(b, field...)
	}
	return writeFileAtomic(path, b, 0o600)
}
