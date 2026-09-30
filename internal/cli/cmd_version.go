package cli

import (
	"context"
	"flag"
	"fmt"
	"runtime"

	"github.com/aduthekaddu/relay/internal/version"
)

func init() {
	Register(&Command{
		Name:    "version",
		Group:   "Other",
		Summary: "Print the Relay version",
		Run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
			fmt.Printf("relay %s", version.Version)
			if version.Commit != "" {
				fmt.Printf(" (%s)", version.Commit)
			}
			fmt.Printf(" %s/%s %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
			return nil
		},
	})
}
