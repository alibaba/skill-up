package config

import (
	"errors"
	"fmt"
	"strings"
)

const (
	mcpModeMocked              = "mocked"
	builtinFilesystemMCPServer = "filesystem"
)

// MergeCaseMCP computes the effective MCP configuration for a case by merging
// case-level overrides on top of the eval-level MCP config.
//
// Merge semantics (SUP-0003):
//  1. If the case declares no servers, the eval-level config is returned as a
//     deep copy (inheritance, unchanged behavior).
//  2. Servers are merged by name while preserving eval-level order: a case-level
//     entry whose name matches an eval-level server replaces that entire entry;
//     a new name is appended to the end.
//  3. Replacement is whole-entry, not a field-level deep merge.
//  4. Duplicate server names within either level are rejected.
//  5. In the MVP, every case-level server must use mode: mocked.
//
// The returned MCPConfig is a fresh value; callers cannot mutate the original
// eval or case configuration through it.
func MergeCaseMCP(evalMCP MCPConfig, caseMCP MCPConfig) (MCPConfig, error) {
	if len(caseMCP.Servers) == 0 {
		return cloneMCPConfig(evalMCP), nil
	}

	merged := cloneMCPConfig(evalMCP)

	indexByName := make(map[string]int, len(merged.Servers))
	for i, server := range merged.Servers {
		if strings.TrimSpace(server.Name) == "" {
			return MCPConfig{}, errors.New("eval-level mcp server name is required")
		}
		if _, exists := indexByName[server.Name]; exists {
			return MCPConfig{}, fmt.Errorf("duplicate eval-level mcp server name %q", server.Name)
		}
		indexByName[server.Name] = i
	}

	seenCaseNames := make(map[string]struct{}, len(caseMCP.Servers))
	for _, server := range caseMCP.Servers {
		if server.Name == "" {
			return MCPConfig{}, errors.New("case-level mcp server name is required")
		}
		if _, exists := seenCaseNames[server.Name]; exists {
			return MCPConfig{}, fmt.Errorf("duplicate case-level mcp server name %q", server.Name)
		}
		seenCaseNames[server.Name] = struct{}{}

		if server.Mode != mcpModeMocked {
			return MCPConfig{}, fmt.Errorf("case-level mcp server %q must use mode: mocked", server.Name)
		}
		if server.Name != builtinFilesystemMCPServer && strings.TrimSpace(server.ConfigRef) == "" {
			return MCPConfig{}, fmt.Errorf("case-level mcp server %q mocked mode requires config_ref", server.Name)
		}

		cloned := cloneMCPServer(server)
		if idx, ok := indexByName[server.Name]; ok {
			merged.Servers[idx] = cloned
			continue
		}
		merged.Servers = append(merged.Servers, cloned)
		indexByName[server.Name] = len(merged.Servers) - 1
	}

	return merged, nil
}

// cloneMCPConfig returns a deep copy of an MCPConfig so that mutations of the
// result never affect the source configuration.
func cloneMCPConfig(cfg MCPConfig) MCPConfig {
	if cfg.Servers == nil {
		return MCPConfig{}
	}
	servers := make([]MCPServer, len(cfg.Servers))
	for i, server := range cfg.Servers {
		servers[i] = cloneMCPServer(server)
	}
	return MCPConfig{Servers: servers}
}

// cloneMCPServer copies an MCPServer, duplicating its slice fields.
func cloneMCPServer(server MCPServer) MCPServer {
	cloned := server
	if server.Args != nil {
		cloned.Args = append([]string(nil), server.Args...)
	}
	return cloned
}
