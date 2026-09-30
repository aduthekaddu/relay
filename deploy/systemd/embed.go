// Package systemd embeds the systemd user unit templates that
// `relay setup` renders into ~/.config/systemd/user.
//
// Templates use text/template with two helpers provided by the renderer:
// `exec` (quote an ExecStart command line) and `envq` (quote one
// Environment= assignment). Data: {Binary string; Env []string}.
package systemd

import "embed"

// Units maps unit file name to template source.
//
//go:embed *.service
var Units embed.FS

// Names lists the unit files in start order (ptyd first).
var Names = []string{"relay-ptyd.service", "relay.service"}
