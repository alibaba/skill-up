package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/platform"
)

const (
	envSessionCleanupDir  = "SKILL_UP_SESSION_CLEANUP_DIR"
	envSessionCleanupFile = "SKILL_UP_SESSION_CLEANUP_FILE"
	// sessionCleanupMaxFileBytes bounds one transcript read. A larger file is
	// treated as unknown rather than read in full: the run cannot prove the
	// directory belongs to it, and deleting on a partial read is how a
	// conflicting workspace would be missed.
	sessionCleanupMaxFileBytes = 4 << 20
)

// sessionOwnership is what one session file (or one directory of them) says
// about which workspace it belongs to.
type sessionOwnership int

const (
	// sessionOwnerUnknown means the evidence is missing or unreadable.
	sessionOwnerUnknown sessionOwnership = iota
	// sessionOwnerOurs means every recorded identity field names this workspace.
	sessionOwnerOurs
	// sessionOwnerOther means some recorded identity field names another one.
	sessionOwnerOther
)

// removeAgentSessionDir deletes the project directories the agent CLI created
// for this workspace, so deleting the workspace does not leave the run's
// session history in the user's home.
//
// A directory is removed only when every session file in it records this
// workspace and nothing else. Identity is parsed from the transcripts rather
// than searched for as a string: a cwd path can appear inside a tool argument,
// and a truncated or hashed directory name can still hold this run's session.
// When discovery or parsing cannot prove ownership, the directory is kept.
func removeAgentSessionDir(ctx context.Context, rt Runtime, lookup agentSessionLookup) error {
	workspace := workspaceForRuntime(rt)
	workspaceKey := canonicalWorkspaceKey(workspace)
	if workspace == "" || workspaceKey == "" {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, sessionCleanupTimeout*4)
	defer cancel()

	dirs, err := sessionDirCandidates(cleanupCtx, rt, lookup, workspaceKey)
	if err != nil {
		return err
	}
	depth := lookup.sessionDepth
	if depth <= 0 {
		depth = 1
	}
	cmp := sessionPathComparison{foldCase: rt.Shell().GOOS == platform.GOOSWindows}

	removed := 0
	for _, dir := range dirs {
		ownership, err := sessionDirOwnership(cleanupCtx, rt, dir, workspace, depth, cmp)
		if err != nil {
			return err
		}
		if ownership != sessionOwnerOurs {
			logging.DebugContextf(ctx, "session cleanup: keeping %s (ownership %d)", dir, ownership)
			continue
		}
		if err := removeSessionDir(cleanupCtx, rt, dir); err != nil {
			return err
		}
		removed++
	}
	logging.DebugContextf(ctx, "session cleanup: removed %d of %d candidate directories", removed, len(dirs))
	return nil
}

// sessionDirCandidates lists directories under the projects root that could
// belong to this run. It is the same two-phase discovery the session lookup
// uses: directories whose canonicalised name matches the workspace key first;
// when none does (a CLI that truncates or hashes the name), every directory at
// that depth, so a directory the lookup could find is never silently skipped
// here. Ownership is still decided per directory afterwards.
func sessionDirCandidates(ctx context.Context, rt Runtime, lookup agentSessionLookup, workspaceKey string) ([]string, error) {
	paths := caseSensitivePaths
	if rt.Shell().GOOS == platform.GOOSWindows {
		paths = caseInsensitivePaths
	}
	result, err := rt.Exec(ctx, buildSessionDirCandidatesScript(lookup, paths), ExecOptions{
		Env: map[string]string{envSessionWorkspaceKey: workspaceKey},
	})
	if err != nil {
		return nil, fmt.Errorf("list session directories: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("list session directories: exit %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return splitLines(result.Stdout), nil
}

func buildSessionDirCandidatesScript(lookup agentSessionLookup, paths pathComparison) string {
	return fmt.Sprintf(`home=$(printenv HOME); [ -n "$home" ] || exit 0
root="%s"; [ -d "$root" ] || exit 0
wskey=$(printenv %s); [ -n "$wskey" ] || exit 0
tmp=$(mktemp) || exit 1
trap 'rm -f "$tmp" "$tmp.match"' 0
find "$root" -mindepth 1 -maxdepth 1 -type d 2>/dev/null >"$tmp" || true
awk -v key="$wskey" '{n=$0; sub(/.*\//,"",n); gsub(/[^a-zA-Z0-9]/,"-",n); if (%s) print}' "$tmp" >"$tmp.match" || true
if [ -s "$tmp.match" ]; then cat "$tmp.match"; else cat "$tmp"; fi
`, lookup.projectsRootTmpl, envSessionWorkspaceKey, paths.awkNameMatch)
}

// sessionDirOwnership reads every session file in dir and reports whether they
// all belong to workspace. The first file that proves another owner wins; a
// file that cannot be read or parsed, or that records no identity at all,
// leaves the directory unknown, and unknown directories are kept.
func sessionDirOwnership(ctx context.Context, rt Runtime, dir, workspace string, depth int, cmp sessionPathComparison) (sessionOwnership, error) {
	files, err := listSessionFiles(ctx, rt, dir, depth)
	if err != nil {
		return sessionOwnerUnknown, err
	}
	ours := false
	for _, file := range files {
		content, usable, err := readSessionFile(ctx, rt, file)
		if err != nil {
			return sessionOwnerUnknown, err
		}
		if !usable {
			return sessionOwnerUnknown, nil
		}
		switch sessionFileOwnership(content, workspace, cmp) {
		case sessionOwnerOther:
			return sessionOwnerOther, nil
		case sessionOwnerUnknown:
			return sessionOwnerUnknown, nil
		case sessionOwnerOurs:
			ours = true
		}
	}
	if !ours {
		return sessionOwnerUnknown, nil
	}
	return sessionOwnerOurs, nil
}

// sessionFileOwnership classifies a transcript by its parsed identity fields.
// Only top-level cwd and directories entries count: a cwd inside a tool
// argument is part of a nested object or a string and must not decide which
// workspace the run was.
func sessionFileOwnership(content []byte, workspace string, cmp sessionPathComparison) sessionOwnership {
	verdict := sessionOwnerUnknown
	for raw := range bytes.SplitSeq(content, []byte{'\n'}) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var record struct {
			Cwd         string   `json:"cwd"`
			Directories []string `json:"directories"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			// A line that does not parse could hold the conflicting identity.
			return sessionOwnerUnknown
		}
		for _, recorded := range append([]string{record.Cwd}, record.Directories...) {
			if recorded == "" {
				continue
			}
			if !cmp.samePath(recorded, workspace) {
				return sessionOwnerOther
			}
			verdict = sessionOwnerOurs
		}
	}
	return verdict
}

// sessionPathComparison compares recorded working directories with the run's
// workspace: verbatim on POSIX, folded on Windows where the same directory can
// be spelled with different case.
type sessionPathComparison struct {
	foldCase bool
}

func (c sessionPathComparison) samePath(recorded, workspace string) bool {
	left, right := filepath.Clean(recorded), filepath.Clean(workspace)
	if c.foldCase {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// listSessionFiles lists the transcript files that may carry identity for one
// workspace directory, bounded by the same depth the lookup uses.
func listSessionFiles(ctx context.Context, rt Runtime, dir string, depth int) ([]string, error) {
	script := fmt.Sprintf(`d=$(printenv %s); [ -n "$d" ] || exit 0
[ -d "$d" ] || exit 0
find "$d" -maxdepth %d -type f -name '*.jsonl' 2>/dev/null
`, envSessionCleanupDir, depth)
	result, err := rt.Exec(ctx, script, ExecOptions{Env: map[string]string{envSessionCleanupDir: dir}})
	if err != nil {
		return nil, fmt.Errorf("list session files in %s: %w", dir, err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("list session files in %s: exit %d: %s", dir, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return splitLines(result.Stdout), nil
}

// readSessionFile returns the transcript's contents. usable is false when the
// file is larger than the cleanup read bound, in which case ownership cannot be
// proven and the directory must be kept.
func readSessionFile(ctx context.Context, rt Runtime, file string) ([]byte, bool, error) {
	script := fmt.Sprintf(`f=$(printenv %s); [ -f "$f" ] || exit 0
size=$(stat -c %%s "$f" 2>/dev/null || stat -f %%z "$f" 2>/dev/null) || exit 1
case $size in ''|*[!0-9]*) exit 1;; esac
[ "$size" -le %d ] || exit 3
cat -- "$f"
`, envSessionCleanupFile, sessionCleanupMaxFileBytes)
	result, err := rt.Exec(ctx, script, ExecOptions{Env: map[string]string{envSessionCleanupFile: file}})
	if err != nil {
		return nil, false, fmt.Errorf("read session file %s: %w", file, err)
	}
	switch result.ExitCode {
	case 0:
		return []byte(result.Stdout), true, nil
	case 3:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("read session file %s: exit %d: %s", file, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
}

func removeSessionDir(ctx context.Context, rt Runtime, dir string) error {
	script := fmt.Sprintf(`d=$(printenv %s); [ -n "$d" ] || exit 0
rm -rf -- "$d"
`, envSessionCleanupDir)
	result, err := rt.Exec(ctx, script, ExecOptions{Env: map[string]string{envSessionCleanupDir: dir}})
	if err != nil {
		return fmt.Errorf("remove session directory %s: %w", dir, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("remove session directory %s: exit %d: %s", dir, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

func splitLines(out string) []string {
	var lines []string
	for line := range strings.SplitSeq(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
