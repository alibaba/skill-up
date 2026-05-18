package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

func TestNewRuntimeAcceptsOpenSandbox(t *testing.T) {
	rt, err := NewRuntime(Config{
		Type:           "opensandbox",
		Image:          "ubuntu:24.04",
		WorkspaceMount: "/work",
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
	if rt.Workspace() != "/work" {
		t.Fatalf("Workspace() = %q, want /work", rt.Workspace())
	}
}

func setOpenSandboxTestAuth(t *testing.T) {
	t.Helper()
	t.Setenv(openSandboxAPIKeyEnv, "opensandbox-secret")
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
		WorkspaceMount: "/work",
		Image:          "ubuntu:24.04",
		UseServerProxy: true,
		ReadyTimeout:   3 * time.Second,
		SandboxTimeout: 2 * time.Minute,
		Env:            map[string]string{"RUNTIME_ENV": "1"},
		Entrypoint:     []string{"tail", "-f", "/dev/null"},
		Metadata:       map[string]string{"skill_eval": "true"},
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
	if fake.createdDirs[0] != "/work" {
		t.Fatalf("created workspace = %q, want /work", fake.createdDirs[0])
	}
}

func TestOpenSandboxCreatePassesNetworkPolicy(t *testing.T) {
	origCreate := createOpenSandbox
	t.Cleanup(func() { createOpenSandbox = origCreate })
	setOpenSandboxTestAuth(t)

	tests := []struct {
		name       string
		policy     string
		wantNil    bool
		wantAction string
	}{
		{"empty policy", "", true, ""},
		{"deny_all", "deny_all", false, "deny"},
		{"allow_declared", "allow_declared", false, "allow"},
		{"unknown value", "custom", true, ""},
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
			} else {
				if gotOpts.NetworkPolicy == nil {
					t.Fatal("NetworkPolicy = nil, want non-nil")
				}
				if gotOpts.NetworkPolicy.DefaultAction != tt.wantAction {
					t.Fatalf("DefaultAction = %q, want %q", gotOpts.NetworkPolicy.DefaultAction, tt.wantAction)
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
	if _, ok := req.Envs["PATH"]; ok {
		t.Fatalf("PATH should be expanded remotely instead of passed literally: %+v", req.Envs)
	}
	if !strings.Contains(req.Command, `export PATH="$CUSTOM_BIN:$PATH"`) || !strings.Contains(req.Command, "echo hello") {
		t.Fatalf("unexpected command with env expansion: %q", req.Command)
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

func TestOpenSandboxUploadDirUploadsFilesConcurrently(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nested"), noneDirMode); err != nil {
		t.Fatalf("create nested dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty"), noneDirMode); err != nil {
		t.Fatalf("create empty dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), noneFileMode); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "other.txt"), []byte("other"), noneFileMode); err != nil {
		t.Fatalf("write nested source: %v", err)
	}
	fake := &fakeOpenSandbox{
		execResult:  &opensandbox.Execution{ExitCode: intPtr(0)},
		uploadDelay: 20 * time.Millisecond,
	}
	rt := &OpenSandboxRuntime{workspace: "/workspace", sandbox: fake, fileParallelism: 2}

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
	if fake.maxConcurrentUploads < 2 {
		t.Fatalf("max concurrent uploads = %d, want at least 2", fake.maxConcurrentUploads)
	}
	if fake.maxConcurrentUploads > 2 {
		t.Fatalf("max concurrent uploads = %d, want no more than 2", fake.maxConcurrentUploads)
	}
	if strings.Contains(fake.execs[0].Command, "tar") {
		t.Fatalf("UploadDir exec command = %q, want no tar command", fake.execs[0].Command)
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
	downloadDelay          time.Duration
	downloadMu             sync.Mutex
	activeDownloads        int
	maxConcurrentDownloads int
	killed                 bool
	killCtxErr             error
}

func (f *fakeOpenSandbox) ID() string { return "sbx-test" }
func (f *fakeOpenSandbox) Close()     {}
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

func (f *fakeOpenSandbox) DownloadFile(_ context.Context, remotePath, _ string) (io.ReadCloser, error) {
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
