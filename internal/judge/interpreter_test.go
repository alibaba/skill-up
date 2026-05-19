package judge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/platform"
)

func TestPlanScript_POSIXTarget(t *testing.T) {
	plan, err := planScript("/skill/evals/check.sh", "linux")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.uploadName != "script" {
		t.Fatalf("uploadName = %q, want \"script\"", plan.uploadName)
	}
	got := plan.command("/tmp/d/script")
	want := "chmod 700 '/tmp/d/script' && '/tmp/d/script'"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

// POSIX targets preserve the original behavior: the file extension is ignored
// and the script runs via its own shebang.
func TestPlanScript_POSIXTarget_IgnoresExtension(t *testing.T) {
	plan, err := planScript("/skill/evals/check.ps1", "darwin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.uploadName != "script" {
		t.Fatalf("uploadName = %q, want \"script\"", plan.uploadName)
	}
}

func TestPlanWindowsScript(t *testing.T) {
	tests := []struct {
		name        string
		scriptPath  string
		wantUpload  string
		wantCmdHead string
	}{
		{"powershell", `C:\skill\check.ps1`, "script.ps1", "powershell -NoProfile -ExecutionPolicy Bypass -File "},
		{"cmd", `C:\skill\check.cmd`, "script.cmd", "cmd /c "},
		{"bat", `C:\skill\check.bat`, "script.bat", "cmd /c "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := planWindowsScript(tt.scriptPath)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.uploadName != tt.wantUpload {
				t.Fatalf("uploadName = %q, want %q", plan.uploadName, tt.wantUpload)
			}
			if cmd := plan.command(`C:\tmp\d\` + tt.wantUpload); !strings.HasPrefix(cmd, tt.wantCmdHead) {
				t.Fatalf("command = %q, want prefix %q", cmd, tt.wantCmdHead)
			}
		})
	}
}

func TestPlanWindowsScript_UnknownInterpreter(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "mystery.txt")
	if err := os.WriteFile(scriptPath, []byte("echo hi\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := planWindowsScript(scriptPath)
	if err == nil || !strings.Contains(err.Error(), "cannot determine interpreter") {
		t.Fatalf("expected cannot-determine-interpreter error, got: %v", err)
	}
}

// TestPlanWindowsScript_ShellScript covers the .sh branch, whose outcome
// depends on whether bash is discoverable on the host running the test.
func TestPlanWindowsScript_ShellScript(t *testing.T) {
	plan, err := planWindowsScript(`C:\skill\check.sh`)
	if _, ok := platform.DiscoverBash(); ok {
		if err != nil {
			t.Fatalf("bash is available but planning failed: %v", err)
		}
		if plan.uploadName != "script.sh" {
			t.Fatalf("uploadName = %q, want \"script.sh\"", plan.uploadName)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "requires bash on Windows") {
		t.Fatalf("expected bash-required error, got: %v", err)
	}
}

func TestShebangExtension(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"posix sh", "#!/bin/sh\necho hi\n", ".sh"},
		{"env bash", "#!/usr/bin/env bash\necho hi\n", ".sh"},
		{"pwsh", "#!/usr/bin/env pwsh\nWrite-Host hi\n", ".ps1"},
		{"no shebang", "echo hi\n", ""},
		{"empty", "", ""},
		{"unrecognized", "#!/usr/bin/env ruby\nputs 1\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "script")
			if err := os.WriteFile(p, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := shebangExtension(p); got != tt.want {
				t.Fatalf("shebangExtension = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRemoveDirCommand(t *testing.T) {
	if got, want := removeDirCommand("linux", "/tmp/d"), "rm -rf '/tmp/d'"; got != want {
		t.Fatalf("posix removeDirCommand = %q, want %q", got, want)
	}
	if got, want := removeDirCommand("windows", `C:\tmp\d`), `cmd /c rd /s /q C:\tmp\d`; got != want {
		t.Fatalf("windows removeDirCommand = %q, want %q", got, want)
	}
}

func TestJudgeTempDir(t *testing.T) {
	if d := judgeTempDir("linux"); !strings.HasPrefix(d, "/tmp/skill-up-judge-") {
		t.Fatalf("posix judgeTempDir = %q, want /tmp/skill-up-judge- prefix", d)
	}
	if d := judgeTempDir("windows"); !strings.Contains(d, "skill-up-judge-") {
		t.Fatalf("windows judgeTempDir = %q, want skill-up-judge- substring", d)
	}
}
