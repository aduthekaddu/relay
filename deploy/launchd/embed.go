// Package launchd embeds the macOS LaunchAgent templates that
// `relay setup` renders into ~/Library/LaunchAgents.
//
// Templates use text/template with an `xml` escaping helper. Data:
// {Binary, LogDir string; EnvPairs []struct{Key, Value string}}.
package launchd

import "embed"

// Agents maps plist file name to template source.
//
//go:embed *.plist
var Agents embed.FS

// Names lists the plists in start order (ptyd first).
var Names = []string{"dev.relay.ptyd.plist", "dev.relay.serve.plist"}

// Label returns the launchd label for a plist file name.
func Label(name string) string {
	const ext = ".plist"
	if len(name) > len(ext) && name[len(name)-len(ext):] == ext {
		return name[:len(name)-len(ext)]
	}
	return name
}
