package platform

import (
	"context"
	"strings"
	"testing"
)

func TestHost(t *testing.T) {
	shell := Host()
	if err := shell.Target.Validate(); err != nil {
		t.Fatalf("Host returned an invalid target shell: %v", err)
	}
	if shell.Cmd == nil {
		t.Fatal("Host returned a HostShell with nil Cmd")
	}
	cmd := shell.Cmd(context.Background(), "echo hi")
	if cmd == nil {
		t.Fatal("HostShell.Cmd returned nil")
	}
	if cmd.Path == "" {
		t.Fatal("HostShell.Cmd produced a command with no executable path")
	}
	// On POSIX hosts bash should be discoverable (PATH has it); IsBash
	// being false would point at a misconfigured runner.
	if !shell.Target.IsBash() {
		t.Logf("note: HostShell.Target.IsBash is false (no bash discovered); the fallback is exercised")
	}
}

func TestShellValidate(t *testing.T) {
	tests := []struct {
		name    string
		shell   Shell
		wantErr string
	}{
		{name: "linux posix", shell: Shell{GOOS: GOOSLinux, Family: ShellPOSIX}},
		{name: "windows bash", shell: Shell{GOOS: GOOSWindows, Family: ShellPOSIX, BashPath: `C:\Program Files\Git\bin\bash.exe`}},
		{name: "windows cmd", shell: Shell{GOOS: GOOSWindows, Family: ShellCmd}},
		{name: "empty goos", shell: Shell{Family: ShellPOSIX}, wantErr: "GOOS must not be empty"},
		{name: "empty family", shell: Shell{GOOS: GOOSLinux}, wantErr: "family must not be empty"},
		{name: "unknown family", shell: Shell{GOOS: GOOSLinux, Family: ShellFamily("fish")}, wantErr: `unsupported shell family "fish"`},
		{name: "cmd on linux", shell: Shell{GOOS: GOOSLinux, Family: ShellCmd}, wantErr: "cmd shell requires windows"},
		{name: "windows posix without bash", shell: Shell{GOOS: GOOSWindows, Family: ShellPOSIX}, wantErr: "requires BashPath"},
		{name: "cmd with bash path", shell: Shell{GOOS: GOOSWindows, Family: ShellCmd, BashPath: `C:\bash.exe`}, wantErr: "cmd shell must not set BashPath"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.shell.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestShellQuoter(t *testing.T) {
	tests := []struct {
		name  string
		shell Shell
		input string
		want  string
	}{
		{
			name:  "posix",
			shell: Shell{GOOS: GOOSLinux, Family: ShellPOSIX},
			input: "a'b",
			want:  `'a'\''b'`,
		},
		{
			name:  "windows bash",
			shell: Shell{GOOS: GOOSWindows, Family: ShellPOSIX, BashPath: `C:\Program Files\Git\bin\bash.exe`},
			input: `C:\tmp\$VAR\script.sh`,
			want:  `"C:\\tmp\\\$VAR\\script.sh"`,
		},
		{
			name:  "windows cmd",
			shell: Shell{GOOS: GOOSWindows, Family: ShellCmd},
			input: `C:\tmp\$VAR\script.cmd`,
			want:  `"C:\tmp\$VAR\script.cmd"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quote, err := tt.shell.Quoter()
			if err != nil {
				t.Fatalf("Quoter() error = %v", err)
			}
			if got := quote(tt.input); got != tt.want {
				t.Fatalf("quote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
