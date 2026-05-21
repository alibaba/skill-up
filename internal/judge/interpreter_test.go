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
	if got := plan.envPath("/tmp/d/transcript.json"); got != "/tmp/d/transcript.json" {
		t.Fatalf("POSIX envPath should be identity, got %q", got)
	}
}

func TestQuoteWindowsThroughBash(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		// No bash-active characters: identical to QuoteWindows.
		{"plain", `C:\tmp\skill-up-judge-1\script.cmd`, `"C:\tmp\skill-up-judge-1\script.cmd"`},
		// `$VAR` would otherwise be expanded by bash inside double quotes.
		{"dollar", `C:\tmp\$VAR\script.cmd`, `"C:\tmp\\$VAR\script.cmd"`},
		// Backtick triggers command substitution inside bash double quotes;
		// both backticks of a pair must be escaped (leaving only one would
		// turn the second into the start of a new, never-closed substitution).
		{"backtick", "C:\\tmp\\`cmd`\\s.cmd", "\"C:\\tmp\\\\`cmd\\`\\s.cmd\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quoteWindowsThroughBash(tt.in); got != tt.want {
				t.Fatalf("quoteWindowsThroughBash(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseShebang(t *testing.T) {
	tests := []struct {
		name, body string
		wantInt    string
		wantOpts   []string
	}{
		{"empty", "", "", nil},
		{"posix sh", "/bin/sh", "sh", []string{}},
		{"bash with opts", "/bin/bash -eu", "bash", []string{"-eu"}},
		{"env bash", "/usr/bin/env bash", "bash", []string{}},
		{"env -S bash -eu", "/usr/bin/env -S bash -eu", "bash", []string{"-eu"}},
		{"env -i python", "/usr/bin/env -i python3", "python3", []string{}},
		{"only env flags", "/usr/bin/env -S", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotInt, gotOpts := parseShebang(tt.body)
			if gotInt != tt.wantInt {
				t.Fatalf("interpreter = %q, want %q", gotInt, tt.wantInt)
			}
			if len(gotOpts) != len(tt.wantOpts) {
				t.Fatalf("opts = %v, want %v", gotOpts, tt.wantOpts)
			}
			for i := range gotOpts {
				if gotOpts[i] != tt.wantOpts[i] {
					t.Fatalf("opts[%d] = %q, want %q", i, gotOpts[i], tt.wantOpts[i])
				}
			}
		})
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
		{"cmd", `C:\skill\check.cmd`, "script.cmd", "cmd /d /c "},
		{"bat", `C:\skill\check.bat`, "script.bat", "cmd /d /c "},
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
		{"env -S bash", "#!/usr/bin/env -S bash -eu\necho hi\n", ".sh"},
		{"pwsh", "#!/usr/bin/env pwsh\nWrite-Host hi\n", ".ps1"},
		{"powershell direct", "#!/usr/local/bin/powershell\nWrite-Host hi\n", ".ps1"},
		{"no shebang", "echo hi\n", ""},
		{"empty", "", ""},
		{"unrecognized ruby", "#!/usr/bin/env ruby\nputs 1\n", ""},
		// fish, ksh-suffixed names etc. must not be misclassified as `.sh`
		// just because their name contains the letters "sh".
		{"fish not sh", "#!/usr/bin/env fish\necho hi\n", ""},
		{"python not sh", "#!/usr/bin/env python3\nprint(1)\n", ""},
		{"swish not sh", "#!/usr/local/bin/swish\n", ""},
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
	if got, want := removeDirCommand("windows", `C:\tmp\d`), `cmd /d /c rd /s /q "C:\tmp\d"`; got != want {
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
