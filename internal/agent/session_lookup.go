package agent

import (
	"context"
	"encoding/json"
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

var nonAlphanumeric = regexp.MustCompile(`[^A-Za-z0-9]`)

const (
	// envSessionWorkspace carries the workspace path as the CLI sees it.
	envSessionWorkspace = "SKILL_UP_SESSION_WS"
	// envSessionWorkspaceKey carries the canonical comparison form of that path.
	envSessionWorkspaceKey = "SKILL_UP_SESSION_WSKEY"
	// envSessionCwdMarkers carries the newline-separated JSON fragments that
	// identify a transcript as belonging to the workspace.
	envSessionCwdMarkers = "SKILL_UP_SESSION_CWD_MARKERS"
)

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
// claude-code / qoder / qwen / future CLIs can share the same shell script.
type agentSessionLookup struct {
	// projectsRootTmpl is the shell-expanded directory that holds one
	// subdirectory per workspace (e.g. "$home/.claude/projects").
	projectsRootTmpl string
	// sessionDepth bounds how deep below a workspace directory a session file
	// may live: 1 when session files sit directly inside it (claude, qoder), 2
	// for layouts with one intermediate directory (qwen's "chats"). It keeps
	// unrelated transcripts out of the result — qodercli stores sub-agent
	// transcripts in <sessionID>/subagents/, which are neither resumable
	// sessions nor this run's conversation.
	sessionDepth int
	// findExtra appends extra `find` predicates such as exclusion patterns.
	// Empty string means no extra predicates.
	findExtra string
}

// findAgentSessionJSONL resolves the newest *.jsonl session file that belongs to
// the runtime's workspace. HOME and the tree are read only inside the runtime via
// Exec to preserve runtime isolation.
//
// The workspace directory is identified without reproducing any CLI's naming
// rule: see buildSessionLookupScriptWithPrinter.
func findAgentSessionJSONL(ctx context.Context, rt Runtime, lookup agentSessionLookup) string {
	workspace := workspaceForRuntime(rt)
	workspaceKey := canonicalWorkspaceKey(workspace)
	if workspaceKey == "" {
		return ""
	}
	logging.DebugContextf(ctx, "agent session lookup: workspace=%q key=%q", workspace, workspaceKey)

	script := buildSessionLookupScript(lookup)
	if rt.Shell().GOOS == platform.GOOSWindows {
		script = buildWindowsSessionLookupScript(lookup)
	}
	result, err := rt.Exec(ctx, script, ExecOptions{
		Env: map[string]string{
			envSessionWorkspace:    workspace,
			envSessionWorkspaceKey: workspaceKey,
			envSessionCwdMarkers:   strings.Join(workspaceCwdMarkers(workspace), "\n"),
		},
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

// workspaceForRuntime returns the workspace path as the agent CLI sees it.
// POSIX CLIs learn their cwd from getcwd, which reports the physical path, so
// symlinks are resolved first. On Windows the spelling the CLI receives is kept
// as-is (8.3 short names are not expanded).
func workspaceForRuntime(rt Runtime) string {
	workspace := rt.Workspace()
	if rt.Shell().GOOS == platform.GOOSWindows {
		return workspace
	}
	if realPath, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = realPath
	}
	return workspace
}

// canonicalWorkspaceKey maps a workspace path to a comparison form: every
// non-alphanumeric character becomes "-".
//
// This is deliberately *not* an attempt to reproduce how a CLI names the
// directory it stores sessions in. Those rules are unversioned implementation
// details that differ per CLI and have changed between releases (qodercli once
// preserved spaces, today it replaces them). Instead, the same canonical form is
// applied to both sides of the comparison in the lookup script, so any rule that
// derives the directory name by substituting separators and special characters —
// with "-", "_", or anything else non-alphanumeric — still matches.
func canonicalWorkspaceKey(workspace string) string {
	return nonAlphanumeric.ReplaceAllString(workspace, "-")
}

// workspaceCwdMarkers renders the JSON fragments that mark a transcript as this
// workspace's. The path is JSON-encoded, so a Windows working directory is
// matched in the escaped form it is actually written in ("C:\\Users\\..."), and
// the key is part of the fragment, so a path that merely appears somewhere in the
// conversation — a tool argument, say — is not mistaken for recorded identity.
func workspaceCwdMarkers(workspace string) []string {
	encoded, err := json.Marshal(workspace)
	if err != nil {
		return nil
	}
	quoted := string(encoded)

	markers := make([]string, 0, 4)
	for _, key := range []string{`"cwd":`, `"directories":[`} {
		markers = append(markers, key+quoted, key+" "+quoted)
	}
	return markers
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

// buildSessionLookupScriptWithPrinter renders the shared lookup script.
//
// The workspace directory is located without reproducing any CLI's naming rule:
// each directory name under the projects root and the workspace path are both
// reduced to the same form (every non-alphanumeric character becomes "-") before
// comparing, in a single awk pass. Any rule that substitutes separators and
// special characters matches, whatever it substitutes them with, so a CLI release
// changing its rule cannot silently break resume.
//
// That comparison alone cannot establish identity, because it is lossy in both
// directions: the CLIs collapse punctuation too, so workspaces whose paths differ
// only in punctuation (say /w/a_b and /w/a-b) are stored in one directory.
// Candidates are therefore resolved against the working directory a transcript
// records, matched as the JSON fragment it is actually written as (`"cwd":"..."`,
// `"directories":["..."]`). Matching the encoded form keeps Windows paths
// (`C:\\Users\\...`) working and prevents a path that merely appears in the
// conversation, such as a tool argument, from being read as identity:
//
//  1. transcripts recording this workspace are the answer;
//  2. otherwise, if any candidate records a working directory at all, it belongs
//     to a different workspace and the directory is provably shared — the lookup
//     returns nothing, because reporting a lost session (which the evaluator
//     warns about) beats resuming or grading another workspace's conversation;
//  3. otherwise no candidate carries identity at all — qwen's chat JSONL never
//     does, and neither did older CLI releases — so the directory match is the
//     only signal available and the newest transcript is used.
//
// Step 3 leaves one residual case: two colliding workspaces whose transcripts all
// omit identity are indistinguishable on disk. Resolving that requires taking the
// session id from the CLI instead of the filesystem (qodercli offers
// `--session-id`, and a session_id field under `--output-format json`), which is a
// change to how every engine is invoked and is tracked separately.
//
// The depth bound keeps nested transcripts (e.g. qodercli's
// <sessionID>/subagents/) out.
//
// Shell constraints (some runtimes provide a minimal POSIX shell): no process
// substitution, and no pipeline whose body is shell code — such a body runs in a
// subshell, and a subshell exiting can fire the EXIT trap and delete the temp
// files the outer script still needs. Candidate lists therefore go through temp
// files that plain `while ... done <file` loops read back.
func buildSessionLookupScriptWithPrinter(lookup agentSessionLookup, printBest string) string {
	extra := ""
	if lookup.findExtra != "" {
		extra = " " + lookup.findExtra
	}
	sessionDepth := lookup.sessionDepth
	if sessionDepth <= 0 {
		sessionDepth = 1
	}

	return fmt.Sprintf(`home=$(printenv HOME); [ -n "$home" ] || exit 0
root="%s"; [ -d "$root" ] || exit 0
ws=$(printenv %s); wskey=$(printenv %s); [ -n "$wskey" ] || exit 0
tmp=$(mktemp) || exit 0; list=$(mktemp) || exit 0; marks=$(mktemp) || exit 0
trap 'rm -f "$tmp" "$list" "$marks"' 0
printenv %s >"$marks" || true
records_cwd() {
  head -c 65536 "$1" 2>/dev/null | grep -qE '"cwd"[[:space:]]*:|"workspace-directories"'
}
keep_recording_ws() {
  : >"$tmp"
  [ -s "$marks" ] || return 0
  while IFS= read -r c || [ -n "$c" ]; do
    if head -c 65536 "$c" 2>/dev/null | grep -qFf "$marks"; then printf '%%s\n' "$c" >>"$tmp"; fi
  done <"$1"
}
any_records_cwd() {
  while IFS= read -r c || [ -n "$c" ]; do
    if records_cwd "$c"; then return 0; fi
  done <"$1"
  return 1
}
find "$root" -mindepth 1 -maxdepth 1 -type d 2>/dev/null >"$tmp" || true
awk -v key="$wskey" '{n=$0; sub(/.*\//,"",n); gsub(/[^a-zA-Z0-9]/,"-",n); if (n==key) print}' "$tmp" >"$list" || true
: >"$tmp"
while IFS= read -r d || [ -n "$d" ]; do
  find "$d" -maxdepth %d -type f -name "*.jsonl"%s 2>/dev/null >>"$tmp" || true
done <"$list"
if [ -s "$tmp" ]; then
  cp "$tmp" "$list"
  keep_recording_ws "$list"
  if [ ! -s "$tmp" ] && ! any_records_cwd "$list"; then cp "$list" "$tmp"; fi
else
  find "$root" -maxdepth %d -type f -name "*.jsonl"%s 2>/dev/null >"$list" || true
  keep_recording_ws "$list"
fi
best=; ts=-1
while IFS= read -r p || [ -n "$p" ]; do
  [ -f "$p" ] || continue
  m=$(stat -c %%Y "$p" 2>/dev/null || stat -f %%m "$p" 2>/dev/null) || continue
  case $m in ''|*[!0-9]*) continue;; esac
  if [ "$ts" -eq -1 ] || [ "$m" -gt "$ts" ]; then ts=$m; best=$p; fi
done <"$tmp"
%s`,
		lookup.projectsRootTmpl,
		envSessionWorkspace, envSessionWorkspaceKey,
		envSessionCwdMarkers,
		sessionDepth, extra,
		sessionDepth+1, extra,
		printBest)
}
