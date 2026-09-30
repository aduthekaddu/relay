// Package version is set at build time with
//
//	-ldflags "-X github.com/aduthekaddu/relay/internal/version.Version=v0.1.0 -X ...Commit=abc123"
package version

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)
