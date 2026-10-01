package config

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
)

// SettingsSourceID identifies file and home expansion semantics without
// exposing either path through the browser API.
func SettingsSourceID(p Paths) string {
	file, err := filepath.Abs(p.ConfigFile)
	if err != nil || p.ConfigFile == "" {
		return ""
	}
	home, err := filepath.Abs(p.Home)
	if err != nil || p.Home == "" {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(file+"\x00"+home)))
}
