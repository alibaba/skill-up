package judge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/platform"
)

var (
	testLinuxShell       = platform.Shell{GOOS: platform.GOOSLinux, Family: platform.ShellPOSIX}
	testDarwinShell      = platform.Shell{GOOS: platform.GOOSDarwin, Family: platform.ShellPOSIX}
	testWindowsCmdShell  = platform.Shell{GOOS: platform.GOOSWindows, Family: platform.ShellCmd}
	testWindowsBashShell = platform.Shell{GOOS: platform.GOOSWindows, Family: platform.ShellPOSIX, BashPath: `C:\Program Files\Git\bin\bash.exe`}
)

func TestPlanScript_POSIXTarget(t *testing.T) {
	plan, err := planScript("/skill/evals/check.sh", testLinuxShell)
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

// TestHostShellQuote verifies the quoter selected by platform.Host() per
// host OS. POSIX hosts always use QuotePOSIX (single quotes). Windows hosts
// use bash-double-quote escapes when bash is discoverable (every \ / " /
// $ / backtick doubled so bash decodes them back to the original byte) and
// plain QuoteWindows otherwise.
func TestHostShellQuote(t *testing.T) {
	target := platform.Host().Target
	quote, err := target.Quoter()
	if err != nil {
		t.Fatalf("host target Quoter: %v", err)
	}
	got := quote(`C:\tmp\$VAR\script.cmd`)
	if target.GOOS != platform.GOOSWindows {
		want := `'C:\tmp\$VAR\script.cmd'`
		if got != want {
			t.Fatalf("posix shell.Quote = %q, want %q", got, want)
		}
		return
	}
	if target.IsBash() {
		want := `"C:\\tmp\\\$VAR\\script.cmd"`
		if got != want {
			t.Fatalf("windows+bash shell.Quote = %q, want %q", got, want)
		}
	} else {
		want := `"C:\tmp\$VAR\script.cmd"`
		if got != want {
			t.Fatalf("windows+cmd shell.Quote = %q, want %q", got, want)
		}
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
		{"env -S split", "/usr/bin/env -S bash -eu", "bash", []string{"-eu"}},
		{"env -S compact", "/usr/bin/env -Sbash -eu", "bash", []string{"-eu"}},
		{"env -S compact full", "/usr/bin/env -Sbash\t-eu", "bash", []string{"-eu"}},
		{"env --split-string=", "/usr/bin/env --split-string=bash -eu", "bash", []string{"-eu"}},
		{"env -i python", "/usr/bin/env -i python3", "python3", []string{}},
		// Value-taking short flags (-u NAME, -C DIR) must consume their
		// value token so it does not get mistaken for the interpreter.
		{"env -u VAR bash", "/usr/bin/env -u FOO bash -eu", "bash", []string{"-eu"}},
		{"env -C DIR bash", "/usr/bin/env -C /tmp bash", "bash", []string{}},
		// Long-form value-taking flags in split form too (the =NAME form
		// is already handled by the generic "starts with -" skip arm).
		{"env --unset split", "/usr/bin/env --unset FOO bash -eu", "bash", []string{"-eu"}},
		{"env --chdir split", "/usr/bin/env --chdir /tmp bash", "bash", []string{}},
		// env -S with quoted bash -c "..." must preserve the quoted arg
		// as one token instead of breaking it on whitespace.
		{"env -S bash -c quoted", `/usr/bin/env -S bash -c "echo ok"`, "bash", []string{"-c", "echo ok"}},
		// env -S \_ (backslash-underscore) is the documented separator
		// for embedding a space inside the split-string body.
		{"env -S backslash space", `/usr/bin/env -S bash\_-eu`, "bash", []string{"-eu"}},
		{"env -S backslash tab", `/usr/bin/env -S bash\t-eu`, "bash", []string{"-eu"}},
		{"only env flags", "/usr/bin/env -S", "", nil},
		// GNU env -a / --argv0=ARG overrides argv[0] of the executed
		// command. -a takes a separate token; the parser must consume the
		// argv0 value so it does not get mistaken for the interpreter.
		{"env -a argv0 bash", "/usr/bin/env -a myargv0 bash -eu", "bash", []string{"-eu"}},
		// --default-signal / --ignore-signal / --block-signal are
		// optional-argument flags (`[=sig]`). Without `=sig` they take no
		// value, so the next token IS the interpreter.
		{"env --ignore-signal bash", "/usr/bin/env --ignore-signal bash -eu", "bash", []string{"-eu"}},
		{"env --default-signal bash", "/usr/bin/env --default-signal bash", "bash", []string{}},
		// GNU env accepts leading `NAME=VALUE` assignments before the
		// command. The parser must skip them and pick the first
		// non-assignment token as the interpreter.
		{"env NAME=VALUE bash", "/usr/bin/env PYTHONPATH=/opt python3 -V", "python3", []string{"-V"}},
		{"env -S NAME=VALUE bash", "/usr/bin/env -S PYTHONPATH=/opt python3 -V", "python3", []string{"-V"}},
		// `\c` in unquoted env -S body terminates tokenization; tokens
		// after it are ignored entirely.
		{"env -S backslash c terminator", `/usr/bin/env -S bash -eu\c trailing junk`, "bash", []string{"-eu"}},
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
	plan, err := planScript("/skill/evals/check.ps1", testDarwinShell)
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
		{"cmd", `C:\skill\check.cmd`, "script.cmd", "cmd /d /s /c "},
		{"bat", `C:\skill\check.bat`, "script.bat", "cmd /d /s /c "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := planWindowsScript(tt.scriptPath, testWindowsCmdShell)
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
	_, err := planWindowsScript(scriptPath, testWindowsCmdShell)
	if err == nil || !strings.Contains(err.Error(), "cannot determine interpreter") {
		t.Fatalf("expected cannot-determine-interpreter error, got: %v", err)
	}
}

// .ps1 file with a shebang that names pwsh must dispatch to pwsh.exe, not
// the legacy powershell.exe.
func TestPlanWindowsScript_PS1ShebangPwsh(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "check.ps1")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env pwsh\nWrite-Host hi\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	plan, err := planWindowsScript(scriptPath, testWindowsCmdShell)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cmd := plan.command(`C:\tmp\d\script.ps1`)
	if !strings.HasPrefix(cmd, "pwsh ") {
		t.Fatalf("expected pwsh prefix, got %q", cmd)
	}
	if strings.HasPrefix(cmd, "powershell ") {
		t.Fatalf("pwsh shebang should not route to powershell.exe: %q", cmd)
	}
}

// .ps1 file with `#!/usr/bin/env -S pwsh -NoLogo` must forward -NoLogo to
// the interpreter -- shebang options must not be silently dropped.
func TestPlanWindowsScript_PS1ShebangForwardsOpts(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "check.ps1")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env -S pwsh -NoLogo\nWrite-Host hi\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	plan, err := planWindowsScript(scriptPath, testWindowsCmdShell)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cmd := plan.command(`C:\tmp\d\script.ps1`)
	if !strings.Contains(cmd, "-NoLogo") {
		t.Fatalf("expected -NoLogo forwarded from shebang, got %q", cmd)
	}
	// Sanity: defaults still get appended.
	for _, want := range []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("expected %q in command, got %q", want, cmd)
		}
	}
}

// .ps1 file with no shebang (or a non-PowerShell shebang) must keep the
// legacy default: powershell.exe with the standard flag set.
func TestPlanWindowsScript_PS1NoShebangUsesLegacyDefault(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "check.ps1")
	if err := os.WriteFile(scriptPath, []byte("Write-Host hi\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	plan, err := planWindowsScript(scriptPath, testWindowsCmdShell)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cmd := plan.command(`C:\tmp\d\script.ps1`)
	if !strings.HasPrefix(cmd, "powershell -NoProfile -ExecutionPolicy Bypass -File ") {
		t.Fatalf("expected legacy powershell default, got %q", cmd)
	}
}

// TestPlanWindowsScript_ShellScript proves the planner consumes the injected
// target shell instead of re-discovering the shell on the test host.
func TestPlanWindowsScript_ShellScript(t *testing.T) {
	plan, err := planScript(`C:\skill\check.sh`, testWindowsBashShell)
	if err != nil {
		t.Fatalf("planScript: %v", err)
	}
	if plan.uploadName != "script.sh" {
		t.Fatalf("uploadName = %q, want \"script.sh\"", plan.uploadName)
	}
}

func TestPlanWindowsScript_ShellScriptRejectsCmdTarget(t *testing.T) {
	_, err := planScript(`C:\skill\check.sh`, testWindowsCmdShell)
	if err == nil || !strings.Contains(err.Error(), "requires a bash target shell") {
		t.Fatalf("error = %v, want bash target shell requirement", err)
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
		// On Windows the .sh runner always invokes bash; non-bash POSIX
		// shells must not get silently routed to it. Reject so the planner
		// reports "cannot determine interpreter".
		{"zsh rejected", "#!/usr/bin/env zsh\necho hi\n", ""},
		{"dash rejected", "#!/bin/dash\necho hi\n", ""},
		{"ksh rejected", "#!/usr/bin/env ksh\necho hi\n", ""},
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

func TestCleanupCommand_POSIX(t *testing.T) {
	plan, err := planScript("/skill/check.sh", testLinuxShell)
	if err != nil {
		t.Fatalf("planScript: %v", err)
	}
	if got, want := plan.cleanupCommand("/tmp/d"), "rm -rf '/tmp/d'"; got != want {
		t.Fatalf("posix cleanupCommand = %q, want %q", got, want)
	}
}

func TestCleanupCommand_Windows(t *testing.T) {
	plan, err := planWindowsScript(`C:\skill\check.ps1`, testWindowsCmdShell)
	if err != nil {
		t.Fatalf("planWindowsScript: %v", err)
	}
	got := plan.cleanupCommand(`C:\tmp\d`)
	if !strings.HasPrefix(got, "cmd /d /s /c rd /s /q ") {
		t.Fatalf("windows cleanupCommand = %q, want prefix %q", got, "cmd /d /s /c rd /s /q ")
	}
}

// .sh on Windows cleans up via bash `rm -rf` rather than dispatching to cmd,
// avoiding the bash -> cmd hop that the .ps1/.cmd/.bat paths necessarily take.
func TestCleanupCommand_Windows_ShellScriptUsesBashRm(t *testing.T) {
	plan, err := planWindowsScript(`C:\skill\check.sh`, testWindowsBashShell)
	if err != nil {
		t.Fatalf("planWindowsScript: %v", err)
	}
	got := plan.cleanupCommand(`C:\tmp\d`)
	if !strings.HasPrefix(got, "rm -rf ") {
		t.Fatalf("windows .sh cleanupCommand = %q, want prefix %q", got, "rm -rf ")
	}
	if strings.Contains(got, "cmd /") {
		t.Fatalf("windows .sh cleanupCommand should not invoke cmd, got %q", got)
	}
	// Note: filepath.ToSlash is a no-op when this test runs on a POSIX host
	// (separator is `/`, no backslashes to convert), so we cannot assert the
	// forward-slash form here. On a real Windows host the production code
	// path converts the backslashes; behaviour is exercised by Windows CI.
}

func TestJudgeTempDir(t *testing.T) {
	if d := judgeTempDir("linux"); !strings.HasPrefix(d, "/tmp/skill-up-judge-") {
		t.Fatalf("posix judgeTempDir = %q, want /tmp/skill-up-judge- prefix", d)
	}
	if d := judgeTempDir("windows"); !strings.Contains(d, "skill-up-judge-") {
		t.Fatalf("windows judgeTempDir = %q, want skill-up-judge- substring", d)
	}
}
