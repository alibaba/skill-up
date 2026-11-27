// Package cli defines Cobra commands for the skill-up CLI.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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
// persistent --config flag is resolved without setting flag values before Cobra
// executes the selected command.
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

// loadUserConfig loads defaults before command execution.
// For init, --config is a copy source and must not grant expansion permission.
func loadUserConfig(args []string) (userconfig.Config, []userconfig.Source, error) {
	explicit, err := resolveConfigFlag(args)
	if err != nil {
		return userconfig.Config{}, nil, err
	}
	return userconfig.LoadEffective(userconfig.LoadOptions{
		ExplicitPath: explicit,
		Env:          os.Getenv,
	})
}

// resolveConfigFlag preflights --config using Cobra's selected command and
// pflag's parser. A second occurrence is rejected before any config is applied.
// The parser callback only collects --config, without mutating any flag values.
func resolveConfigFlag(args []string) (string, error) {
	// Shell completion receives the proposed invocation as data, not flags
	// for an execution. Do not grant expansion based on that data.
	if len(args) > 0 && (args[0] == cobra.ShellCompRequestCmd || args[0] == cobra.ShellCompNoDescRequestCmd) {
		return "", nil
	}
	// Keep normal command parsing and error reporting unchanged when the
	// invocation has no potential --config argument.
	if !hasConfigFlagArg(args) {
		return "", nil
	}
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd(args...)
	cmd, flagArgs, err := rootCmd.Find(args)
	if err != nil || cmd.DisableFlagParsing {
		return "", err
	}
	cmd.InitDefaultHelpFlag()
	cmd.InitDefaultVersionFlag()
	_ = cmd.InheritedFlags() // Ensure Flags includes persistent definitions.
	definitions := cmd.Flags()
	flags := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	flags.SetNormalizeFunc(definitions.GetNormalizeFunc())
	flags.ParseErrorsAllowlist = pflag.ParseErrorsAllowlist(cmd.FParseErrWhitelist)
	definitions.VisitAll(func(flag *pflag.Flag) {
		definition := *flag
		flags.AddFlag(&definition)
	})

	var explicit string
	var seen bool
	err = flags.ParseAll(flagArgs, func(flag *pflag.Flag, value string) error {
		if flag.Name != configFlagName {
			return nil
		}
		if seen {
			return errors.New("--config may only be specified once")
		}
		seen = true
		explicit = value
		return nil
	})
	if err != nil || cmd == initCmd {
		return "", err
	}
	return explicit, nil
}

func hasConfigFlagArg(args []string) bool {
	for _, arg := range args {
		// Do not stop at --: it may be the value of a preceding flag.
		// The actual parser distinguishes a value from the delimiter.
		if arg == "--"+configFlagName || strings.HasPrefix(arg, "--"+configFlagName+"=") {
			return true
		}
	}
	return false
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

	rootCmd.PersistentFlags().String(configFlagName, "", "Path to a trusted skill-up config (enables ${VAR} expansion in its env defaults)")

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
