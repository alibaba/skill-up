// Package mcp configures MCP servers in mocked or real modes for evaluation runs.
package mcp

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/runtime"
)

const (
	modeReal       = "real"
	modeMocked     = "mocked"
	transportHTTP  = "http"
	transportStdio = "stdio"
)

var (
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envRefPattern  = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
)

// Provisioner resolves MCP server configuration before it is installed into an
// engine-specific agent. It deliberately requires all real-server auth to be
// available non-interactively from process environment variables.
type Provisioner struct {
	SkillDir  string
	LookupEnv func(string) (string, bool)
}

// Provision resolves eval MCP configuration into runtime-ready server config
// and the environment variables that must be present for agent execution.
func (p Provisioner) Provision(mcpCfg config.MCPConfig) (runtime.MCPConfig, map[string]string, error) {
	lookupEnv := p.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	servers := make([]runtime.MCPServerConfig, 0, len(mcpCfg.Servers))
	runEnv := map[string]string{}
	for _, server := range mcpCfg.Servers {
		resolved, err := p.resolveServer(server, lookupEnv)
		if err != nil {
			return runtime.MCPConfig{}, nil, err
		}
		maps.Copy(runEnv, resolved.Env)
		servers = append(servers, resolved)
	}

	return runtime.MCPConfig{Servers: servers}, runEnv, nil
}

func (p Provisioner) resolveServer(server config.MCPServer, lookupEnv func(string) (string, bool)) (runtime.MCPServerConfig, error) {
	if err := validateServerMode(server); err != nil {
		return runtime.MCPServerConfig{}, err
	}

	fileCfg, configRef, err := p.loadServerFileConfig(server)
	if err != nil {
		return runtime.MCPServerConfig{}, err
	}
	if server.Mode == modeMocked {
		return buildMockedRuntimeServerConfig(server, fileCfg, configRef)
	}

	endpoint, endpointEnv, err := resolveEndpoint(server, fileCfg, lookupEnv)
	if err != nil {
		return runtime.MCPServerConfig{}, err
	}
	command, args := resolveCommandAndArgs(server, fileCfg)

	transport, err := resolveTransport(server.Name, server.Transport, fileCfg.Transport, endpoint, command)
	if err != nil {
		return runtime.MCPServerConfig{}, err
	}

	env, headers, headerEnv, err := resolveAuth(server.Name, fileCfg, lookupEnv)
	if err != nil {
		return runtime.MCPServerConfig{}, err
	}
	maps.Copy(env, endpointEnv)

	return buildRuntimeServerConfig(server, runtimeServerFields{
		transport: transport,
		command:   command,
		args:      args,
		endpoint:  endpoint,
		configRef: configRef,
		env:       env,
		headers:   headers,
		headerEnv: headerEnv,
	}), nil
}

func validateServerMode(server config.MCPServer) error {
	switch server.Mode {
	case "":
		return fmt.Errorf("mcp server %q mode is required", server.Name)
	case modeMocked:
		return nil
	case modeReal:
		return nil
	default:
		return fmt.Errorf("mcp server %q mode must be one of: mocked, real", server.Name)
	}
}

func (p Provisioner) loadServerFileConfig(server config.MCPServer) (mcpServerFile, string, error) {
	configRef := strings.TrimSpace(server.ConfigRef)
	if configRef == "" {
		return mcpServerFile{}, "", nil
	}

	path := p.resolveConfigRef(configRef)
	fileCfg, err := loadServerFile(path)
	if err != nil {
		return mcpServerFile{}, "", fmt.Errorf("failed to load mcp server %q config_ref %s: %w", server.Name, configRef, err)
	}
	return fileCfg, path, nil
}

func resolveEndpoint(
	server config.MCPServer,
	fileCfg mcpServerFile,
	lookupEnv func(string) (string, bool),
) (string, map[string]string, error) {
	endpoint := strings.TrimSpace(server.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(fileCfg.Endpoint)
	}
	if endpoint == "" {
		return "", nil, nil
	}
	env, err := resolveReferencedEnv(endpoint, lookupEnv)
	if err != nil {
		return "", nil, fmt.Errorf("mcp server %q endpoint: %w", server.Name, err)
	}
	return endpoint, env, nil
}

func resolveCommandAndArgs(server config.MCPServer, fileCfg mcpServerFile) (string, []string) {
	command := strings.TrimSpace(server.Command)
	if command == "" {
		command = strings.TrimSpace(fileCfg.Command)
	}
	args := fileCfg.Args
	if server.Args != nil {
		args = server.Args
	}
	return command, args
}

func resolveAuth(
	serverName string,
	fileCfg mcpServerFile,
	lookupEnv func(string) (string, bool),
) (env map[string]string, headers map[string]string, headerEnv map[string]string, err error) {
	env, err = resolveEnv(fileCfg.Env, fileCfg.RequiredEnv, lookupEnv)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mcp server %q auth env: %w", serverName, err)
	}
	headers, headerEnv, headerRunEnv, err := resolveHeaders(fileCfg.Headers, lookupEnv)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mcp server %q headers: %w", serverName, err)
	}
	maps.Copy(env, headerRunEnv)
	return env, headers, headerEnv, nil
}

type runtimeServerFields struct {
	transport string
	command   string
	args      []string
	endpoint  string
	configRef string
	env       map[string]string
	headers   map[string]string
	headerEnv map[string]string
}

func buildRuntimeServerConfig(server config.MCPServer, fields runtimeServerFields) runtime.MCPServerConfig {
	return runtime.MCPServerConfig{
		Name:      server.Name,
		Mode:      server.Mode,
		Transport: fields.transport,
		Command:   fields.command,
		Args:      append([]string(nil), fields.args...),
		Endpoint:  fields.endpoint,
		ConfigRef: fields.configRef,
		Env:       fields.env,
		Headers:   fields.headers,
		HeaderEnv: fields.headerEnv,
	}
}

func (p Provisioner) resolveConfigRef(configRef string) string {
	if filepath.IsAbs(configRef) || p.SkillDir == "" {
		return filepath.Clean(configRef)
	}
	return filepath.Join(p.SkillDir, configRef)
}

type mcpServerFile struct {
	Transport     string            `yaml:"transport"`
	Command       string            `yaml:"command"`
	Args          []string          `yaml:"args"`
	Endpoint      string            `yaml:"endpoint"`
	Env           map[string]string `yaml:"env"`
	RequiredEnv   []string          `yaml:"required_env"`
	Headers       map[string]string `yaml:"headers"`
	ToolResponses map[string]any    `yaml:"tool_responses"`
}

func loadServerFile(path string) (mcpServerFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return mcpServerFile{}, err
	}
	var cfg mcpServerFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return mcpServerFile{}, err
	}
	return cfg, nil
}

func resolveTransport(serverName, serverTransport, fileTransport, endpoint, command string) (string, error) {
	transport := strings.TrimSpace(serverTransport)
	if transport == "" {
		transport = strings.TrimSpace(fileTransport)
	}
	switch transport {
	case "":
		if command != "" {
			transport = transportStdio
		} else {
			transport = transportHTTP
		}
	case "streamable_http", "streamable-http":
		transport = transportHTTP
	}

	switch transport {
	case transportHTTP:
		if endpoint == "" {
			return "", fmt.Errorf("mcp server %q real http mode requires endpoint or config_ref endpoint", serverName)
		}
	case transportStdio:
		if command == "" {
			return "", fmt.Errorf("mcp server %q real stdio mode requires command or config_ref command", serverName)
		}
	default:
		return "", fmt.Errorf("mcp server %q transport must be one of: stdio, http", serverName)
	}
	return transport, nil
}

func resolveEnv(env map[string]string, required []string, lookupEnv func(string) (string, bool)) (map[string]string, error) {
	resolved := map[string]string{}
	for _, name := range required {
		value, ok := lookupEnv(name)
		if !ok || value == "" {
			return nil, fmt.Errorf("required environment variable %s is not set", name)
		}
		resolved[name] = value
	}
	for key, value := range env {
		resolvedValue, err := resolveEnvValue(value, lookupEnv)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		resolved[key] = resolvedValue
	}
	return resolved, nil
}

func resolveHeaders(
	headers map[string]string,
	lookupEnv func(string) (string, bool),
) (resolved map[string]string, envNames map[string]string, runEnv map[string]string, err error) {
	resolved = map[string]string{}
	envNames = map[string]string{}
	runEnv = map[string]string{}
	for key, value := range headers {
		if name, ok := wholeEnvRef(value); ok {
			resolvedValue, exists := lookupEnv(name)
			if !exists || resolvedValue == "" {
				return nil, nil, nil, fmt.Errorf("%s: referenced environment variable %s is not set", key, name)
			}
			resolved[key] = value
			envNames[key] = name
			runEnv[name] = resolvedValue
			continue
		}
		valueEnv, err := resolveReferencedEnv(value, lookupEnv)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		maps.Copy(runEnv, valueEnv)
		resolvedValue, err := resolveEnvValue(value, lookupEnv)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		if resolvedValue != value {
			resolved[key] = value
			continue
		}
		resolved[key] = value
	}
	return resolved, envNames, runEnv, nil
}

func resolveReferencedEnv(value string, lookupEnv func(string) (string, bool)) (map[string]string, error) {
	if name, ok := wholeEnvRef(value); ok {
		resolved, exists := lookupEnv(name)
		if !exists || resolved == "" {
			return nil, fmt.Errorf("referenced environment variable %s is not set", name)
		}
		return map[string]string{name: resolved}, nil
	}
	if strings.HasPrefix(value, "$") {
		return nil, fmt.Errorf("invalid environment variable reference %q", value)
	}

	resolved := map[string]string{}
	var missing []string
	for _, match := range envRefPattern.FindAllStringSubmatch(value, -1) {
		name := match[1]
		envValue, ok := lookupEnv(name)
		if !ok || envValue == "" {
			missing = append(missing, name)
			continue
		}
		resolved[name] = envValue
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("referenced environment variable %s is not set", strings.Join(missing, ", "))
	}
	return resolved, nil
}

func resolveEnvValue(value string, lookupEnv func(string) (string, bool)) (string, error) {
	if name, ok := wholeEnvRef(value); ok {
		resolved, exists := lookupEnv(name)
		if !exists || resolved == "" {
			return "", fmt.Errorf("referenced environment variable %s is not set", name)
		}
		return resolved, nil
	}
	if strings.HasPrefix(value, "$") {
		return "", fmt.Errorf("invalid environment variable reference %q", value)
	}

	var missing []string
	resolved := envRefPattern.ReplaceAllStringFunc(value, func(match string) string {
		name := envRefPattern.FindStringSubmatch(match)[1]
		envValue, ok := lookupEnv(name)
		if !ok || envValue == "" {
			missing = append(missing, name)
			return match
		}
		return envValue
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("referenced environment variable %s is not set", strings.Join(missing, ", "))
	}
	return resolved, nil
}

func wholeEnvRef(value string) (string, bool) {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		match := envRefPattern.FindStringSubmatch(value)
		if len(match) == 2 && match[0] == value {
			return match[1], true
		}
	}
	if name, ok := strings.CutPrefix(value, "$"); ok {
		if envNamePattern.MatchString(name) {
			return name, true
		}
	}
	return "", false
}
