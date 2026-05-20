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
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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
