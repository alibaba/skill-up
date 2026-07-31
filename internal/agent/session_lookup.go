package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/platform"
)

// sessionCleanupTimeout caps how long the post-run session-file lookup,
// download and stdout persistence steps may run. It is independent of the
// agent run's own timeout because those steps execute after the agent
// process has already returned (or been killed) and must not inherit a
// canceled run context — otherwise every Exec / DownloadFile call fires
// against a dead ctx and logs misleading "command exited with code -1"
// noise that hides the real timeout.
const sessionCleanupTimeout = 30 * time.Second

var windowsWorkspaceKeyInvalidChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// sessionCleanupContext derives a context for post-run session-file
// persistence, lookup and download. It detaches from cancellation of the
// run context (so a timed-out agent run does not poison the cleanup) but
// preserves context values such as the OTel trace ID.
func sessionCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), sessionCleanupTimeout)
}

// withDownloadedSession encapsulates the "download → register → parse" pipeline
// shared by claude_code / qodercli / codex. It calls `apply` with the local
// artifact path exactly when the download succeeds and registers the artifact
// in `generatedFiles`. The returned cleanup function must be deferred by the
// caller (kept on the caller's stack so it runs at the right scope).
//
// `apply` is responsible for the per-agent parse + overwrite policy on its own
// captured locals (e.g. claude/qoder always overwrite if non-empty, codex only
// overwrites when the new transcript is longer than the stream-derived one).
func withDownloadedSession(
	ctx context.Context,
	rt Runtime,
	artifactDir, sessionFilePath string,
	generatedFiles []string,
	apply func(artifactPath string),
) (files []string, cleanup func()) {
	noopCleanup := func() {}
	if sessionFilePath == "" {
		return generatedFiles, noopCleanup
	}
	artifactPath, registeredPath, cleanup, ok := downloadSessionArtifact(ctx, rt, artifactDir, sessionFilePath)
	if !ok {
		return generatedFiles, noopCleanup
	}
	if registeredPath != "" {
		generatedFiles = append(generatedFiles, registeredPath)
	}
	// Protect against apply() panicking: if it does, cleanup is still called
	// before the panic propagates so the temp file is removed. This preserves
	// the defer-cleanup-before-use invariant from the pre-refactor code.
	defer func() {
		if r := recover(); r != nil {
			cleanup()
			panic(r) // re-panic so caller still sees the original panic
		}
	}()
	apply(artifactPath)
	return generatedFiles, cleanup
}

// agentSessionLookup parametrises the per-agent project-tree lookup so that
// claude-code / qoder / future CLIs can share the same shell script.
type agentSessionLookup struct {
	// envVar is the environment variable name used inside the runtime to pass
	// the workspace key (e.g. SKILL_UP_CLAUDE_WSKEY).
	envVar string
	// rootTmpl is the shell-expanded root directory expression, referencing
	// $home and the envVar (e.g. "$home/.claude/projects/$SKILL_UP_CLAUDE_WSKEY").
	rootTmpl string
	// findExtra appends extra `find` predicates such as exclusion patterns.
	// Empty string means no extra predicates.
	findExtra string
}

// findAgentSessionJSONL resolves the newest *.jsonl session file under the
// agent-specific projects tree for the runtime's workspace. HOME and the tree
// are read only inside the runtime via Exec to preserve runtime isolation.
//
// Shell logic (cannot use process substitution / set -e in some runtimes):
//
//	tmp=$(mktemp); find ... >"$tmp"; while read -r p; do pick newest mtime; done <"$tmp"
func findAgentSessionJSONL(ctx context.Context, rt Runtime, lookup agentSessionLookup) string {
	workspaceKey := workspaceKeyForRuntime(rt)
	if workspaceKey == "" {
		return ""
	}
	logging.DebugContextf(ctx, "agent session lookup: workspace=%q key=%q", rt.Workspace(), workspaceKey)

	script := buildSessionLookupScript(lookup)
	if rt.Shell().GOOS == platform.GOOSWindows {
		script = buildWindowsSessionLookupScript(lookup)
	}
	result, err := rt.Exec(ctx, script, ExecOptions{
		Env: map[string]string{lookup.envVar: workspaceKey},
	})
	if err != nil || result.ExitCode != 0 {
		logging.DebugContextf(ctx, "agent session lookup failed: err=%v exit_code=%d", err, result.ExitCode)
		return ""
	}
	sessionPath := strings.TrimSpace(result.Stdout)
	if sessionPath == "" || !strings.HasSuffix(sessionPath, ".jsonl") {
		logging.DebugContextf(ctx, "agent session lookup returned no JSONL path")
		return ""
	}
	logging.DebugContextf(ctx, "agent session lookup found %q", sessionPath)
	return sessionPath
}

// workspaceKeyForRuntime computes the projects-tree subdirectory name for the
// runtime's workspace path. Claude derives Windows keys from the cwd spelling
// it receives, so preserve short-path components such as RUNNER~1 instead of
// resolving them to their long form.
func workspaceKeyForRuntime(rt Runtime) string {
	workspace := rt.Workspace()
	if rt.Shell().GOOS == platform.GOOSWindows {
		return windowsWorkspaceKeyInvalidChars.ReplaceAllString(workspace, "-")
	}
	if realPath, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = realPath
	}
	return strings.ReplaceAll(workspace, "/", "-")
}

// extractSessionIDFromPath extracts a session identifier from a session JSONL
// file path. It strips the directory and .jsonl extension, returning the bare
// filename which agents use as the session identifier for resume.
func extractSessionIDFromPath(sessionPath string) string {
	base := filepath.Base(sessionPath)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	return base
}

// buildSessionLookupScript renders the shared shell snippet that picks the
// newest *.jsonl under the configured root.
func buildSessionLookupScript(lookup agentSessionLookup) string {
	return buildSessionLookupScriptWithPrinter(lookup, `printf %s "$best"`)
}

func buildWindowsSessionLookupScript(lookup agentSessionLookup) string {
	// Git Bash reports /c/... paths, which Windows filepath handling does
	// not consider absolute. Convert the selected session path before it is
	// passed to Runtime.DownloadFile.
	return buildSessionLookupScriptWithPrinter(
		lookup,
		`if [ -n "$best" ]; then cygpath -w "$best" 2>/dev/null || printf %s "$best"; fi`,
	)
}

func buildSessionLookupScriptWithPrinter(lookup agentSessionLookup, printBest string) string {
	extra := ""
	if lookup.findExtra != "" {
		extra = " " + lookup.findExtra
	}
	return fmt.Sprintf(`home=$(printenv HOME); [ -n "$home" ] || exit 0
root="%s"; [ -d "$root" ] || exit 0
tmp=$(mktemp) || exit 0; trap 'rm -f "$tmp"' 0
find "$root" -type f -name "*.jsonl"%s 2>/dev/null >"$tmp" || true
best=; ts=-1
while IFS= read -r p || [ -n "$p" ]; do
  [ -f "$p" ] || continue
  m=$(stat -c %%Y "$p" 2>/dev/null || stat -f %%m "$p" 2>/dev/null) || continue
  case $m in ''|*[!0-9]*) continue;; esac
  if [ "$ts" -eq -1 ] || [ "$m" -gt "$ts" ]; then ts=$m; best=$p; fi
done <"$tmp"
%s`, lookup.rootTmpl, extra, printBest)
}
