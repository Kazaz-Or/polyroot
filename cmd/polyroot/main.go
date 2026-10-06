// Command polyroot opens multi-repository workspaces in coding agents.
package main

import (
	"os"

	"github.com/Kazaz-Or/polyroot/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
