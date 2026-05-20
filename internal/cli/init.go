package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/userconfig"
)

const (
	initDirMode  = 0o755
	initFileMode = 0o600
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Write a skill-up user-config file",
	Long: `Write a skill-up user-config file.

Without --config, writes a commented YAML template. With --config <path>,
reads that file (validating it as a skill-up config) and writes its raw
contents to the target, preserving comments and formatting.

Target selection:
  default        $XDG_CONFIG_HOME/skill-up/config.yaml (or ~/.config/...)
  --local        $PWD/.skill-up.yaml
  --print        stdout (no file written)`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().Bool("local", false, "Write target is $PWD/.skill-up.yaml instead of the XDG path")
	initCmd.Flags().Bool("print", false, "Print to stdout instead of writing to disk")
	initCmd.Flags().Bool("force", false, "Overwrite an existing file")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, _ []string) error {
	local, _ := cmd.Flags().GetBool("local")
	printOnly, _ := cmd.Flags().GetBool("print")
	force, _ := cmd.Flags().GetBool("force")
	sourcePath, _ := cmd.Flags().GetString(configFlagName)

	content, err := resolveInitContent(sourcePath)
	if err != nil {
		return err
	}

	if printOnly {
		_, err := fmt.Fprint(cmd.OutOrStdout(), content)
		return err
	}

	target, err := resolveInitTarget(local)
	if err != nil {
		return err
	}
	target = filepath.Clean(target)

	if _, statErr := os.Stat(target); statErr == nil && !force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), initDirMode); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(content), initFileMode); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", target)
	return nil
}

// resolveInitContent returns the bytes to write. If sourcePath is empty the
// embedded template is used. Otherwise the source is loaded and validated as
// a skill-up config, and its raw bytes are returned so comments/formatting
// survive.
func resolveInitContent(sourcePath string) (string, error) {
	if sourcePath == "" {
		return initTemplateContent, nil
	}
	if _, err := userconfig.LoadFile(sourcePath); err != nil {
		return "", err
	}
	data, err := os.ReadFile(sourcePath) //nolint:gosec // path is user-supplied by design
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func resolveInitTarget(local bool) (string, error) {
	if local {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(wd, userconfig.ProjectConfigFile), nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, userconfig.UserConfigDir, userconfig.UserConfigFile), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", userconfig.UserConfigDir, userconfig.UserConfigFile), nil
}

// initTemplateContent is the commented YAML template used by `init` when no
// --config source is given. All optional knobs are commented out so the
// default behavior is a no-op.
const initTemplateContent = `# skill-up user config -- auto-loaded by every command.
# Discovery order (lowest to highest precedence):
#   embedded defaults < ~/.config/skill-up/config.yaml < $PWD/.skill-up.yaml < --config
# Use ` + "`skill-up init --print`" + ` to regenerate this template.

schema_version: v1alpha1
kind: SkillEvalConfig

# OpenTelemetry defaults. Each key maps to a well-known OTEL_* env var.
# Values are only applied if the corresponding env var is not already set.
telemetry:
  # OTEL_SERVICE_NAME
  # service_name: skill-up

  # OTEL_TRACES_EXPORTER. Common values: otlp, none.
  # traces_exporter: otlp

  traces:
    # OTEL_EXPORTER_OTLP_TRACES_ENDPOINT.
    # Default protocol is grpc (port 4317). For http/protobuf use
    # http://localhost:4318/v1/traces.
    # endpoint: http://localhost:4317
    # OTEL_EXPORTER_OTLP_TRACES_PROTOCOL. grpc or http/protobuf.
    # protocol: grpc

  # OTEL_RESOURCE_ATTRIBUTES, serialized as k=v,k=v in sorted order.
  # resource_attributes:
  #   deployment.environment: local
  #   service.namespace: skill-up

  # When true, also sets OTEL_LOG_USER_PROMPTS=1, OTEL_LOG_TOOL_CONTENT=1,
  # OTEL_LOG_TOOL_DETAILS=1 to capture agent-side payloads in traces.
  # verbose: false

# Arbitrary additional env vars. Same only-if-unset semantics as telemetry.
# Use ${VAR} placeholders for secrets so they are not baked into the file.
# env:
#   OTEL_EXPORTER_OTLP_HEADERS: authorization=${OTLP_TOKEN}

# Per-environment.type default kwargs for ` + "`run`" + `. eval.yaml values win;
# missing keys are filled from here. Keys mirror the environment provider's
# kwargs schema (see docs/design-docs for the opensandbox provider).
# runtime_kwargs:
#   opensandbox:
#     base_url: http://localhost:8080
#     # extensions: '{}'
`
