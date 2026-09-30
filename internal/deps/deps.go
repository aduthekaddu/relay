// Package deps pins third-party modules that features use, so that
// parallel feature branches never need to edit go.mod. Add a blank import
// here (and run `go mod tidy`) when introducing a new dependency.
package deps

import (
	_ "github.com/SherClockHolmes/webpush-go"
	_ "github.com/coder/websocket"
	_ "github.com/creack/pty"
	_ "github.com/fsnotify/fsnotify"
	_ "github.com/go-webauthn/webauthn/webauthn"
	_ "github.com/pelletier/go-toml/v2"
	_ "github.com/robfig/cron/v3"
	_ "golang.org/x/crypto/acme/autocert"
	_ "golang.org/x/crypto/argon2"
	_ "golang.org/x/image/webp"
	_ "golang.org/x/sys/unix"
	_ "golang.org/x/term"
	_ "modernc.org/sqlite"
	_ "rsc.io/qr"
)
