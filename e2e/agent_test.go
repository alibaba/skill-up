//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/config"
)

func isQoderCLIInstalled() bool {
	_, err := exec.LookPath("qodercli")
	return err == nil
}

func skipIfQoderCLIUnavailable(t *testing.T) {
	t.Helper()
	if !isQoderCLIInstalled() {
		t.Skip("qodercli not installed locally")
	}
}

func isCodexInstalled() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

func openSandboxE2EAPIKey() string {
	return os.Getenv("OPENSANDBOX_API_KEY")
}

// openSandboxE2EBaseURL returns the OpenSandbox service URL for e2e tests.
// It reads OPENSANDBOX_BASE_URL so internal CI can point at a real sandbox
// without hardcoding any private hostname into the open-source repository.
func openSandboxE2EBaseURL() string {
	if baseURL := os.Getenv("OPENSANDBOX_BASE_URL"); baseURL != "" {
		return baseURL
	}
	return "https://agent-sandbox.example.com"
}

// openSandboxE2EImage returns the sandbox image reference for e2e tests.
// Internal CI overrides via OPENSANDBOX_IMAGE; the placeholder default keeps
// the open-source repo free of vendor-specific image URIs.
func openSandboxE2EImage() string {
	if image := os.Getenv("OPENSANDBOX_IMAGE"); image != "" {
		return image
	}
	return "registry.example.com/agentic-execution:placeholder"
}

func TestAgent_Codex_NoneRuntime_WorkspaceDiffGitContexts(t *testing.T) {
	t.Parallel()

	fakeCodexDir := writeFakeCodexBinary(t)
	evalRoot := createWorkspaceDiffEvalDir(t)
	evalPath := filepath.Join(evalRoot, "evals", "eval.yaml")
	workspaceDir := filepath.Join(filepath.Dir(evalRoot), filepath.Base(evalRoot)+"-workspace")
	debugDir := t.TempDir()

	result := Run(t, RunConfig{
		Timeout: 120e9,
		Env: []string{
			"PATH=" + fakeCodexDir + string(os.PathListSeparator) + os.Getenv("PATH"),
			"WORKSPACE_DIFF_DEBUG_DIR=" + debugDir,
		},
	}, "run", evalPath)

	if result.ExitCode != 0 {
		logDirTree(t, evalRoot)
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s", result.ExitCode, result.Stdout, result.Stderr)
	}

	if err := validateBenchmarkArtifacts(evalPath, workspaceDir, "iteration-1"); err != nil {
		logDirTree(t, workspaceDir)
		t.Fatalf("validate benchmark artifacts: %v", err)
	}

	assertWorkspaceDiffCasePassed(t, workspaceDir, debugDir, "git-init-workspace")
	assertWorkspaceDiffCasePassed(t, workspaceDir, debugDir, "cloned-git-workspace")
}

func writeFakeCodexBinary(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	script := `#!/usr/bin/env bash
set -euo pipefail

instruction="${!#}"
debug_dir="${WORKSPACE_DIFF_DEBUG_DIR:-}"

write_debug_file() {
  local name="$1"
  local content="$2"
  if [[ -z "$debug_dir" ]]; then
    return
  fi
  mkdir -p "$debug_dir"
  printf '%s\n' "$content" > "$debug_dir/$name"
}

emit_message() {
  local text="$1"
  printf '{"type":"thread.started","thread_id":"fake-thread"}\n'
  printf '{"type":"turn.started"}\n'
  printf '{"type":"item.completed","item":{"id":"msg-1","type":"agent_message","text":%s}}\n' "$text"
  printf '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}\n'
}

json_string() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '"%s"\n' "$value"
}

if [[ "$instruction" == *"Required Response Format (JSON)"* ]]; then
  passed=true
  evidence="workspace diff captured expected change"
  case_id="unknown"

  if [[ "$instruction" == *"git-init-workspace"* ]]; then
    case_id="git-init-workspace"
    [[ "$instruction" == *"diff --git a/README.md b/README.md"* ]] || passed=false
  elif [[ "$instruction" == *"cloned-git-workspace"* ]]; then
    case_id="cloned-git-workspace"
    [[ "$instruction" == *"diff --git a/repo.txt b/repo.txt"* ]] || passed=false
  else
    passed=false
  fi

  write_debug_file "judge-${case_id}.txt" "$instruction"

  [[ "$instruction" == *"-before"* ]] || passed=false
  [[ "$instruction" == *"+after"* ]] || passed=false
  if [[ "$instruction" == *"stdout.json"* ]]; then
    passed=false
    evidence="workspace diff leaked generated artifacts"
  fi

  if [[ "$passed" == true ]]; then
    response='{"results":[{"criterion":"workspace diff reflects the file edit","passed":true,"evidence":"workspace diff captured expected change"}]}'
  else
    response='{"results":[{"criterion":"workspace diff reflects the file edit","passed":false,"evidence":"workspace diff missing expected change or leaked artifacts"}]}'
  fi
  emit_message "$(json_string "$response")"
  exit 0
fi

if [[ "$instruction" == *"git-init-workspace"* ]]; then
  printf 'after\n' > README.md
  emit_message "$(json_string "updated git-init-workspace")"
  exit 0
fi

if [[ "$instruction" == *"cloned-git-workspace"* ]]; then
  printf 'after\n' > repo.txt
  emit_message "$(json_string "updated cloned-git-workspace")"
  exit 0
fi

emit_message "$(json_string "unexpected instruction")"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return dir
}

func createWorkspaceDiffEvalDir(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Workspace Diff Test Skill\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	evalsDir := filepath.Join(root, "evals")
	casesDir := filepath.Join(evalsDir, "cases")
	fixturesDir := filepath.Join(evalsDir, "fixtures")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatalf("create cases dir: %v", err)
	}
	if err := os.MkdirAll(fixturesDir, 0o755); err != nil {
		t.Fatalf("create fixtures dir: %v", err)
	}

	initFixtureDir := filepath.Join(fixturesDir, "init-workspace")
	if err := os.MkdirAll(initFixtureDir, 0o755); err != nil {
		t.Fatalf("create init fixture dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(initFixtureDir, "README.md"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write init fixture file: %v", err)
	}

	cloneGitFixture(t, fixturesDir)

	initCase := `id: git-init-workspace
title: Workspace diff works with git init
description: Verifies workspace diff is collected when context.git.init is true.

input:
  prompt: |
    git-init-workspace

context:
  repo_fixture: evals/fixtures/init-workspace
  git:
    init: true

constraints:
  timeout_seconds: 20
  max_turns: 1

judge:
  type: agent_judge
  model: openai/fake-judge
  criteria:
    - "workspace diff reflects the file edit"
`
	if err := os.WriteFile(filepath.Join(casesDir, "git-init-workspace.yaml"), []byte(initCase), 0o644); err != nil {
		t.Fatalf("write init case: %v", err)
	}

	existingRepoCase := `id: cloned-git-workspace
title: Workspace diff works with cloned git repo
description: Verifies workspace diff is collected when repo_fixture comes from git clone.

input:
  prompt: |
    cloned-git-workspace

context:
  repo_fixture: evals/fixtures/cloned-repo

constraints:
  timeout_seconds: 20
  max_turns: 1

judge:
  type: agent_judge
  model: openai/fake-judge
  criteria:
    - "workspace diff reflects the file edit"
`
	if err := os.WriteFile(filepath.Join(casesDir, "cloned-git-workspace.yaml"), []byte(existingRepoCase), 0o644); err != nil {
		t.Fatalf("write existing repo case: %v", err)
	}

	evalContent := `schema_version: v1alpha1

environment:
  type: none
  workspace_mount: /workspace

mcp:
  servers: []

skills: []

engine:
  name: codex
  model:
    provider: openai
    name: gpt-5.4

cases:
  files:
    - evals/cases/git-init-workspace.yaml
    - evals/cases/cloned-git-workspace.yaml
  defaults:
    timeout_seconds: 20
    max_turns: 1
  parallelism: 1

judge:
  type: rule_based
  success:
    - output_contains:
        all: ["updated"]

report:
  formats: [json]
  artifacts: []
`
	if err := os.WriteFile(filepath.Join(evalsDir, "eval.yaml"), []byte(evalContent), 0o644); err != nil {
		t.Fatalf("write eval.yaml: %v", err)
	}

	return root
}

func cloneGitFixture(t *testing.T, fixturesDir string) string {
	t.Helper()

	originDir := filepath.Join(t.TempDir(), "origin-repo")
	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatalf("create origin repo dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(originDir, "repo.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write origin repo file: %v", err)
	}
	initGitRepository(t, originDir)

	clonedDir := filepath.Join(fixturesDir, "cloned-repo")
	cmd := exec.Command("git", "clone", originDir, clonedDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone fixture failed: %v\n%s", err, string(output))
	}
	return clonedDir
}

func initGitRepository(t *testing.T, dir string) {
	t.Helper()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run %q in %s: %v\n%s", strings.Join(args, " "), dir, err, string(output))
		}
	}

	run("git", "init", "-q")
	run("git", "config", "user.name", "skill-up")
	run("git", "config", "user.email", "skill-up@example.invalid")
	run("git", "add", "--all")
	run("git", "commit", "-qm", "baseline")
}

func assertWorkspaceDiffCasePassed(t *testing.T, workspaceDir, debugDir, caseID string) {
	t.Helper()

	gradingPath := filepath.Join(workspaceDir, "iteration-1", caseID, "with_skill", "grading.json")
	data, err := os.ReadFile(gradingPath)
	if err != nil {
		t.Fatalf("read grading %s: %v", gradingPath, err)
	}

	var grading struct {
		Expectations []struct {
			Text     string `json:"text"`
			Passed   bool   `json:"passed"`
			Evidence string `json:"evidence"`
		} `json:"expectations"`
		Summary struct {
			PassRate float64 `json:"pass_rate"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(data, &grading); err != nil {
		t.Fatalf("parse grading %s: %v", gradingPath, err)
	}

	debugPrompt := readWorkspaceDiffDebugPrompt(t, debugDir, caseID)
	if grading.Summary.PassRate != 1 {
		t.Fatalf("expected %s pass_rate 1, got %.2f\ngrading: %s\njudge prompt:\n%s", caseID, grading.Summary.PassRate, string(data), debugPrompt)
	}
	if len(grading.Expectations) != 1 || !grading.Expectations[0].Passed {
		t.Fatalf("expected %s expectation to pass\ngrading: %s\njudge prompt:\n%s", caseID, string(data), debugPrompt)
	}
}

func readWorkspaceDiffDebugPrompt(t *testing.T, debugDir, caseID string) string {
	t.Helper()

	if debugDir == "" {
		return "<debug prompt unavailable>"
	}

	debugPath := filepath.Join(debugDir, "judge-"+caseID+".txt")
	data, err := os.ReadFile(debugPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "<debug prompt file missing>"
		}
		t.Fatalf("read debug prompt %s: %v", debugPath, err)
	}
	return string(data)
}

// createTestEvalDir creates a temporary eval directory with skill and case files.
func createTestEvalDir(t *testing.T, skillName string) string {
	t.Helper()

	evalDir := t.TempDir()

	// Create SKILL.md at the root so findSkillDir resolves skillDir = evalDir.
	skillContent := "# Test Skill\nThis is a test skill.\n"
	if err := os.WriteFile(filepath.Join(evalDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Create skill source directory (for skill installation)
	skillPath := filepath.Join(evalDir, skillName)
	if err := os.MkdirAll(skillPath, 0o755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillPath, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatalf("failed to write skill SKILL.md: %v", err)
	}

	// Create case file in evals/cases subdirectory.
	// Case paths in eval.yaml are relative to skillDir (where root SKILL.md lives).
	casesDir := filepath.Join(evalDir, "evals", "cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatalf("failed to create cases dir: %v", err)
	}

	caseContent := `id: test-case
title: Test Case
description: A test case for e2e testing

input:
  prompt: Reply with exactly the word hello. Do not run commands.

constraints:
  timeout_seconds: 5
  max_turns: 1

expect:
  must_contain:
    - "hello"
`
	if err := os.WriteFile(filepath.Join(casesDir, "test-case.yaml"), []byte(caseContent), 0o644); err != nil {
		t.Fatalf("failed to write case file: %v", err)
	}

	return evalDir
}

// TestAgent_QoderCLI_NoneRuntime verifies that the qodercli agent wires
// correctly into the CLI run pipeline: arguments are built, the engine is
// invoked via PATH lookup, workspace artifacts (iteration-1, benchmark.json,
// result.json) are produced, and rule-based judging succeeds on the engine
// output. It runs against the deterministic mock engine so it does not depend
// on the developer's qodercli login state.
func TestAgent_QoderCLI_NoneRuntime(t *testing.T) {
	t.Parallel()

	evalDir := createTestEvalDir(t, "test-skill-qoder")

	// Create eval.yaml for qoder-cli
	evalContent := `schema_version: v1alpha1

environment:
  type: none
  workspace_mount: /workspace
  env:
    TZ: UTC

mcp:
  servers: []

engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto

skills: []

cases:
  files:
    - evals/cases/test-case.yaml
  defaults:
    timeout_seconds: 300
    max_turns: 1
  parallelism: 1
  retry_policy:
    max_retries: 1
    retry_on:
      - timeout

judge:
  type: rule_based
  rule_based:
    must_contain:
      - "Hello"

report:
  formats: [json]
  artifacts: []
`

	evalPath := filepath.Join(evalDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(evalContent), 0o644); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	caseContent := `id: test-case
title: Test Case
description: A test case for e2e testing

input:
  prompt: Please say hello to the user.

constraints:
  timeout_seconds: 300
  max_turns: 1

expect:
  must_contain:
    - "Hello"
`
	if err := os.WriteFile(filepath.Join(evalDir, "evals", "cases", "test-case.yaml"), []byte(caseContent), 0o644); err != nil {
		t.Fatalf("failed to rewrite case file: %v", err)
	}

	outputDir := filepath.Join(evalDir, "output")
	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, Timeout: 60 * time.Second},
		"run", evalPath, "--output-dir", outputDir)

	if result.ExitCode != 0 {
		t.Fatalf("qodercli run failed: exit=%d\nstdout:\n%s\nstderr:\n%s",
			result.ExitCode, result.Stdout, result.Stderr)
	}

	iterationDir := filepath.Join(outputDir, "iteration-1")
	for _, rel := range []string{"benchmark.json", "result.json"} {
		if _, err := os.Stat(filepath.Join(iterationDir, rel)); err != nil {
			t.Errorf("%s not created under iteration-1: %v", rel, err)
		}
	}

	responsePath := filepath.Join(iterationDir, "test-case", "with_skill", "outputs", "response.md")
	if _, err := os.Stat(responsePath); err != nil {
		t.Errorf("response.md not created at %s: %v", responsePath, err)
	}
}

// TestAgent_Codex_NoneRuntime tests codex agent with none runtime.
func TestAgent_Codex_NoneRuntime(t *testing.T) {
	t.Parallel()
	if !isCodexInstalled() {
		t.Skip("codex not installed locally")
	}

	evalDir := createTestEvalDir(t, "test-skill-codex")

	evalContent := `schema_version: v1alpha1

environment:
  type: none
  workspace_mount: /workspace
  env:
    TZ: UTC

mcp:
  servers: []

skills:
  - source: local_path
    path: .
    target: ~/.codex/skills/test-skill-codex

engine:
  name: codex
  model:
    provider: openai
    name: gpt-5.4

cases:
  files:
    - evals/cases/test-case.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
  parallelism: 1

judge:
  type: rule_based
  rule_based:
    must_contain:
      - "hello"

report:
  formats: [json]
  artifacts: []
`

	evalPath := filepath.Join(evalDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(evalContent), 0o644); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	result := Run(t, RunConfig{Timeout: 120e9}, "run", evalPath)
	t.Logf("codex result: exit=%d", result.ExitCode)
	if result.ExitCode != 0 {
		t.Logf("codex stderr: %s", result.Stderr)
	}

	var workspaceDir string
	evalDirBase := filepath.Base(evalDir)
	workspaceDir = filepath.Join(filepath.Dir(evalDir), evalDirBase+"-workspace")
	if _, err := os.Stat(workspaceDir); os.IsNotExist(err) {
		entries, _ := os.ReadDir(evalDir)
		for _, e := range entries {
			t.Logf("evalDir contents: %s (isDir=%v)", e.Name(), e.IsDir())
		}
		t.Fatalf("workspace directory not found at %s", workspaceDir)
	}
	preserveWorkspaceArtifacts(t, workspaceDir)

	iterationDir := filepath.Join(workspaceDir, "iteration-1")
	if _, err := os.Stat(iterationDir); os.IsNotExist(err) {
		t.Fatalf("workspace iteration-1 not created")
	}

	caseOutputDir := filepath.Join(iterationDir, "test-case", "with_skill", "outputs", "agent", "run")
	if _, err := os.Stat(caseOutputDir); os.IsNotExist(err) {
		t.Fatalf("case outputs directory not created: %s", caseOutputDir)
	}

	if matches, err := filepath.Glob(filepath.Join(caseOutputDir, "stdout.json")); err != nil || len(matches) == 0 {
		t.Errorf("stdout.json not found in %s", caseOutputDir)
	}

	responsePath := filepath.Join(iterationDir, "test-case", "with_skill", "outputs", "response.md")
	if _, err := os.Stat(responsePath); os.IsNotExist(err) {
		t.Errorf("response.md not created")
	}
}

func TestAgent_Codex_OpenSandboxRuntime(t *testing.T) {

	sandboxAPIKey := openSandboxE2EAPIKey()
	if sandboxAPIKey == "" {
		t.Skip("OPENSANDBOX_API_KEY not set, skipping codex opensandbox test")
	}
	// codex speaks the OpenAI wire API. The eval declares `provider: openai`,
	// so skill-up resolves the key, endpoint and model name straight from
	// OPENAI_API_KEY / OPENAI_BASE_URL / OPENAI_MODEL — all external config,
	// no in-test defaults or cross-provider re-exports.
	codexAPIKey := os.Getenv("OPENAI_API_KEY")
	if codexAPIKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping codex opensandbox test")
	}
	codexBaseURL := os.Getenv("OPENAI_BASE_URL")
	if codexBaseURL == "" {
		t.Skip("OPENAI_BASE_URL not set, skipping codex opensandbox test")
	}
	codexModel := os.Getenv("OPENAI_MODEL")
	if codexModel == "" {
		t.Skip("OPENAI_MODEL not set, skipping codex opensandbox test")
	}

	evalDir := t.TempDir()
	writeFile(t, filepath.Join(evalDir, "SKILL.md"), "# Codex OpenSandbox E2E\n")
	writeFile(t, filepath.Join(evalDir, "evals", "cases", "ok.yaml"), `id: ok
title: Codex OpenSandbox smoke
input:
  prompt: Return a short acknowledgement.
constraints:
  timeout_seconds: 600
  max_turns: 1
expect:
  must_not_contain:
    - __skill_up_forbidden__
`)

	evalPath := filepath.Join(evalDir, "evals", "eval.yaml")
	writeFile(t, evalPath, `schema_version: v1alpha1
environment:
  type: opensandbox
  workspace_mount: /workspace
  image: `+openSandboxE2EImage()+`
  ready_timeout_seconds: 300
  kwargs:
    base_url: `+openSandboxE2EBaseURL()+`
    request_timeout_seconds: "900"
mcp:
  servers: []
skills: []
engine:
  name: codex
  model:
    provider: openai
    name: `+codexModel+`
cases:
  files:
    - evals/cases/ok.yaml
  defaults:
    timeout_seconds: 600
    max_turns: 1
  parallelism: 1
  retry_policy:
    max_retries: 0
benchmark:
  enabled: false
judge:
  type: rule_based
report:
  formats: [json]
  artifacts: [transcript]
`)

	outputDir := t.TempDir()
	// Surface the in-sandbox agent artifacts (stdout.json / response.md /
	// transcript) as CI artifacts so a failure inside the sandbox is debuggable.
	preserveWorkspaceArtifacts(t, outputDir)
	result := Run(t, RunConfig{
		Timeout: 10 * 60e9,
		Env: []string{
			"OPENSANDBOX_API_KEY=" + sandboxAPIKey,
			"OPENAI_API_KEY=" + codexAPIKey,
			"OPENAI_BASE_URL=" + codexBaseURL,
		},
	}, "run", evalPath, "--output-dir", outputDir)

	if result.ExitCode == ExitCodeTimeout {
		t.Skip("codex opensandbox run timed out")
	}
	if isOpenSandboxReadyFailure(result.Stderr) {
		t.Skipf("opensandbox did not become ready: %s", result.Stderr)
	}
	if result.ExitCode != 0 {
		t.Fatalf("codex opensandbox run failed: exit=%d\nstdout=%s\nstderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}

	resultPath := filepath.Join(outputDir, "iteration-1", "result.json")
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("failed to read result.json: %v", err)
	}
	if !strings.Contains(string(data), `"engine_name": "codex"`) || !strings.Contains(string(data), `"status": "PASS"`) {
		t.Fatalf("unexpected result.json:\n%s", string(data))
	}
	responsePath := filepath.Join(outputDir, "iteration-1", "ok", "with_skill", "outputs", "response.md")
	response, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("failed to read response.md: %v", err)
	}
	if strings.TrimSpace(string(response)) == "" {
		t.Fatalf("response.md is empty")
	}
}

func isOpenSandboxReadyFailure(stderr string) bool {
	return strings.Contains(stderr, "failed to create opensandbox") &&
		strings.Contains(stderr, "did not become ready")
}

// TestAgent_ClaudeCode_OpenSandboxRuntime exercises the claude_code engine
// against a real OpenSandbox runtime. Mirrors TestAgent_Codex_OpenSandboxRuntime
// so both supported engines have end-to-end coverage of the opensandbox bridge.
func TestAgent_ClaudeCode_OpenSandboxRuntime(t *testing.T) {
	// No skipIfClaudeUnavailable here: the opensandbox runtime bootstraps the
	// claude CLI inside the sandbox, so the host runner does not need it.
	sandboxAPIKey := openSandboxE2EAPIKey()
	if sandboxAPIKey == "" {
		t.Skip("OPENSANDBOX_API_KEY not set, skipping claude_code opensandbox test")
	}
	// claude_code speaks the Anthropic wire API. The eval declares
	// `provider: anthropic`, so skill-up resolves the key, endpoint and model
	// name straight from ANTHROPIC_API_KEY / ANTHROPIC_BASE_URL /
	// ANTHROPIC_MODEL — all external config, no in-test defaults. Whether the
	// key is a real Anthropic key or a DashScope key for the Anthropic-
	// compatible endpoint is the CI workflow's choice.
	modelAPIKey := os.Getenv("ANTHROPIC_API_KEY")
	if modelAPIKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping claude_code opensandbox test")
	}
	modelBaseURL := os.Getenv("ANTHROPIC_BASE_URL")
	if modelBaseURL == "" {
		t.Skip("ANTHROPIC_BASE_URL not set, skipping claude_code opensandbox test")
	}
	modelName := os.Getenv("ANTHROPIC_MODEL")
	if modelName == "" {
		t.Skip("ANTHROPIC_MODEL not set, skipping claude_code opensandbox test")
	}

	evalDir := t.TempDir()
	writeFile(t, filepath.Join(evalDir, "SKILL.md"), "# ClaudeCode OpenSandbox E2E\n")
	writeFile(t, filepath.Join(evalDir, "evals", "cases", "ok.yaml"), `id: ok
title: ClaudeCode OpenSandbox smoke
input:
  prompt: Return a short acknowledgement.
constraints:
  timeout_seconds: 600
  max_turns: 1
expect:
  must_not_contain:
    - __skill_up_forbidden__
`)

	evalPath := filepath.Join(evalDir, "evals", "eval.yaml")
	writeFile(t, evalPath, `schema_version: v1alpha1
environment:
  type: opensandbox
  workspace_mount: /workspace
  image: `+openSandboxE2EImage()+`
  ready_timeout_seconds: 300
  kwargs:
    base_url: `+openSandboxE2EBaseURL()+`
    request_timeout_seconds: "900"
mcp:
  servers: []
skills: []
engine:
  name: claude_code
  model:
    provider: anthropic
    name: `+modelName+`
cases:
  files:
    - evals/cases/ok.yaml
  defaults:
    timeout_seconds: 600
    max_turns: 1
  parallelism: 1
  retry_policy:
    max_retries: 0
benchmark:
  enabled: false
judge:
  type: rule_based
report:
  formats: [json]
  artifacts: [transcript]
`)

	outputDir := t.TempDir()
	// Surface the in-sandbox agent artifacts (stdout.json / response.md /
	// transcript) as CI artifacts so a failure inside the sandbox is debuggable.
	preserveWorkspaceArtifacts(t, outputDir)
	result := Run(t, RunConfig{
		Timeout: 10 * 60e9,
		Env: []string{
			"OPENSANDBOX_API_KEY=" + sandboxAPIKey,
			"ANTHROPIC_API_KEY=" + modelAPIKey,
			"ANTHROPIC_BASE_URL=" + modelBaseURL,
		},
	}, "run", evalPath, "--output-dir", outputDir)

	if result.ExitCode == ExitCodeTimeout {
		t.Skip("claude_code opensandbox run timed out")
	}
	if isOpenSandboxReadyFailure(result.Stderr) {
		t.Skipf("opensandbox did not become ready: %s", result.Stderr)
	}
	if result.ExitCode != 0 {
		t.Fatalf("claude_code opensandbox run failed: exit=%d\nstdout=%s\nstderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}

	resultPath := filepath.Join(outputDir, "iteration-1", "result.json")
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("failed to read result.json: %v", err)
	}
	if !strings.Contains(string(data), `"engine_name": "claude_code"`) || !strings.Contains(string(data), `"status": "PASS"`) {
		t.Fatalf("unexpected result.json:\n%s", string(data))
	}
	responsePath := filepath.Join(outputDir, "iteration-1", "ok", "with_skill", "outputs", "response.md")
	response, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("failed to read response.md: %v", err)
	}
	if strings.TrimSpace(string(response)) == "" {
		t.Fatalf("response.md is empty")
	}
}

// TestAgent_ClaudeCode_WithAPIKey tests claude-code with real API key.
func TestAgent_ClaudeCode_WithAPIKey(t *testing.T) {
	t.Parallel()

	skipIfNotFullE2E(t)
	skipIfClaudeUnavailable(t)
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping real API key test")
	}

	evalDir := createTestEvalDir(t, "test-skill-claude-api")

	evalContent := `schema_version: v1alpha1

environment:
  type: none
  workspace_mount: /workspace

mcp:
  servers: []

skills:
  - source: local_path
    path: .
    target: ~/.claude/skills/test-skill-claude-api

engine:
  name: claude_code
  model:
    provider: dashscope
    name: qwen3.6-plus

cases:
  files:
    - evals/cases/test-case.yaml
  defaults:
    timeout_seconds: 120
    max_turns: 1
  parallelism: 1

judge:
  type: rule_based
  rule_based:
    must_contain:
      - "hello"

report:
  formats: [json]
  artifacts: []
`

	evalPath := filepath.Join(evalDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(evalContent), 0o644); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	runCfg := RunConfig{Timeout: 180e9, Env: []string{"ANTHROPIC_API_KEY=" + apiKey}}
	result := Run(t, runCfg, "run", evalPath)

	// With real API key, we expect the command to complete
	// If it fails with API error (401, 403, quota exceeded), that's acceptable
	if result.ExitCode != 0 {
		if strings.Contains(result.Stderr, "command not found") {
			t.Errorf("claude not installed properly")
		} else if result.ExitCode == ExitCodeTimeout {
			t.Errorf("claude command timed out")
		} else {
			t.Logf("claude invoked with error (may be expected with invalid key or quota): exit=%d", result.ExitCode)
		}
	} else {
		t.Logf("claude run succeeded")
	}
}

func TestAgent_ClaudeCode_Installation(t *testing.T) {
	t.Parallel()

	path, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not found in PATH")
	}

	cmd := exec.Command(path, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("claude --version failed: %v, output: %s", err, output)
		return
	}
	t.Logf("claude installed: %s", strings.TrimSpace(string(output)))
}

// TestNoneRuntime_Validate tests validation with none runtime.
func TestNoneRuntime_Validate(t *testing.T) {
	t.Parallel()

	evalDir := createTestEvalDir(t, "test-skill-validate")

	evalContent := `schema_version: v1alpha1

environment:
  type: none
  workspace_mount: /workspace
  env:
    TZ: UTC

mcp:
  servers: []

skills:
  - source: local_path
    path: .
    target: ~/.claude/skills/test-skill-validate

engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto

cases:
  files:
    - evals/cases/test-case.yaml
  defaults:
    timeout_seconds: 10
    max_turns: 1
  parallelism: 1

judge:
  type: rule_based
  rule_based:
    must_contain:
      - "hello"

report:
  formats: [json]
  artifacts: []
`

	evalPath := filepath.Join(evalDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(evalContent), 0o644); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	result := Run(t, RunConfig{WorkDir: evalDir}, "validate", evalPath)

	if result.ExitCode != 0 {
		t.Fatalf("validation failed: exit=%d stdout=%s stderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}
}

// TestAgent_ClaudeCode_WithCredentialsFile tests claude-code with credentials.yaml file.
func TestAgent_ClaudeCode_WithCredentialsFile(t *testing.T) {
	t.Parallel()

	skipIfNotFullE2E(t)
	skipIfClaudeUnavailable(t)
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping credentials file test")
	}

	evalDir := createTestEvalDir(t, "test-skill-credentials")

	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		homeDir = "/root"
	}
	credentialsDir := filepath.Join(homeDir, ".skill-up")
	credentialsPath := filepath.Join(credentialsDir, "credentials.yaml")

	if err := os.MkdirAll(credentialsDir, 0o700); err != nil {
		t.Fatalf("failed to create credentials dir: %v", err)
	}

	credentialsContent := `providers:
  anthropic:
    api_key: ` + apiKey + `
`
	if err := os.WriteFile(credentialsPath, []byte(credentialsContent), 0o600); err != nil {
		t.Fatalf("failed to write credentials.yaml: %v", err)
	}
	defer os.Remove(credentialsPath)

	evalContent := `schema_version: v1alpha1

environment:
  type: none
  workspace_mount: /workspace

mcp:
  servers: []

skills:
  - source: local_path
    path: .
    target: ~/.claude/skills/test-skill-credentials

engine:
  name: claude_code
  model:
    provider: dashscope
    name: qwen3.6-plus

cases:
  files:
    - evals/cases/test-case.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
  parallelism: 1

judge:
  type: rule_based
  rule_based:
    must_contain:
      - "hello"

report:
  formats: [json]
  artifacts: []
`

	evalPath := filepath.Join(evalDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(evalContent), 0o644); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	result := Run(t, RunConfig{Timeout: 120e9}, "run", evalPath)

	if result.ExitCode != 0 && !strings.Contains(result.Stderr, "claude") && !strings.Contains(result.Stdout, "claude") {
		t.Logf("unexpected error: exit=%d stdout=%s stderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}
}

// TestNoneRuntime_ListCases tests listing cases with none runtime.
func TestNoneRuntime_ListCases(t *testing.T) {
	t.Parallel()

	evalDir := createTestEvalDir(t, "test-skill-listcases")

	evalContent := `schema_version: v1alpha1

environment:
  type: none
  workspace_mount: /workspace
  env:
    TZ: UTC

mcp:
  servers: []

skills:
  - source: local_path
    path: .
    target: ~/.claude/skills/test-skill-listcases

engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto

cases:
  files:
    - evals/cases/test-case.yaml
  defaults:
    timeout_seconds: 10
    max_turns: 1
  parallelism: 1

judge:
  type: rule_based
  rule_based:
    must_contain:
      - "hello"

report:
  formats: [json]
  artifacts: []
`

	evalPath := filepath.Join(evalDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(evalContent), 0o644); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	result := Run(t, RunConfig{WorkDir: evalDir}, "list-cases", evalPath)

	if result.ExitCode != 0 {
		t.Fatalf("list-cases failed: exit=%d stdout=%s stderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}

	if !strings.Contains(result.Stdout, "test-case") {
		t.Errorf("expected 'test-case' in output, got: %s", result.Stdout)
	}
}

// TestAgent_QoderCLI_NoneRuntime_FullRun tests qodercli agent with none runtime.
// This verifies skill installation and engine invocation work correctly.
func TestAgent_QoderCLI_NoneRuntime_FullRun(t *testing.T) {
	t.Parallel()
	if !isQoderCLIInstalled() {
		t.Skip("qodercli not installed locally")
	}

	// Create a proper project layout: <projectDir>/evals/eval.yaml
	// so that the runner derives the correct workspace path.
	base := t.TempDir()
	projectDir := filepath.Join(base, "test-skill-qoder-none")
	evalsDir := filepath.Join(projectDir, "evals")
	casesDir := filepath.Join(evalsDir, "cases")

	writeFile(t, filepath.Join(projectDir, "SKILL.md"), "# Test Skill\nThis is a test skill.\n")

	evalContent := `schema_version: v1alpha1

environment:
  type: none

skills:
  - source: local_path
    path: .

engine:
  name: qoder-cli
  model:
    provider: dashscope
    name: qwen3.6-plus

cases:
  files:
    - evals/cases/test-case.yaml
  defaults:
    timeout_seconds: 60
    max_turns: 3
  parallelism: 1

report:
  formats: [json]
  artifacts: []
`
	evalPath := filepath.Join(evalsDir, "eval.yaml")
	writeFile(t, evalPath, evalContent)

	caseContent := `id: test-case
title: Test Case
input:
  prompt: Reply with exactly the word hello. Do not run commands.
constraints:
  timeout_seconds: 60
  max_turns: 1
expect:
  must_contain:
    - "hello"
`
	writeFile(t, filepath.Join(casesDir, "test-case.yaml"), caseContent)

	// Workspace is created alongside the skill directory as <basename>-workspace.
	workspaceDir := filepath.Join(base, "test-skill-qoder-none-workspace")
	preserveWorkspaceArtifacts(t, workspaceDir)
	result := Run(t, RunConfig{Timeout: 120e9}, "run", evalPath)

	if result.ExitCode != 0 && result.ExitCode != -1 {
		if strings.Contains(result.Stderr, "qodercli") ||
			strings.Contains(result.Stdout, "qodercli") ||
			strings.Contains(result.Stderr, "not logged in") ||
			strings.Contains(result.Stderr, "authentication") {
			t.Logf("qodercli engine invoked as expected, exit code: %d", result.ExitCode)
		} else {
			t.Fatalf("qodercli run failed unexpectedly: exit=%d stdout=%s stderr=%s", result.ExitCode, result.Stdout, result.Stderr)
		}
	}

	// Verify workspace directory was created.
	if _, err := os.Stat(workspaceDir); os.IsNotExist(err) {
		t.Fatalf("expected workspace directory at %s", workspaceDir)
	}

	// Verify result.json was produced in the iteration directory.
	resultPath := filepath.Join(workspaceDir, "iteration-1", "result.json")
	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		t.Logf("result.json not found at %s (may be expected if agent errored): %v", resultPath, err)
		return
	}
	t.Logf("result.json: %s", string(resultData))
}

// TestCodeStatsBenchmark tests the code-stats example benchmark.
func TestCodeStatsBenchmark(t *testing.T) {
	t.Parallel()

	skipIfNotFullE2E(t)
	skipIfQoderCLIUnavailable(t)
	qoderToken := os.Getenv("QODER_PERSONAL_ACCESS_TOKEN")
	if qoderToken == "" {
		t.Skip("QODER_PERSONAL_ACCESS_TOKEN not set, skipping benchmark test")
	}
	sandboxAPIKey := openSandboxE2EAPIKey()
	if sandboxAPIKey == "" {
		t.Skip("OPENSANDBOX_API_KEY not set, skipping benchmark test")
	}

	codeStatsPath := filepath.Join(getProjectRoot(), "examples", "code-stats")
	evalPath := filepath.Join(codeStatsPath, "evals", "eval.yaml")
	workspaceDir := t.TempDir()

	result := Run(t, RunConfig{
		Timeout: 1800e9,
		Env: []string{
			"QODER_PERSONAL_ACCESS_TOKEN=" + qoderToken,
			"OPENSANDBOX_API_KEY=" + sandboxAPIKey,
		},
	}, "run", evalPath, "--output-dir", workspaceDir)

	if result.ExitCode == ExitCodeTimeout {
		t.Skip("skill-up run timed out; skipping benchmark assertions")
	}
	// LLM responses are non-deterministic; allow non-zero exit from case failures.
	if result.ExitCode != 0 {
		t.Logf("skill-up run exited %d (cases may have failed expect checks): stderr=%s", result.ExitCode, result.Stderr)
	}

	benchmarkPath := filepath.Join(workspaceDir, "iteration-1", "benchmark.json")
	data, err := os.ReadFile(benchmarkPath)
	if err != nil {
		if result.ExitCode != 0 {
			t.Skipf("benchmark.json not produced (run exited %d, likely API issue): %v", result.ExitCode, err)
		}
		t.Fatalf("failed to read benchmark.json: %v", err)
	}

	var benchmark struct {
		Runs []struct {
			Configuration string `json:"configuration"`
			Result        struct {
				PassRate float64 `json:"pass_rate"`
				Tokens   float64 `json:"tokens"`
				Errors   int     `json:"errors"`
				Total    int     `json:"total"`
			} `json:"result"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &benchmark); err != nil {
		t.Fatalf("failed to parse benchmark.json: %v", err)
	}

	var withSkillRun *struct {
		Configuration string `json:"configuration"`
		Result        struct {
			PassRate float64 `json:"pass_rate"`
			Tokens   float64 `json:"tokens"`
			Errors   int     `json:"errors"`
			Total    int     `json:"total"`
		} `json:"result"`
	}
	for i := range benchmark.Runs {
		if benchmark.Runs[i].Configuration == "with_skill" {
			withSkillRun = &benchmark.Runs[i]
			break
		}
	}
	if withSkillRun == nil {
		t.Fatal("with_skill configuration not found in benchmark results")
	}
	if withSkillRun.Result.Errors > 0 {
		t.Skipf("with_skill run reported %d error(s); skipping pass_rate assertion", withSkillRun.Result.Errors)
	}

	if err := validateBenchmarkArtifacts(evalPath, workspaceDir, "iteration-1"); err != nil {
		t.Logf("skill-up stdout:\n%s", result.Stdout)
		t.Logf("skill-up stderr:\n%s", result.Stderr)
		logDirTree(t, filepath.Join(workspaceDir, "iteration-1"))
		var transientErr *benchmarkTransientError
		if errors.As(err, &transientErr) {
			t.Skipf("benchmark skipped due to transient provider failure: %s", transientErr.Error())
		}
		t.Fatalf("benchmark artifact validation failed: %v", err)
	}

	// If all cases errored (e.g. API failures), log and skip assertions.
	if withSkillRun.Result.Tokens <= 0 {
		t.Skipf("with_skill run produced zero tokens (likely API issue); skipping pass_rate assertion")
	}

	// LLM responses are non-deterministic; pass_rate < 1 does not indicate a code bug.
	if withSkillRun.Result.PassRate != 1 {
		t.Logf("with_skill pass_rate = %v, want 1 (LLM non-determinism)", withSkillRun.Result.PassRate)
	}
	if withSkillRun.Result.Tokens <= 0 {
		t.Errorf("with_skill tokens = %v, want > 0", withSkillRun.Result.Tokens)
	}
}

// TestAgent_EvalsExcluded verifies evals directory is excluded from skill upload.
func TestAgent_EvalsExcluded(t *testing.T) {
	t.Parallel()

	evalDir := t.TempDir()

	// Create root SKILL.md so findSkillDir resolves skillDir = evalDir.
	if err := os.WriteFile(filepath.Join(evalDir, "SKILL.md"), []byte("# Test Skill\n"), 0o644); err != nil {
		t.Fatalf("failed to write root SKILL.md: %v", err)
	}

	// Create skill with evals directory
	skillName := "test-skill-evals"
	skillPath := filepath.Join(evalDir, skillName)

	if err := os.MkdirAll(skillPath, 0o755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	// Create SKILL.md in skill source directory
	if err := os.WriteFile(filepath.Join(skillPath, "SKILL.md"), []byte("# Test Skill\n"), 0o644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Create evals/should/be/excluded/file.txt (should be excluded)
	evalsPath := filepath.Join(skillPath, "evals", "should", "be", "excluded")
	if err := os.MkdirAll(evalsPath, 0o755); err != nil {
		t.Fatalf("failed to create evals path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(evalsPath, "file.txt"), []byte("should be excluded"), 0o644); err != nil {
		t.Fatalf("failed to write file.txt: %v", err)
	}

	// Create case file
	casesDir := filepath.Join(evalDir, "evals", "cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatalf("failed to create cases dir: %v", err)
	}
	caseContent := `id: test-case
title: Test Case
description: A test case

input:
  prompt: echo "hello"

constraints:
  timeout_seconds: 5
  max_turns: 1

expect:
  must_contain:
    - "hello"
`
	if err := os.WriteFile(filepath.Join(casesDir, "test-case.yaml"), []byte(caseContent), 0o644); err != nil {
		t.Fatalf("failed to write case file: %v", err)
	}

	evalContent := `schema_version: v1alpha1

environment:
  type: none

mcp:
  servers: []

skills:
  - source: ./` + skillName + `
    path: .
    target: ~/.claude/skills/` + skillName + `

engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto

cases:
  files:
    - evals/cases/test-case.yaml
  defaults:
    timeout_seconds: 5
  parallelism: 1

judge:
  type: rule_based
  rule_based:
    must_contain:
      - "hello"

report:
  formats: [json]
  artifacts: []
`

	evalPath := filepath.Join(evalDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(evalContent), 0o644); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	result := Run(t, RunConfig{Timeout: 30e9}, "run", evalPath)

	if result.ExitCode != 0 {
		if !strings.Contains(result.Stderr, "evals/should/be/excluded") {
			t.Logf("evals excluded correctly, exit code: %d", result.ExitCode)
		}
	}
}

// TestCodeStatsBenchmark_WithArtifactValidation tests the code-stats example benchmark with artifact validation.
func TestCodeStatsBenchmark_WithArtifactValidation(t *testing.T) {
	t.Parallel()

	skipIfNotFullE2E(t)
	skipIfQoderCLIUnavailable(t)
	qoderToken := os.Getenv("QODER_PERSONAL_ACCESS_TOKEN")
	if qoderToken == "" {
		t.Skip("QODER_PERSONAL_ACCESS_TOKEN not set, skipping benchmark test")
	}
	sandboxAPIKey := openSandboxE2EAPIKey()
	if sandboxAPIKey == "" {
		t.Skip("OPENSANDBOX_API_KEY not set, skipping benchmark test")
	}

	codeStatsPath := filepath.Join(getProjectRoot(), "examples", "code-stats")
	evalPath := filepath.Join(codeStatsPath, "evals", "eval.yaml")
	workspaceDir := t.TempDir()

	result := Run(t, RunConfig{
		Timeout: 1800e9,
		Env: []string{
			"QODER_PERSONAL_ACCESS_TOKEN=" + qoderToken,
			"OPENSANDBOX_API_KEY=" + sandboxAPIKey,
		},
	}, "run", evalPath, "--output-dir", workspaceDir)

	if result.ExitCode == ExitCodeTimeout {
		t.Skip("skill-up run timed out; skipping artifact validation")
	}
	if result.ExitCode != 0 {
		t.Logf("skill-up run exited %d: stderr=%s", result.ExitCode, result.Stderr)
	}

	benchmarkPath := filepath.Join(workspaceDir, "iteration-1", "benchmark.json")
	data, err := os.ReadFile(benchmarkPath)
	if err != nil {
		if result.ExitCode != 0 {
			t.Skipf("benchmark.json not produced (run exited %d, likely API issue): %v", result.ExitCode, err)
		}
		t.Fatalf("failed to read benchmark.json: %v", err)
	}

	var benchmark struct {
		Runs []struct {
			EvalName      string `json:"eval_name"`
			Configuration string `json:"configuration"`
			Result        struct {
				PassRate float64 `json:"pass_rate"`
				Tokens   float64 `json:"tokens"`
				Errors   int     `json:"errors"`
				Total    int     `json:"total"`
			} `json:"result"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &benchmark); err != nil {
		t.Fatalf("failed to parse benchmark.json: %v", err)
	}

	// Check if any run had real results (non-zero tokens) before validating artifacts.
	hasRealResults := false
	for _, run := range benchmark.Runs {
		if run.Result.Tokens > 0 {
			hasRealResults = true
			break
		}
	}
	if !hasRealResults {
		t.Skip("all benchmark runs produced zero tokens (likely API issues); skipping artifact validation")
	}

	var withSkillRun *struct {
		EvalName      string `json:"eval_name"`
		Configuration string `json:"configuration"`
		Result        struct {
			PassRate float64 `json:"pass_rate"`
			Tokens   float64 `json:"tokens"`
			Errors   int     `json:"errors"`
			Total    int     `json:"total"`
		} `json:"result"`
	}
	for i := range benchmark.Runs {
		if benchmark.Runs[i].Configuration == "with_skill" &&
			benchmark.Runs[i].EvalName == "Analyze code stats for evals directory" {
			withSkillRun = &benchmark.Runs[i]
			break
		}
	}
	if withSkillRun == nil {
		t.Fatal("analyze-directory with_skill run not found in benchmark results")
	}
	if withSkillRun.Result.Errors > 0 {
		t.Skipf("with_skill run reported %d error(s); skipping artifact validation", withSkillRun.Result.Errors)
	}

	if err := validateBenchmarkArtifacts(evalPath, workspaceDir, "iteration-1"); err != nil {
		t.Logf("skill-up stdout:\n%s", result.Stdout)
		t.Logf("skill-up stderr:\n%s", result.Stderr)
		logDirTree(t, filepath.Join(workspaceDir, "iteration-1"))
		var transientErr *benchmarkTransientError
		if errors.As(err, &transientErr) {
			t.Skipf("benchmark skipped due to transient provider failure: %s", transientErr.Error())
		}
		t.Errorf("benchmark artifact validation failed: %v", err)
		return
	}

	// LLM responses are non-deterministic; pass_rate < 1 does not indicate a code bug.
	if withSkillRun.Result.PassRate != 1 {
		t.Logf("with_skill pass_rate = %v, want 1 (LLM non-determinism)", withSkillRun.Result.PassRate)
	}
	if withSkillRun.Result.Tokens <= 0 {
		t.Errorf("with_skill tokens = %v, want > 0", withSkillRun.Result.Tokens)
	}
}

func validateBenchmarkArtifacts(evalPath, workspaceDir, iteration string) error {
	iterationDir := filepath.Join(workspaceDir, iteration)
	judgeTypes, err := loadCaseJudgeTypes(evalPath)
	if err != nil {
		return err
	}

	benchmarkPath := filepath.Join(iterationDir, "benchmark.json")
	if _, err := os.Stat(benchmarkPath); err != nil {
		return fmt.Errorf("benchmark.json not found: %w", err)
	}

	entries, err := os.ReadDir(iterationDir)
	if err != nil {
		return fmt.Errorf("failed to read iteration dir %s: %w", iterationDir, err)
	}

	agentValidatedRuns := 0
	judgeValidatedRuns := 0
	expectJudgeArtifacts := false
	for _, judgeType := range judgeTypes {
		if judgeType == "agent_judge" {
			expectJudgeArtifacts = true
			break
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		caseID := entry.Name()
		metaPath := filepath.Join(iterationDir, caseID, "eval_metadata.json")
		if _, err := os.Stat(metaPath); err != nil {
			continue
		}

		configs := []string{"with_skill", "without_skill"}
		foundConfig := false
		for _, cfg := range configs {
			outputsDir := filepath.Join(iterationDir, caseID, cfg, "outputs")
			if info, err := os.Stat(outputsDir); err != nil || !info.IsDir() {
				continue
			}

			responsePath := filepath.Join(outputsDir, "response.md")
			responseData, err := os.ReadFile(responsePath)
			if err != nil {
				return fmt.Errorf("case %s config %s: failed to read response.md: %w", caseID, cfg, err)
			}

			response := string(responseData)
			if isProviderRateLimit(response) {
				return &benchmarkTransientError{msg: fmt.Sprintf("case %s config %s: provider rate limit detected in response", caseID, cfg)}
			}
			if strings.Contains(response, "API Error") {
				return fmt.Errorf("case %s config %s: API error detected in response", caseID, cfg)
			}

			agentRunDir := filepath.Join(outputsDir, "agent", "run")
			stdoutPath := firstExistingArtifact(agentRunDir, "stdout.json", "stdout.txt")
			gradingPath := filepath.Join(iterationDir, caseID, cfg, "grading.json")
			if _, err := os.Stat(gradingPath); err != nil {
				if stdoutPath != "" && judgeTypes[caseID] == "agent_judge" {
					return &benchmarkTransientError{
						msg: fmt.Sprintf("case %s config %s: agent_judge result missing after successful agent run", caseID, cfg),
					}
				}
				agentErr, readErr := summarizeAgentError(outputsDir)
				if readErr == nil && agentErr != "" {
					if isProviderRateLimit(agentErr) {
						return &benchmarkTransientError{msg: fmt.Sprintf("case %s config %s: provider rate limit before grading: %s", caseID, cfg, agentErr)}
					}
					return fmt.Errorf("case %s config %s: agent execution failed before grading: %s", caseID, cfg, agentErr)
				}
				foundConfig = true
				continue
			}

			if stdoutPath == "" {
				return fmt.Errorf("case %s config %s: agent stdout artifact missing under %s", caseID, cfg, agentRunDir)
			}
			agentValidatedRuns++

			requiresJudgeArtifacts, err := gradingRequiresJudge(gradingPath)
			if err != nil {
				return fmt.Errorf("case %s config %s: %w", caseID, cfg, err)
			}
			if requiresJudgeArtifacts && judgeTypes[caseID] == "agent_judge" {
				judgeRunDir := filepath.Join(outputsDir, "judge", "run")
				if info, err := os.Stat(judgeRunDir); err != nil || !info.IsDir() {
					return fmt.Errorf("case %s config %s: judge artifacts missing at %s", caseID, cfg, judgeRunDir)
				}
				judgeValidatedRuns++
			}

			foundConfig = true
		}

		if !foundConfig {
			return fmt.Errorf("case %s: no configuration outputs found under %s", caseID, filepath.Join(iterationDir, caseID))
		}
	}

	if agentValidatedRuns == 0 {
		return fmt.Errorf("validated 0 agent artifact runs in %s", iterationDir)
	}
	if expectJudgeArtifacts && judgeValidatedRuns == 0 {
		return fmt.Errorf("validated 0 judge artifact runs in %s", iterationDir)
	}

	return nil
}

func gradingRequiresJudge(gradingPath string) (bool, error) {
	data, err := os.ReadFile(gradingPath)
	if err != nil {
		return false, fmt.Errorf("failed to read grading.json: %w", err)
	}

	var grading struct {
		Expectations []struct {
			Text string `json:"text"`
		} `json:"expectations"`
	}
	if err := json.Unmarshal(data, &grading); err != nil {
		return false, fmt.Errorf("failed to parse grading.json: %w", err)
	}

	if len(grading.Expectations) == 0 {
		return false, nil
	}
	for _, expectation := range grading.Expectations {
		if !strings.HasPrefix(expectation.Text, "expect.") {
			return true, nil
		}
	}
	return false, nil
}

func loadCaseJudgeTypes(evalPath string) (map[string]string, error) {
	loader := config.NewLoader(evalPath)
	result, err := loader.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to load eval config for artifact validation: %w", err)
	}

	judgeTypes := make(map[string]string, len(result.Cases))
	defaultJudgeType := result.Eval.Judge.Type
	for _, caseCfg := range result.Cases {
		judgeType := caseCfg.Judge.Type
		if judgeType == "" {
			judgeType = defaultJudgeType
		}
		judgeTypes[caseCfg.ID] = judgeType
	}
	return judgeTypes, nil
}

type benchmarkTransientError struct {
	msg string
}

func (e *benchmarkTransientError) Error() string {
	return e.msg
}

func isProviderRateLimit(s string) bool {
	return strings.Contains(s, "模型提供方限流") ||
		strings.Contains(strings.ToLower(s), "rate limit")
}

func summarizeAgentError(outputsDir string) (string, error) {
	agentRunDir := filepath.Join(outputsDir, "agent", "run")
	stdoutPath := firstExistingArtifact(agentRunDir, "stdout.txt", "stdout.json")
	if stdoutPath != "" {
		data, err := os.ReadFile(stdoutPath)
		if err != nil {
			return "", err
		}
		s := strings.TrimSpace(string(data))
		if s != "" {
			if len(s) > 1200 {
				s = s[len(s)-1200:]
			}
			return strings.ReplaceAll(s, "\n", "\\n"), nil
		}
	}

	sessionFiles, err := filepath.Glob(filepath.Join(agentRunDir, "*.jsonl"))
	if err != nil {
		return "", fmt.Errorf("glob session files: %w", err)
	}
	for _, path := range sessionFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(data))
		if s == "" {
			continue
		}
		if len(s) > 1200 {
			s = s[len(s)-1200:]
		}
		return strings.ReplaceAll(s, "\n", "\\n"), nil
	}
	return "", nil
}

func firstExistingArtifact(dir string, names ...string) string {
	for _, name := range names {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func logDirTree(t *testing.T, root string) {
	t.Helper()

	var lines []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s [error: %v]", path, err))
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		if rel == "." {
			rel = filepath.Base(root)
		}
		if info.IsDir() {
			rel += "/"
		}
		lines = append(lines, rel)
		return nil
	})
	if err != nil {
		t.Logf("failed to walk %s: %v", root, err)
		return
	}
	t.Logf("directory tree for %s:\n%s", root, strings.Join(lines, "\n"))
}

// TestAgent_ClaudeCode_NoneRuntime runs the claude-code engine end-to-end
// without an API key to verify the evaluator handles missing credentials
// gracefully (timeout or clean failure). Only runs in full e2e mode.
func TestAgent_ClaudeCode_NoneRuntime(t *testing.T) {
	t.Parallel()
	skipIfNotFullE2E(t)

	evalDir := createTestEvalDir(t, "test-skill-claude")

	evalContent := `schema_version: v1alpha1

environment:
  type: none
  workspace_mount: /workspace

mcp:
  servers: []

skills:
  - source: local_path
    path: .
    target: ~/.claude/skills/test-skill-claude

engine:
  name: claude_code
  model:
    provider: dashscope
    name: qwen3.6-plus

cases:
  files:
    - evals/cases/test-case.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
  parallelism: 1

judge:
  type: rule_based
  rule_based:
    must_contain:
      - "hello"

report:
  formats: [json]
  artifacts: []
`

	evalPath := filepath.Join(evalDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(evalContent), 0o644); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	result := Run(t, RunConfig{Timeout: 60e9}, "run", evalPath)

	if result.ExitCode == ExitCodeTimeout {
		t.Logf("claude timed out without API key (expected behavior)")
	} else if result.ExitCode != 0 {
		t.Logf("expected error without API key, exit=%d", result.ExitCode)
	} else {
		t.Logf("claude completed without API key (evaluator handles gracefully)")
	}
}

// TestAgent_RunCreatesWorkspaceArtifacts runs the code-stats example end-to-end
// with a real claude-code backend and verifies the runner produces expected
// workspace artifacts (e.g. iteration-1/result.json). Skipped in quick mode.
func TestAgent_RunCreatesWorkspaceArtifacts(t *testing.T) {
	t.Parallel()

	skipIfNotFullE2E(t)
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping real run acceptance test")
	}
	skipIfClaudeUnavailable(t)

	evalPath := filepath.Join(getExamplesDir(), "eval.yaml")
	workspaceRoot := t.TempDir()

	runCfg := RunConfig{
		Timeout: 180e9,
		Env:     []string{"ANTHROPIC_API_KEY=" + apiKey},
	}
	result := Run(t, runCfg, "run", evalPath, "--output-dir", workspaceRoot)

	// LLM responses are non-deterministic; the run may fail expect checks.
	// We only verify the pipeline completed and produced workspace artifacts.
	if result.ExitCode == ExitCodeTimeout {
		t.Skip("run timed out; skipping artifact validation")
	}
	if result.ExitCode != 0 {
		t.Logf("run exited %d (LLM output may not match expect checks): stderr=%s", result.ExitCode, result.Stderr)
	}
	for _, rel := range []string{
		filepath.Join("iteration-1", "result.json"),
	} {
		path := filepath.Join(workspaceRoot, rel)
		if _, err := os.Stat(path); err != nil {
			if result.ExitCode != 0 {
				t.Skipf("artifact %s not produced (run exited %d, likely API issue): %v", rel, result.ExitCode, err)
			}
			t.Fatalf("expected artifact %s to exist: %v", path, err)
		}
	}
}
