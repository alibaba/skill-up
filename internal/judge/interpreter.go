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
	// cleanupCommand builds the command that recursively removes the
	// per-judge temp dir on the target OS, using the same quoting rules as
	// command so the same shell ultimately interprets it.
	cleanupCommand func(dir string) string
	// envPath translates a runtime-side path (e.g. EVAL_TRANSCRIPT_PATH)
	// into the form the script's interpreter will accept. Identity for most
	// targets; the Windows `.sh` plan converts to forward slashes so POSIX
	// tools running inside Git Bash can open the file.
	envPath func(p string) string
}

// identityEnvPath is the default envPath used by plans that need no
// translation between the runtime-side path and the script's view of it.
func identityEnvPath(p string) string { return p }

// windowsQuoter returns the quoter that matches the shell NoneRuntime.Exec
// will pick on the current Windows host. When a usable bash is discoverable
// commands route through `bash -c`, so we must escape the two characters bash
// keeps active inside double quotes (the dollar sign and the backtick) to
// keep e.g. `C:\tmp\$foo\script.ps1` intact. When bash is unavailable the
// command runs under `cmd /d /s /c` which treats both characters literally,
// so plain QuoteWindows is correct -- inserting `\$` there would corrupt the
// literal path.
func windowsQuoter() func(string) string {
	if _, ok := platform.DiscoverBash(); ok {
		return func(s string) string {
			q := shellquote.QuoteWindows(s)
			q = strings.ReplaceAll(q, "$", `\$`)
			q = strings.ReplaceAll(q, "`", "\\`")
			return q
		}
	}
	return shellquote.QuoteWindows
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
			cleanupCommand: func(dir string) string {
				return "rm -rf " + shellquote.QuotePOSIX(dir)
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

	// Pick a quoter that matches the shell NoneRuntime.Exec will use on
	// this host -- once per plan so every command we emit (script run +
	// cleanup) goes through the same shell semantics.
	quote := windowsQuoter()
	winCleanup := func(dir string) string {
		// `/d /s /c` matches NewShellCmd's cmd fallback so the strip rule
		// behaves the same way for the inner command.
		return "cmd /d /s /c rd /s /q " + quote(dir)
	}

	switch ext {
	case ".ps1":
		return scriptPlan{
			uploadName: "script.ps1",
			command: func(remoteScript string) string {
				return "powershell -NoProfile -ExecutionPolicy Bypass -File " + quote(remoteScript)
			},
			cleanupCommand: winCleanup,
			envPath:        identityEnvPath,
		}, nil
	case ".cmd", ".bat":
		return scriptPlan{
			uploadName: "script" + ext,
			command: func(remoteScript string) string {
				return "cmd /d /s /c " + quote(remoteScript)
			},
			cleanupCommand: winCleanup,
			envPath:        identityEnvPath,
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
		bashArgs := []string{quote(bash)}
		for _, o := range opts {
			bashArgs = append(bashArgs, quote(o))
		}
		return scriptPlan{
			uploadName: "script.sh",
			command: func(remoteScript string) string {
				// bash on Windows accepts forward-slash paths; we also
				// keep EVAL_TRANSCRIPT_PATH in that form (see envPath
				// below) so POSIX tools inside the script can `cat` it.
				args := append([]string{}, bashArgs...)
				args = append(args, quote(filepath.ToSlash(remoteScript)))
				return strings.Join(args, " ")
			},
			cleanupCommand: winCleanup,
			envPath:        filepath.ToSlash,
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
// passed through to the interpreter). It understands direct paths
// (`#!/bin/bash -eu`), the `/usr/bin/env <name>` form, and both the
// split `env -S <body>` and compact `env -S<body>` GNU extensions.
//
// The `-S <body>` body is treated as whitespace-separated tokens; nested
// shell quoting inside -S (e.g. `-S bash -c "echo hi"`) is not parsed and
// the embedded quotes survive in the returned options. Real-world judge
// shebangs use only flag-style options, where this approximation is fine.
//
// Returns ("", nil) when the body has no usable interpreter.
func parseShebang(body string) (string, []string) {
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return "", nil
	}
	if filepath.Base(fields[0]) == "env" {
		return parseEnvShebang(fields[1:])
	}
	return filepath.Base(fields[0]), append([]string{}, fields[1:]...)
}

// parseEnvShebang processes the args after `env` in a shebang line.
func parseEnvShebang(args []string) (string, []string) {
	for i, f := range args {
		switch {
		case f == "-S" || f == "--split-string":
			// Split form: -S takes everything that follows as one logical
			// string. On a kernel shebang only one arg reaches env, so
			// rejoining with spaces matches what env would receive.
			return splitStringInterpreter(strings.Join(args[i+1:], " "))
		case strings.HasPrefix(f, "--split-string="):
			// Long compact: --split-string=<body>; any trailing tokens
			// were whitespace-split out of the same logical arg, so
			// rejoin them like the kernel-shebang one-arg rule.
			rest := strings.TrimPrefix(f, "--split-string=")
			if i+1 < len(args) {
				if rest != "" {
					rest += " "
				}
				rest += strings.Join(args[i+1:], " ")
			}
			return splitStringInterpreter(rest)
		case strings.HasPrefix(f, "-S"):
			// Compact form: -S<body> packs the split-string into the same
			// token. Strip the -S prefix and concatenate any trailing
			// tokens with a space (mirroring the kernel-shebang one-arg
			// rule).
			rest := strings.TrimPrefix(f, "-S")
			if i+1 < len(args) {
				if rest != "" {
					rest += " "
				}
				rest += strings.Join(args[i+1:], " ")
			}
			return splitStringInterpreter(rest)
		case strings.HasPrefix(f, "-"):
			// Other env flag we do not interpret (-i, -u VAR, -v, --chdir, ...).
			// Skip the flag; we do not try to consume its argument because
			// shebang flags that take arguments in two tokens are vanishingly
			// rare and out of scope for script judges.
			continue
		default:
			// First non-flag token is the interpreter.
			return filepath.Base(f), append([]string{}, args[i+1:]...)
		}
	}
	return "", nil
}

// splitStringInterpreter parses the body that env's -S would split. We use
// whitespace tokenization rather than a full shell parser because real judge
// shebangs only use flag-style options here.
func splitStringInterpreter(body string) (string, []string) {
	tokens := strings.Fields(body)
	if len(tokens) == 0 {
		return "", nil
	}
	return filepath.Base(tokens[0]), append([]string{}, tokens[1:]...)
}
