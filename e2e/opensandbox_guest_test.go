//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCustomEngine_OpenSandboxWindowsGuest(t *testing.T) {
	apiKey := openSandboxE2EAPIKey()
	baseURL := openSandboxE2EBaseURL()
	image := os.Getenv("OPENSANDBOX_WINDOWS_IMAGE")
	if baseURL == "" {
		baseURL = "https://sandbox.example.test"
	}
	if image == "" {
		image = "dockurr/windows:latest"
	}

	fixture := filepath.Join(getProjectRoot(), "e2e", "testdata", "opensandbox-windows")
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixture)); err != nil {
		t.Fatalf("copy Windows fixture: %v", err)
	}
	evalPath := filepath.Join(dir, "evals", "eval.yaml")
	writeFile(t, evalPath, `schema_version: v1alpha1
environment:
  type: opensandbox
  image: `+strconv.Quote(image)+`
  platform:
    os: windows
    arch: amd64
  resources:
    memory: 8Gi
  ready_timeout_seconds: 1800
  sandbox_timeout_seconds: 3600
  env:
    VERSION: "11"
  setup_steps:
    - run: echo setup-complete>setup-marker.txt
  kwargs:
    base_url: `+strconv.Quote(baseURL)+`
    request_timeout_seconds: "1800"
mcp:
  servers: []
skills: []
engine:
  name: windows-custom-engine
  custom:
    transport: local
    response_format: session_result
    local:
      command: powershell.exe
      args:
        - -NoProfile
        - -ExecutionPolicy
        - Bypass
        - -File
        - ${workspace}\agent.ps1
        - ${input_file}
cases:
  files:
    - evals/cases/windows.yaml
  defaults:
    timeout_seconds: 600
    max_turns: 1
  parallelism: 1
  retry_policy:
    max_retries: 0
benchmark:
  enabled: false
report:
  formats: [json]
`)
	validation := Run(t, RunConfig{WorkDir: dir}, "validate", evalPath)
	if validation.ExitCode != 0 {
		t.Fatalf("Windows guest fixture validation failed: exit=%d\nstdout=%s\nstderr=%s", validation.ExitCode, validation.Stdout, validation.Stderr)
	}
	if os.Getenv("SKILL_UP_WINDOWS_GUEST_E2E") != "1" {
		t.Skip("fixture validated; set SKILL_UP_WINDOWS_GUEST_E2E=1 to run the real Windows guest test")
	}
	if apiKey == "" || os.Getenv("OPENSANDBOX_BASE_URL") == "" || os.Getenv("OPENSANDBOX_WINDOWS_IMAGE") == "" {
		t.Skip("OPENSANDBOX_API_KEY, OPENSANDBOX_BASE_URL, and OPENSANDBOX_WINDOWS_IMAGE are required")
	}

	outputDir := t.TempDir()
	preserveWorkspaceArtifacts(t, outputDir)
	result := Run(t, RunConfig{
		Timeout: 30 * time.Minute,
		Env: []string{
			"OPENSANDBOX_API_KEY=" + apiKey,
			"OPENSANDBOX_BASE_URL=" + baseURL,
		},
	}, "run", evalPath, "--output-dir", outputDir)
	if result.ExitCode != 0 {
		t.Fatalf("Windows guest run failed: exit=%d\nstdout=%s\nstderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "1 passed") {
		t.Fatalf("Windows guest summary did not pass:\n%s", result.Stdout)
	}
	artifact := filepath.Join(outputDir, "iteration-1", "windows-lifecycle", "with_skill", "outputs", "workspace", "result.txt")
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("read collected Windows artifact %s: %v", artifact, err)
	}
	if strings.TrimSpace(string(data)) != "windows-guest-complete" {
		t.Fatalf("artifact = %q", data)
	}
}
