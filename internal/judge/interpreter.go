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
					shellquote.QuoteWindows(remoteScript)
			},
		}, nil
	case ".cmd", ".bat":
		return scriptPlan{
			uploadName: "script" + ext,
			command: func(remoteScript string) string {
				return "cmd /c " + shellquote.QuoteWindows(remoteScript)
			},
		}, nil
	case ".sh", ".bash":
		bash, ok := platform.DiscoverBash()
		if !ok {
			return scriptPlan{}, fmt.Errorf(
				"script judge: .sh script requires bash on Windows; install Git Bash or set %s",
				platform.BashEnvOverride)
		}
		return scriptPlan{
			uploadName: "script.sh",
			command: func(remoteScript string) string {
				// bash on Windows reliably accepts forward-slash paths.
				return shellquote.QuoteWindows(bash) + " " +
					shellquote.QuoteWindows(filepath.ToSlash(remoteScript))
			},
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
		return "cmd /c rd /s /q " + shellquote.QuoteWindows(dir)
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
	interp := parseShebangInterpreter(line[2:])
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

// parseShebangInterpreter extracts the interpreter basename from the body of a
// shebang line. It understands both direct paths and the `/usr/bin/env <name>`
// form so e.g. `#!/usr/bin/env bash` and `#!/bin/sh` both resolve to a single
// token. Returns "" when the line has no usable interpreter.
func parseShebangInterpreter(body string) string {
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return ""
	}
	first := filepath.Base(fields[0])
	if first == "env" && len(fields) >= 2 {
		// Skip env's own option flags (e.g. `env -S bash`).
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-") {
				continue
			}
			return filepath.Base(f)
		}
		return ""
	}
	return first
}
