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
// commands route through `bash -c`, so we use double-quote-with-bash-escapes
// (every \, ", $, ` doubled): bash decodes them back to the original byte,
// and cmd later collapses the resulting `\\` runs through normal Windows
// path normalization. When bash is unavailable the command runs under
// `cmd /d /s /c` which already treats \, $, ` literally, so plain
// QuoteWindows is correct -- bash-style escapes there would corrupt the
// literal path (and cmd cannot escape `%VAR%` expansion regardless).
func windowsQuoter() func(string) string {
	if _, ok := platform.DiscoverBash(); ok {
		return quoteForBashDoubleQuote
	}
	return shellquote.QuoteWindows
}

// quoteForBashDoubleQuote returns s wrapped in double quotes with every
// character that bash treats as active inside double quotes escaped with a
// backslash. The four actives are \, ", $, `. After bash decodes the
// resulting string each of those bytes is delivered intact to the program
// bash spawns (cmd / powershell / a second bash), so a path like
// `C:\tmp\$foo\script.ps1` survives the bash -c hop without losing the
// backslash before `$`. The cmd-fallback path is not affected because we
// only choose this quoter when bash was discovered.
func quoteForBashDoubleQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ { //nolint:intrange // hot loop on bytes, no slicing tricks
		c := s[i]
		if c == '\\' || c == '"' || c == '$' || c == '`' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
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

// envValueTakingShortFlags lists GNU env short flags that consume a separate
// value token after them in a shebang.
var envValueTakingShortFlags = map[string]bool{
	"-u": true, // --unset NAME
	"-C": true, // --chdir DIR
}

// envValueTakingLongFlags lists GNU env long flags that accept their value
// as the next token (the `--name=value` form is self-contained and handled
// by the generic `strings.HasPrefix(f, "-")` skip arm).
var envValueTakingLongFlags = map[string]bool{
	"--unset":          true,
	"--chdir":          true,
	"--argv0":          true,
	"--block-signal":   true,
	"--default-signal": true,
	"--ignore-signal":  true,
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
			// First non-flag token is the interpreter.
			return filepath.Base(f), append([]string{}, args[i+1:]...)
		}
	}
	return "", nil
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
	return filepath.Base(tokens[0]), append([]string{}, tokens[1:]...)
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
		if envSWhitespaceEscapes[next] {
			// Whitespace escape: ends the current token without writing.
			s.flush()
		} else {
			s.cur.WriteByte(next)
			s.started = true
		}
		s.skipNext = true
	default:
		s.cur.WriteByte(c)
		s.started = true
	}
}
