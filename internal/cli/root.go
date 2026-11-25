// Package cli defines Cobra commands for the skill-up CLI.
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/userconfig"
)

// configFlagName is the persistent --config flag pointing to a skill-up
// user-config file.
const configFlagName = "config"

// Execute runs the root command with the given version string.
func Execute(version string) error {
	return ExecuteArgs(version, nil)
}

// ExecuteArgs runs the root command with explicit command-line arguments.
//
// User-config is loaded BEFORE observability.Init so that any OTEL_* env vars
// derived from the config are visible to the OTel SDK on first read. The
// persistent --config flag is parsed manually here because cobra has not yet
// processed flags by the time we need them.
func ExecuteArgs(version string, args []string) error {
	rootCmd.Version = version

	configFlagArgs := args
	if configFlagArgs == nil {
		configFlagArgs = os.Args[1:]
	}
	cfg, sources, err := loadUserConfig(configFlagArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return err
	}
	if _, err := userconfig.ApplyEnv(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to apply user-config env: %v\n", err)
	}

	ctx, shutdown, err := observability.Init(context.Background(), version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to initialize observability: %v\n", err)
		ctx = context.Background()
		shutdown = func(context.Context) error { return nil }
	}
	defer func() { _ = shutdown(context.Background()) }()
	ctx = userconfig.WithContext(ctx, cfg, sources)
	rootCmd.SetContext(ctx)
	rootCmd.SetArgs(args)

	return rootCmd.Execute()
}

// loadUserConfig extracts --config from raw args and delegates to
// userconfig.LoadEffective. Only an explicit --config <path> that is
// missing or corrupt is treated as fatal; auto-discovered user and
// project layers downgrade parse errors to stderr warnings inside
// LoadEffective and continue with a partial config.
//
// For the `init` subcommand --config names a WRITE target, not a load
// path, so we clear the explicit path before delegating. Discovery of
// existing user/project layers still runs but cannot block init
// because of the warning downgrade above.
func loadUserConfig(args []string) (userconfig.Config, []userconfig.Source, error) {
	explicit := extractConfigFlag(args)
	if isInitInvocation(args) {
		explicit = ""
	}
	return userconfig.LoadEffective(userconfig.LoadOptions{
		ExplicitPath: explicit,
		Env:          os.Getenv,
	})
}

// persistentValueFlags lists the long names of root-level flags that consume
// a following positional argument as their value (e.g. `--config foo`). When
// adding a new persistent flag that takes a value, append its name here so the
// pre-cobra scanners below skip its value correctly.
var persistentValueFlags = map[string]struct{}{
	configFlagName: {},
}

// isInitInvocation reports whether args invokes the `init` subcommand. It
// must work before cobra has parsed the flag set, so it is a small POSIX-ish
// scanner that stops at `--` (end-of-flags) and only consumes values for the
// persistent flags it explicitly knows about.
func isInitInvocation(args []string) bool {
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			return false
		}
		if name, hasValue, consumed := parseLongFlag(a); name != "" {
			if hasValue {
				i += consumed
				continue
			}
			if _, ok := persistentValueFlags[name]; ok && i+1 < len(args) {
				i += 2
				continue
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			i++
			continue
		}
		return a == "init"
	}
	return false
}

// extractConfigFlag returns the value of --config in args, if present. Stops
// scanning at `--` so positional arguments after end-of-flags are not
// misinterpreted as flag values.
func extractConfigFlag(args []string) string {
	for i := range args {
		a := args[i]
		if a == "--" {
			return ""
		}
		name, hasValue, _ := parseLongFlag(a)
		if name != configFlagName {
			continue
		}
		if hasValue {
			return strings.TrimPrefix(a, "--"+configFlagName+"=")
		}
		if i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// parseLongFlag returns the long-flag name (if any), whether it has an inline
// `=value`, and how many args it consumed (always 1 here; callers handle the
// 2-token form themselves to keep the value lookup explicit).
func parseLongFlag(a string) (name string, hasValue bool, consumed int) {
	if !strings.HasPrefix(a, "--") || a == "--" {
		return "", false, 0
	}
	body := a[2:]
	if k, _, ok := strings.Cut(body, "="); ok {
		return k, true, 1
	}
	return body, false, 1
}

var rootCmd = &cobra.Command{
	Use:          "skill-up",
	Short:        "Agent Skill evaluation framework CLI",
	Long:         "Load eval.yaml, run cases against Agent Engines, and produce reports.",
	SilenceUsage: true,
}

func init() {
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		_ = cmd.Usage()
		return err
	})

	rootCmd.PersistentFlags().String(configFlagName, "", "Path to skill-up user config (overrides discovered defaults)")

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(listCasesCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(compareCmd)
	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(importCmd)
}

func usageOnError(args cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, argv []string) error {
		if err := args(cmd, argv); err != nil {
			_ = cmd.Usage()
			return err
		}

		return nil
	}
}
