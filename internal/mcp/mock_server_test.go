package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type mockProc struct {
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

func startMockServer(t *testing.T, script, dir string) *mockProc {
	t.Helper()
	if goruntime.GOOS == "windows" {
		// Every mocked MCP server test spawns a child Node process and
		// exchanges Content-Length-framed JSON-RPC over its stdout. Node on
		// default Windows applies codepage / CRLF translation to that
		// stdout, which corrupts the framed byte stream and makes the
		// reader hang on the response header until the 15s ctx timeout
		// fires. Verifying the framing/transport on POSIX is sufficient;
		// the framing logic itself is platform-independent Go code.
		t.Skip("Node stdio codepage/CRLF translation corrupts framed binary on Windows")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for mock server tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, "node", "-e", script)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start node: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	return &mockProc{stdin: stdin, stdout: bufio.NewReader(stdout)}
}

func (p *mockProc) writeFramed(t *testing.T, msg any) {
	t.Helper()
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, err := fmt.Fprintf(p.stdin, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := p.stdin.Write(body); err != nil {
		t.Fatalf("write body: %v", err)
	}
}

func (p *mockProc) readFramed(t *testing.T) map[string]any {
	t.Helper()
	length := -1
	for {
		line, err := p.stdout.ReadString('\n')
		if err != nil {
			t.Fatalf("read response header: %v", err)
		}
		header := strings.TrimRight(line, "\r\n")
		if header == "" {
			break
		}
		if name, val, ok := strings.Cut(header, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "content-length") {
			length, err = strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				t.Fatalf("bad content-length %q: %v", val, err)
			}
		}
	}
	if length < 0 {
		t.Fatal("response missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(p.stdout, body); err != nil {
		t.Fatalf("read response body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response %q: %v", body, err)
	}
	return out
}

func toolCallText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result: %v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("result has no content: %v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] is not an object: %v", content[0])
	}
	text, _ := first["text"].(string)
	return text
}

// TestMockedGenericServer_FramedNonASCIIRoundTrip exercises Content-Length
// framed messages whose body contains multibyte characters: the byte length in
// the header must not be confused with the UTF-16 unit count of the buffer.
func TestMockedGenericServer_FramedNonASCIIRoundTrip(t *testing.T) {
	script, err := buildMockedServerScript("echo-server", mcpServerFile{
		ToolResponses: map[string]any{
			"echo": map[string]any{"default": "{{params.text}}"},
		},
	})
	if err != nil {
		t.Fatalf("buildMockedServerScript: %v", err)
	}

	proc := startMockServer(t, script, t.TempDir())
	proc.writeFramed(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	proc.readFramed(t)

	const marker = "日本語-マーカー-✅"
	proc.writeFramed(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "echo",
			"arguments": map[string]any{"text": marker},
		},
	})
	if got := toolCallText(t, proc.readFramed(t)); got != marker {
		t.Fatalf("framed non-ASCII tool call returned %q, want %q", got, marker)
	}
}

// TestMockedFilesystemServer_RejectsSymlinkEscape verifies that a symlinked
// parent component cannot be used to read files outside the workspace.
func TestMockedFilesystemServer_RejectsSymlinkEscape(t *testing.T) {
	script, err := buildMockedServerScript("filesystem", mcpServerFile{})
	if err != nil {
		t.Fatalf("buildMockedServerScript: %v", err)
	}

	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("top secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	proc := startMockServer(t, script, workspace)
	proc.writeFramed(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	proc.readFramed(t)

	proc.writeFramed(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "read_file",
			"arguments": map[string]any{"path": "escape/secret.txt"},
		},
	})
	resp := proc.readFramed(t)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error for symlink escape, got %v", resp)
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "outside workspace") {
		t.Fatalf("error message = %q, want it to mention outside workspace", msg)
	}
}

// TestMockedFilesystemServer_ReadsWorkspaceFile is the positive counterpart:
// a regular file inside the workspace is still readable.
func TestMockedFilesystemServer_ReadsWorkspaceFile(t *testing.T) {
	script, err := buildMockedServerScript("filesystem", mcpServerFile{})
	if err != nil {
		t.Fatalf("buildMockedServerScript: %v", err)
	}

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("hello workspace"), 0o600); err != nil {
		t.Fatal(err)
	}

	proc := startMockServer(t, script, workspace)
	proc.writeFramed(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	proc.readFramed(t)

	proc.writeFramed(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "read_file",
			"arguments": map[string]any{"path": "note.txt"},
		},
	})
	if got := toolCallText(t, proc.readFramed(t)); got != "hello workspace" {
		t.Fatalf("read_file returned %q, want %q", got, "hello workspace")
	}
}

func startGenericMockServer(t *testing.T, fileCfg mcpServerFile) *mockProc {
	t.Helper()
	script, err := buildMockedServerScript("project-mgmt", fileCfg)
	if err != nil {
		t.Fatalf("buildMockedServerScript: %v", err)
	}
	proc := startMockServer(t, script, t.TempDir())
	proc.writeFramed(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	proc.readFramed(t)
	return proc
}

func listTools(t *testing.T, proc *mockProc) []any {
	t.Helper()
	proc.writeFramed(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	resp := proc.readFramed(t)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result: %v", resp)
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("result has no tools: %v", result)
	}
	return tools
}

func parseMockFixture(t *testing.T, text string) mcpServerFile {
	t.Helper()
	var fileCfg mcpServerFile
	if err := yaml.Unmarshal([]byte(text), &fileCfg); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return fileCfg
}

func callTool(t *testing.T, proc *mockProc, id int, name string, args map[string]any) string {
	t.Helper()
	proc.writeFramed(t, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	return toolCallText(t, proc.readFramed(t))
}

// TestMockedGenericServer_ToolsListDefaultMetadata pins the placeholder
// description and open schema a legacy tool_responses fixture advertises.
func TestMockedGenericServer_ToolsListDefaultMetadata(t *testing.T) {
	fileCfg := mcpServerFile{ToolResponses: map[string]any{"echo": map[string]any{"default": "ok"}}}

	want := []any{map[string]any{
		"name":        "echo",
		"description": "Mocked MCP tool echo",
		"inputSchema": map[string]any{"type": "object", "additionalProperties": true},
	}}
	if got := listTools(t, startGenericMockServer(t, fileCfg)); !reflect.DeepEqual(got, want) {
		t.Fatalf("tools/list = %#v, want %#v", got, want)
	}
}

// TestMockedGenericServer_LegacyToolResponsesUnchanged keeps the response
// semantics of the tool_responses format: a default key, a whole entry, and
// an explicit null.
func TestMockedGenericServer_LegacyToolResponsesUnchanged(t *testing.T) {
	proc := startGenericMockServer(t, parseMockFixture(t, `tool_responses:
  with_default:
    default: {name: "{{params.name}}"}
  whole_entry: {status: OPEN}
  null_response: null
`))
	for _, tc := range []struct {
		tool string
		want string
	}{
		{"with_default", `{"name":"alpha"}`},
		{"whole_entry", `{"status":"OPEN"}`},
		{"null_response", "null"},
	} {
		if got := callTool(t, proc, 3, tc.tool, map[string]any{"name": "alpha"}); got != tc.want {
			t.Fatalf("%s returned %q, want %q", tc.tool, got, tc.want)
		}
	}
}

// TestMockedGenericServer_ToolsFormat decodes the tools format from YAML, so
// the tools, input_schema and response keys are exercised end to end, then
// confirms the advertised schema is not enforced on tools/call.
func TestMockedGenericServer_ToolsFormat(t *testing.T) {
	proc := startGenericMockServer(t, parseMockFixture(t, `tools:
  create_publish_plan_simple:
    description: Create a publish plan for a project release.
    input_schema:
      type: object
      properties:
        name: {type: string}
      required: [name]
    response:
      default: {id: 999, name: "{{params.name}}"}
`))

	want := []any{map[string]any{
		"name":        "create_publish_plan_simple",
		"description": "Create a publish plan for a project release.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
			"required":   []any{"name"},
		},
	}}
	if got := listTools(t, proc); !reflect.DeepEqual(got, want) {
		t.Fatalf("tools/list = %#v, want %#v", got, want)
	}
	if got := callTool(t, proc, 3, "create_publish_plan_simple", map[string]any{"name": "v2"}); got != `{"id":999,"name":"v2"}` {
		t.Fatalf("tools/call returned %q", got)
	}
	if got := callTool(t, proc, 4, "create_publish_plan_simple", map[string]any{"unexpected": true}); got != `{"id":999,"name":""}` {
		t.Fatalf("tools/call with off-schema arguments returned %q, want the fixture response", got)
	}
}

// TestMockedGenericServer_MixedFormats lets the two formats declare different
// tools in one fixture; omitted metadata keeps the defaults, and an explicit
// null response is returned as null.
func TestMockedGenericServer_MixedFormats(t *testing.T) {
	proc := startGenericMockServer(t, parseMockFixture(t, `tools:
  described:
    description: Only a description.
    response: described-ok
  typed:
    input_schema: {type: object, properties: {}}
    response: null
tool_responses:
  legacy:
    default: legacy-ok
`))

	want := []any{
		map[string]any{
			"name":        "described",
			"description": "Only a description.",
			"inputSchema": map[string]any{"type": "object", "additionalProperties": true},
		},
		map[string]any{
			"name":        "legacy",
			"description": "Mocked MCP tool legacy",
			"inputSchema": map[string]any{"type": "object", "additionalProperties": true},
		},
		map[string]any{
			"name":        "typed",
			"description": "Mocked MCP tool typed",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
	if got := listTools(t, proc); !reflect.DeepEqual(got, want) {
		t.Fatalf("tools/list = %#v, want %#v", got, want)
	}
	for i, tc := range []struct{ tool, want string }{
		{"described", "described-ok"},
		{"typed", "null"},
		{"legacy", "legacy-ok"},
	} {
		if got := callTool(t, proc, 3+i, tc.tool, map[string]any{}); got != tc.want {
			t.Fatalf("%s returned %q, want %q", tc.tool, got, tc.want)
		}
	}
}

// TestMockedToolDefinitions_Normalizes checks the normalized definitions
// without starting node: an explicit null response is kept, a null schema
// keeps the default, and legacy entries carry only their response.
func TestMockedToolDefinitions_Normalizes(t *testing.T) {
	t.Parallel()

	defs, err := mockedToolDefinitions("project-mgmt", parseMockFixture(t, `tools:
  null_response:
    description: Returns null.
    input_schema: null
    response: null
  with_default:
    input_schema: {type: object}
    response: {default: ok}
tool_responses:
  legacy: {default: old}
`))
	if err != nil {
		t.Fatalf("mockedToolDefinitions: %v", err)
	}
	want := map[string]mockedToolDefinition{
		"null_response": {Description: "Returns null.", Response: nil},
		"with_default":  {InputSchema: map[string]any{"type": "object"}, Response: map[string]any{"default": "ok"}},
		"legacy":        {Response: map[string]any{"default": "old"}},
	}
	if !reflect.DeepEqual(defs, want) {
		t.Fatalf("definitions = %#v, want %#v", defs, want)
	}
	encoded, err := json.Marshal(defs["null_response"])
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"description":"Returns null.","response":null}` {
		t.Fatalf("encoded null response = %s, want the response key kept as null", encoded)
	}
}

func TestBuildMockedServerScript_RejectsInvalidTools(t *testing.T) {
	t.Parallel()

	const schemaErr = `tool "get_project" input_schema must be a mapping with type: object`
	tests := []struct {
		name    string
		fixture string
		wantErr string
	}{
		{
			name:    "missing response",
			fixture: "tools:\n  get_project:\n    description: No response.\n",
			wantErr: `tool "get_project" has no response; set response (null is allowed)`,
		},
		{
			name:    "declared in both maps",
			fixture: "tools:\n  get_project:\n    response: new\ntool_responses:\n  get_project: old\n",
			wantErr: `tool "get_project" is declared under both tools and tool_responses; keep one`,
		},
		{
			name:    "unknown field",
			fixture: "tools:\n  get_project:\n    inputSchema: {type: object}\n    response: ok\n",
			wantErr: `tool "get_project" has unknown field "inputSchema"; supported fields are description, input_schema and response`,
		},
		{
			name:    "schema is not a mapping",
			fixture: "tools:\n  get_project:\n    input_schema: object\n    response: ok\n",
			wantErr: schemaErr,
		},
		{
			name:    "schema type is not object",
			fixture: "tools:\n  get_project:\n    input_schema: {type: string}\n    response: ok\n",
			wantErr: schemaErr,
		},
		{
			name:    "schema is an empty mapping",
			fixture: "tools:\n  get_project:\n    input_schema: {}\n    response: ok\n",
			wantErr: schemaErr,
		},
		{
			name:    "schema without type",
			fixture: "tools:\n  get_project:\n    input_schema: {properties: {}}\n    response: ok\n",
			wantErr: schemaErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := buildMockedServerScript("project-mgmt", parseMockFixture(t, tt.fixture))
			if err == nil || !strings.Contains(err.Error(), `"project-mgmt"`) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("buildMockedServerScript error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
