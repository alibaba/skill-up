package agent

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// CLIAgent is a generic CLI-based agent implementation.
type CLIAgent struct {
	BaseAgent
}

// InstallMCP installs MCP servers into the runtime environment.
func (a *CLIAgent) InstallMCP(ctx context.Context, rt Runtime, mcpCfg runtime.MCPConfig) error {
	if a.Cfg.InstallMCPCmd == "" {
		if len(mcpCfg.Servers) > 0 {
			return fmt.Errorf("agent %s does not support MCP installation: InstallMCPCmd is not configured", a.Name())
		}
		return nil
	}

	tmpl, err := template.New("installMCP").Parse(a.Cfg.InstallMCPCmd)
	if err != nil {
		return fmt.Errorf("invalid InstallMCPCmd template: %w", err)
	}

	workspace := rt.Workspace()

	for _, server := range mcpCfg.Servers {
		data := struct {
			Name      string
			Transport string
			Command   string
			Args      []string
			Endpoint  string
			ConfigRef string
			Workspace string
			Env       map[string]string
			Headers   map[string]string
		}{
			Name:      server.Name,
			Transport: server.Transport,
			Command:   server.Command,
			Args:      server.Args,
			Endpoint:  server.Endpoint,
			ConfigRef: server.ConfigRef,
			Workspace: workspace,
			Env:       server.Env,
			Headers:   server.Headers,
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("failed to execute InstallMCPCmd for %s: %w", server.Name, err)
		}

		cmd := buf.String()
		result, err := rt.Exec(ctx, cmd, ExecOptions{Env: server.Env})
		if err != nil {
			return fmt.Errorf("failed to install MCP server %s: %w", server.Name, err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("MCP server %s installation failed: %s", server.Name, result.Stderr)
		}
	}

	return nil
}

// InstallSkill installs a skill into the runtime environment.
func (a *CLIAgent) InstallSkill(ctx context.Context, rt Runtime, skillCfg runtime.SkillConfig) error {
	if a.Cfg.InstallSkillCmd == "" {
		return a.installSkillDefault(ctx, rt, skillCfg)
	}

	tmpl, err := template.New("installSkill").Parse(a.Cfg.InstallSkillCmd)
	if err != nil {
		return fmt.Errorf("invalid InstallSkillCmd template: %w", err)
	}

	workspace := rt.Workspace()
	target := skillCfg.Target
	if target == "" {
		target = filepath.Join(workspace, "skills", filepath.Base(skillCfg.Source))
	}

	data := struct {
		Source    string
		Target    string
		Workspace string
	}{
		Source:    skillCfg.Source,
		Target:    target,
		Workspace: workspace,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute InstallSkillCmd: %w", err)
	}

	cmd := buf.String()
	result, err := rt.Exec(ctx, cmd, ExecOptions{})
	if err != nil {
		return fmt.Errorf("failed to install skill: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("skill installation failed: %s", result.Stderr)
	}

	return nil
}

// Run executes the agent with the given messages and returns the session result.
func (a *CLIAgent) Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (*SessionResult, error) {
	if err := requireBashOnWindowsHost(rt); err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name(), err)
	}
	start := time.Now()

	instruction := BuildInstructionFromMessages(messages)
	cmd := fmt.Sprintf(a.Cfg.RunCmd, shellQuote(instruction))
	opts = a.mergeExecOptionsEnv(ctx, opts, a.credentialEnvVars("", ""), a.buildAgentObservabilityAttrs(nil))
	ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)
	result, err := rt.Exec(ctx, cmd, opts)
	sessionResult := &SessionResult{
		Engine:       a.Name(),
		ExitCode:     result.ExitCode,
		DurationMs:   time.Since(start).Milliseconds(),
		FinalMessage: result.Stdout,
		Stderr:       result.Stderr,
		Transcript:   nil,
		Artifacts:    &SessionArtifacts{},
	}
	if err != nil {
		if sessionResult.ExitCode == 0 {
			sessionResult.ExitCode = 1
		}
		return sessionResult, fmt.Errorf("agent run failed: %w", err)
	}

	if result.ExitCode != 0 {
		return sessionResult, fmt.Errorf("agent run failed (exit %d): %s", result.ExitCode, result.Stderr)
	}

	return sessionResult, nil
}

// commandVRegexp matches a POSIX `command -v <binary> [rest]` check. The
// regex form (vs strings.CutPrefix) lets us capture the binary separately
// from any trailing redirect or pipe, and supports surrounding whitespace
// the way a real shell would.
var commandVRegexp = regexp.MustCompile(`^\s*command\s+-v\s+(\S+)(\s.*)?$`)

// checkCommandForOS adapts a POSIX `command -v X` availability check to the
// target OS. Windows cmd.exe has no `command` builtin; `where` is the
// equivalent. Common POSIX-only redirect targets (`/dev/null`) are rewritten
// to their cmd equivalent (`nul`) so a quiet probe like
// `command -v codex >/dev/null 2>&1` continues to silence its output
// instead of failing to open the missing /dev/null path. Other command
// forms are returned unchanged.
func checkCommandForOS(checkCmd, goos string) string {
	if goos != platform.GOOSWindows {
		return checkCmd
	}
	m := commandVRegexp.FindStringSubmatch(checkCmd)
	if m == nil {
		return checkCmd
	}
	binary, rest := m[1], m[2]
	rest = strings.ReplaceAll(rest, "/dev/null", "nul")
	return "where " + binary + rest
}

// Check verifies the agent executable is available.
func (a *CLIAgent) Check(ctx context.Context, rt Runtime) error {
	if err := requireBashOnWindowsHost(rt); err != nil {
		return fmt.Errorf("%s: %w", a.Name(), err)
	}
	checkCmd := a.Cfg.CheckCmd
	if checkCmd == "" {
		return fmt.Errorf("CheckCmd not configured for agent %s", a.Name())
	}
	checkCmd = checkCommandForOS(checkCmd, rt.TargetGOOS())

	result, err := rt.Exec(ctx, checkCmd, a.mergeExecOptionsEnv(ctx, ExecOptions{}, nil, nil))
	if err != nil {
		return fmt.Errorf("check failed: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%w: %s not found", ErrAgentNotFound, a.Name())
	}

	return nil
}
