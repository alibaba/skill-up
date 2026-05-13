// Package skillup exposes the skill-up CLI for wrapper binaries.
package skillup

import "github.com/alibaba/skill-up/internal/cli"

// Execute runs skill-up with explicit command-line arguments.
func Execute(version string, args []string) error {
	return cli.ExecuteArgs(version, args)
}
