package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/runtime"
)

const (
	mockNodeCommand         = "node"
	fixturesJSONPlaceholder = "__FIXTURES_JSON__"
)

func buildMockedRuntimeServerConfig(server config.MCPServer, fileCfg mcpServerFile, configRef string) (runtime.MCPServerConfig, error) {
	if transport := strings.TrimSpace(server.Transport); transport != "" && transport != transportStdio {
		return runtime.MCPServerConfig{}, fmt.Errorf("mcp server %q mocked mode only supports stdio transport", server.Name)
	}
	if transport := strings.TrimSpace(fileCfg.Transport); transport != "" && transport != transportStdio {
		return runtime.MCPServerConfig{}, fmt.Errorf("mcp server %q mocked mode only supports stdio transport", server.Name)
	}
	if strings.TrimSpace(server.Endpoint) != "" || strings.TrimSpace(fileCfg.Endpoint) != "" {
		return runtime.MCPServerConfig{}, fmt.Errorf("mcp server %q mocked mode does not support endpoint", server.Name)
	}
	if strings.TrimSpace(server.Command) != "" || strings.TrimSpace(fileCfg.Command) != "" || len(server.Args) > 0 || len(fileCfg.Args) > 0 {
		return runtime.MCPServerConfig{}, fmt.Errorf("mcp server %q mocked mode generates its own stdio server command", server.Name)
	}

	script, err := buildMockedServerScript(server.Name, fileCfg)
	if err != nil {
		return runtime.MCPServerConfig{}, err
	}

	return buildRuntimeServerConfig(server, runtimeServerFields{
		transport: transportStdio,
		command:   mockNodeCommand,
		args:      []string{"-e", script},
		configRef: configRef,
		env:       map[string]string{},
		headers:   map[string]string{},
		headerEnv: map[string]string{},
	}), nil
}

func buildMockedServerScript(serverName string, fileCfg mcpServerFile) (string, error) {
	if len(fileCfg.ToolResponses) > 0 {
		fixtures, err := json.Marshal(map[string]any{"toolResponses": fileCfg.ToolResponses})
		if err != nil {
			return "", fmt.Errorf("mcp server %q mock fixture is invalid: %w", serverName, err)
		}
		return strings.Replace(genericMockServerScript, fixturesJSONPlaceholder, string(fixtures), 1), nil
	}
	if serverName == "filesystem" {
		return filesystemMockServerScript, nil
	}
	return "", fmt.Errorf("mcp server %q mocked mode requires config_ref tool_responses or a built-in mock server name", serverName)
}

// mcpProtocolScript is shared by all mock server scripts. It provides the MCP
// stdio framing layer (Content-Length headers and newline-delimited JSON), with
// defensive guards against malformed input and oversized messages.
const mcpProtocolScript = `
let buffer = Buffer.alloc(0);
process.stdin.on("data", chunk => {
  buffer = Buffer.concat([buffer, chunk]);
  for (;;) {
    const message = readMessage();
    if (!message) break;
    handleMessage(message.payload, message.framed);
  }
});
function readMessage() {
  const headerEnd = buffer.indexOf("\r\n\r\n");
  if (headerEnd >= 0) {
    const header = buffer.slice(0, headerEnd).toString("utf8");
    const match = /content-length:\s*(\d+)/i.exec(header);
    if (!match) {
      const lineEnd = buffer.indexOf("\n");
      if (lineEnd < 0) return null;
      return readLineMessage(lineEnd);
    }
    const length = Number(match[1]);
    if (length > 10 * 1024 * 1024) { buffer = Buffer.alloc(0); return null; }
    const start = headerEnd + 4;
    if (buffer.length < start + length) return null;
    const body = buffer.slice(start, start + length).toString("utf8");
    buffer = buffer.slice(start + length);
    try { return { payload: JSON.parse(body), framed: true }; } catch { return null; }
  }
  const lineEnd = buffer.indexOf("\n");
  if (lineEnd < 0) return null;
  return readLineMessage(lineEnd);
}
function readLineMessage(lineEnd) {
  const line = buffer.slice(0, lineEnd).toString("utf8").trim();
  buffer = buffer.slice(lineEnd + 1);
  if (!line) return null;
  try { return { payload: JSON.parse(line), framed: false }; } catch { return null; }
}
function send(id, result, framed) {
  const body = JSON.stringify({ jsonrpc: "2.0", id, result });
  if (framed) {
    process.stdout.write("Content-Length: " + Buffer.byteLength(body) + "\r\n\r\n" + body);
    return;
  }
  process.stdout.write(body + "\n");
}
function sendError(id, code, message, framed) {
  const body = JSON.stringify({ jsonrpc: "2.0", id, error: { code, message } });
  if (framed) {
    process.stdout.write("Content-Length: " + Buffer.byteLength(body) + "\r\n\r\n" + body);
    return;
  }
  process.stdout.write(body + "\n");
}
`

const genericMockServerScript = `
const fixtures = ` + fixturesJSONPlaceholder + `;
const tools = Object.keys(fixtures.toolResponses || {});
` + mcpProtocolScript + `
function handleMessage(message, framed) {
  if (!message || message.id === undefined) return;
  if (message.method === "initialize") {
    send(message.id, { protocolVersion: "2024-11-05", capabilities: { tools: {} }, serverInfo: { name: "skill-up-mock", version: "0.1.0" } }, framed);
    return;
  }
  if (message.method === "tools/list") {
    send(message.id, { tools: tools.map(name => ({ name, description: "Mocked MCP tool " + name, inputSchema: { type: "object", additionalProperties: true } })) }, framed);
    return;
  }
  if (message.method === "tools/call") {
    const name = message.params && message.params.name;
    if (!Object.prototype.hasOwnProperty.call(fixtures.toolResponses || {}, name)) {
      sendError(message.id, -32602, "unknown mocked tool " + name, framed);
      return;
    }
    const args = (message.params && message.params.arguments) || {};
    const response = render(selectResponse(fixtures.toolResponses[name]), args);
    const text = typeof response === "string" ? response : JSON.stringify(response);
    send(message.id, { content: [{ type: "text", text }] }, framed);
    return;
  }
  send(message.id, {}, framed);
}
function selectResponse(fixture) {
  if (fixture && Object.prototype.hasOwnProperty.call(fixture, "default")) return fixture.default;
  return fixture;
}
function render(value, params) {
  if (typeof value === "string") {
    return value.replace(/\{\{\s*params\.([A-Za-z0-9_.$-]+)(?:\s*\|\s*default:\s*['"]([^'"]*)['"])?\s*\}\}/g, (_match, path, fallback) => {
      const found = getPath(params, path);
      if (found === undefined || found === null || found === "") return fallback === undefined ? "" : fallback;
      return String(found);
    });
  }
  if (Array.isArray(value)) return value.map(item => render(item, params));
  if (value && typeof value === "object") {
    const out = {};
    for (const [key, item] of Object.entries(value)) out[key] = render(item, params);
    return out;
  }
  return value;
}
function getPath(value, path) {
  return path.split(".").reduce((acc, key) => acc == null ? undefined : acc[key], value);
}
`

const filesystemMockServerScript = `
const fs = require("fs");
const path = require("path");
` + mcpProtocolScript + `
function handleMessage(message, framed) {
  if (!message || message.id === undefined) return;
  if (message.method === "initialize") {
    send(message.id, { protocolVersion: "2024-11-05", capabilities: { tools: {} }, serverInfo: { name: "skill-up-filesystem-mock", version: "0.1.0" } }, framed);
    return;
  }
  if (message.method === "tools/list") {
    send(message.id, { tools: [
      { name: "list_directory", description: "List files under a directory.", inputSchema: { type: "object", additionalProperties: true } },
      { name: "read_file", description: "Read a file or directory tree.", inputSchema: { type: "object", additionalProperties: true } },
      { name: "write_file", description: "Write a file.", inputSchema: { type: "object", additionalProperties: true } }
    ] }, framed);
    return;
  }
  if (message.method !== "tools/call") {
    send(message.id, {}, framed);
    return;
  }
  const name = message.params && message.params.name;
  const args = (message.params && message.params.arguments) || {};
  try {
    if (name === "list_directory") {
      sendText(message.id, listDirectory(resolvePath(args.path || args.directory || ".")), framed);
      return;
    }
    if (name === "read_file") {
      sendText(message.id, readPath(resolvePath(args.path || args.file_path || args.file || ".")), framed);
      return;
    }
    if (name === "write_file") {
      const target = resolvePath(args.path || args.file_path || args.file);
      try {
        if (fs.lstatSync(target).isSymbolicLink()) throw new Error("symbolic links are not allowed: " + path.relative(process.cwd(), target));
      } catch (e) { if (e.code !== "ENOENT") throw e; }
      fs.mkdirSync(path.dirname(target), { recursive: true });
      fs.writeFileSync(target, String(args.content === undefined ? "" : args.content));
      sendText(message.id, "wrote " + path.relative(process.cwd(), target), framed);
      return;
    }
    sendError(message.id, -32602, "unknown mocked tool " + name, framed);
  } catch (err) {
    sendError(message.id, -32000, err && err.message ? err.message : String(err), framed);
  }
}
function sendText(id, text, framed) {
  send(id, { content: [{ type: "text", text }] }, framed);
}
function resolvePath(input) {
  if (!input) throw new Error("path is required");
  const raw = String(input);
  const root = fs.realpathSync(process.cwd());
  // path.resolve is purely lexical, so a symlinked parent component would
  // pass a startsWith check while Node still follows it on disk. Resolve the
  // deepest existing ancestor through realpath and re-check the canonical
  // path; the non-existing tail (for write_file) cannot contain symlinks.
  let existing = path.resolve(root, raw);
  let suffix = "";
  for (;;) {
    try {
      existing = fs.realpathSync(existing);
      break;
    } catch (e) {
      if (e.code !== "ENOENT") throw e;
      suffix = suffix ? path.join(path.basename(existing), suffix) : path.basename(existing);
      const parent = path.dirname(existing);
      if (parent === existing) throw new Error("path outside workspace: " + raw);
      existing = parent;
    }
  }
  const resolved = suffix ? path.join(existing, suffix) : existing;
  if (resolved !== root && !resolved.startsWith(root + path.sep)) {
    throw new Error("path outside workspace: " + raw);
  }
  return resolved;
}
function listDirectory(target) {
  const stat = fs.lstatSync(target);
  if (stat.isSymbolicLink()) throw new Error("symbolic links are not allowed: " + path.relative(process.cwd(), target));
  const entries = fs.readdirSync(target, { withFileTypes: true });
  return entries.filter(entry => !entry.isSymbolicLink()).map(entry => entry.name + (entry.isDirectory() ? "/" : "")).sort().join("\n");
}
function readPath(target) {
  const stat = fs.lstatSync(target);
  if (stat.isSymbolicLink()) throw new Error("symbolic links are not allowed: " + path.relative(process.cwd(), target));
  if (stat.isDirectory()) return readDirectory(target).join("\n\n");
  return fs.readFileSync(target, "utf8");
}
function readDirectory(root) {
  const out = [];
  for (const name of fs.readdirSync(root).sort()) {
    const target = path.join(root, name);
    const stat = fs.lstatSync(target);
    if (stat.isSymbolicLink()) continue;
    if (stat.isDirectory()) {
      out.push(...readDirectory(target));
      continue;
    }
    out.push("--- " + path.relative(process.cwd(), target) + " ---\n" + fs.readFileSync(target, "utf8"));
  }
  return out;
}
`
