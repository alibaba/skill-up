// Package platform centralizes OS-conditional process, shell, and executable
// discovery so the rest of skill-up does not scatter runtime.GOOS branches
// across packages.
package platform

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/alibaba/skill-up/internal/shellquote"
)

// BashEnvOverride is the environment variable a user may set to point at a
// specific bash interpreter, taking precedence over PATH and well-known
// install locations.
const BashEnvOverride = "SKILL_UP_BASH"

// GOOS-value constants that callers across packages compare against
// runtime.GOOS / Runtime.Shell(). Centralizing them avoids string
// literals duplicated across the OS-dispatch sites.
const (
	GOOSWindows = "windows"
	GOOSLinux   = "linux"
	GOOSDarwin  = "darwin"
)

// ShellFamily identifies the command language understood by a runtime's
// outer command interpreter. It deliberately describes syntax rather than a
// concrete executable: remote runtimes may choose bash or sh dynamically.
type ShellFamily string

const (
	// ShellPOSIX is the sh-compatible command language used by POSIX shells.
	ShellPOSIX ShellFamily = "posix"
	// ShellCmd is the Windows cmd.exe command language.
	ShellCmd ShellFamily = "cmd"
)

// Shell describes the target command interpreter used by a runtime's Exec
// method. GOOS is the target environment, not necessarily the skill-up host.
type Shell struct {
	GOOS   string
	Family ShellFamily
	// BashPath is set when a Windows POSIX target explicitly invokes bash.
	// POSIX targets may leave it empty when the runtime owns shell selection.
	BashPath string
}

// Validate rejects incomplete or contradictory target-shell descriptors.
func (s Shell) Validate() error {
	if s.GOOS == "" {
		return errors.New("shell GOOS must not be empty")
	}
	switch s.Family {
	case ShellPOSIX:
		if s.GOOS == GOOSWindows && s.BashPath == "" {
			return errors.New("windows POSIX shell requires BashPath")
		}
	case ShellCmd:
		if s.GOOS != GOOSWindows {
			return fmt.Errorf("cmd shell requires windows GOOS, got %q", s.GOOS)
		}
		if s.BashPath != "" {
			return errors.New("cmd shell must not set BashPath")
		}
	case "":
		return errors.New("shell family must not be empty")
	default:
		return fmt.Errorf("unsupported shell family %q", s.Family)
	}
	return nil
}

// Quoter returns the argument quoting function matching the target shell.
func (s Shell) Quoter() (func(string) string, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if s.Family == ShellCmd {
		return shellquote.QuoteWindows, nil
	}
	if s.GOOS == GOOSWindows {
		return quoteForBashDoubleQuote, nil
	}
	return shellquote.QuotePOSIX, nil
}

// IsBash reports whether the descriptor identifies an explicit bash target.
func (s Shell) IsBash() bool {
	return s.Family == ShellPOSIX && s.BashPath != ""
}

// HostShell describes the shell that NoneRuntime.Exec will use on the
// current host. Callers that need to quote arguments for the same shell use
// Target.Quoter; Env carries any required environment variables.
type HostShell struct {
	// Target describes the command language interpreted by Cmd.
	Target Shell
	// Cmd builds an exec.Cmd configured to run `command` through the host
	// shell. The caller still sets Dir, Env (which must be merged with
	// HostShell.Env), Stdout, and Stderr.
	Cmd func(ctx context.Context, command string) *exec.Cmd
	// Env lists extra environment variables the shell needs to behave
	// predictably (for example MSYS_NO_PATHCONV for Git Bash on Windows).
	// Callers append it to their own env list; nil means "no extras".
	Env []string
}

// quoteForBashDoubleQuote returns s wrapped in double quotes with every
// character that bash treats as active inside double quotes escaped with a
// backslash. It is used for a Windows target executed through Git Bash.
func quoteForBashDoubleQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := range len(s) {
		c := s[i]
		if c == '\\' || c == '"' || c == '$' || c == '`' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}
