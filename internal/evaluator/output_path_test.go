package evaluator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOutputDirectoryGuardsRejectEscapingCaseID(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outputDir := filepath.Join(root, "outputs")
	victimDir := filepath.Join(root, "victim")
	victimFile := filepath.Join(victimDir, "keep.txt")
	if err := os.MkdirAll(victimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victimFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestEvaluator(EvalOptions{OutputDir: outputDir})
	caseID := "../../victim"

	if got := e.prepareOutputDir(context.Background(), "with_skill", caseID, "agent/run"); got != "" {
		t.Fatalf("prepareOutputDir returned %q for an escaping case ID", got)
	}

	callbackCalled := false
	e.downloadOutputs(context.Background(), "with_skill", caseID, "workspace", func(string) error {
		callbackCalled = true
		return nil
	})
	if callbackCalled {
		t.Fatal("downloadOutputs invoked callback for an escaping case ID")
	}

	if data, err := os.ReadFile(victimFile); err != nil || string(data) != "keep" {
		t.Fatalf("victim file changed after rejected output paths: data=%q err=%v", data, err)
	}
}

func TestDownloadArtifactsIntoDirRejectsOutsideTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	e := newTestEvaluator(EvalOptions{OutputDir: filepath.Join(root, "outputs")})
	target := filepath.Join(root, "outside")
	e.downloadArtifactsIntoDir(context.Background(), nil, "agent/run", target, nil)
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("downloadArtifactsIntoDir created outside target %s, stat err=%v", target, err)
	}
}
