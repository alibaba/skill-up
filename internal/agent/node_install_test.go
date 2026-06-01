package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/runtime"
)

func TestNodeBootstrapLines(t *testing.T) {
	t.Parallel()

	cmd := strings.Join(nodeBootstrapLines("20"), "\n")
	for _, want := range []string{
		`NVM_DIR="${NVM_DIR:-$HOME/.nvm}"`,
		`NVM_SYMLINK_CURRENT=true`,
		`NVM_SOURCE="${NVM_SOURCE:-` + agentNVMSource + `}"`,
		`NVM_NODEJS_ORG_MIRROR="${NVM_NODEJS_ORG_MIRROR:-` + agentNodeMirror + `}"`,
		`npm_config_registry="${npm_config_registry:-${NPM_CONFIG_REGISTRY:-` + agentNPMRegistry + `}}"`,
		`agent_npm_prefix="${npm_config_prefix:-$HOME/.local}"`,
		`unset npm_config_prefix`,
		`export npm_config_prefix="$agent_npm_prefix"`,
		`export PATH="$npm_config_prefix/bin:$PATH"`,
		`if [ -s "$NVM_DIR/nvm.sh" ]; then . "$NVM_DIR/nvm.sh"; fi`,
		`node_major="$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || echo 0)"`,
		`[ "$node_major" -lt '20' ]`,
		"nvm install '20'",
		"nvm use '20'",
		"curl --connect-timeout '" + agentCurlConnectTimeout + "' --max-time '" + agentCurlMaxTime + "' -fsSL '" + agentNVMInstallURL + `' -o "$nvm_install_script" || curl --connect-timeout '` + agentCurlConnectTimeout + "' --max-time '" + agentCurlMaxTime + "' -fsSL '" + agentNVMFallbackURL + `' -o "$nvm_install_script"`,
		`command -v sha256sum`,
		`shasum -a 256 "$nvm_install_script"`,
		`[ "$nvm_install_sha256" != '` + agentNVMInstallSHA256 + `' ]`,
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("node bootstrap command missing %q:\n%s", want, cmd)
		}
	}
}

func TestNodeBootstrapLinesUsesDefaultVersion(t *testing.T) {
	t.Parallel()

	cmd := strings.Join(nodeBootstrapLines(""), "\n")
	if !strings.Contains(cmd, "nvm install '"+agentNodeDefaultVersion+"'") {
		t.Fatalf("node bootstrap command does not use default version %s:\n%s", agentNodeDefaultVersion, cmd)
	}
}

func TestEnsureNodeRuntime_ScriptShape(t *testing.T) {
	t.Parallel()

	rt := &nodeBootstrapTestRuntime{result: runtime.ExecResult{ExitCode: 0}}
	if err := ensureNodeRuntime(context.Background(), rt, "claude", ExecOptions{}); err != nil {
		t.Fatalf("ensureNodeRuntime returned error: %v", err)
	}
	if len(rt.commands) != 1 {
		t.Fatalf("expected 1 Exec call, got %d: %v", len(rt.commands), rt.commands)
	}
	cmd := rt.commands[0]
	// The guard short-circuit must precede the bootstrap so it never executes
	// when the CLI is already on PATH.
	if !strings.HasPrefix(cmd, "set -e\nif command -v 'claude' >/dev/null 2>&1; then exit 0; fi\n") {
		t.Fatalf("bootstrap script missing CLI short-circuit at top:\n%s", cmd)
	}
	if !strings.Contains(cmd, "nvm install '"+agentNodeDefaultVersion+"'") {
		t.Fatalf("bootstrap script missing nvm install:\n%s", cmd)
	}
}

func TestEnsureNodeRuntime_WrapsExecError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("network down")
	rt := &nodeBootstrapTestRuntime{err: sentinel}
	err := ensureNodeRuntime(context.Background(), rt, "claude", ExecOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel error, got %v", err)
	}
	if !strings.Contains(err.Error(), "node bootstrap failed") {
		t.Fatalf("expected 'node bootstrap failed' prefix, got %v", err)
	}
}

func TestEnsureNodeRuntime_WrapsNonZeroExit(t *testing.T) {
	t.Parallel()

	rt := &nodeBootstrapTestRuntime{result: runtime.ExecResult{ExitCode: 7, Stderr: "checksum mismatch"}}
	err := ensureNodeRuntime(context.Background(), rt, "codex", ExecOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "node bootstrap failed") {
		t.Fatalf("expected 'node bootstrap failed' prefix, got %v", err)
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected stderr to be surfaced, got %v", err)
	}
}

func TestEnsureNodeRuntime_RejectsEmptyCLIBin(t *testing.T) {
	t.Parallel()

	rt := &nodeBootstrapTestRuntime{}
	if err := ensureNodeRuntime(context.Background(), rt, "", ExecOptions{}); err == nil {
		t.Fatal("expected error when cliBin is empty")
	}
	if len(rt.commands) != 0 {
		t.Fatalf("expected no Exec calls when cliBin is empty, got %v", rt.commands)
	}
}

type nodeBootstrapTestRuntime struct {
	commands []string
	result   runtime.ExecResult
	err      error
}

func (r *nodeBootstrapTestRuntime) Create(context.Context) error { return nil }
func (r *nodeBootstrapTestRuntime) Close() error                 { return nil }
func (r *nodeBootstrapTestRuntime) Start(context.Context) error  { return nil }
func (r *nodeBootstrapTestRuntime) Stop(context.Context) error   { return nil }
func (r *nodeBootstrapTestRuntime) UploadFile(context.Context, string, string) error {
	return nil
}
func (r *nodeBootstrapTestRuntime) UploadDir(context.Context, string, string) error { return nil }
func (r *nodeBootstrapTestRuntime) DownloadFile(context.Context, string, string) error {
	return nil
}
func (r *nodeBootstrapTestRuntime) DownloadDir(context.Context, string, string) error { return nil }
func (r *nodeBootstrapTestRuntime) Exec(_ context.Context, command string, _ runtime.ExecOptions) (runtime.ExecResult, error) {
	r.commands = append(r.commands, command)
	return r.result, r.err
}
func (r *nodeBootstrapTestRuntime) Workspace() string            { return "/workspace" }
func (r *nodeBootstrapTestRuntime) RequiresProcessSandbox() bool { return false }
func (r *nodeBootstrapTestRuntime) MergeEnv(map[string]string)   {}
