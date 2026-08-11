package agent

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/platform"
)

// TestFindQoderSessionFileAcrossProjectDirNamingRules is the core guarantee of
// the session lookup: it must find the workspace's transcript without depending
// on how the CLI happened to name the directory it lives in.
//
// The rules below are all real or realistic: "non-alphanumeric to hyphen" is what
// qodercli 1.1.8 does, "spaces preserved" is what an older release did (such
// directories still exist on developer machines), "underscores" stands for a
// future release picking a different separator, and the truncated-plus-hashed
// name is what the CLIs fall back to for very long paths.
//
// Fixtures for the derivable names deliberately omit the recorded working
// directory, so only directory matching can resolve them; the truncated name
// cannot be derived from the path at all and is resolved from the recorded
// working directory instead. Each mechanism is therefore covered on its own and
// neither can mask the other.
func TestFindQoderSessionFileAcrossProjectDirNamingRules(t *testing.T) {
	skipIfNoLocalBash(t)
	t.Parallel()

	tests := []struct {
		name       string
		dirName    func(workspace string) string
		recordsCwd bool
	}{
		{
			name: "non-alphanumeric replaced with hyphen",
			dirName: func(workspace string) string {
				return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(workspace, "-")
			},
		},
		{
			name: "only separators replaced, spaces preserved",
			dirName: func(workspace string) string {
				return strings.ReplaceAll(workspace, "/", "-")
			},
		},
		{
			name: "underscores instead of hyphens",
			dirName: func(workspace string) string {
				return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(workspace, "_")
			},
		},
		{
			name: "truncated with hash suffix",
			dirName: func(workspace string) string {
				key := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(workspace, "-")
				return key[:len(key)/2] + "-1a2b3c"
			},
			recordsCwd: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			// An underscore and a dot in the workspace path reproduce the shape of
			// a macOS temp directory.
			workspace := filepath.Join(t.TempDir(), "skill_up.ws")
			if err := os.MkdirAll(workspace, 0o755); err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			rt := newShellSessionRuntime(workspace, home)
			resolved := resolvePath(t, workspace)

			fixture := `{"type":"user","message":{"content":"hi","role":"user"}}`
			if tt.recordsCwd {
				fixture = qoderSessionFixture(resolved)
			}

			const sessionID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
			mainSession := filepath.Join(home, ".qoder", "projects", tt.dirName(resolved), sessionID+".jsonl")
			writeSessionFixture(t, mainSession, fixture)

			if got := findQoderSessionFile(context.Background(), rt); got != mainSession {
				t.Fatalf("findQoderSessionFile() = %q, want %q", got, mainSession)
			}
		})
	}
}

// TestFindQoderSessionFileIgnoresOtherWorkspaces guards the discriminator the
// lookup relies on: cases running in parallel share one projects tree, so a
// newer transcript from a different workspace must never be picked up.
func TestFindQoderSessionFileIgnoresOtherWorkspaces(t *testing.T) {
	skipIfNoLocalBash(t)
	t.Parallel()

	home := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "skill_up.ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	rt := newShellSessionRuntime(workspace, home)
	resolved := resolvePath(t, workspace)

	projects := filepath.Join(home, ".qoder", "projects")
	mainSession := filepath.Join(projects, cliProjectDirKey(t, workspace), "11111111-1111-1111-1111-111111111111.jsonl")
	writeSessionFixture(t, mainSession, qoderSessionFixture(resolved))

	otherWorkspace := filepath.Join(t.TempDir(), "other_case.ws")
	if err := os.MkdirAll(otherWorkspace, 0o755); err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	otherSession := filepath.Join(projects, cliProjectDirKey(t, otherWorkspace), "22222222-2222-2222-2222-222222222222.jsonl")
	writeSessionFixture(t, otherSession, qoderSessionFixture(resolvePath(t, otherWorkspace)))

	touch(t, mainSession, time.Now().Add(-2*time.Minute))
	touch(t, otherSession, time.Now())

	if got := findQoderSessionFile(context.Background(), rt); got != mainSession {
		t.Fatalf("findQoderSessionFile() = %q, want this workspace's session %q", got, mainSession)
	}
}

// TestFindQoderSessionFileDisambiguatesCollidingWorkspaces covers workspaces whose
// paths differ only in punctuation, such as /w/ws_x and /w/ws-x. The CLIs replace
// every non-alphanumeric character, so both workspaces are stored under the *same*
// project directory and the directory name alone cannot tell them apart. Picking
// the newest transcript there would resume another workspace's session, which is
// how concurrent cases would silently grade each other's conversations.
func TestFindQoderSessionFileDisambiguatesCollidingWorkspaces(t *testing.T) {
	skipIfNoLocalBash(t)
	t.Parallel()

	home := t.TempDir()
	parent := t.TempDir()
	workspace := filepath.Join(parent, "ws_x")
	otherWorkspace := filepath.Join(parent, "ws-x")
	for _, dir := range []string{workspace, otherWorkspace} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	projectDir := cliProjectDirKey(t, workspace)
	if other := cliProjectDirKey(t, otherWorkspace); other != projectDir {
		t.Fatalf("fixture no longer collides: %q vs %q", projectDir, other)
	}

	root := filepath.Join(home, ".qoder", "projects", projectDir)
	oursSession := filepath.Join(root, "11111111-1111-1111-1111-111111111111.jsonl")
	otherSession := filepath.Join(root, "22222222-2222-2222-2222-222222222222.jsonl")
	writeSessionFixture(t, oursSession, qoderSessionFixture(resolvePath(t, workspace)))
	writeSessionFixture(t, otherSession, qoderSessionFixture(resolvePath(t, otherWorkspace)))

	// The other workspace ran last, so modification time alone would pick it.
	touch(t, oursSession, time.Now().Add(-2*time.Minute))
	touch(t, otherSession, time.Now())

	rt := newShellSessionRuntime(workspace, home)
	if got := findQoderSessionFile(context.Background(), rt); got != oursSession {
		t.Fatalf("findQoderSessionFile() = %q, want this workspace's session %q", got, oursSession)
	}
}

// TestFindQoderSessionFileIgnoresPathMentionedInContent guards the workspace
// predicate against false positives: a transcript belonging to a colliding
// workspace may quote our workspace path in a prompt or tool output (agents print
// paths routinely), which must not be read as "this transcript is ours". Only
// recorded working-directory metadata identifies a workspace.
func TestFindQoderSessionFileIgnoresPathMentionedInContent(t *testing.T) {
	skipIfNoLocalBash(t)
	t.Parallel()

	home := t.TempDir()
	parent := t.TempDir()
	workspace := filepath.Join(parent, "ws_x")
	otherWorkspace := filepath.Join(parent, "ws-x")
	for _, dir := range []string{workspace, otherWorkspace} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	resolved := resolvePath(t, workspace)
	root := filepath.Join(home, ".qoder", "projects", cliProjectDirKey(t, workspace))

	oursSession := filepath.Join(root, "11111111-1111-1111-1111-111111111111.jsonl")
	writeSessionFixture(t, oursSession, qoderSessionFixture(resolved))

	// The other workspace's transcript passes our directory to a tool, so the path
	// appears there as a plain JSON string just like a recorded cwd would.
	otherSession := filepath.Join(root, "22222222-2222-2222-2222-222222222222.jsonl")
	writeSessionFixture(t, otherSession, qoderSessionFixture(resolvePath(t, otherWorkspace))+"\n"+
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"path":"`+resolved+`"}}],"role":"assistant"}}`)

	touch(t, oursSession, time.Now().Add(-2*time.Minute))
	touch(t, otherSession, time.Now())

	rt := newShellSessionRuntime(workspace, home)
	if got := findQoderSessionFile(context.Background(), rt); got != oursSession {
		t.Fatalf("findQoderSessionFile() = %q, want this workspace's session %q", got, oursSession)
	}
}

// TestFindQoderSessionFileFailsSafeWhenIdentityUnknown covers a collision where
// our own transcript carries no working directory but a neighbour's does. The
// recorded directory proves the project directory is shared, so nothing here can
// be attributed to this workspace: returning no result (which the evaluator
// reports as a lost session) beats grading another workspace's conversation.
//
// Transcripts that record no directory at all are still usable when nothing
// contradicts them — qwen's format never records one, and neither did older CLI
// releases; TestFindQoderSessionFile_SelectsNewestByModTime covers that path.
func TestFindQoderSessionFileFailsSafeWhenIdentityUnknown(t *testing.T) {
	skipIfNoLocalBash(t)
	t.Parallel()

	home := t.TempDir()
	parent := t.TempDir()
	workspace := filepath.Join(parent, "ws_x")
	otherWorkspace := filepath.Join(parent, "ws-x")
	for _, dir := range []string{workspace, otherWorkspace} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	root := filepath.Join(home, ".qoder", "projects", cliProjectDirKey(t, workspace))

	oursSession := filepath.Join(root, "11111111-1111-1111-1111-111111111111.jsonl")
	otherSession := filepath.Join(root, "22222222-2222-2222-2222-222222222222.jsonl")
	writeSessionFixture(t, oursSession, `{"type":"user","message":{"content":"hi","role":"user"}}`)
	writeSessionFixture(t, otherSession, qoderSessionFixture(resolvePath(t, otherWorkspace)))
	touch(t, oursSession, time.Now().Add(-2*time.Minute))
	touch(t, otherSession, time.Now())

	rt := newShellSessionRuntime(workspace, home)
	if got := findQoderSessionFile(context.Background(), rt); got != "" {
		t.Fatalf("findQoderSessionFile() = %q, want no result rather than an unattributable transcript", got)
	}
}

// TestFindClaudeSessionFileMatchesJSONEscapedWindowsCwd pins the predicate to the
// JSON encoding a transcript actually uses: a Windows working directory appears
// as "C:\\Users\\...", so a raw comparison against the path never matches and the
// correct transcript would be discarded. The script itself runs under a POSIX
// shell here; only the workspace spelling and the recorded cwd are Windows-like,
// which is what the predicate is being tested on.
func TestFindClaudeSessionFileMatchesJSONEscapedWindowsCwd(t *testing.T) {
	skipIfNoLocalBash(t)
	t.Parallel()

	home := t.TempDir()
	const workspace = `C:\Users\tester\AppData\Local\Temp\skill_up-1`
	root := filepath.Join(home, ".claude", "projects", canonicalWorkspaceKey(workspace))
	session := filepath.Join(root, "33333333-3333-3333-3333-333333333333.jsonl")
	writeSessionFixture(t, session,
		`{"type":"user","cwd":"C:\\Users\\tester\\AppData\\Local\\Temp\\skill_up-1","message":{"content":"hi","role":"user"}}`)

	// A colliding workspace (skill-up-1 vs skill_up-1) shares the directory and ran later.
	other := filepath.Join(root, "44444444-4444-4444-4444-444444444444.jsonl")
	writeSessionFixture(t, other,
		`{"type":"user","cwd":"C:\\Users\\tester\\AppData\\Local\\Temp\\skill-up-1","message":{"content":"hi","role":"user"}}`)
	touch(t, session, time.Now().Add(-2*time.Minute))
	touch(t, other, time.Now())

	rt := newShellSessionRuntime(workspace, home)
	if got := findClaudeSessionFile(context.Background(), rt); got != session {
		t.Fatalf("findClaudeSessionFile() = %q, want %q", got, session)
	}
}

// qoderSessionFixture returns a minimal transcript in the shape qodercli writes,
// including the recorded working directory the lookup can fall back to.
func qoderSessionFixture(workspace string) string {
	return `{"type":"workspace-directories","directories":["` + workspace + `"]}
{"type":"user","cwd":"` + workspace + `","message":{"content":"hi","role":"user"}}`
}

func resolvePath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	return resolved
}

// TestFindQoderSessionFileSkipsSubagentTranscripts covers the case where a
// Skill (or any Task-style tool) spawns a sub-agent: qodercli stores that
// sub-agent's transcript at <projects>/<key>/<sessionID>/subagents/agent-*.jsonl,
// inside the same project tree. The lookup must still return the main session
// file, because the selected file name is handed to `-r` as the session ID and
// "agent-aExplore-…" is not a resumable session.
func TestFindQoderSessionFileSkipsSubagentTranscripts(t *testing.T) {
	skipIfNoLocalBash(t)
	t.Parallel()

	home := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	rt := newShellSessionRuntime(workspace, home)

	const sessionID = "11111111-2222-3333-4444-555555555555"
	root := filepath.Join(home, ".qoder", "projects", cliProjectDirKey(t, workspace))
	mainSession := filepath.Join(root, sessionID+".jsonl")
	subagentSession := filepath.Join(root, sessionID, "subagents", "agent-aExplore-deadbeef.jsonl")
	writeSessionFixture(t, mainSession, `{"type":"user","message":{"content":"hi","role":"user"}}`)
	writeSessionFixture(t, subagentSession, `{"type":"user","message":{"content":"sub","role":"user"}}`)

	// The sub-agent transcript is the newest file in the tree, so an unbounded
	// search would prefer it over the session skill-up actually drove.
	touch(t, mainSession, time.Now().Add(-2*time.Minute))
	touch(t, subagentSession, time.Now())

	got := findQoderSessionFile(context.Background(), rt)
	if got != mainSession {
		t.Fatalf("findQoderSessionFile() = %q, want main session %q", got, mainSession)
	}
	if id := extractSessionIDFromPath(got); id != sessionID {
		t.Fatalf("extractSessionIDFromPath(%q) = %q, want %q", got, id, sessionID)
	}
}

// TestFindQoderSessionFileWithNonAlphanumericWorkspace drives the lookup through
// a workspace path shaped like a macOS temp directory (underscore plus dot).
func TestFindQoderSessionFileWithNonAlphanumericWorkspace(t *testing.T) {
	skipIfNoLocalBash(t)
	t.Parallel()

	home := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "skill_up.ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	rt := newShellSessionRuntime(workspace, home)

	const sessionID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	root := filepath.Join(home, ".qoder", "projects", cliProjectDirKey(t, workspace))
	mainSession := filepath.Join(root, sessionID+".jsonl")
	writeSessionFixture(t, mainSession, `{"type":"user","message":{"content":"hi","role":"user"}}`)

	if got := findQoderSessionFile(context.Background(), rt); got != mainSession {
		t.Fatalf("findQoderSessionFile() = %q, want %q", got, mainSession)
	}
}

// cliProjectDirKey mirrors qodercli 1.1.8's naming rule independently of the
// production code, for fixtures that only need one plausible directory name.
// TestFindQoderSessionFileAcrossProjectDirNamingRules covers the other rules.
func cliProjectDirKey(t *testing.T, path string) string {
	t.Helper()
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(resolvePath(t, path), "-")
}

func writeSessionFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func touch(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func skipIfNoLocalBash(t *testing.T) {
	t.Helper()
	if goruntime.GOOS == platform.GOOSWindows {
		t.Skip("session lookup script needs a POSIX shell")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found in PATH")
	}
}

// shellSessionRuntime runs the generated lookup script through a real bash so
// the test exercises the script itself (depth, ordering, filters) instead of a
// canned Exec result.
type shellSessionRuntime struct {
	workspace string
	home      string
	merged    map[string]string
}

func newShellSessionRuntime(workspace, home string) *shellSessionRuntime {
	return &shellSessionRuntime{workspace: workspace, home: home}
}

func (r *shellSessionRuntime) Create(context.Context) error                      { return nil }
func (r *shellSessionRuntime) Close() error                                      { return nil }
func (r *shellSessionRuntime) Start(context.Context) error                       { return nil }
func (r *shellSessionRuntime) Stop(context.Context) error                        { return nil }
func (r *shellSessionRuntime) UploadFile(context.Context, string, string) error  { return nil }
func (r *shellSessionRuntime) UploadDir(context.Context, string, string) error   { return nil }
func (r *shellSessionRuntime) DownloadDir(context.Context, string, string) error { return nil }
func (r *shellSessionRuntime) Workspace() string                                 { return r.workspace }
func (r *shellSessionRuntime) RequiresProcessSandbox() bool                      { return false }

func (r *shellSessionRuntime) DownloadFile(_ context.Context, remote, local string) error {
	data, err := os.ReadFile(remote) //nolint:gosec // both paths are fixtures the test itself created
	if err != nil {
		return err
	}

	return os.WriteFile(local, data, 0o600) //nolint:gosec // both paths are fixtures the test itself created
}

func (r *shellSessionRuntime) Exec(ctx context.Context, cmd string, opts ExecOptions) (ExecResult, error) {
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	c.Env = append(os.Environ(), "HOME="+r.home)
	for k, v := range opts.Env {
		c.Env = append(c.Env, k+"="+v)
	}
	if opts.Cwd != "" {
		c.Dir = opts.Cwd
	}
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	result := ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	if err != nil {
		if ok := asExitError(err, &exitErr); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}
	return result, nil
}

func (r *shellSessionRuntime) MergeEnv(env map[string]string) {
	if r.merged == nil {
		r.merged = make(map[string]string, len(env))
	}
	maps.Copy(r.merged, env)
}

func (r *shellSessionRuntime) Shell() platform.Shell {
	return platform.Shell{GOOS: platform.GOOSLinux, Family: platform.ShellPOSIX, BashPath: "bash"}
}

func asExitError(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError) //nolint:errorlint // direct type assert keeps the helper trivial
	if ok {
		*target = exitErr
	}
	return ok
}
