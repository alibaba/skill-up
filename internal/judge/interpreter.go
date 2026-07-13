package judge

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/shellquote"
)

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

// planScript determines how to execute scriptPath in a runtime whose commands
// run with the target shell.
//
// POSIX targets keep the original behavior: the script is uploaded verbatim
// and run via its own shebang. Windows targets dispatch to an interpreter
// based on the file extension (or shebang when the extension is absent).
func planScript(scriptPath string, shell platform.Shell) (scriptPlan, error) {
	if err := shell.Validate(); err != nil {
		return scriptPlan{}, fmt.Errorf("invalid runtime shell: %w", err)
	}
	if shell.GOOS != platform.GOOSWindows {
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
	return planWindowsScript(scriptPath, shell)
}

func planWindowsScript(scriptPath string, shell platform.Shell) (scriptPlan, error) {
	quote, err := shell.Quoter()
	if err != nil {
		return scriptPlan{}, fmt.Errorf("select runtime shell quoter: %w", err)
	}

	// Read and classify the shebang once: both the .ps1 (pwsh-vs-powershell
	// selection, option forwarding) and .sh (bash strict-mode flags) plans
	// consume the result. shebangInterp is "" when no recognized shebang
	// was found.
	shebangInterp, shebangOpts := parseShebang(readShebang(scriptPath))

	ext := strings.ToLower(filepath.Ext(scriptPath))
	if ext == "" {
		ext = extensionForShebangInterpreter(shebangInterp)
	}

	// Every command we emit (script run + cleanup) uses the target shell's
	// quoter so the same shell semantics applies end to end.
	// .ps1 / .cmd / .bat all execute through cmd.exe, so cleanup through cmd
	// keeps quoting / strip semantics inside one shell. The `/d /s /c` flags
	// match the cmd fallback in platform.Host so the strip rule behaves the
	// same way for the inner command.
	winCleanup := func(dir string) string {
		return "cmd /d /s /c rd /s /q " + quote(dir)
	}

	switch ext {
	case ".ps1":
		// Pick pwsh.exe (PowerShell Core 7+) when the shebang explicitly
		// asks for it; default to powershell.exe (Windows PowerShell 5.x)
		// otherwise. Forward any shebang-encoded interpreter flags so
		// `#!/usr/bin/env -S pwsh -NoLogo` does not silently lose -NoLogo
		// when we re-invoke the interpreter ourselves. -NoProfile and
		// -ExecutionPolicy Bypass are always appended because Windows
		// PowerShell's default Restricted policy blocks unsigned scripts;
		// the duplicate -NoProfile case is harmless if the shebang also
		// sets it.
		psBinary := psInterpLegacy
		if shebangInterp == psInterpCore {
			psBinary = psInterpCore
		}
		psArgs := []string{psBinary}
		// Only forward shebang opts when the shebang itself names a
		// PowerShell interpreter -- a `.ps1` file with a bogus
		// `#!/bin/bash -eu` shebang otherwise leaks bash flags into the
		// powershell invocation.
		if shebangInterp == psInterpCore || shebangInterp == psInterpLegacy {
			for _, o := range shebangOpts {
				psArgs = append(psArgs, quote(o))
			}
		}
		psArgs = append(psArgs, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File")
		return scriptPlan{
			uploadName: "script.ps1",
			command: func(remoteScript string) string {
				// Defensive copy: appending to the captured psArgs
				// directly would mutate its backing array if cap
				// exceeds len. The .sh branch below does the same.
				args := append([]string{}, psArgs...)
				args = append(args, quote(remoteScript))
				return strings.Join(args, " ")
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
		if !shell.IsBash() {
			return scriptPlan{}, errors.New("script judge: .sh script requires a bash target shell on Windows")
		}
		// Forward shebang-encoded options (`#!/bin/bash -eu`,
		// `#!/usr/bin/env -S bash -eu`, ...) so strict-mode flags that
		// POSIX honors via shebang aren't silently dropped when we invoke
		// bash explicitly on Windows.
		bashArgs := []string{quote(shell.BashPath)}
		// Forward only when the shebang actually names a POSIX shell, so a
		// `.sh` file with an unrelated shebang (e.g. `#!/usr/bin/env pwsh`
		// renamed to .sh) does not feed PowerShell flags to bash.
		if shebangPOSIXShells[shebangInterp] {
			for _, o := range shebangOpts {
				bashArgs = append(bashArgs, quote(o))
			}
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
			// Stay inside bash for cleanup too. The .sh case already runs
			// through bash -c, and Git Bash's rm understands the
			// forward-slash form of the Windows temp dir natively, so
			// `rm -rf <forward-slash dir>` avoids the bash -> cmd hop the
			// other extensions go through.
			cleanupCommand: func(dir string) string {
				return "rm -rf " + quote(filepath.ToSlash(dir))
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
	if targetGOOS == platform.GOOSWindows {
		return filepath.Join(os.TempDir(), name)
	}
	return path.Join("/tmp", name)
}

// joinForGOOS joins path elements using the separator of the target OS.
func joinForGOOS(targetGOOS string, elem ...string) string {
	if targetGOOS == platform.GOOSWindows {
		return filepath.Join(elem...)
	}
	return path.Join(elem...)
}

// shebangPOSIXShells lists interpreter basenames that the Windows planner
// is willing to dispatch through Git Bash. Only `sh` and `bash` are listed:
// the Windows `.sh` runner always invokes the discovered bash, so dropping
// a `#!/usr/bin/env zsh` script into bash would silently change semantics.
// Other POSIX shells (dash, ksh, zsh, ash) are intentionally rejected so
// the planner returns "cannot determine interpreter" rather than mis-routing.
// On POSIX targets this list is irrelevant: planScript ignores extension
// and lets the kernel honor the script's actual shebang.
var shebangPOSIXShells = map[string]bool{
	"sh": true, "bash": true,
}

// PowerShell interpreter basenames recognized by the Windows planner.
// `pwsh` is PowerShell Core 7+; `powershell` is the legacy Windows
// PowerShell 5.x. They differ in syntax and module set, so we track them
// separately and pick the binary that matches the shebang when present.
const (
	psInterpCore   = "pwsh"
	psInterpLegacy = "powershell"
)

// extensionForShebangInterpreter maps a parsed shebang interpreter basename
// to the synthetic file extension planWindowsScript dispatches on. Returns
// "" for empty / unrecognized interpreters so the planner reports
// "cannot determine interpreter" rather than mis-routing the script.
func extensionForShebangInterpreter(interp string) string {
	if interp == "" {
		return ""
	}
	switch interp {
	case psInterpCore, psInterpLegacy:
		return ".ps1"
	}
	if shebangPOSIXShells[interp] {
		return ".sh"
	}
	return ""
}

// shebangExtension reads scriptPath's first line and returns the synthetic
// extension implied by its shebang. Kept as a thin wrapper for tests that
// exercise the full path → extension flow.
func shebangExtension(scriptPath string) string {
	interp, _ := parseShebang(readShebang(scriptPath))
	return extensionForShebangInterpreter(interp)
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

// envValueTakingShortFlags lists GNU env short flags that consume a separate
// value token after them in a shebang.
var envValueTakingShortFlags = map[string]bool{
	"-u": true, // --unset NAME
	"-C": true, // --chdir DIR
	"-a": true, // --argv0 ARG (override argv[0])
}

// envValueTakingLongFlags lists GNU env long flags that accept their value
// as the next token (the `--name=value` form is self-contained and handled
// by the generic `strings.HasPrefix(f, "-")` skip arm).
//
// Intentionally omitted: --block-signal, --default-signal, --ignore-signal.
// GNU env documents those as OPTIONAL-argument flags (`[=sig]`) -- without
// the `=sig` suffix they take no value, so consuming the next token would
// swallow the interpreter (e.g. `env --ignore-signal bash` would otherwise
// be parsed as flag `--ignore-signal` with value `bash` and no interpreter
// remaining).
var envValueTakingLongFlags = map[string]bool{
	"--unset": true,
	"--chdir": true,
	"--argv0": true,
}

// isEnvAssignment reports whether tok has the env `NAME=VALUE` shape that
// GNU env accepts in front of the command. The portable form is
// `[A-Za-z_][A-Za-z0-9_]*=...`; everything before the first `=` must be a
// valid C-identifier byte sequence.
func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for i := range eq {
		c := tok[i]
		switch {
		case c == '_':
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}

// parseEnvShebang processes the args after `env` in a shebang line.
func parseEnvShebang(args []string) (string, []string) {
	for i := 0; i < len(args); { //nolint:intrange // we conditionally advance i by 2 when consuming flag values
		f := args[i]
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
		case envValueTakingShortFlags[f], envValueTakingLongFlags[f]:
			// Consume the flag and its separate value token (e.g.
			// `-u VAR`, `-C DIR`, `--unset FOO`, `--chdir /tmp`) so the
			// value token does not get mistaken for the interpreter.
			i += 2
		case strings.HasPrefix(f, "-"):
			// Self-contained env flag (-i, --ignore-environment,
			// --chdir=DIR, --unset=VAR, ...). Skip the single token.
			i++
		default:
			// First non-flag token. GNU env allows leading `NAME=VALUE`
			// assignments before the command (e.g.
			// `env PYTHONPATH=/opt python3`); the helper skips those so we
			// land on the real interpreter.
			return interpreterFromArgs(args[i:])
		}
	}
	return "", nil
}

// interpreterFromArgs returns the interpreter and trailing args after
// skipping leading GNU env `NAME=VALUE` assignments. The first non-
// assignment token is the interpreter; everything after it is forwarded.
func interpreterFromArgs(args []string) (string, []string) {
	i := 0
	for i < len(args) && isEnvAssignment(args[i]) {
		i++
	}
	if i >= len(args) {
		return "", nil
	}
	return filepath.Base(args[i]), append([]string{}, args[i+1:]...)
}

// splitStringInterpreter parses the body that env's -S would split. The
// tokenizer respects single and double quotes plus backslash escapes so
// shebangs like `#!/usr/bin/env -S bash -c "echo ok"` produce the same argv
// as a POSIX kernel-shebang dispatch. Full env-S escape sequences (\xHH,
// \n, \t, etc.) are not decoded -- they are rarely used in real script-judge
// shebangs and decoding them would not survive in the cmd-fallback path
// anyway. Unterminated quotes return ("", nil) which the planner converts
// into a "cannot determine interpreter" error.
func splitStringInterpreter(body string) (string, []string) {
	tokens, err := tokenizeShebangSplitString(body)
	if err != nil || len(tokens) == 0 {
		return "", nil
	}
	// GNU env allows leading `NAME=VALUE` assignments inside -S too
	// (e.g. `env -S PYTHONPATH=/opt python3`); reuse the same skip helper.
	return interpreterFromArgs(tokens)
}

// tokenizerState carries the small bit of state the tokenizer mutates as it
// walks body. Keeping it in a struct lets the per-state handlers stay small
// enough that the top-level loop stays under gocyclo's threshold.
type tokenizerState struct {
	cur      strings.Builder
	tokens   []string
	inSingle bool
	inDouble bool
	started  bool
	skipNext bool // when true, the next byte was consumed as an escape
	done     bool // set by `\c` in unquoted context: ignore the rest of body
}

func (s *tokenizerState) flush() {
	if s.started {
		s.tokens = append(s.tokens, s.cur.String())
		s.cur.Reset()
		s.started = false
	}
}

// tokenizeShebangSplitString splits body into shell-style tokens used by
// env -S: whitespace separates tokens; single quotes preserve every
// character literally until the next single quote; double quotes preserve
// characters with `\"` and `\\` decoded; outside quotes a backslash escapes
// the next character. Returns an error when a quote is left unterminated.
func tokenizeShebangSplitString(body string) ([]string, error) {
	s := &tokenizerState{}
	for i := 0; i < len(body); i++ { //nolint:intrange // we conditionally advance i to consume escape sequences
		if s.skipNext {
			s.skipNext = false
			continue
		}
		next := byte(0)
		if i+1 < len(body) {
			next = body[i+1]
		}
		switch {
		case s.inSingle:
			tokenizeStepSingle(s, body[i])
		case s.inDouble:
			tokenizeStepDouble(s, body[i], next)
		default:
			tokenizeStepUnquoted(s, body[i], next)
		}
		if s.done {
			break
		}
	}
	if s.inSingle || s.inDouble {
		return nil, fmt.Errorf("unterminated quote in shebang -S body: %q", body)
	}
	s.flush()
	return s.tokens, nil
}

func tokenizeStepSingle(s *tokenizerState, c byte) {
	if c == '\'' {
		s.inSingle = false
	} else {
		s.cur.WriteByte(c)
	}
	s.started = true
}

func tokenizeStepDouble(s *tokenizerState, c, next byte) {
	switch {
	case c == '"':
		s.inDouble = false
	case c == '\\' && (next == '"' || next == '\\'):
		s.cur.WriteByte(next)
		s.skipNext = true
	default:
		s.cur.WriteByte(c)
	}
	s.started = true
}

// envSWhitespaceEscapes lists the env -S backslash escapes that decode to a
// whitespace character. In unquoted context they act as token separators --
// most importantly `\_`, which is the documented way to embed a space
// inside an env -S body without surrounding quotes (e.g. `bash\_-eu`).
var envSWhitespaceEscapes = map[byte]bool{
	'_': true, // space
	't': true,
	'n': true,
	'r': true,
	'v': true,
	'f': true,
}

func tokenizeStepUnquoted(s *tokenizerState, c, next byte) {
	switch c {
	case ' ', '\t', '\n', '\v', '\r', '\f':
		s.flush()
	case '\'':
		s.inSingle = true
		s.started = true
	case '"':
		s.inDouble = true
		s.started = true
	case '\\':
		if next == 0 {
			return
		}
		switch {
		case next == 'c':
			// GNU env -S documents `\c` (unquoted) as "ignore the rest
			// of the split-string body". Stop tokenization right here so
			// the planner sees only the tokens emitted so far.
			s.flush()
			s.skipNext = true
			s.done = true
			return
		case envSWhitespaceEscapes[next]:
			// Whitespace escape: ends the current token without writing.
			s.flush()
		default:
			s.cur.WriteByte(next)
			s.started = true
		}
		s.skipNext = true
	default:
		s.cur.WriteByte(c)
		s.started = true
	}
}
