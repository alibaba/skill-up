package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

func newCustomTestRuntime(t *testing.T) *runtime.NoneRuntime {
	t.Helper()
	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func customLocalAgent(custom *config.CustomEngineConfig) *CustomAgent {
	return NewCustomAgent(Config{Name: "my-agent", Custom: custom})
}

func userMessages() []transcript.Message {
	return []transcript.Message{{Role: transcript.RoleUser, Content: "review the diff"}}
}

func TestCustomAgent_RunLocal_StdoutSessionResult(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":0,"final_message":"done","turns":2}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{CaseID: "c1", Variant: "with_skill"}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 || res.FinalMessage != "done" || res.Turns != 2 {
		t.Fatalf("unexpected result: %#v", res)
	}
	if res.Engine != "my-agent" {
		t.Errorf("engine = %q, want my-agent", res.Engine)
	}
}

func TestCustomAgent_RunLocal_OutputFile(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command:    "sh",
			Args:       []string{"-c", `mkdir -p "$(dirname '${output_file}')" && echo '{"exit_code":0,"final_message":"from-file"}' > '${output_file}'`},
			OutputFile: "${output_file}",
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "from-file" {
		t.Fatalf("final_message = %q, want from-file", res.FinalMessage)
	}
}

func TestCustomAgent_RunLocal_RelativeCwd(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// "inputs" is created under the workspace when the session input is
	// written; a relative cwd must resolve against the workspace.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Cwd:     "inputs",
			Args:    []string{"-c", `printf '{"exit_code":0,"final_message":"%s"}' "$(pwd)"`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasSuffix(res.FinalMessage, "/inputs") {
		t.Fatalf("cwd = %q, want it resolved under the workspace inputs/ dir", res.FinalMessage)
	}
}

func TestCustomAgent_InstallMCP_NoopWithServers(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local:     &config.CustomLocalConfig{Command: "/opt/agent"},
	})

	// CLIAgent.InstallMCP would error here (empty InstallMCPCmd + servers);
	// CustomAgent overrides it to a no-op.
	err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{
		Servers: []runtime.MCPServerConfig{{Name: "demo", Mode: "mocked"}},
	})
	if err != nil {
		t.Fatalf("InstallMCP: %v", err)
	}
}

//nolint:dupl // distinct scenario from InputFileEqualsOutputFile (relative cwd vs path collision)
func TestCustomAgent_RunLocal_RelativeOutputFileWithCwd(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// A relative output_file combined with a non-default cwd must still be
	// resolved against the workspace, so readRawResult finds the file.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command:    "sh",
			Cwd:        "inputs",
			OutputFile: "result.json",
			Args:       []string{"-c", `echo '{"exit_code":0,"final_message":"rel-file"}' > '${output_file}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "rel-file" {
		t.Fatalf("final_message = %q, want rel-file (result read from the relative output file)", res.FinalMessage)
	}
}

func TestCustomAgent_RunLocal_TextFormat(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport:      "local",
		ResponseFormat: "text",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", "echo plain output text"},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "plain output text" {
		t.Fatalf("final_message = %q, want plain output text", res.FinalMessage)
	}
}

func TestCustomAgent_RunLocal_IgnoresStaleOutputFile(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// Pre-seed a stale default output file, as a fixture or setup step might.
	stale := filepath.Join(rt.Workspace(), "outputs", "session-result.json")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte(`{"exit_code":0,"final_message":"STALE"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// The command returns its result on stdout and never writes the file.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":0,"final_message":"fresh-stdout"}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "fresh-stdout" {
		t.Fatalf("final_message = %q, want fresh-stdout (stale output file must be ignored)", res.FinalMessage)
	}
	// output_file was not configured, so the default path must be left
	// untouched — it may be ordinary fixture input.
	if _, statErr := os.Stat(stale); statErr != nil {
		t.Fatalf("default output file was removed though output_file is unset: %v", statErr)
	}
}

func TestCustomAgent_RunLocal_KwargsInOutputFilePath(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// output_file references a kwarg; kwargs must be resolved before the I/O
	// path templates so ${kwargs.profile} expands rather than vanishing.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Kwargs:    map[string]string{"profile": "rep"},
		Local: &config.CustomLocalConfig{
			Command:    "sh",
			OutputFile: "outputs/${kwargs.profile}.json",
			Args:       []string{"-c", `mkdir -p "$(dirname '${output_file}')" && echo '{"exit_code":0,"final_message":"kwarg-path"}' > '${output_file}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "kwarg-path" {
		t.Fatalf("final_message = %q, want kwarg-path (kwargs must resolve in output_file)", res.FinalMessage)
	}
}

func TestCustomAgent_RunLocal_ClearedStaleOutputRegistered(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// With an explicitly configured output_file, a stale file from a previous
	// run is cleared before the run; that path must be registered so the
	// deletion is excluded from workspace diffs even when the engine returns
	// its result on stdout and never recreates the file.
	stale := filepath.Join(rt.Workspace(), "result.json")
	if err := os.WriteFile(stale, []byte(`{"exit_code":0,"final_message":"STALE"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command:    "sh",
			OutputFile: "result.json",
			Args:       []string{"-c", `echo '{"exit_code":0,"final_message":"fresh"}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsBasename(res.Artifacts.GeneratedFiles, "result.json") {
		t.Fatalf("generated_files = %v, want the cleared output path registered", res.Artifacts.GeneratedFiles)
	}
}

func TestCustomAgent_RunLocal_TextIgnoresOutputFile(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// A text engine prints the answer on stdout but also writes a bookkeeping
	// file at the default ${output_file} path; stdout must be graded.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport:      "local",
		ResponseFormat: "text",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `mkdir -p outputs && echo bookkeeping > outputs/session-result.json && echo stdout-answer`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "stdout-answer" {
		t.Fatalf("final_message = %q, want stdout-answer (output file must not be graded)", res.FinalMessage)
	}
}

func TestCustomAgent_RunLocal_NonZeroExitCodePreserved(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":3,"final_message":"boom","stderr":"bad"}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil {
		t.Fatal("expected error for non-zero exit_code")
	}
	if res == nil || res.ExitCode != 3 || res.FinalMessage != "boom" {
		t.Fatalf("session result not preserved: %#v", res)
	}
}

func TestCustomAgent_RunLocal_NonZeroExitWithoutJSON(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// The command crashes (non-zero exit) without ever emitting JSON.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo boom-stderr >&2; exit 42`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "exited 42") {
		t.Fatalf("error = %v, want the real command exit surfaced", err)
	}
	if res.ExitCode != 42 {
		t.Fatalf("res.ExitCode = %d, want 42", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "boom-stderr") {
		t.Fatalf("res.Stderr = %q, want the command stderr preserved", res.Stderr)
	}
}

func TestCustomAgent_RunLocal_UnparseableResult(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", "echo not-json-at-all"},
		},
	})

	_, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("error = %v, want JSON parse error", err)
	}
}

func TestCustomAgent_RunLocal_MissingExitCode(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"final_message":"hi"}'`},
		},
	})

	_, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "exit_code") {
		t.Fatalf("error = %v, want missing exit_code error", err)
	}
}

func TestCustomAgent_RunLocal_NonZeroProcessExitFails(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// The JSON reports success but the process crashes afterwards.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":0,"final_message":"ok"}'; exit 7`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "exited 7") {
		t.Fatalf("error = %v, want non-zero process exit error", err)
	}
	if res == nil {
		t.Fatal("expected session result to be preserved")
	}
	// The real process exit code must surface in the result, not the JSON's 0.
	if res.ExitCode != 7 {
		t.Fatalf("res.ExitCode = %d, want 7 (the process exit code)", res.ExitCode)
	}
}

func TestCustomAgent_RunLocal_NilLocalConfigNoPanic(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// transport: local with no local block (can slip past validation via a
	// --engine override) must error, not panic.
	ag := customLocalAgent(&config.CustomEngineConfig{Transport: "local"})

	_, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "local.command is required") {
		t.Fatalf("error = %v, want local.command required error", err)
	}
}

func TestCustomAgent_RunLocal_FallbackTranscript(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// The engine returns final_message but no transcript.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":0,"final_message":"the answer"}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Transcript) != 2 {
		t.Fatalf("transcript = %#v, want input message + assistant reply", res.Transcript)
	}
	if res.Transcript[0].Role != transcript.RoleUser ||
		res.Transcript[1].Role != transcript.RoleAssistant ||
		res.Transcript[1].Content != "the answer" {
		t.Fatalf("unexpected fallback transcript: %#v", res.Transcript)
	}
}

func TestCustomAgent_RunLocal_RejectsPathTraversalInOutputFile(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// A literal "../" escape in output_file would otherwise let the pre-run
	// cleanup remove arbitrary host files; workspacePath must reject it.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command:    "true",
			OutputFile: "../../etc/important",
		},
	})

	_, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "escapes the runtime workspace") {
		t.Fatalf("error = %v, want path-escape rejection", err)
	}
}

func TestCustomAgent_RunLocal_RejectsTemplateDrivenPathTraversal(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// A template-driven traversal (via a kwarg) must also be confined to the
	// workspace at resolve time.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Kwargs:    map[string]string{"target": "../../etc/important"},
		Local: &config.CustomLocalConfig{
			Command:    "true",
			OutputFile: "${kwargs.target}",
		},
	})

	_, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "escapes the runtime workspace") {
		t.Fatalf("error = %v, want template-driven path-escape rejection", err)
	}
}

func TestCustomAgent_RunLocal_RejectsAPIKeyInArgs(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := NewCustomAgent(Config{
		Name:   "my-agent",
		APIKey: "sk-super-secret",
		Custom: &config.CustomEngineConfig{
			Transport: "local",
			Local: &config.CustomLocalConfig{
				Command: "sh",
				Args:    []string{"-c", "true", "--api-key", "${api_key}"},
			},
		},
	})

	_, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "custom.env") {
		t.Fatalf("error = %v, want rejection of API key in command line", err)
	}
}

func TestCustomAgent_RunLocal_PathArtifactPreservesName(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	artifactDir := t.TempDir()
	// The engine writes a file whose basename differs from the declared name.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args: []string{"-c", `mkdir -p outputs && echo body > outputs/gen-xyz.tmp && ` +
				`echo '{"exit_code":0,"final_message":"ok","artifacts":{"files":[{"name":"report.md","path":"outputs/gen-xyz.tmp"}]}}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(artifactDir, "report.md")); statErr != nil {
		t.Fatalf("artifact not archived under declared name: %v", statErr)
	}
	if !containsBasename(res.Artifacts.GeneratedFiles, "report.md") {
		t.Fatalf("generated_files = %v, want an entry named report.md", res.Artifacts.GeneratedFiles)
	}
	// The original workspace path must also be registered so the workspace
	// diff collector excludes it.
	if !containsBasename(res.Artifacts.GeneratedFiles, "gen-xyz.tmp") {
		t.Fatalf("generated_files = %v, want the original path kept for diff exclusion", res.Artifacts.GeneratedFiles)
	}
}

// assertCustomGeneratedFile runs a local custom agent with the given shell
// args and asserts that a file with wantBasename is registered in
// GeneratedFiles (so it is excluded from workspace diffs).
func assertCustomGeneratedFile(t *testing.T, scriptArgs []string, wantBasename string) {
	t.Helper()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local:     &config.CustomLocalConfig{Command: "sh", Args: scriptArgs},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsBasename(res.Artifacts.GeneratedFiles, wantBasename) {
		t.Fatalf("generated_files = %v, want %s registered", res.Artifacts.GeneratedFiles, wantBasename)
	}
}

func TestCustomAgent_RunLocal_RegistersFrameworkInputFile(t *testing.T) {
	t.Parallel()
	// The framework-written input file must be registered so it is excluded
	// from workspace diffs.
	assertCustomGeneratedFile(t,
		[]string{"-c", `echo '{"exit_code":0,"final_message":"ok"}'`},
		"messages.json")
}

func TestCustomAgent_RunLocal_PartialResultOnTimeout(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// The engine prints a valid result, then hangs well past the timeout.
	// The 2s budget keeps the echo reliably captured even on a loaded CI.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport:      "local",
		TimeoutSeconds: 2,
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":0,"final_message":"partial answer"}'; sleep 30`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if res == nil || res.FinalMessage != "partial answer" {
		t.Fatalf("res = %#v, want the partial result preserved", res)
	}
	// An interrupted run must not report exit_code 0 even though the partial
	// JSON did, or the evaluator could treat it as a clean success.
	if res.ExitCode == 0 {
		t.Fatalf("res.ExitCode = 0, want a non-zero code for an interrupted run")
	}
}

func TestCustomAgent_RunLocal_RendersTemplatedKwargs(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// kwargs reference a built-in template variable; it must be rendered
	// before reaching ${kwargs.*}.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport:      "local",
		ResponseFormat: "text",
		Kwargs:         map[string]string{"cid": "${case_id}"},
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", "printf %s ${kwargs.cid}"},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{CaseID: "case-42"}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "case-42" {
		t.Fatalf("final_message = %q, want the rendered case_id (case-42)", res.FinalMessage)
	}
}

func TestCustomAgent_RunLocal_TimeoutEnforced(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport:      "local",
		TimeoutSeconds: 1,
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", "sleep 5"},
		},
	})

	start := time.Now()
	_, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// 1s deadline + NoneRuntime's WaitDelay grace; well under the 5s sleep.
	if elapsed > 4500*time.Millisecond {
		t.Fatalf("run took %s, want the 1s timeout to be enforced", elapsed)
	}
}

func TestCustomAgent_RunLocal_InlineArtifactRegistered(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	artifactDir := t.TempDir()
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":0,"final_message":"ok","artifacts":{"files":[{"name":"report.md","content":"hello-artifact"}]}}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsBasename(res.Artifacts.GeneratedFiles, "report.md") {
		t.Fatalf("generated_files = %v, want the inline artifact registered", res.Artifacts.GeneratedFiles)
	}
	data, readErr := os.ReadFile(filepath.Join(artifactDir, "report.md"))
	if readErr != nil || string(data) != "hello-artifact" {
		t.Fatalf("inline artifact = %q (err %v), want hello-artifact", data, readErr)
	}
}

func containsBasename(paths []string, base string) bool {
	for _, p := range paths {
		if filepath.Base(p) == base {
			return true
		}
	}
	return false
}

func TestCustomAgent_RunLocal_DefaultsTurnsFromTranscript(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// Engine returns the documented minimal SessionResult (no turns); the
	// agent must default Turns from the produced transcript.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":0,"final_message":"ok"}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Turns < 1 {
		t.Fatalf("res.Turns = %d, want it defaulted from the transcript (>=1)", res.Turns)
	}
}

func TestCustomAgent_RunLocal_DerivesTurnsFromTranscriptWithoutTurnField(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// Engine supplies a transcript whose messages omit the `turn` field
	// (matches the design's transcript example). Turns must still be > 0.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":0,"final_message":"answer","transcript":[{"role":"user","content":"q1"},{"role":"assistant","content":"a1"},{"role":"user","content":"q2"},{"role":"assistant","content":"answer"}]}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Two assistant replies → 2 turns inferred from conversation structure.
	if res.Turns != 2 {
		t.Fatalf("res.Turns = %d, want 2 (one per assistant reply)", res.Turns)
	}
}

func TestCustomAgent_RunLocal_DerivesFinalMessageFromTranscript(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// Engine omits final_message but provides a transcript; the agent must
	// derive final_message from the last assistant reply so judges and
	// reports do not grade or display a blank answer.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":0,"transcript":[{"role":"user","content":"q"},{"role":"assistant","content":"derived-answer"}]}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "derived-answer" {
		t.Fatalf("final_message = %q, want it derived from the transcript", res.FinalMessage)
	}
}

func TestCustomAgent_RunLocal_PreservesResultOnWaitDelay(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// The parent prints a valid SessionResult and exits 0, but a backgrounded
	// child keeps stdout open past NoneRuntime's WaitDelay. classifyExecError
	// now treats a bare exec.ErrWaitDelay as a clean exit 0 (the process
	// itself completed; only the pipe lingered), so the agent surfaces a
	// successful result rather than a hard exec error — otherwise any
	// agent that backgrounds a child would be reported as failed.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args:    []string{"-c", `echo '{"exit_code":0,"final_message":"recovered"}'; (sleep 30 &); exit 0`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v (WaitDelay should classify as a clean exit 0)", err)
	}
	if res == nil || res.FinalMessage != "recovered" {
		t.Fatalf("res = %#v, want the result preserved across WaitDelay", res)
	}
}

//nolint:dupl // distinct scenario: input/output path collision, asserted via a different command shape than RelativeOutputFileWithCwd
func TestCustomAgent_RunLocal_InputFileEqualsOutputFile(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// Both paths resolve to the same file. The framework must not delete the
	// SessionInput it just wrote, so the command can still read it before
	// overwriting it with its own SessionResult.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local: &config.CustomLocalConfig{
			Command:    "sh",
			InputFile:  "io.json",
			OutputFile: "io.json",
			Args: []string{"-c", `set -e
test -s '${input_file}' || { echo "input was deleted" >&2; exit 1; }
echo '{"exit_code":0,"final_message":"same-path-ok"}' > '${output_file}'`},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "same-path-ok" {
		t.Fatalf("final_message = %q, want same-path-ok (input must survive when paths collide)", res.FinalMessage)
	}
}

func TestCustomAgent_RunHTTP_NotImplemented(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "http",
		HTTP:      &config.CustomHTTPConfig{URL: "https://example.com"},
	})

	_, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "not yet implemented") {
		t.Fatalf("error = %v, want not-implemented error", err)
	}
}

func TestRenderTemplate(t *testing.T) {
	t.Setenv("RT_TEST_ENV", "env-value")

	vars := map[string]string{
		"workspace":      "/ws",
		"api_key":        "sk-secret",
		"kwargs.profile": "strict",
	}
	tests := []struct {
		in   string
		want string
	}{
		{"${workspace}/run", "/ws/run"},
		{"key=${api_key}", "key=sk-secret"},
		{"profile=${kwargs.profile}", "profile=strict"},
		{"missing kwarg=${kwargs.absent}", "missing kwarg="},
		{"${RT_TEST_ENV}", "env-value"},
		{"${UNSET_VAR:-fallback}", "fallback"},
		{"plain", "plain"},
	}
	for _, tc := range tests {
		got, err := renderTemplate(tc.in, vars)
		if err != nil {
			t.Errorf("renderTemplate(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("renderTemplate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderTemplate_EmptyBuiltinHonorsDefaultAndError(t *testing.T) {
	t.Parallel()
	// Built-in vars present but empty (e.g. unconfigured api_key / model).
	vars := map[string]string{"api_key": "", "model": ""}

	got, err := renderTemplate("model=${model:-gpt-fallback}", vars)
	if err != nil || got != "model=gpt-fallback" {
		t.Fatalf("renderTemplate default = %q (err %v), want model=gpt-fallback", got, err)
	}

	if _, err := renderTemplate("${api_key?api key required}", vars); err == nil ||
		!strings.Contains(err.Error(), "api key required") {
		t.Fatalf("error = %v, want required-form error for empty api_key", err)
	}

	// A plain reference to an empty built-in still resolves to empty.
	if got, err := renderTemplate("[${model}]", vars); err != nil || got != "[]" {
		t.Fatalf("renderTemplate plain = %q (err %v), want []", got, err)
	}
}

func TestRenderTemplate_UnresolvedErrors(t *testing.T) {
	t.Parallel()
	if _, err := renderTemplate("${NOPE}", map[string]string{}); err == nil {
		t.Error("expected error for unresolved variable")
	}
	if _, err := renderTemplate("${NOPE?need it}", map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "need it") {
		t.Errorf("expected custom error message, got %v", err)
	}
}

func TestDetectAgent_CustomDispatch(t *testing.T) {
	t.Parallel()
	custom := &config.CustomEngineConfig{
		Transport: "local",
		Local:     &config.CustomLocalConfig{Command: "/opt/agent"},
	}
	ag, err := DetectAgent("my-agent", Config{Name: "my-agent", Custom: custom})
	if err != nil {
		t.Fatalf("DetectAgent: %v", err)
	}
	if _, ok := ag.(*CustomAgent); !ok {
		t.Fatalf("agent type = %T, want *CustomAgent", ag)
	}
}

func TestDetectAgent_NonBuiltinWithoutCustom(t *testing.T) {
	t.Parallel()
	_, err := DetectAgent("my-agent", Config{Name: "my-agent"})
	if err == nil || !strings.Contains(err.Error(), "missing engine.custom") {
		t.Fatalf("error = %v, want missing engine.custom", err)
	}
}

func TestDetectAgentWithInitParams_KeepsAutoModelForCustom(t *testing.T) {
	t.Parallel()
	custom := &config.CustomEngineConfig{
		Transport: "local",
		Local:     &config.CustomLocalConfig{Command: "/opt/agent"},
	}
	ag, err := DetectAgentWithInitParams("my-agent", credential.AgentInitParams{
		Model:  modelAuto,
		Custom: custom,
	}, nil)
	if err != nil {
		t.Fatalf("DetectAgentWithInitParams: %v", err)
	}
	ca, ok := ag.(*CustomAgent)
	if !ok {
		t.Fatalf("agent type = %T, want *CustomAgent", ag)
	}
	// "auto" must not be stripped for a custom engine.
	if ca.Cfg.ModelName != modelAuto {
		t.Fatalf("ModelName = %q, want auto preserved for custom engine", ca.Cfg.ModelName)
	}
}

func TestValidateArtifactFiles_RejectsOversizedInlineContent(t *testing.T) {
	t.Parallel()
	files := []ArtifactFile{{
		Name:    "huge.bin",
		Content: strings.Repeat("x", maxInlineArtifactBytes+1),
	}}
	err := validateArtifactFiles(files)
	if err == nil || !strings.Contains(err.Error(), "per-file limit") {
		t.Fatalf("error = %v, want per-file size cap rejection", err)
	}
}

func TestValidateArtifactFiles_RejectsOversizedTotalInline(t *testing.T) {
	t.Parallel()
	// Five 50MB files = 250MB > 200MB total cap.
	chunk := strings.Repeat("y", maxInlineArtifactBytes)
	files := make([]ArtifactFile, 5)
	for i := range files {
		files[i] = ArtifactFile{Name: "f.bin", Content: chunk}
	}
	err := validateArtifactFiles(files)
	if err == nil || !strings.Contains(err.Error(), "total inline content size") {
		t.Fatalf("error = %v, want total size cap rejection", err)
	}
}

func TestValidateArtifactFiles_AllowsSmallInlineContent(t *testing.T) {
	t.Parallel()
	files := []ArtifactFile{
		{Name: "a.txt", Content: "hello"},
		{Name: "b.txt", ContentBase64: "aGVsbG8="},
	}
	if err := validateArtifactFiles(files); err != nil {
		t.Fatalf("validateArtifactFiles: %v", err)
	}
}

func TestCustomAgent_MaskAPIKey_InStderr(t *testing.T) {
	t.Parallel()
	a := &CustomAgent{BaseAgent: BaseAgent{Cfg: Config{Name: "x", APIKey: "sk-real-secret-token"}}} //nolint:gosec // fake credential fixture
	got := a.maskAPIKey("auth error: token sk-real-secret-token rejected")
	if strings.Contains(got, "sk-real-secret-token") {
		t.Fatalf("maskAPIKey leaked the key: %q", got)
	}
	if !strings.Contains(got, "***REDACTED***") {
		t.Fatalf("maskAPIKey output missing redaction marker: %q", got)
	}
	if a.maskAPIKey("") != "" {
		t.Fatalf("empty input must stay empty")
	}
	// Empty APIKey is a no-op.
	a2 := &CustomAgent{BaseAgent: BaseAgent{Cfg: Config{Name: "x"}}}
	if got := a2.maskAPIKey("plain text"); got != "plain text" {
		t.Fatalf("no-key mask changed string: %q", got)
	}
}

func TestCustomAgent_RunLocal_CustomTimeoutClampedToCaseTimeout(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	// Engine declares 600s but the case only grants 5s. The agent must see
	// the smaller value in ${timeout_seconds} so it doesn't think it has more
	// budget than the outer case context actually allows.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport:      "local",
		TimeoutSeconds: 600,
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args: []string{
				"-c",
				`printf '{"exit_code":0,"final_message":"%s"}' '${timeout_seconds}'`,
			},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{TimeoutSec: 5}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "5" {
		t.Fatalf("final_message = %q, want %q (case timeout must clamp engine.custom.timeout_seconds)", res.FinalMessage, "5")
	}
}

func TestWorkspacePath_RejectsSymlinkOutsideWorkspace(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	outside := t.TempDir()
	// Plant a symlink inside the workspace pointing outside it.
	link := filepath.Join(ws, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported on this filesystem: %v", err)
	}
	rt := &stubRuntime{workspace: ws}

	if _, err := workspacePath(rt, "escape/out.json"); err == nil {
		t.Fatalf("workspacePath accepted a symlinked path that escapes the workspace")
	}
}

func TestWorkspacePath_AllowsNonExistingLeafUnderWorkspace(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	rt := &stubRuntime{workspace: ws}
	// Output files don't exist before the run; EvalSymlinks must not require
	// the leaf to exist.
	got, err := workspacePath(rt, "outputs/result.json")
	if err != nil {
		t.Fatalf("workspacePath: %v", err)
	}
	// macOS resolves /var to /private/var; compare against the symlink-resolved
	// workspace so the prefix check is meaningful.
	resolvedWS, _ := filepath.EvalSymlinks(ws)
	if !strings.HasPrefix(got, ws) && !strings.HasPrefix(got, resolvedWS) {
		t.Fatalf("workspacePath = %q, want path under %q (or its resolved form %q)", got, ws, resolvedWS)
	}
}

// stubRuntime is a minimal Runtime implementation for workspacePath tests.
type stubRuntime struct {
	runtime.NoneRuntime

	workspace string
}

func (s *stubRuntime) Workspace() string { return s.workspace }

func TestValidateArtifactFiles_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	err := validateArtifactFiles([]ArtifactFile{{Content: "x"}})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("error = %v, want empty name rejected", err)
	}
}

func TestCustomAgent_CollectArtifacts_DropsPathOutsideWorkspace(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local:     &config.CustomLocalConfig{Command: "/opt/agent"},
	})
	// A malicious engine returns /etc/passwd via files[].path and an absolute
	// /tmp path via generated_files. Both must be dropped before the
	// evaluator copies them into the report.
	artifacts := &SessionArtifacts{
		GeneratedFiles: []string{"/etc/passwd"},
		Files: []ArtifactFile{
			{Name: "leak.txt", Path: "/etc/passwd"},
		},
	}
	ag.collectArtifacts(context.Background(), rt, ExecOptions{}, artifacts)
	for _, p := range artifacts.GeneratedFiles {
		if strings.HasPrefix(p, "/etc") || strings.HasPrefix(p, "/tmp") {
			t.Fatalf("collectArtifacts kept escaping path %q", p)
		}
	}
	_ = rt // keep ref used
}

func TestCustomAgent_CollectArtifacts_KeepsWorkspacePath(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local:     &config.CustomLocalConfig{Command: "/opt/agent"},
	})
	// A path inside the workspace must survive the filter. Create the file
	// so EvalSymlinks succeeds.
	in := filepath.Join(rt.Workspace(), "ok.txt")
	if err := os.WriteFile(in, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts := &SessionArtifacts{
		Files: []ArtifactFile{{Name: "ok.txt", Path: in}},
	}
	ag.collectArtifacts(context.Background(), rt, ExecOptions{}, artifacts)
	if len(artifacts.GeneratedFiles) == 0 {
		t.Fatalf("workspace-local path was dropped: %+v", artifacts)
	}
}

func TestCustomAgent_MaskAPIKey_StripsCustomEnvValues(t *testing.T) {
	t.Parallel()
	// engine.custom.env is the user-declared secret channel. A value
	// echoed back by the agent (e.g. on auth failure) must not survive
	// into SessionResult.Stderr or the rendered report.
	a := &CustomAgent{BaseAgent: BaseAgent{Cfg: Config{
		Name: "x",
		Custom: &config.CustomEngineConfig{
			Env: map[string]string{ //nolint:gosec // fake credential fixture
				"MY_TOKEN": "ghp_realtokenvalue_xxxxxxxxxxxx",
				"SHORT":    "yes", // too short, intentionally not masked to avoid collapsing flags/words
			},
		},
	}}}
	got := a.maskAPIKey("error: token ghp_realtokenvalue_xxxxxxxxxxxx invalid; flag=yes")
	if strings.Contains(got, "ghp_realtokenvalue") {
		t.Fatalf("custom.env value leaked: %q", got)
	}
	if !strings.Contains(got, "***REDACTED***") {
		t.Fatalf("missing redaction marker: %q", got)
	}
	if !strings.Contains(got, "flag=yes") {
		t.Fatalf("short value got masked too aggressively: %q", got)
	}
}

func TestCustomAgent_CollectArtifacts_MixedSafeAndEscaping(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Local:     &config.CustomLocalConfig{Command: "/opt/agent"},
	})
	// Create a real file under the workspace so the safe entry survives.
	safe := filepath.Join(rt.Workspace(), "safe.txt")
	if err := os.WriteFile(safe, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts := &SessionArtifacts{
		GeneratedFiles: []string{safe, "/etc/passwd"},
		Files: []ArtifactFile{
			{Name: "leak.txt", Path: "/etc/shadow"},
			{Name: "safe.txt", Path: safe},
		},
	}
	ag.collectArtifacts(context.Background(), rt, ExecOptions{}, artifacts)

	// Safe entries must survive; escaping entries must be dropped. Use
	// filepath.Base for the safe-suffix check so the assertion is OS-neutral
	// — on Windows the path separator is `\`, which would otherwise miss a
	// `/safe.txt` suffix match.
	hasSafe := false
	for _, p := range artifacts.GeneratedFiles {
		if strings.HasPrefix(p, "/etc") {
			t.Fatalf("escaping path leaked through: %q", p)
		}
		if filepath.Base(p) == "safe.txt" {
			hasSafe = true
		}
	}
	if !hasSafe {
		t.Fatalf("safe.txt was dropped along with the escaping entries: %+v", artifacts.GeneratedFiles)
	}
}

func TestCustomAgent_MasksFinalMessageAndTranscript(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	const token = "ghp_realtokenvalue_xxxxxxxxxxxx" //nolint:gosec // fake credential fixture
	// Configure custom.env so the token is recognized as a user-declared
	// secret. The agent echoes it in both final_message and the transcript;
	// neither must survive to judges or the report.
	ag := customLocalAgent(&config.CustomEngineConfig{
		Transport: "local",
		Env:       map[string]string{"MY_TOKEN": token}, //nolint:gosec // fake credential fixture
		Local: &config.CustomLocalConfig{
			Command: "sh",
			Args: []string{
				"-c",
				`cat <<EOF
{"exit_code":0,"final_message":"auth ` + token + ` ok","transcript":[{"role":"user","content":"hi"},{"role":"assistant","content":"token is ` + token + `"}]}
EOF`,
			},
		},
	})

	res, err := ag.Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(res.FinalMessage, token) {
		t.Fatalf("FinalMessage leaked the token: %q", res.FinalMessage)
	}
	for i, m := range res.Transcript {
		if strings.Contains(m.Content, token) {
			t.Fatalf("Transcript[%d].Content leaked the token: %q", i, m.Content)
		}
	}
}
