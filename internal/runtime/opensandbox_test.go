package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"github.com/alibaba/skill-up/internal/platform"
)

const testWorkspaceMount = "/work"

func TestNewRuntimeAcceptsOpenSandbox(t *testing.T) {
	rt, err := NewRuntime(Config{
		Type:           "opensandbox",
		Image:          "ubuntu:24.04",
		WorkspaceMount: testWorkspaceMount,
		SandboxTimeout: 2 * time.Minute,
		ReadyTimeout:   time.Second,
		UseServerProxy: true,
		Delete:         true,
		Env:            map[string]string{"A": "B"},
		Metadata:       map[string]string{"case": "one"},
		Kwargs: map[string]string{
			"base_url":   "https://sandbox.example.test",
			"extensions": `{"template":"basic"}`,
		},
		SandboxTemplate: "ignored-when-image-present",
		SkillPath:       "unused",
		SetupSteps:      []SetupStep{{Run: "echo setup"}},
		Entrypoint:      []string{"tail", "-f", "/dev/null"},
	})
	if err != nil {
		t.Fatalf("NewRuntime returned error: %v", err)
	}
	if rt.Workspace() != testWorkspaceMount {
		t.Fatalf("Workspace() = %q, want /work", rt.Workspace())
	}
}

func setOpenSandboxTestAuth(t *testing.T) {
	t.Helper()
	t.Setenv(openSandboxAPIKeyEnv, "opensandbox-secret")
}

func TestCreateOpenSandboxCompatDelegatesAllOptionsToSDK(t *testing.T) {
	origCreateSDK := createOpenSandboxSDK
	t.Cleanup(func() { createOpenSandboxSDK = origCreateSDK })

	fake := &fakeOpenSandbox{}
	wantCfg := opensandbox.ConnectionConfig{Domain: "https://sandbox.example.test", APIKey: "secret"}
	wantOpts := opensandbox.SandboxCreateOptions{
		Image:          "dockurr/windows:latest",
		TimeoutSeconds: intPtr(600),
		Env:            map[string]string{"VERSION": "11"},
		Metadata:       map[string]string{"case": "windows"},
		Extensions:     map[string]string{"profile": "windows"},
		Platform:       &opensandbox.PlatformSpec{OS: opensandbox.OSWindows, Arch: opensandbox.ArchAMD64},
		ResourceLimits: opensandbox.ResourceLimits{"cpu": "8", "memory": "16Gi", "disk": "128Gi"},
	}

	var gotCfg opensandbox.ConnectionConfig
	var gotOpts opensandbox.SandboxCreateOptions
	createOpenSandboxSDK = func(_ context.Context, cfg opensandbox.ConnectionConfig, opts opensandbox.SandboxCreateOptions) (openSandboxClient, error) {
		gotCfg, gotOpts = cfg, opts
		return fake, nil
	}

	got, err := createOpenSandboxCompat(context.Background(), wantCfg, wantOpts)
	if err != nil {
		t.Fatalf("createOpenSandboxCompat: %v", err)
	}
	if got != fake {
		t.Fatalf("client = %T, want fake client", got)
	}
	if !reflect.DeepEqual(gotCfg, wantCfg) {
		t.Fatalf("connection config = %+v, want %+v", gotCfg, wantCfg)
	}
	if !reflect.DeepEqual(gotOpts, wantOpts) {
		t.Fatalf("create options = %+v, want %+v", gotOpts, wantOpts)
	}
}

func TestOpenSandboxCreateUsesSDKOptions(t *testing.T) {
	origCreate := createOpenSandbox
	t.Cleanup(func() { createOpenSandbox = origCreate })
	setOpenSandboxTestAuth(t)

	fake := &fakeOpenSandbox{}
	var gotCfg opensandbox.ConnectionConfig
	var gotOpts opensandbox.SandboxCreateOptions
	createOpenSandbox = func(ctx context.Context, cfg opensandbox.ConnectionConfig, opts opensandbox.SandboxCreateOptions) (openSandboxClient, error) {
		gotCfg = cfg
		gotOpts = opts
		return fake, nil
	}

	rt, err := NewOpenSandboxRuntime(Config{
		WorkspaceMount: testWorkspaceMount,
		Image:          "ubuntu:24.04",
		UseServerProxy: true,
		ReadyTimeout:   3 * time.Second,
		SandboxTimeout: 2 * time.Minute,
		Env:            map[string]string{"RUNTIME_ENV": "1"},
		Entrypoint:     []string{"tail", "-f", "/dev/null"},
		Metadata:       map[string]string{"skill_up": "true"},
		Kwargs: map[string]string{
			"base_url":                "https://sandbox.example.test",
			"extensions":              `{"template":"basic"}`,
			"request_timeout_seconds": "600",
		},
		Delete: true,
	})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if gotCfg.Domain != "https://sandbox.example.test" || gotCfg.APIKey != "opensandbox-secret" || !gotCfg.UseServerProxy {
		t.Fatalf("connection config mismatch: %+v", gotCfg)
	}
	if gotCfg.RequestTimeout != 10*time.Minute {
		t.Fatalf("RequestTimeout = %s, want 10m", gotCfg.RequestTimeout)
	}
	if gotOpts.Image != "ubuntu:24.04" {
		t.Fatalf("Image = %q, want ubuntu:24.04", gotOpts.Image)
	}
	if gotOpts.TimeoutSeconds == nil || *gotOpts.TimeoutSeconds != 120 {
		t.Fatalf("TimeoutSeconds = %v, want 120", gotOpts.TimeoutSeconds)
	}
	if gotOpts.Extensions["template"] != "basic" {
		t.Fatalf("Extensions = %#v, want kwargs extensions", gotOpts.Extensions)
	}
	if fake.createdDirs[0] != testWorkspaceMount {
		t.Fatalf("created workspace = %q, want /work", fake.createdDirs[0])
	}
}

func TestOpenSandboxRuntimeShellIsLinuxPOSIX(t *testing.T) {
	rt, err := NewOpenSandboxRuntime(Config{})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime: %v", err)
	}
	want := platform.Shell{GOOS: platform.GOOSLinux, Family: platform.ShellPOSIX}
	if got := rt.Shell(); got != want {
		t.Fatalf("Shell() = %+v, want %+v", got, want)
	}
}

func TestOpenSandboxCreatePassesNetworkPolicy(t *testing.T) {
	origCreate := createOpenSandbox
	t.Cleanup(func() { createOpenSandbox = origCreate })
	setOpenSandboxTestAuth(t)

	tests := []struct {
		name          string
		policy        string
		allowedEgress []string
		wantNil       bool
		wantAction    string
		wantEgress    []string
	}{
		{name: "empty policy", policy: "", wantNil: true},
		{name: "deny_all", policy: "deny_all", wantAction: "deny"},
		{
			name:          "allow_declared denies by default with allow rules",
			policy:        "allow_declared",
			allowedEgress: []string{"pypi.org", " *.example.com ", ""},
			wantAction:    "deny",
			wantEgress:    []string{"pypi.org", "*.example.com"},
		},
		{
			name:       "allow_declared with no targets denies all",
			policy:     "allow_declared",
			wantAction: "deny",
		},
		{name: "unknown value", policy: "custom", wantNil: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotOpts opensandbox.SandboxCreateOptions
			createOpenSandbox = func(_ context.Context, _ opensandbox.ConnectionConfig, opts opensandbox.SandboxCreateOptions) (openSandboxClient, error) {
				gotOpts = opts
				return &fakeOpenSandbox{}, nil
			}
			rt, err := NewOpenSandboxRuntime(Config{
				Image:         "ubuntu:24.04",
				NetworkPolicy: tt.policy,
				AllowedEgress: tt.allowedEgress,
				Kwargs:        map[string]string{"base_url": "https://sandbox.example.test"},
				Delete:        true,
			})
			if err != nil {
				t.Fatalf("NewOpenSandboxRuntime error: %v", err)
			}
			if err := rt.Create(context.Background()); err != nil {
				t.Fatalf("Create error: %v", err)
			}
			if tt.wantNil {
				if gotOpts.NetworkPolicy != nil {
					t.Fatalf("NetworkPolicy = %+v, want nil", gotOpts.NetworkPolicy)
				}
				return
			}
			if gotOpts.NetworkPolicy == nil {
				t.Fatal("NetworkPolicy = nil, want non-nil")
			}
			if gotOpts.NetworkPolicy.DefaultAction != tt.wantAction {
				t.Fatalf("DefaultAction = %q, want %q", gotOpts.NetworkPolicy.DefaultAction, tt.wantAction)
			}
			if len(gotOpts.NetworkPolicy.Egress) != len(tt.wantEgress) {
				t.Fatalf("Egress = %+v, want %d rule(s)", gotOpts.NetworkPolicy.Egress, len(tt.wantEgress))
			}
			for i, rule := range gotOpts.NetworkPolicy.Egress {
				if rule.Action != "allow" {
					t.Errorf("Egress[%d].Action = %q, want allow", i, rule.Action)
				}
				if rule.Target != tt.wantEgress[i] {
					t.Errorf("Egress[%d].Target = %q, want %q", i, rule.Target, tt.wantEgress[i])
				}
			}
		})
	}
}

func TestOpenSandboxCreateSnapshotsKwargs(t *testing.T) {
	origCreate := createOpenSandbox
	t.Cleanup(func() { createOpenSandbox = origCreate })
	setOpenSandboxTestAuth(t)

	createCalls := 0
	var gotCfg opensandbox.ConnectionConfig
	var gotOpts opensandbox.SandboxCreateOptions
	createOpenSandbox = func(ctx context.Context, cfg opensandbox.ConnectionConfig, opts opensandbox.SandboxCreateOptions) (openSandboxClient, error) {
		createCalls++
		gotCfg = cfg
		gotOpts = opts
		return &fakeOpenSandbox{}, nil
	}

	kwargs := map[string]string{
		"base_url":                  "https://sandbox.example.test",
		"extensions":                `{"template":"basic"}`,
		"request_timeout_seconds":   "600",
		"file_transfer_parallelism": "3",
	}
	rt, err := NewOpenSandboxRuntime(Config{
		Image:  "ubuntu:24.04",
		Kwargs: kwargs,
		Delete: true,
	})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	kwargs["base_url"] = "https://mutated.example.test"
	kwargs["extensions"] = `{"template":"mutated"}`
	kwargs["request_timeout_seconds"] = "1"
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if createCalls != 1 {
		t.Fatalf("createOpenSandbox calls = %d, want 1", createCalls)
	}
	if gotCfg.Domain != "https://sandbox.example.test" {
		t.Fatalf("Domain = %q, want snapshot base_url", gotCfg.Domain)
	}
	if gotCfg.RequestTimeout != 10*time.Minute {
		t.Fatalf("RequestTimeout = %s, want 10m snapshot", gotCfg.RequestTimeout)
	}
	if gotOpts.Extensions["template"] != "basic" {
		t.Fatalf("Extensions = %#v, want snapshot extensions", gotOpts.Extensions)
	}
	if rt.fileParallelism != 3 {
		t.Fatalf("fileParallelism = %d, want snapshot value 3", rt.fileParallelism)
	}
}

func TestOpenSandboxLocalHelpersRejectUnsafePathsAndPreserveRemoteScope(t *testing.T) {
	t.Parallel()

	rel, err := remoteRelativePath("/workspace/src", "/workspace/src/a/b.txt")
	if err != nil {
		t.Fatalf("remoteRelativePath returned error: %v", err)
	}
	if rel != "a/b.txt" {
		t.Fatalf("remoteRelativePath = %q, want a/b.txt", rel)
	}
	if rel, err := remoteRelativePath("/workspace/src", "/workspace/src"); err != nil || rel != "." {
		t.Fatalf("remoteRelativePath root = %q, %v; want . nil", rel, err)
	}
	if _, err := remoteRelativePath("/workspace/src", "/workspace/other.txt"); err == nil {
		t.Fatal("remoteRelativePath outside root returned nil error")
	}

	root := t.TempDir()
	target, err := safeLocalTarget(root, "nested/file.txt")
	if err != nil {
		t.Fatalf("safeLocalTarget returned error: %v", err)
	}
	if target != filepath.Join(root, "nested", "file.txt") {
		t.Fatalf("safeLocalTarget = %q, want nested target", target)
	}
	if target, err := safeLocalTarget(root, "."); err != nil || target != root {
		t.Fatalf("safeLocalTarget dot = %q, %v; want root nil", target, err)
	}
	for _, rel := range []string{"../escape", "/absolute", "nested/../../escape"} {
		if _, err := safeLocalTarget(root, rel); err == nil {
			t.Fatalf("safeLocalTarget(%q) returned nil error", rel)
		}
	}
}

func TestOpenSandboxExecutionToResult(t *testing.T) {
	t.Parallel()

	exitCode := 7
	result := executionToResult(&opensandbox.Execution{
		Stdout:   []opensandbox.OutputMessage{{Text: "out"}, {Text: "put"}},
		Stderr:   []opensandbox.OutputMessage{{Text: "err"}},
		ExitCode: &exitCode,
	})
	if result.Stdout != "output" || result.Stderr != "err" || result.ExitCode != 7 {
		t.Fatalf("executionToResult = %+v, want combined stdout/stderr and exit 7", result)
	}

	result = executionToResult(&opensandbox.Execution{
		Error: &opensandbox.ExecutionError{Traceback: []string{"line1", "line2"}, Value: "fallback"},
	})
	if result.Stderr != "line1\nline2" || result.ExitCode != 0 {
		t.Fatalf("executionToResult traceback = %+v", result)
	}

	result = executionToResult(&opensandbox.Execution{
		Error: &opensandbox.ExecutionError{Value: "fallback"},
	})
	if result.Stderr != "fallback" {
		t.Fatalf("executionToResult fallback stderr = %q, want fallback", result.Stderr)
	}

	if result := executionToResult(nil); result.ExitCode != -1 {
		t.Fatalf("executionToResult(nil).ExitCode = %d, want -1", result.ExitCode)
	}
}

func TestOpenSandboxRuntimeStateAndPathHelpers(t *testing.T) {
	t.Parallel()

	rt, err := NewOpenSandboxRuntime(Config{WorkspaceMount: testWorkspaceMount})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	if rt.RequiresProcessSandbox() {
		t.Fatal("OpenSandbox runtime should report no additional process sandbox required")
	}
	if err := rt.ensureCreated(); err == nil {
		t.Fatal("ensureCreated before Create returned nil error")
	}
	if got := rt.Workspace(); got != testWorkspaceMount {
		t.Fatalf("Workspace() = %q, want /work", got)
	}
	if got := rt.execCwd(""); got != testWorkspaceMount {
		t.Fatalf("execCwd empty = %q, want /work", got)
	}
	if got := rt.execCwd("nested"); got != "/work/nested" {
		t.Fatalf("execCwd nested = %q, want /work/nested", got)
	}
	if got := rt.remotePath("/abs/path"); got != "/abs/path" {
		t.Fatalf("remotePath absolute = %q, want /abs/path", got)
	}
	if got := rt.remotePath("../escape"); got != "/escape" {
		t.Fatalf("remotePath cleans traversal relative to workspace, got %q", got)
	}
	if got := cleanRemotePath("./nested/../file.txt"); got != "file.txt" {
		t.Fatalf("cleanRemotePath = %q, want file.txt", got)
	}
}

func TestOpenSandboxRuntimeLifecycleMethods(t *testing.T) {
	t.Parallel()

	fake := &fakeOpenSandbox{}
	rt := &OpenSandboxRuntime{sandbox: fake, cfg: Config{Delete: true}}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !fake.killed {
		t.Fatal("Close with Delete=true did not kill sandbox")
	}

	keep := &fakeOpenSandbox{}
	rt = &OpenSandboxRuntime{sandbox: keep, cfg: Config{Delete: false}}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close keep-alive returned error: %v", err)
	}
	if keep.killed {
		t.Fatal("Close with Delete=false should not kill sandbox")
	}
}

func TestOpenSandboxConfigParsingHelpers(t *testing.T) {
	t.Parallel()

	rt := &OpenSandboxRuntime{cfg: Config{Kwargs: map[string]string{
		openSandboxKwargReqTimeout:  "5",
		openSandboxKwargParallelism: "128",
	}}}
	if got := rt.resolveRequestTimeout(); got != 5*time.Second {
		t.Fatalf("resolveRequestTimeout = %s, want 5s", got)
	}
	if got := rt.resolveFileTransferParallelism(); got != 64 {
		t.Fatalf("resolveFileTransferParallelism clamps to %d, want 64", got)
	}

	rt.cfg.Kwargs[openSandboxKwargReqTimeout] = "-1"
	rt.cfg.Kwargs[openSandboxKwargParallelism] = "0"
	if got := rt.resolveRequestTimeout(); got != 0 {
		t.Fatalf("invalid request timeout = %s, want 0", got)
	}
	if got := rt.resolveFileTransferParallelism(); got != openSandboxDefaultFileTransferParallelism {
		t.Fatalf("invalid parallelism = %d, want default", got)
	}

	if got := durationSecondsPtr(1500 * time.Millisecond); got == nil || *got != 1 {
		t.Fatalf("durationSecondsPtr(1.5s) = %v, want 1", got)
	}
	if got := durationSecondsPtr(0); got != nil {
		t.Fatalf("durationSecondsPtr(0) = %v, want nil", got)
	}
	if got := (&OpenSandboxRuntime{}).entrypoint(); len(got) != 1 || got[0] != "tail -F /dev/null" {
		t.Fatalf("default entrypoint = %#v", got)
	}
	if got := (&OpenSandboxRuntime{cfg: Config{Entrypoint: []string{"sleep", "infinity"}}}).entrypoint(); len(got) != 2 || got[0] != "sleep" {
		t.Fatalf("configured entrypoint = %#v", got)
	}

	parsed := parseExtensions(context.Background(), "test", `{"template":"basic"}`)
	if parsed["template"] != "basic" {
		t.Fatalf("parseExtensions valid = %#v", parsed)
	}
	if got := parseExtensions(context.Background(), "test", `{bad json`); got != nil {
		t.Fatalf("parseExtensions invalid = %#v, want nil", got)
	}
	if got := parseExtensions(context.Background(), "test", `{}`); got != nil {
		t.Fatalf("parseExtensions empty = %#v, want nil", got)
	}
}

func TestOpenSandboxDirectoryFallbacks(t *testing.T) {
	t.Parallel()

	fake := &fakeOpenSandbox{createDirErr: errors.New("api unavailable")}
	rt := &OpenSandboxRuntime{sandbox: fake}
	if err := rt.ensureDirectory(context.Background(), "/work/a b", 0o755); err != nil {
		t.Fatalf("ensureDirectory should accept shell-verified directory: %v", err)
	}
	if len(fake.execs) != 1 || !strings.Contains(fake.execs[0].Command, "mkdir -p '/work/a b'") {
		t.Fatalf("fallback command = %#v", fake.execs)
	}

	exit := 2
	fake = &fakeOpenSandbox{
		createDirErr: errors.New("api unavailable"),
		execResult:   &opensandbox.Execution{ExitCode: &exit, Stderr: []opensandbox.OutputMessage{{Text: "denied"}}},
	}
	rt = &OpenSandboxRuntime{sandbox: fake}
	if err := rt.ensureDirectory(context.Background(), "/work/nope", 0o755); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("ensureDirectory error = %v, want shell stderr", err)
	}

	fake = &fakeOpenSandbox{execResult: &opensandbox.Execution{ExitCode: &exit}}
	rt = &OpenSandboxRuntime{sandbox: fake}
	if err := rt.ensureDirectories(context.Background(), []string{"/work/a", "/work/b"}); err == nil || !strings.Contains(err.Error(), "exit code 2") {
		t.Fatalf("ensureDirectories error = %v, want exit code", err)
	}
	if err := rt.ensureDirectories(context.Background(), nil); err != nil {
		t.Fatalf("ensureDirectories(nil) = %v, want nil", err)
	}
}

func TestOpenSandboxCreateUsesBaseURLAndAuthFromEnv(t *testing.T) {
	origCreate := createOpenSandbox
	t.Cleanup(func() { createOpenSandbox = origCreate })
	setOpenSandboxTestAuth(t)
	t.Setenv(openSandboxBaseURLEnv, "https://agent-sandbox.example.com")

	var gotCfg opensandbox.ConnectionConfig
	var gotOpts opensandbox.SandboxCreateOptions
	createOpenSandbox = func(ctx context.Context, cfg opensandbox.ConnectionConfig, opts opensandbox.SandboxCreateOptions) (openSandboxClient, error) {
		gotCfg = cfg
		gotOpts = opts
		return &fakeOpenSandbox{}, nil
	}

	rt, err := NewOpenSandboxRuntime(Config{
		Image:  "ubuntu:24.04",
		Delete: true,
	})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if gotCfg.Domain != "https://agent-sandbox.example.com" || gotCfg.APIKey != "opensandbox-secret" {
		t.Fatalf("connection config mismatch: %+v", gotCfg)
	}
	if gotOpts.Image != "ubuntu:24.04" {
		t.Fatalf("Image = %q, want ubuntu:24.04", gotOpts.Image)
	}
	if len(gotOpts.Entrypoint) != 1 || gotOpts.Entrypoint[0] != "tail -F /dev/null" {
		t.Fatalf("Entrypoint = %#v, want single shell command", gotOpts.Entrypoint)
	}
}

func TestOpenSandboxCreatePrefersConfiguredImage(t *testing.T) {
	origCreate := createOpenSandbox
	t.Cleanup(func() { createOpenSandbox = origCreate })
	setOpenSandboxTestAuth(t)

	var gotOpts opensandbox.SandboxCreateOptions
	createOpenSandbox = func(ctx context.Context, cfg opensandbox.ConnectionConfig, opts opensandbox.SandboxCreateOptions) (openSandboxClient, error) {
		gotOpts = opts
		return &fakeOpenSandbox{}, nil
	}

	rt, err := NewOpenSandboxRuntime(Config{
		Image:  "ubuntu:24.04",
		Kwargs: map[string]string{"base_url": "https://sandbox.example.test"},
		Delete: true,
	})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if gotOpts.Image != "ubuntu:24.04" {
		t.Fatalf("Image = %q, want configured image", gotOpts.Image)
	}
}

func TestOpenSandboxCreateRequiresBaseURL(t *testing.T) {
	// Clear ambient env so this negative test is deterministic regardless of
	// CI shell exports (e.g. internal pipelines inject OPENSANDBOX_BASE_URL).
	t.Setenv(openSandboxBaseURLEnv, "")
	rt, err := NewOpenSandboxRuntime(Config{Delete: true})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	err = rt.Create(context.Background())
	if err == nil || !strings.Contains(err.Error(), "environment.kwargs.base_url or OPENSANDBOX_BASE_URL") {
		t.Fatalf("Create error = %v, want missing base_url error", err)
	}
}

func TestOpenSandboxCreateRequiresAuthKey(t *testing.T) {
	// Clear ambient env so this negative test is deterministic regardless of
	// CI shell exports (e.g. internal pipelines inject OPENSANDBOX_API_KEY).
	t.Setenv(openSandboxAPIKeyEnv, "")
	rt, err := NewOpenSandboxRuntime(Config{
		Kwargs: map[string]string{"base_url": "https://sandbox.example.test"},
		Delete: true,
	})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	err = rt.Create(context.Background())
	if err == nil || !strings.Contains(err.Error(), openSandboxAPIKeyEnv) {
		t.Fatalf("Create error = %v, want missing %s error", err, openSandboxAPIKeyEnv)
	}
}

func TestOpenSandboxCreateRequiresImage(t *testing.T) {
	setOpenSandboxTestAuth(t)
	rt, err := NewOpenSandboxRuntime(Config{
		Kwargs: map[string]string{"base_url": "https://sandbox.example.test"},
		Delete: true,
	})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	err = rt.Create(context.Background())
	if err == nil || !strings.Contains(err.Error(), "environment.image or environment.sandbox_template") {
		t.Fatalf("Create error = %v, want missing image error", err)
	}
}

func TestOpenSandboxCreateFallsBackWhenDirectoryAPICannotChmodWorkspace(t *testing.T) {
	origCreate := createOpenSandbox
	t.Cleanup(func() { createOpenSandbox = origCreate })
	setOpenSandboxTestAuth(t)

	fake := &fakeOpenSandbox{
		createDirErr: errors.New("RUNTIME_ERROR: error accessing file: chmod /workspace: operation not permitted"),
		execResult:   &opensandbox.Execution{ExitCode: intPtr(0)},
	}
	createOpenSandbox = func(ctx context.Context, cfg opensandbox.ConnectionConfig, opts opensandbox.SandboxCreateOptions) (openSandboxClient, error) {
		return fake, nil
	}

	rt, err := NewOpenSandboxRuntime(Config{
		Image:  "ubuntu:24.04",
		Kwargs: map[string]string{"base_url": "https://sandbox.example.test"},
		Delete: true,
	})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if fake.lastExec.Cwd != "/" {
		t.Fatalf("fallback cwd = %q, want /", fake.lastExec.Cwd)
	}
	if !strings.Contains(fake.lastExec.Command, "test -w '/workspace'") {
		t.Fatalf("fallback command = %q, want workspace write check", fake.lastExec.Command)
	}
}

func TestOpenSandboxCreateCleansUpWithUncanceledContext(t *testing.T) {
	origCreate := createOpenSandbox
	t.Cleanup(func() { createOpenSandbox = origCreate })
	setOpenSandboxTestAuth(t)

	fake := &fakeOpenSandbox{
		createDirErr: errors.New("create directory failed"),
		execResult:   &opensandbox.Execution{ExitCode: intPtr(1), Stderr: []opensandbox.OutputMessage{{Text: "denied"}}},
	}
	createOpenSandbox = func(ctx context.Context, cfg opensandbox.ConnectionConfig, opts opensandbox.SandboxCreateOptions) (openSandboxClient, error) {
		return fake, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rt, err := NewOpenSandboxRuntime(Config{
		Image:  "ubuntu:24.04",
		Kwargs: map[string]string{"base_url": "https://sandbox.example.test"},
		Delete: true,
	})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}

	if err := rt.Create(ctx); err == nil {
		t.Fatal("Create returned nil error, want workspace creation failure")
	}
	if !fake.killed {
		t.Fatal("sandbox was not killed after workspace creation failure")
	}
	if fake.killCtxErr != nil {
		t.Fatalf("Kill context error = %v, want cleanup to ignore canceled Create context", fake.killCtxErr)
	}
	if rt.sandbox != nil {
		t.Fatalf("runtime sandbox = %#v, want nil after cleanup", rt.sandbox)
	}
}

func TestOpenSandboxCreateUsesExtensionsFromEnv(t *testing.T) {
	origCreate := createOpenSandbox
	t.Cleanup(func() { createOpenSandbox = origCreate })
	setOpenSandboxTestAuth(t)
	t.Setenv(openSandboxExtensionsEnv, `{"profile":"ci","region":"default"}`)

	var gotOpts opensandbox.SandboxCreateOptions
	createOpenSandbox = func(ctx context.Context, cfg opensandbox.ConnectionConfig, opts opensandbox.SandboxCreateOptions) (openSandboxClient, error) {
		gotOpts = opts
		return &fakeOpenSandbox{}, nil
	}

	rt, err := NewOpenSandboxRuntime(Config{
		Image:  "registry.example.test/team/image:tag",
		Kwargs: map[string]string{"base_url": "https://sandbox.example.test"},
		Delete: true,
	})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if gotOpts.Extensions["profile"] != "ci" || gotOpts.Extensions["region"] != "default" {
		t.Fatalf("Extensions = %#v, want env extensions", gotOpts.Extensions)
	}
	if gotOpts.Image != "registry.example.test/team/image:tag" {
		t.Fatalf("Image = %q, want original registry-qualified image", gotOpts.Image)
	}
}

func TestOpenSandboxExecMapsOptionsAndResult(t *testing.T) {
	rt := &OpenSandboxRuntime{
		workspace: "/workspace",
		cfg: Config{Env: map[string]string{
			"CUSTOM_BIN": "/agent/bin",
			"PATH":       "$CUSTOM_BIN:$PATH",
		}},
		sandbox: &fakeOpenSandbox{
			execResult: &opensandbox.Execution{
				Stdout:   []opensandbox.OutputMessage{{Text: "hello\n"}},
				Stderr:   []opensandbox.OutputMessage{{Text: "warn\n"}},
				ExitCode: intPtr(7),
			},
		},
	}

	result, err := rt.Exec(context.Background(), "echo hello", ExecOptions{
		Cwd:        "repo",
		Env:        map[string]string{"X": "Y"},
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	if result.Stdout != "hello\n" || result.Stderr != "warn\n" || result.ExitCode != 7 {
		t.Fatalf("unexpected result: %+v", result)
	}
	fake, ok := rt.sandbox.(*fakeOpenSandbox)
	if !ok {
		t.Fatalf("sandbox type = %T, want *fakeOpenSandbox", rt.sandbox)
	}
	req := fake.lastExec
	if req.Cwd != "/workspace/repo" || req.Timeout != 5000 || req.Envs["X"] != "Y" || req.Envs["CUSTOM_BIN"] != "/agent/bin" {
		t.Fatalf("unexpected exec request: %+v", req)
	}
	// Env values forward literally — including $-bearing PATH. Callers
	// that need shell expansion must resolve the value first (see
	// internal/agent.probeAndMergePATH).
	if got := req.Envs["PATH"]; got != "$CUSTOM_BIN:$PATH" {
		t.Fatalf("PATH should forward literally; got %q in %+v", got, req.Envs)
	}
	if req.Command != "echo hello" {
		t.Fatalf("command should forward unchanged; got %q", req.Command)
	}
}

func TestOpenSandboxUploadDownloadFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(source, []byte("content"), noneFileMode); err != nil {
		t.Fatalf("write source: %v", err)
	}
	fake := &fakeOpenSandbox{files: map[string][]byte{"/workspace/out.txt": []byte("downloaded")}}
	rt := &OpenSandboxRuntime{workspace: "/workspace", sandbox: fake}

	if err := rt.UploadFile(context.Background(), source, "in/source.txt"); err != nil {
		t.Fatalf("UploadFile returned error: %v", err)
	}
	if string(fake.uploads["/workspace/in/source.txt"]) != "content" {
		t.Fatalf("uploaded content mismatch: %q", fake.uploads["/workspace/in/source.txt"])
	}

	target := filepath.Join(dir, "target.txt")
	if err := rt.DownloadFile(context.Background(), "out.txt", target); err != nil {
		t.Fatalf("DownloadFile returned error: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "downloaded" {
		t.Fatalf("downloaded content = %q, want downloaded", data)
	}
}

func writeUploadDirFixture(t *testing.T, dir string) {
	t.Helper()
	for _, sub := range []string{"nested", "empty"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), noneDirMode); err != nil {
			t.Fatalf("create %s dir: %v", sub, err)
		}
	}
	files := map[string]string{
		"file.txt":         "content",
		"nested/other.txt": "other",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(content), noneFileMode); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func TestOpenSandboxUploadDirUploadsFilesInSingleBatch(t *testing.T) {
	dir := t.TempDir()
	writeUploadDirFixture(t, dir)
	fake := &fakeOpenSandbox{
		execResult: &opensandbox.Execution{ExitCode: intPtr(0)},
	}
	rt := &OpenSandboxRuntime{workspace: "/workspace", sandbox: fake}

	if err := rt.UploadDir(context.Background(), dir, "dest"); err != nil {
		t.Fatalf("UploadDir returned error: %v", err)
	}
	if string(fake.uploads["/workspace/dest/file.txt"]) != "content" {
		t.Fatalf("uploaded root content mismatch: %q", fake.uploads["/workspace/dest/file.txt"])
	}
	if string(fake.uploads["/workspace/dest/nested/other.txt"]) != "other" {
		t.Fatalf("uploaded nested content mismatch: %q", fake.uploads["/workspace/dest/nested/other.txt"])
	}
	if len(fake.createdDirs) != 0 {
		t.Fatalf("created dirs = %#v, want UploadDir to use batched mkdir", fake.createdDirs)
	}
	if len(fake.execs) != 1 {
		t.Fatalf("UploadDir execs = %#v, want one batched mkdir command", fake.execs)
	}
	for _, want := range []string{"'/workspace/dest'", "'/workspace/dest/empty'", "'/workspace/dest/nested'"} {
		if !strings.Contains(fake.execs[0].Command, want) {
			t.Fatalf("UploadDir mkdir command = %q, want %s", fake.execs[0].Command, want)
		}
	}
	if fake.uploadFilesCalls != 1 {
		t.Fatalf("UploadFiles calls = %d, want a single batched call", fake.uploadFilesCalls)
	}
	if len(fake.uploadFilesBatchSizes) != 1 || fake.uploadFilesBatchSizes[0] != 2 {
		t.Fatalf("UploadFiles batch sizes = %#v, want one batch of 2 files", fake.uploadFilesBatchSizes)
	}
	if strings.Contains(fake.execs[0].Command, "tar") {
		t.Fatalf("UploadDir exec command = %q, want no tar command", fake.execs[0].Command)
	}
}

func TestOpenSandboxUploadDirChunksLargeTreeIntoBoundedBatches(t *testing.T) {
	dir := t.TempDir()
	const total = openSandboxUploadBatchSize + 2
	for i := range total {
		name := fmt.Sprintf("file-%03d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), noneFileMode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	fake := &fakeOpenSandbox{execResult: &opensandbox.Execution{ExitCode: intPtr(0)}}
	rt := &OpenSandboxRuntime{workspace: "/workspace", sandbox: fake}

	if err := rt.UploadDir(context.Background(), dir, "dest"); err != nil {
		t.Fatalf("UploadDir returned error: %v", err)
	}
	if len(fake.uploads) != total {
		t.Fatalf("uploaded files = %d, want %d", len(fake.uploads), total)
	}
	wantBatches := []int{openSandboxUploadBatchSize, 2}
	if !slices.Equal(fake.uploadFilesBatchSizes, wantBatches) {
		t.Fatalf("UploadFiles batch sizes = %#v, want %#v", fake.uploadFilesBatchSizes, wantBatches)
	}
}

func TestOpenSandboxDownloadDirSearchesAndDownloadsFiles(t *testing.T) {
	fake := &fakeOpenSandbox{
		files: map[string][]byte{
			"/workspace/outputs/root.txt":        []byte("root"),
			"/workspace/outputs/nested/file.txt": []byte("ok"),
		},
		searchResults: []opensandbox.FileInfo{
			{Path: "/workspace/outputs/root.txt", Mode: 600},
			{Path: "/workspace/outputs/nested/file.txt", Mode: 600},
		},
	}
	rt := &OpenSandboxRuntime{workspace: "/workspace", sandbox: fake}

	target := t.TempDir()
	if err := rt.DownloadDir(context.Background(), "outputs", target); err != nil {
		t.Fatalf("DownloadDir returned error: %v", err)
	}
	if fake.searchDir != "/workspace/outputs" || fake.searchPattern != "**" {
		t.Fatalf("search = (%q, %q), want /workspace/outputs **", fake.searchDir, fake.searchPattern)
	}
	rootData, err := os.ReadFile(filepath.Join(target, "root.txt"))
	if err != nil {
		t.Fatalf("read root file: %v", err)
	}
	if string(rootData) != "root" {
		t.Fatalf("root content = %q, want root", rootData)
	}
	data, err := os.ReadFile(filepath.Join(target, "nested", "file.txt"))
	if err != nil {
		t.Fatalf("read nested file: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("nested content = %q, want ok", data)
	}
	if len(fake.execs) != 0 {
		t.Fatalf("DownloadDir execs = %#v, want no tar command", fake.execs)
	}
}

func TestOpenSandboxDownloadDirDownloadsConcurrently(t *testing.T) {
	fake := &fakeOpenSandbox{
		files:         map[string][]byte{},
		downloadDelay: 20 * time.Millisecond,
	}
	for i := range 4 {
		remotePath := "/workspace/outputs/file-" + strconv.Itoa(i) + ".txt"
		fake.files[remotePath] = []byte("content")
		fake.searchResults = append(fake.searchResults, opensandbox.FileInfo{Path: remotePath, Mode: 600})
	}
	rt := &OpenSandboxRuntime{workspace: "/workspace", sandbox: fake, fileParallelism: 3}

	if err := rt.DownloadDir(context.Background(), "outputs", t.TempDir()); err != nil {
		t.Fatalf("DownloadDir returned error: %v", err)
	}
	if fake.maxConcurrentDownloads < 2 {
		t.Fatalf("max concurrent downloads = %d, want at least 2", fake.maxConcurrentDownloads)
	}
	if fake.maxConcurrentDownloads > 3 {
		t.Fatalf("max concurrent downloads = %d, want no more than 3", fake.maxConcurrentDownloads)
	}
}

func TestOpenSandboxRuntimeRealSmoke(t *testing.T) {
	if os.Getenv("SKILL_UP_REAL_OPENSANDBOX") != "1" {
		t.Skip("set SKILL_UP_REAL_OPENSANDBOX=1 to run against a real OpenSandbox service")
	}

	image := os.Getenv("SKILL_UP_REAL_OPENSANDBOX_IMAGE")
	if image == "" {
		image = openSandboxDefaultImage
	}

	baseURL := os.Getenv("SKILL_UP_REAL_OPENSANDBOX_BASE_URL")
	if baseURL == "" {
		baseURL = "https://agent-sandbox.example.com"
	}

	rt, err := NewOpenSandboxRuntime(Config{
		Image:          image,
		Kwargs:         map[string]string{"base_url": baseURL},
		Entrypoint:     realSmokeEntrypoint(),
		UseServerProxy: os.Getenv("SKILL_UP_REAL_OPENSANDBOX_PROXY") == "1",
		ReadyTimeout:   5 * time.Minute,
		SandboxTimeout: 10 * time.Minute,
		Delete:         true,
	})
	if err != nil {
		t.Fatalf("NewOpenSandboxRuntime returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := rt.Create(ctx); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}()

	result, err := rt.Exec(ctx, "printf skill-up-opensandbox && pwd", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Exec exit code = %d stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "skill-up-opensandbox") {
		t.Fatalf("Exec stdout = %q, want smoke marker", result.Stdout)
	}
}

func realSmokeEntrypoint() []string {
	raw := os.Getenv("SKILL_UP_REAL_OPENSANDBOX_ENTRYPOINT")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "|")
}

type fakeOpenSandbox struct {
	createdDirs            []string
	createDirErr           error
	uploads                map[string][]byte
	files                  map[string][]byte
	searchDir              string
	searchPattern          string
	searchResults          []opensandbox.FileInfo
	searchErr              error
	lastExec               opensandbox.RunCommandRequest
	execs                  []opensandbox.RunCommandRequest
	execResult             *opensandbox.Execution
	downloadHook           func(string) []byte
	uploadDelay            time.Duration
	uploadMu               sync.Mutex
	activeUploads          int
	maxConcurrentUploads   int
	uploadFilesCalls       int
	uploadFilesBatchSizes  []int
	downloadDelay          time.Duration
	downloadMu             sync.Mutex
	activeDownloads        int
	maxConcurrentDownloads int
	killed                 bool
	killCtxErr             error
}

func (f *fakeOpenSandbox) ID() string   { return "sbx-test" }
func (f *fakeOpenSandbox) Close() error { return nil }
func (f *fakeOpenSandbox) Kill(ctx context.Context) error {
	f.killed = true
	f.killCtxErr = ctx.Err()
	return nil
}

func (f *fakeOpenSandbox) Pause(context.Context) error {
	return nil
}

func (f *fakeOpenSandbox) Ping(context.Context) error {
	return nil
}

func (f *fakeOpenSandbox) CreateDirectory(_ context.Context, dir string, _ int) error {
	f.createdDirs = append(f.createdDirs, dir)
	if f.createDirErr != nil {
		return f.createDirErr
	}
	return nil
}

func (f *fakeOpenSandbox) UploadFile(_ context.Context, reader io.Reader, opts opensandbox.UploadFileOptions) error {
	if f.uploadDelay > 0 {
		f.uploadMu.Lock()
		f.activeUploads++
		f.maxConcurrentUploads = max(f.maxConcurrentUploads, f.activeUploads)
		f.uploadMu.Unlock()
		time.Sleep(f.uploadDelay)
		f.uploadMu.Lock()
		f.activeUploads--
		f.uploadMu.Unlock()
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	f.uploadMu.Lock()
	if f.uploads == nil {
		f.uploads = map[string][]byte{}
	}
	f.uploads[opts.Metadata.Path] = data
	f.uploadMu.Unlock()
	return nil
}

func (f *fakeOpenSandbox) UploadFiles(ctx context.Context, entries []opensandbox.UploadFileEntry) error {
	f.uploadMu.Lock()
	f.uploadFilesCalls++
	f.uploadFilesBatchSizes = append(f.uploadFilesBatchSizes, len(entries))
	f.uploadMu.Unlock()
	for _, entry := range entries {
		if err := f.UploadFile(ctx, entry.File, entry.Options); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeOpenSandbox) DownloadFile(_ context.Context, remotePath, _ string, _ ...opensandbox.DownloadFileOptions) (io.ReadCloser, error) {
	if f.downloadDelay > 0 {
		f.downloadMu.Lock()
		f.activeDownloads++
		f.maxConcurrentDownloads = max(f.maxConcurrentDownloads, f.activeDownloads)
		f.downloadMu.Unlock()
		time.Sleep(f.downloadDelay)
		f.downloadMu.Lock()
		f.activeDownloads--
		f.downloadMu.Unlock()
	}
	if f.downloadHook != nil {
		return io.NopCloser(bytes.NewReader(f.downloadHook(remotePath))), nil
	}
	return io.NopCloser(bytes.NewReader(f.files[remotePath])), nil
}

func (f *fakeOpenSandbox) SearchFiles(_ context.Context, dir, pattern string) ([]opensandbox.FileInfo, error) {
	f.searchDir = dir
	f.searchPattern = pattern
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

func (f *fakeOpenSandbox) RunCommandWithOpts(_ context.Context, req opensandbox.RunCommandRequest, _ *opensandbox.ExecutionHandlers) (*opensandbox.Execution, error) {
	f.lastExec = req
	f.execs = append(f.execs, req)
	if f.execResult != nil {
		return f.execResult, nil
	}
	return &opensandbox.Execution{ExitCode: intPtr(0)}, nil
}

func intPtr(v int) *int {
	return &v
}

func TestOpenSandboxRuntime_MergeEnv_AppliesToSubsequentExec(t *testing.T) {
	rt := &OpenSandboxRuntime{
		workspace: "/workspace",
		sandbox: &fakeOpenSandbox{
			execResult: &opensandbox.Execution{ExitCode: intPtr(0)},
		},
	}
	rt.MergeEnv(map[string]string{"FROM_MERGE": "yes"})

	if _, err := rt.Exec(context.Background(), "true", ExecOptions{}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	fake, _ := rt.sandbox.(*fakeOpenSandbox)
	if got := fake.lastExec.Envs["FROM_MERGE"]; got != "yes" {
		t.Fatalf("Envs[FROM_MERGE] = %q, want yes; got envs=%+v", got, fake.lastExec.Envs)
	}
}
