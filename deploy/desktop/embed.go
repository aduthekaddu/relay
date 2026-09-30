// Package desktop embeds the Relay remote-desktop session files: the
// xstartup session script, the openbox config and theme, and the tint2
// panel config. internal/apps renders them into a private per-instance
// directory before starting the session, so the user's own window
// manager and panel configuration are never read or written.
package desktop

import "embed"

// Files holds the session assets, rooted at this directory:
//
//	xstartup                         session script (sh)
//	openbox/rc.xml                   window manager config (@BIN@, @MENU@ placeholders)
//	tint2/tint2rc                    panel config (launcher items appended)
//	theme/Relay/openbox-3/themerc    openbox theme
//
//go:embed xstartup openbox/rc.xml tint2/tint2rc theme/Relay/openbox-3/themerc
var Files embed.FS
