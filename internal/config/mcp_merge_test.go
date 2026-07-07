package config

import (
	"reflect"
	"testing"
)

func TestMergeCaseMCP_InheritsWhenNoCaseServers(t *testing.T) {
	t.Parallel()
	evalMCP := MCPConfig{Servers: []MCPServer{
		{Name: "project-mgmt", Mode: "mocked", ConfigRef: "evals/fixtures/mcp/default.yaml"},
	}}

	got, err := MergeCaseMCP(evalMCP, MCPConfig{})
	if err != nil {
		t.Fatalf("MergeCaseMCP returned error: %v", err)
	}
	if !reflect.DeepEqual(got, evalMCP) {
		t.Errorf("expected inherited eval MCP %+v, got %+v", evalMCP, got)
	}
}

func TestMergeCaseMCP_ReplacesSameNameWholeEntryPreservingOrder(t *testing.T) {
	t.Parallel()
	evalMCP := MCPConfig{Servers: []MCPServer{
		{Name: "project-mgmt", Mode: "mocked", ConfigRef: "evals/fixtures/mcp/default.yaml"},
		{Name: "filesystem", Mode: "mocked"},
	}}
	caseMCP := MCPConfig{Servers: []MCPServer{
		{Name: "project-mgmt", Mode: "mocked", ConfigRef: "evals/fixtures/mcp/open.yaml"},
	}}

	got, err := MergeCaseMCP(evalMCP, caseMCP)
	if err != nil {
		t.Fatalf("MergeCaseMCP returned error: %v", err)
	}

	want := MCPConfig{Servers: []MCPServer{
		{Name: "project-mgmt", Mode: "mocked", ConfigRef: "evals/fixtures/mcp/open.yaml"},
		{Name: "filesystem", Mode: "mocked"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merge result mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestMergeCaseMCP_AppendsNewServer(t *testing.T) {
	t.Parallel()
	evalMCP := MCPConfig{Servers: []MCPServer{
		{Name: "project-mgmt", Mode: "mocked", ConfigRef: "evals/fixtures/mcp/default.yaml"},
	}}
	caseMCP := MCPConfig{Servers: []MCPServer{
		{Name: "extra-tool", Mode: "mocked", ConfigRef: "evals/fixtures/mcp/extra.yaml"},
	}}

	got, err := MergeCaseMCP(evalMCP, caseMCP)
	if err != nil {
		t.Fatalf("MergeCaseMCP returned error: %v", err)
	}
	if len(got.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d (%+v)", len(got.Servers), got.Servers)
	}
	if got.Servers[1].Name != "extra-tool" {
		t.Errorf("expected appended server 'extra-tool' at end, got %q", got.Servers[1].Name)
	}
}

func TestMergeCaseMCP_DoesNotMutateInputs(t *testing.T) {
	t.Parallel()
	evalMCP := MCPConfig{Servers: []MCPServer{
		{Name: "project-mgmt", Mode: "mocked", ConfigRef: "evals/fixtures/mcp/default.yaml", Args: []string{"a"}},
	}}
	caseMCP := MCPConfig{Servers: []MCPServer{
		{Name: "project-mgmt", Mode: "mocked", ConfigRef: "evals/fixtures/mcp/open.yaml"},
	}}
	evalSnapshot := MCPConfig{Servers: []MCPServer{
		{Name: "project-mgmt", Mode: "mocked", ConfigRef: "evals/fixtures/mcp/default.yaml", Args: []string{"a"}},
	}}

	got, err := MergeCaseMCP(evalMCP, caseMCP)
	if err != nil {
		t.Fatalf("MergeCaseMCP returned error: %v", err)
	}

	if !reflect.DeepEqual(evalMCP, evalSnapshot) {
		t.Errorf("eval MCP was mutated: got %+v want %+v", evalMCP, evalSnapshot)
	}
	// Mutating the result must not affect the original eval config.
	got.Servers[0].ConfigRef = "changed"
	if evalMCP.Servers[0].ConfigRef != "evals/fixtures/mcp/default.yaml" {
		t.Errorf("mutating result leaked into eval config: %q", evalMCP.Servers[0].ConfigRef)
	}
}

func TestMergeCaseMCP_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		evalMCP MCPConfig
		caseMCP MCPConfig
		errMsg  string
	}{
		{
			name:    "empty case server name",
			evalMCP: MCPConfig{},
			caseMCP: MCPConfig{Servers: []MCPServer{{Mode: "mocked"}}},
			errMsg:  "name is required",
		},
		{
			name:    "duplicate case server names",
			evalMCP: MCPConfig{},
			caseMCP: MCPConfig{Servers: []MCPServer{
				{Name: "svc", Mode: "mocked"},
				{Name: "svc", Mode: "mocked"},
			}},
			errMsg: "duplicate case-level mcp server name",
		},
		{
			name:    "case server not mocked",
			evalMCP: MCPConfig{},
			caseMCP: MCPConfig{Servers: []MCPServer{{Name: "svc", Mode: "real"}}},
			errMsg:  "must use mode: mocked",
		},
		{
			name: "duplicate eval server names",
			evalMCP: MCPConfig{Servers: []MCPServer{
				{Name: "svc", Mode: "mocked"},
				{Name: "svc", Mode: "mocked"},
			}},
			caseMCP: MCPConfig{Servers: []MCPServer{{Name: "svc", Mode: "mocked"}}},
			errMsg:  "duplicate eval-level mcp server name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := MergeCaseMCP(tt.evalMCP, tt.caseMCP)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.errMsg)
			}
			if !contains(err.Error(), tt.errMsg) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}
