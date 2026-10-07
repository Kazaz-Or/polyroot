// Command polyroot opens multi-repository workspaces in coding agents.
package main

import (
	"os"
	"runtime/debug"
	"strings"

	"github.com/Kazaz-Or/polyroot/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// `go install ...@v0.2.0` builds have no ldflags; use the module version.
	if info, ok := debug.ReadBuildInfo(); ok && version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = strings.TrimPrefix(info.Main.Version, "v")
	}
	os.Exit(cli.Execute(version))
}
