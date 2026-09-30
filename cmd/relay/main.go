// Command relay is a single binary that serves the Relay web app, runs the
// terminal session daemon and provides the command line tools.
package main

import (
	"os"

	_ "github.com/aduthekaddu/relay/internal/app" // registers serve and feature commands
	"github.com/aduthekaddu/relay/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args))
}
