package evaluator

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/runtime"
)

func TestResolveCollectArtifacts(t *testing.T) {
	evalCfg := &config.EvalConfig{
		Cases: config.CasesConfig{
			Defaults: config.CaseDefaults{CollectArtifacts: []string{"**/*.json", "docs/*.md", " "}},
		},
	}
	caseCfg := &config.CaseConfig{
		CollectArtifacts: []string{"docs/*.md", "out/**", ""}, // dup + new + empty
	}

	got := resolveCollectArtifacts(evalCfg, caseCfg)
	want := []string{"**/*.json", "docs/*.md", "out/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveCollectArtifacts = %v, want %v", got, want)
	}

	if got := resolveCollectArtifacts(&config.EvalConfig{}, &config.CaseConfig{}); len(got) != 0 {
		t.Fatalf("expected empty result, got %v", got)
	}
}

// newNoneRuntimeWithFiles creates a real none runtime and seeds its workspace
// with the given relative path -> content files.
func newNoneRuntimeWithFiles(t *testing.T, files map[string]string) runtime.Runtime {
	t.Helper()
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", Delete: true})
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	for rel, content := range files {
		full := filepath.Join(rt.Workspace(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return rt
}

func TestCollectGlobArtifacts_PreservesRelativePaths(t *testing.T) {
	rt := newNoneRuntimeWithFiles(t, map[string]string{
		"a/b/data.json":  `{"k":1}`,
		"docs/readme.md": "# hi",
		"top.md":         "top",
		"skip.txt":       "nope",
	})
	outputDir := t.TempDir()
	e := newTestEvaluator(EvalOptions{
		OutputDir: outputDir,
		EvalCfg: &config.EvalConfig{
			Cases: config.CasesConfig{
				Defaults: config.CaseDefaults{CollectArtifacts: []string{"**/*.json", "docs/*.md"}},
			},
		},
	})
	caseCfg := &config.CaseConfig{ID: "case-a"}

	e.collectGlobArtifacts(context.Background(), rt, "with_skill", caseCfg)

	base := filepath.Join(outputDir, "case-a", "with_skill", "outputs", "workspace")
	assertFileContent(t, filepath.Join(base, "a", "b", "data.json"), `{"k":1}`)
	assertFileContent(t, filepath.Join(base, "docs", "readme.md"), "# hi")
	assertFileAbsent(t, filepath.Join(base, "top.md"))   // *.md (single star) doesn't cross dirs
	assertFileAbsent(t, filepath.Join(base, "skip.txt")) // no matching pattern
}

func TestCollectGlobArtifacts_DownloadsEvenWhenContextCancelled(t *testing.T) {
	rt := newNoneRuntimeWithFiles(t, map[string]string{"out/result.json": "{}"})
	outputDir := t.TempDir()
	e := newTestEvaluator(EvalOptions{
		OutputDir: outputDir,
		EvalCfg: &config.EvalConfig{
			Cases: config.CasesConfig{
				Defaults: config.CaseDefaults{CollectArtifacts: []string{"out/**"}},
			},
		},
	})

	// Simulate a timed-out / cancelled case context: collection must detach and
	// still run.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	e.collectGlobArtifacts(ctx, rt, "with_skill", &config.CaseConfig{ID: "case-t"})

	assertFileContent(t, filepath.Join(outputDir, "case-t", "with_skill", "outputs", "workspace", "out", "result.json"), "{}")
}

func TestCollectGlobArtifacts_ClearsStaleArtifacts(t *testing.T) {
	outputDir := t.TempDir()
	e := newTestEvaluator(EvalOptions{
		OutputDir: outputDir,
		EvalCfg: &config.EvalConfig{
			Cases: config.CasesConfig{
				Defaults: config.CaseDefaults{CollectArtifacts: []string{"**/*.json"}},
			},
		},
	})
	caseCfg := &config.CaseConfig{ID: "case-r"}
	base := filepath.Join(outputDir, "case-r", "with_skill", "outputs", "workspace")

	// First attempt produces old.json.
	rt1 := newNoneRuntimeWithFiles(t, map[string]string{"old.json": "1"})
	e.collectGlobArtifacts(context.Background(), rt1, "with_skill", caseCfg)
	assertFileContent(t, filepath.Join(base, "old.json"), "1")

	// Second attempt (e.g. retry) produces only new.json; old.json must not linger.
	rt2 := newNoneRuntimeWithFiles(t, map[string]string{"new.json": "2"})
	e.collectGlobArtifacts(context.Background(), rt2, "with_skill", caseCfg)
	assertFileContent(t, filepath.Join(base, "new.json"), "2")
	assertFileAbsent(t, filepath.Join(base, "old.json"))
}

func TestCollectGlobArtifacts_NoPatternsIsNoop(t *testing.T) {
	rt := newNoneRuntimeWithFiles(t, map[string]string{"a.json": "{}"})
	outputDir := t.TempDir()
	e := newTestEvaluator(EvalOptions{OutputDir: outputDir, EvalCfg: &config.EvalConfig{}})

	e.collectGlobArtifacts(context.Background(), rt, "with_skill", &config.CaseConfig{ID: "case-n"})

	if _, err := os.Stat(filepath.Join(outputDir, "case-n")); !os.IsNotExist(err) {
		t.Fatalf("expected no output dir to be created, stat err=%v", err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test reads controlled temp path
	if err != nil {
		t.Fatalf("expected file %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("file %s content = %q, want %q", path, string(data), want)
	}
}

func assertFileAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file %s to be absent, stat err=%v", path, err)
	}
}
