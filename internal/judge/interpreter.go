package judge

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/shellquote"
)

// osWindows is the GOOS value for Windows targets.
const osWindows = "windows"

// scriptPlan describes how the script judge uploads and runs an evaluation
// script in a runtime whose commands execute on a particular target OS.
type scriptPlan struct {
	// uploadName is the basename the script is uploaded as. The extension is
	// preserved so the interpreter dispatch stays unambiguous.
	uploadName string
	// command builds the runtime Exec command string for the uploaded script
	// at remoteScript (its path inside the runtime).
	command func(remoteScript string) string
	// envPath translates a runtime-side path (e.g. EVAL_TRANSCRIPT_PATH)
	// into the form the script's interpreter will accept. Identity for most
	// targets; the Windows `.sh` plan converts to forward slashes so POSIX
	// tools running inside Git Bash can open the file.
	envPath func(p string) string
}

// identityEnvPath is the default envPath used by plans that need no
// translation between the runtime-side path and the script's view of it.
func identityEnvPath(p string) string { return p }

// quoteWindowsThroughBash wraps shellquote.QuoteWindows but also escapes the
// two characters that stay live inside bash's double quotes -- the dollar
// sign and the backtick -- so a script-judge command remains safe when
// NoneRuntime.Exec routes it through bash -c on Windows (Git Bash is
// preferred when available). cmd /d /c receives an extra leading backslash
// before those characters in the rare cases they appear in paths, which
// Windows path normalization collapses transparently, so the same quoting
// works on both shells.
func quoteWindowsThroughBash(s string) string {
	q := shellquote.QuoteWindows(s)
	q = strings.ReplaceAll(q, "$", `\$`)
	q = strings.ReplaceAll(q, "`", "\\`")
	return q
}

// planScript determines how to execute scriptPath in a runtime whose commands
// run on targetGOOS.
//
// POSIX targets keep the original behavior: the script is uploaded verbatim
// and run via its own shebang. Windows targets dispatch to an interpreter
// based on the file extension (or shebang when the extension is absent).
func planScript(scriptPath, targetGOOS string) (scriptPlan, error) {
	if targetGOOS != osWindows {
		return scriptPlan{
			uploadName: "script",
			command: func(remoteScript string) string {
				q := shellquote.QuotePOSIX(remoteScript)
				return "chmod 700 " + q + " && " + q
			},
			envPath: identityEnvPath,
		}, nil
	}
	return planWindowsScript(scriptPath)
}

func planWindowsScript(scriptPath string) (scriptPlan, error) {
	ext := strings.ToLower(filepath.Ext(scriptPath))
	if ext == "" {
		ext = shebangExtension(scriptPath)
	}

	switch ext {
	case ".ps1":
		return scriptPlan{
			uploadName: "script.ps1",
			command: func(remoteScript string) string {
				return "powershell -NoProfile -ExecutionPolicy Bypass -File " +
					quoteWindowsThroughBash(remoteScript)
			},
			envPath: identityEnvPath,
		}, nil
	case ".cmd", ".bat":
		return scriptPlan{
			uploadName: "script" + ext,
			command: func(remoteScript string) string {
				// `/d` disables HKLM/HKCU AutoRun so the host's
				// `Command Processor\AutoRun` cannot inject commands ahead
				// of the script and make judge results non-deterministic.
				return "cmd /d /c " + quoteWindowsThroughBash(remoteScript)
			},
			envPath: identityEnvPath,
		}, nil
	case ".sh", ".bash":
		bash, ok := platform.DiscoverBash()
		if !ok {
			return scriptPlan{}, fmt.Errorf(
				"script judge: .sh script requires bash on Windows; install Git Bash or set %s",
				platform.BashEnvOverride)
		}
		// Forward any shebang-encoded options (`#!/bin/bash -eu`,
		// `#!/usr/bin/env -S bash -eu`, ...) so strict-mode flags that
		// POSIX honors via shebang aren't silently dropped when we invoke
		// bash explicitly on Windows.
		_, opts := parseShebang(readShebang(scriptPath))
		bashArgs := []string{quoteWindowsThroughBash(bash)}
		for _, o := range opts {
			bashArgs = append(bashArgs, quoteWindowsThroughBash(o))
		}
		return scriptPlan{
			uploadName: "script.sh",
			command: func(remoteScript string) string {
				// bash on Windows accepts forward-slash paths; we also
				// keep EVAL_TRANSCRIPT_PATH in that form (see envPath
				// below) so POSIX tools inside the script can `cat` it.
				args := append([]string{}, bashArgs...)
				args = append(args, quoteWindowsThroughBash(filepath.ToSlash(remoteScript)))
				return strings.Join(args, " ")
			},
			envPath: filepath.ToSlash,
		}, nil
	default:
		return scriptPlan{}, fmt.Errorf(
			"script judge: cannot determine interpreter for %s on Windows: "+
				"add a .sh, .ps1, or .cmd extension or a shebang",
			filepath.Base(scriptPath))
	}
}

// judgeTempDir returns an absolute temporary directory for a single script
// judge run, appropriate for the target OS.
func judgeTempDir(targetGOOS string) string {
	name := fmt.Sprintf("skill-up-judge-%d", time.Now().UnixNano())
	if targetGOOS == osWindows {
		return filepath.Join(os.TempDir(), name)
	}
	return path.Join("/tmp", name)
}

// joinForGOOS joins path elements using the separator of the target OS.
func joinForGOOS(targetGOOS string, elem ...string) string {
	if targetGOOS == osWindows {
		return filepath.Join(elem...)
	}
	return path.Join(elem...)
}

// removeDirCommand builds a command that recursively removes dir on the
// target OS.
func removeDirCommand(targetGOOS, dir string) string {
	if targetGOOS == osWindows {
		// `/d` matches the script-judge cmd invocations so AutoRun cannot
		// run between Exec calls.
		return "cmd /d /c rd /s /q " + quoteWindowsThroughBash(dir)
	}
	return "rm -rf " + shellquote.QuotePOSIX(dir)
}

// shebangPOSIXShells lists interpreter basenames mapped to a POSIX `.sh`
// dispatch. Matching is exact so `fish`, `ruby`, `python` etc. do not get
// misclassified just because their name contains the letters "sh".
var shebangPOSIXShells = map[string]bool{
	"sh": true, "bash": true, "dash": true, "ksh": true, "zsh": true, "ash": true,
}

// shebangExtension reads the first line of scriptPath and maps a recognized
// shebang to a synthetic file extension. It returns "" when the shebang is
// missing or unrecognized.
func shebangExtension(scriptPath string) string {
	interp, _ := parseShebang(readShebang(scriptPath))
	if interp == "" {
		return ""
	}
	switch interp {
	case "pwsh", "powershell":
		return ".ps1"
	}
	if shebangPOSIXShells[interp] {
		return ".sh"
	}
	return ""
}

// readShebang returns the body of scriptPath's first line when it is a
// shebang (everything after `#!`), or "" when there is no recognizable
// shebang or the file cannot be opened.
func readShebang(scriptPath string) string {
	f, err := os.Open(scriptPath) //nolint:gosec // scriptPath is a caller-provided evaluation script
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return ""
	}
	line := strings.TrimSpace(sc.Text())
	if !strings.HasPrefix(line, "#!") {
		return ""
	}
	return line[2:]
}

// parseShebang splits a shebang body into (interpreter basename, options
// passed through to the interpreter). It understands direct paths and the
// `/usr/bin/env <name>` / `env -S <name> <flags>` forms. Returns ("", nil)
// when the body has no usable interpreter.
func parseShebang(body string) (string, []string) {
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return "", nil
	}
	if filepath.Base(fields[0]) == "env" {
		// Skip env's own flags (e.g. -S, -i) to find the real interpreter.
		// Everything past the interpreter token is what env's -S passes
		// through.
		i := 1
		for i < len(fields) && strings.HasPrefix(fields[i], "-") {
			i++
		}
		if i >= len(fields) {
			return "", nil
		}
		return filepath.Base(fields[i]), append([]string{}, fields[i+1:]...)
	}
	return filepath.Base(fields[0]), append([]string{}, fields[1:]...)
}
