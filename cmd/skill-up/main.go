// Command skill-up is the CLI entrypoint for the skill-up framework.
package main

import (
	"os"

	"github.com/alibaba/skill-up/internal/cli"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

// exitFailure is the exit code returned when the CLI fails.
const exitFailure = 1

func main() {
	if err := cli.Execute(version); err != nil {
		os.Exit(exitFailure)
	}
}
