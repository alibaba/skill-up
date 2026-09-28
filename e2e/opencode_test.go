//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const openCodeE2EMarker = "SKILL_UP_OPENCODE_E2E_OK"

func TestAgent_OpenCode_NoneRuntime(t *testing.T) {
	skipIfNotFullE2E(t)
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not installed locally")
	}
	if os.Getenv("DASHSCOPE_API_KEY") == "" {
		t.Skip("DASHSCOPE_API_KEY not set")
	}
	runOpenCodeE2E(t, "none")
}

func TestAgent_OpenCode_OpenSandboxRuntime(t *testing.T) {
	skipIfNotFullE2E(t)
	if os.Getenv("OPENSANDBOX_API_KEY") == "" {
		t.Skip("OPENSANDBOX_API_KEY not set")
	}
	if os.Getenv("DASHSCOPE_API_KEY") == "" {
		t.Skip("DASHSCOPE_API_KEY not set")
	}
	runOpenCodeE2E(t, "opensandbox")
}

func runOpenCodeE2E(t *testing.T, runtimeType string) {
	t.Helper()
	evalDir := t.TempDir()
	writeFile(t, filepath.Join(evalDir, "SKILL.md"), "# OpenCode E2E\n")
	writeFile(t, filepath.Join(evalDir, "evals", "cases", "marker.yaml"), `id: marker
title: OpenCode model response
input:
  prompt: Reply with exactly SKILL_UP_OPENCODE_E2E_OK. Do not use tools.
constraints:
  timeout_seconds: 600
  max_turns: 1
expect:
  must_contain:
    - SKILL_UP_OPENCODE_E2E_OK
`)

	environment := "  type: none\n"
	if runtimeType == "opensandbox" {
		environment = `  type: opensandbox
  workspace_mount: /tmp/skill-up-workspace
  image: ` + openSandboxE2EImage() + `
  ready_timeout_seconds: 300
  sandbox_timeout_seconds: 600
  kwargs:
    base_url: ` + openSandboxE2EBaseURL() + `
    request_timeout_seconds: "900"
`
		if os.Getenv("SKILL_UP_E2E_OPENSANDBOX_PROXY") == "1" {
			environment += "  use_server_proxy: true\n"
		}
	}
	evalPath := filepath.Join(evalDir, "evals", "eval.yaml")
	writeFile(t, evalPath, `schema_version: v1alpha1
environment:
`+environment+`engine:
  name: opencode
  model:
    provider: dashscope
    name: qwen3.8-max
    base_url: https://dashscope.aliyuncs.com/compatible-mode/v1
skills:
  - source: local_path
    path: .
cases:
  files:
    - evals/cases/marker.yaml
  defaults:
    timeout_seconds: 600
    max_turns: 1
  parallelism: 1
judge:
  type: rule_based
report:
  formats: [json]
`)

	outputDir := t.TempDir()
	preserveWorkspaceArtifacts(t, outputDir)
	result := Run(t, RunConfig{Timeout: 12 * time.Minute}, "run", evalPath, "--output-dir", outputDir)
	if result.ExitCode != 0 {
		t.Fatalf("opencode %s run failed: exit=%d\nstdout=%s\nstderr=%s", runtimeType, result.ExitCode, result.Stdout, result.Stderr)
	}

	resultPath := filepath.Join(outputDir, "iteration-1", "result.json")
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		EngineName  string `json:"engine_name"`
		ModelName   string `json:"model_name"`
		CaseResults []struct {
			Status   string `json:"status"`
			Response string `json:"response"`
		} `json:"case_results"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.EngineName != "opencode" || report.ModelName != "dashscope/qwen3.8-max" ||
		len(report.CaseResults) != 1 || report.CaseResults[0].Status != "PASS" ||
		strings.TrimSpace(report.CaseResults[0].Response) != openCodeE2EMarker {
		t.Fatalf("unexpected result.json: %s", data)
	}
}
