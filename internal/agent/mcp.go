package agent

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/alibaba/skill-up/internal/runtime"
)

const (
	mcpTransportHTTP  = "http"
	mcpTransportStdio = "stdio"
)

var (
	shellEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	shellEnvRefPattern  = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
)

type mcpInstallCommandBuilder func(runtime.MCPServerConfig) (string, error)

func installMCPServers(ctx context.Context, rt Runtime, mcpCfg runtime.MCPConfig, build mcpInstallCommandBuilder) error {
	for _, server := range mcpCfg.Servers {
		if server.Mode == "mocked" {
			continue
		}

		cmd, err := build(server)
		if err != nil {
			return err
		}
		// ExecOptions.Env exposes auth values to the one-shot install process.
		// Agent CLI --env flags persist selected variables into the registered
		// MCP server config so future tool calls can read them.
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

func buildClaudeCompatibleMCPInstallCmd(binary, agentName string, server runtime.MCPServerConfig) (string, error) {
	var cmd strings.Builder
	cmd.WriteString(binary)
	cmd.WriteString(" mcp remove --scope project ")
	cmd.WriteString(shellQuote(server.Name))
	cmd.WriteString(" >/dev/null 2>&1 || true\n")
	switch server.Transport {
	case mcpTransportStdio:
		if server.Command == "" {
			return "", fmt.Errorf("mcp server %q stdio transport requires command", server.Name)
		}
		cmd.WriteString(binary)
		cmd.WriteString(" mcp add --scope project ")
		cmd.WriteString(shellQuote(server.Name))
		for _, key := range sortedMapKeys(server.Env) {
			envArg, err := shellEnvAssignment(key)
			if err != nil {
				return "", fmt.Errorf("mcp server %q env %q is invalid: %w", server.Name, key, err)
			}
			cmd.WriteString(" -e ")
			cmd.WriteString(envArg)
		}
		cmd.WriteString(" -- ")
		cmd.WriteString(shellQuote(server.Command))
		for _, arg := range server.Args {
			cmd.WriteByte(' ')
			cmd.WriteString(shellQuote(arg))
		}
	case "", mcpTransportHTTP:
		if server.Endpoint == "" {
			return "", fmt.Errorf("mcp server %q http transport requires endpoint", server.Name)
		}
		cmd.WriteString(binary)
		cmd.WriteString(" mcp add --scope project --transport http ")
		cmd.WriteString(shellQuote(server.Name))
		cmd.WriteByte(' ')
		endpoint, err := shellExpandedValue(server.Endpoint)
		if err != nil {
			return "", fmt.Errorf("mcp server %q endpoint is invalid: %w", server.Name, err)
		}
		cmd.WriteString(endpoint)
		for _, key := range sortedMapKeys(server.Headers) {
			header, err := shellExpandedValue(key + ":" + server.Headers[key])
			if err != nil {
				return "", fmt.Errorf("mcp server %q header %q is invalid: %w", server.Name, key, err)
			}
			cmd.WriteString(" --header ")
			cmd.WriteString(header)
		}
	default:
		return "", fmt.Errorf("mcp server %q transport %q is not supported by %s", server.Name, server.Transport, agentName)
	}
	return cmd.String(), nil
}

func shellEnvAssignment(name string) (string, error) {
	if !shellEnvNamePattern.MatchString(name) {
		return "", fmt.Errorf("environment variable name %q must match %s", name, shellEnvNamePattern.String())
	}
	return shellQuote(name+"=") + "\"$" + name + "\"", nil
}

func shellExpandedValue(value string) (string, error) {
	if name, ok := wholeShellEnvRef(value); ok {
		return "\"${" + name + "}\"", nil
	}
	if strings.HasPrefix(value, "$") {
		return "", fmt.Errorf("invalid environment variable reference %q", value)
	}

	matches := shellEnvRefPattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return shellQuote(value), nil
	}

	var out strings.Builder
	pos := 0
	for _, match := range matches {
		if match[0] > pos {
			out.WriteString(shellQuote(value[pos:match[0]]))
		}
		name := value[match[2]:match[3]]
		if !shellEnvNamePattern.MatchString(name) {
			return "", fmt.Errorf("environment variable name %q must match %s", name, shellEnvNamePattern.String())
		}
		out.WriteString("\"${")
		out.WriteString(name)
		out.WriteString("}\"")
		pos = match[1]
	}
	if pos < len(value) {
		out.WriteString(shellQuote(value[pos:]))
	}
	return out.String(), nil
}

func wholeShellEnvRef(value string) (string, bool) {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		match := shellEnvRefPattern.FindStringSubmatch(value)
		if len(match) == 2 && match[0] == value {
			return match[1], true
		}
	}
	if name, ok := strings.CutPrefix(value, "$"); ok {
		if shellEnvNamePattern.MatchString(name) {
			return name, true
		}
	}
	return "", false
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedUniqueMapValues(values map[string]string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for value := range seen {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}
