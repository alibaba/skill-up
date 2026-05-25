package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"go.opentelemetry.io/otel/attribute"

	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/shellquote"
)

const (
	openSandboxDefaultWorkspace = "/workspace"
	// openSandboxDefaultImage is intentionally empty: users must supply a
	// concrete image via environment.image / environment.sandbox_template or
	// the config schema. Shipping a vendor-specific default leaks internal
	// infrastructure to OSS users and gives a confusing failure mode because
	// the image would not be reachable from their network.
	openSandboxDefaultImage     = ""
	openSandboxKwargBaseURL     = "base_url"
	openSandboxKwargExtensions  = "extensions"
	openSandboxKwargReqTimeout  = "request_timeout_seconds"
	openSandboxKwargParallelism = "file_transfer_parallelism"
	openSandboxBaseURLEnv       = "OPENSANDBOX_BASE_URL"
	openSandboxAPIKeyEnv        = "OPENSANDBOX_API_KEY"
	openSandboxExtensionsEnv    = "OPENSANDBOX_EXTENSIONS"
)

const openSandboxDefaultFileTransferParallelism = 8

// openSandboxUploadBatchSize caps how many files a single UploadFiles request
// carries. It bounds the open file descriptors held per batch so uploading a
// large directory tree stays well under a typical ulimit -n.
const openSandboxUploadBatchSize = 128

type openSandboxClient interface {
	ID() string
	Close() error
	Kill(ctx context.Context) error
	Pause(ctx context.Context) error
	Ping(ctx context.Context) error
	CreateDirectory(ctx context.Context, remotePath string, mode int) error
	UploadFiles(ctx context.Context, entries []opensandbox.UploadFileEntry) error
	DownloadFile(ctx context.Context, remotePath, rangeHeader string) (io.ReadCloser, error)
	SearchFiles(ctx context.Context, dir, pattern string) ([]opensandbox.FileInfo, error)
	RunCommandWithOpts(ctx context.Context, req opensandbox.RunCommandRequest, handlers *opensandbox.ExecutionHandlers) (*opensandbox.Execution, error)
}

var createOpenSandbox = createOpenSandboxCompat

func createOpenSandboxCompat(ctx context.Context, cfg opensandbox.ConnectionConfig, opts opensandbox.SandboxCreateOptions) (openSandboxClient, error) {
	if opts.Image == "" {
		return nil, errors.New("opensandbox image is required")
	}
	entrypoint := opts.Entrypoint
	if len(entrypoint) == 0 {
		entrypoint = opensandbox.DefaultEntrypoint
	}
	limits := opts.ResourceLimits
	if limits == nil {
		limits = opensandbox.DefaultResourceLimits
	}
	timeout := opts.TimeoutSeconds
	if opts.ManualCleanup {
		timeout = nil
	} else if timeout == nil {
		t := opensandbox.DefaultTimeoutSeconds
		timeout = &t
	}

	lifecycle := newOpenSandboxLifecycleClient(cfg)
	created, err := lifecycle.CreateSandbox(ctx, opensandbox.CreateSandboxRequest{
		Image:          &opensandbox.ImageSpec{URI: opts.Image, Auth: opts.ImageAuth},
		Entrypoint:     entrypoint,
		ResourceLimits: limits,
		Timeout:        timeout,
		Env:            opts.Env,
		Metadata:       opts.Metadata,
		NetworkPolicy:  opts.NetworkPolicy,
		Volumes:        opts.Volumes,
		Extensions:     opts.Extensions,
	})
	if err != nil {
		return nil, fmt.Errorf("opensandbox: create sandbox: %w", err)
	}

	readyOpts := opensandbox.ReadyOptions{
		Timeout:         opts.ReadyTimeout,
		PollingInterval: opts.HealthCheckInterval,
		HealthCheck:     opts.HealthCheck,
	}
	sb, err := opensandbox.ConnectSandbox(ctx, cfg, created.ID, readyOpts)
	if err != nil {
		_ = lifecycle.DeleteSandbox(context.WithoutCancel(ctx), created.ID)
		return nil, err
	}
	return sb, nil
}

func newOpenSandboxLifecycleClient(cfg opensandbox.ConnectionConfig) *opensandbox.LifecycleClient {
	options := []opensandbox.Option{opensandbox.WithAuthHeader(cfg.GetAuthHeader())}
	if cfg.HTTPClient != nil {
		options = append(options, opensandbox.WithHTTPClient(cfg.HTTPClient))
	}
	if cfg.GetRequestTimeout() > 0 {
		options = append(options, opensandbox.WithTimeout(cfg.GetRequestTimeout()))
	}
	if len(cfg.Headers) > 0 {
		options = append(options, opensandbox.WithHeaders(cfg.Headers))
	}
	if cfg.Retry != nil {
		options = append(options, opensandbox.WithRetry(*cfg.Retry))
	}
	return opensandbox.NewLifecycleClient(cfg.GetBaseURL()+"/"+opensandbox.APIVersion, cfg.GetAPIKey(), options...)
}

// OpenSandboxRuntime executes cases in an OpenSandbox remote sandbox.
type OpenSandboxRuntime struct {
	cfg             Config
	sandbox         openSandboxClient
	workspace       string
	baseURL         string
	extensions      map[string]string
	requestTimeout  time.Duration
	fileParallelism int
}

// NewOpenSandboxRuntime creates a new OpenSandboxRuntime with the given config.
func NewOpenSandboxRuntime(cfg Config) (*OpenSandboxRuntime, error) {
	workspace := cfg.WorkspaceMount
	if workspace == "" {
		workspace = openSandboxDefaultWorkspace
	}
	return &OpenSandboxRuntime{cfg: cfg, workspace: cleanRemotePath(workspace)}, nil
}

// Create provisions the sandbox environment and creates the runtime workspace.
func (r *OpenSandboxRuntime) Create(ctx context.Context) error {
	if r.sandbox != nil {
		return nil
	}
	r.configure(ctx)
	image := strings.TrimSpace(r.cfg.Image)
	if image == "" {
		image = strings.TrimSpace(r.cfg.SandboxTemplate)
	}
	if image == "" {
		image = openSandboxDefaultImage
	}
	if r.baseURL == "" {
		return errors.New("opensandbox runtime requires environment.kwargs.base_url or OPENSANDBOX_BASE_URL")
	}
	if openSandboxAuthKey() == "" {
		return errors.New("opensandbox runtime requires OPENSANDBOX_API_KEY")
	}
	if image == "" {
		return errors.New("opensandbox runtime requires environment.image or environment.sandbox_template to be set")
	}
	sb, err := createOpenSandbox(ctx, r.connectionConfig(), opensandbox.SandboxCreateOptions{
		Image:          image,
		Entrypoint:     r.entrypoint(),
		TimeoutSeconds: durationSecondsPtr(r.cfg.SandboxTimeout),
		Env:            r.cfg.Env,
		Metadata:       r.cfg.Metadata,
		Extensions:     r.extensions,
		ReadyTimeout:   r.cfg.ReadyTimeout,
		NetworkPolicy:  r.networkPolicy(),
	})
	if err != nil {
		return fmt.Errorf("failed to create opensandbox: %w", err)
	}
	r.sandbox = sb

	if err := r.ensureDirectory(ctx, r.workspace, 755); err != nil {
		_ = r.cleanupCreatedSandbox(ctx)
		return fmt.Errorf("failed to create opensandbox workspace %s: %w", r.workspace, err)
	}
	return nil
}

// Close releases sandbox resources. When Delete is false, the remote sandbox is preserved.
func (r *OpenSandboxRuntime) Close() error {
	return r.closeSandbox(context.Background())
}

func (r *OpenSandboxRuntime) closeSandbox(ctx context.Context) error {
	if r.sandbox == nil {
		return nil
	}
	sandbox := r.sandbox
	if !r.cfg.Delete {
		logging.DebugContextf(ctx, "OpenSandboxRuntime.Close: skipping cleanup, sandbox preserved: %s", sandbox.ID())
		err := sandbox.Close()
		r.sandbox = nil
		if err != nil {
			return fmt.Errorf("failed to close opensandbox %s: %w", sandbox.ID(), err)
		}
		return nil
	}
	if err := sandbox.Kill(ctx); err != nil {
		return fmt.Errorf("failed to kill opensandbox %s: %w", sandbox.ID(), err)
	}
	err := sandbox.Close()
	r.sandbox = nil
	if err != nil {
		return fmt.Errorf("failed to close opensandbox %s: %w", sandbox.ID(), err)
	}
	return nil
}

func (r *OpenSandboxRuntime) cleanupCreatedSandbox(ctx context.Context) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return r.closeSandbox(cleanupCtx)
}

// Start checks the sandbox is reachable.
func (r *OpenSandboxRuntime) Start(ctx context.Context) error {
	if err := r.ensureCreated(); err != nil {
		return err
	}
	return r.sandbox.Ping(ctx)
}

// Stop pauses the sandbox if supported by the server.
func (r *OpenSandboxRuntime) Stop(ctx context.Context) error {
	if err := r.ensureCreated(); err != nil {
		return err
	}
	return r.sandbox.Pause(ctx)
}

// UploadFile uploads a file from the host into the sandbox.
func (r *OpenSandboxRuntime) UploadFile(ctx context.Context, sourcePath, targetPath string) error {
	if err := r.ensureCreated(); err != nil {
		return err
	}
	target := r.remotePath(targetPath)
	if err := r.ensureDirectory(ctx, path.Dir(target), 755); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", path.Dir(target), err)
	}
	return r.uploadFiles(ctx, []uploadItem{{source: sourcePath, target: target}})
}

// UploadDir recursively uploads a directory tree from the host into the sandbox.
func (r *OpenSandboxRuntime) UploadDir(ctx context.Context, sourceDir, targetDir string) error {
	if err := r.ensureCreated(); err != nil {
		return err
	}
	targetRoot := r.remotePath(targetDir)
	items, dirs, err := collectUploadItems(sourceDir, targetRoot)
	if err != nil {
		return fmt.Errorf("failed to walk source directory %s: %w", sourceDir, err)
	}
	if err := r.ensureDirectories(ctx, dirs); err != nil {
		return fmt.Errorf("failed to create target directories: %w", err)
	}
	return r.uploadFiles(ctx, items)
}

type uploadItem struct {
	source string
	target string
}

func collectUploadItems(sourceDir, targetRoot string) ([]uploadItem, []string, error) {
	var items []uploadItem
	dirs := []string{targetRoot}
	err := filepath.Walk(sourceDir, func(localPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceDir, localPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		remotePath := path.Join(targetRoot, filepath.ToSlash(rel))
		if info.IsDir() {
			dirs = append(dirs, remotePath)
			return nil
		}
		items = append(items, uploadItem{source: localPath, target: remotePath})
		return nil
	})
	return items, dirs, err
}

func (r *OpenSandboxRuntime) uploadFiles(ctx context.Context, items []uploadItem) error {
	for start := 0; start < len(items); start += openSandboxUploadBatchSize {
		end := min(start+openSandboxUploadBatchSize, len(items))
		if err := r.uploadFileBatch(ctx, items[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// uploadFileBatch uploads one bounded batch of files in a single SDK request.
// Batching keeps the number of simultaneously open file descriptors below
// openSandboxUploadBatchSize so large directory trees do not exhaust ulimit -n.
func (r *OpenSandboxRuntime) uploadFileBatch(ctx context.Context, items []uploadItem) error {
	if len(items) == 0 {
		return nil
	}

	entries := make([]opensandbox.UploadFileEntry, 0, len(items))
	closers := make([]io.Closer, 0, len(items))
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()

	for _, item := range items {
		file, err := os.Open(item.source)
		if err != nil {
			return fmt.Errorf("failed to open source file %s: %w", item.source, err)
		}
		closers = append(closers, file)

		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("failed to stat source file %s: %w", item.source, err)
		}
		entries = append(entries, opensandbox.UploadFileEntry{
			File: file,
			Options: opensandbox.UploadFileOptions{
				FileName: filepath.Base(item.source),
				Metadata: opensandbox.FileMetadata{
					Path: item.target,
					Mode: opensandbox.OctalMode(info.Mode()),
				},
			},
		})
	}

	if err := r.sandbox.UploadFiles(ctx, entries); err != nil {
		return fmt.Errorf("failed to upload %d files: %w", len(entries), err)
	}
	return nil
}

// DownloadFile downloads a file from the sandbox to the host.
func (r *OpenSandboxRuntime) DownloadFile(ctx context.Context, sourcePath, targetPath string) error {
	if err := r.ensureCreated(); err != nil {
		return err
	}
	source := r.remotePath(sourcePath)
	return r.downloadRemoteFile(ctx, source, targetPath, 0)
}

func (r *OpenSandboxRuntime) downloadRemoteFile(ctx context.Context, source, targetPath string, mode int) error {
	rc, err := r.sandbox.DownloadFile(ctx, source, "")
	if err != nil {
		return fmt.Errorf("failed to download file %s: %w", source, err)
	}
	defer func() {
		_ = rc.Close()
	}()

	if err := os.MkdirAll(filepath.Dir(targetPath), noneDirMode); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, safeFileMode(mode))
	if err != nil {
		return fmt.Errorf("failed to create target file %s: %w", targetPath, err)
	}
	defer func() {
		_ = target.Close()
	}()
	if _, err := io.Copy(target, rc); err != nil {
		return fmt.Errorf("failed to write target file %s: %w", targetPath, err)
	}
	return nil
}

// DownloadDir recursively downloads a directory from the sandbox to the host.
func (r *OpenSandboxRuntime) DownloadDir(ctx context.Context, sourceDir, targetDir string) error {
	if err := r.ensureCreated(); err != nil {
		return err
	}
	source := r.remotePath(sourceDir)
	files, err := r.sandbox.SearchFiles(ctx, source, "**")
	if err != nil {
		return fmt.Errorf("failed to search sandbox directory %s: %w", source, err)
	}
	if err := os.MkdirAll(targetDir, noneDirMode); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	return r.downloadFiles(ctx, source, targetDir, files)
}

func (r *OpenSandboxRuntime) downloadFiles(ctx context.Context, source, targetDir string, files []opensandbox.FileInfo) error {
	if len(files) == 0 {
		return nil
	}
	parallelism := r.fileParallelism
	if parallelism <= 0 {
		parallelism = openSandboxDefaultFileTransferParallelism
	}
	parallelism = min(parallelism, len(files))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan opensandbox.FileInfo)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for range parallelism {
		wg.Go(func() {
			for file := range jobs {
				if err := r.downloadSearchResult(ctx, source, targetDir, file); err != nil {
					select {
					case errCh <- err:
						cancel()
					default:
					}
					return
				}
			}
		})
	}

	for _, file := range files {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			select {
			case err := <-errCh:
				return err
			default:
				return ctx.Err()
			}
		case jobs <- file:
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (r *OpenSandboxRuntime) downloadSearchResult(ctx context.Context, source, targetDir string, file opensandbox.FileInfo) error {
	rel, err := remoteRelativePath(source, file.Path)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	targetPath, err := safeLocalTarget(targetDir, rel)
	if err != nil {
		return err
	}
	return r.downloadRemoteFile(ctx, file.Path, targetPath, file.Mode)
}

// Exec runs a command inside the sandbox.
func (r *OpenSandboxRuntime) Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error) {
	if err := r.ensureCreated(); err != nil {
		return ExecResult{}, err
	}
	ctx, span := observability.Tracer().Start(ctx, "runtime.exec")
	defer span.End()
	startTime := time.Now()
	env := mergeEnvMaps(r.cfg.Env, opts.Env)

	req := opensandbox.RunCommandRequest{
		Command: withRemoteEnvExpansion(command, env),
		Cwd:     r.execCwd(opts.Cwd),
		Timeout: int64(opts.TimeoutSec) * 1000,
		Envs:    literalRemoteEnv(env),
	}
	span.SetAttributes(
		attribute.String("process.command", command),
		attribute.String("process.cwd", req.Cwd),
	)

	exec, err := r.sandbox.RunCommandWithOpts(ctx, req, nil)
	result := executionToResult(exec)
	// Distinguish two failure modes that previously both surfaced as exit
	// code -1:
	//   (a) sandbox SDK call itself errored, or returned nil — the remote
	//       command may never have run, and there is no real exit code to
	//       report. Avoid the misleading "command exited with code -1: <full
	//       script>" log; record this as a sandbox-side failure instead.
	//   (b) remote command ran and returned a non-zero exit code — log as
	//       before.
	sandboxCallFailed := err != nil || exec == nil
	if sandboxCallFailed && result.ExitCode == 0 {
		result.ExitCode = -1
	}
	span.SetAttributes(attribute.Int("process.exit_code", result.ExitCode))
	observability.RecordRuntimeExec(ctx, result.ExitCode, time.Since(startTime).Milliseconds())

	switch {
	case sandboxCallFailed:
		logging.ErrorContextf(ctx, "opensandbox SDK call failed before a remote exit code was reported (err=%v); command: %s", err, maskCommand(command))
		if result.Stderr != "" {
			logNonZeroStderr(ctx, result.ExitCode, result.Stderr)
		}
		if result.Stdout != "" {
			logNonZeroStdout(ctx, result.ExitCode, result.Stdout)
		}
	case result.ExitCode != 0:
		logNonZeroExit(ctx, result.ExitCode, command)
		if result.Stderr != "" {
			logNonZeroStderr(ctx, result.ExitCode, result.Stderr)
		}
		if result.Stdout != "" {
			logNonZeroStdout(ctx, result.ExitCode, result.Stdout)
		}
	case result.Stderr != "":
		logging.WarnContextf(ctx, "stderr: %s", result.Stderr)
	}
	if err != nil {
		return result, fmt.Errorf("opensandbox exec failed: %w", err)
	}
	return result, nil
}

func literalRemoteEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	literal := make(map[string]string, len(env))
	for k, v := range env {
		if k == "PATH" && strings.Contains(v, "$") {
			continue
		}
		literal[k] = v
	}
	return literal
}

func withRemoteEnvExpansion(command string, env map[string]string) string {
	pathValue, ok := env["PATH"]
	if !ok || !strings.Contains(pathValue, "$") {
		return command
	}
	return "export PATH=" + shellDoubleQuote(pathValue) + "\n" + command
}

func shellDoubleQuote(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`")
	return `"` + replacer.Replace(s) + `"`
}

// Workspace returns the sandbox workspace path.
func (r *OpenSandboxRuntime) Workspace() string {
	return r.workspace
}

// RequiresProcessSandbox reports that OpenSandbox already isolates agent execution.
func (r *OpenSandboxRuntime) RequiresProcessSandbox() bool {
	return false
}

func (r *OpenSandboxRuntime) connectionConfig() opensandbox.ConnectionConfig {
	return opensandbox.ConnectionConfig{
		Domain:         r.baseURL,
		APIKey:         openSandboxAuthKey(),
		UseServerProxy: r.cfg.UseServerProxy,
		RequestTimeout: r.requestTimeout,
	}
}

func (r *OpenSandboxRuntime) configure(ctx context.Context) {
	r.baseURL = r.resolveBaseURL()
	r.requestTimeout = r.resolveRequestTimeout()
	r.extensions = r.resolveExtensions(ctx)
	r.fileParallelism = r.resolveFileTransferParallelism()
}

func (r *OpenSandboxRuntime) resolveBaseURL() string {
	if baseURL := strings.TrimSpace(r.cfg.Kwargs[openSandboxKwargBaseURL]); baseURL != "" {
		return baseURL
	}
	return os.Getenv(openSandboxBaseURLEnv)
}

func openSandboxAuthKey() string {
	return os.Getenv(openSandboxAPIKeyEnv)
}

func (r *OpenSandboxRuntime) resolveRequestTimeout() time.Duration {
	raw := strings.TrimSpace(r.cfg.Kwargs[openSandboxKwargReqTimeout])
	if raw == "" {
		return 0
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func (r *OpenSandboxRuntime) resolveFileTransferParallelism() int {
	raw := strings.TrimSpace(r.cfg.Kwargs[openSandboxKwargParallelism])
	if raw == "" {
		return openSandboxDefaultFileTransferParallelism
	}
	parallelism, err := strconv.Atoi(raw)
	if err != nil || parallelism <= 0 {
		return openSandboxDefaultFileTransferParallelism
	}
	return min(parallelism, 64)
}

func (r *OpenSandboxRuntime) entrypoint() []string {
	if len(r.cfg.Entrypoint) > 0 {
		return r.cfg.Entrypoint
	}
	// The sandbox joins entrypoint items with " && ", so the default must stay a single shell line.
	return []string{"tail -F /dev/null"}
}

func (r *OpenSandboxRuntime) networkPolicy() *opensandbox.NetworkPolicy {
	switch strings.ToLower(strings.TrimSpace(r.cfg.NetworkPolicy)) {
	case "deny_all":
		return &opensandbox.NetworkPolicy{DefaultAction: "deny"}
	case "allow_declared":
		// Deny by default and permit only the declared egress targets, so an
		// empty allowlist blocks all outbound traffic rather than allowing it.
		policy := &opensandbox.NetworkPolicy{DefaultAction: "deny"}
		for _, target := range r.cfg.AllowedEgress {
			t := strings.TrimSpace(target)
			if t == "" {
				continue
			}
			policy.Egress = append(policy.Egress, opensandbox.NetworkRule{
				Action: "allow",
				Target: t,
			})
		}
		return policy
	default:
		return nil
	}
}

func (r *OpenSandboxRuntime) resolveExtensions(ctx context.Context) map[string]string {
	if raw := strings.TrimSpace(r.cfg.Kwargs[openSandboxKwargExtensions]); raw != "" {
		return parseExtensions(ctx, "environment.kwargs.extensions", raw)
	}
	return parseExtensions(ctx, openSandboxExtensionsEnv, strings.TrimSpace(os.Getenv(openSandboxExtensionsEnv)))
}

func parseExtensions(ctx context.Context, source, raw string) map[string]string {
	if raw == "" {
		return nil
	}
	extensions := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &extensions); err != nil {
		logging.WarnContextf(ctx, "%s is not valid JSON object, ignoring: %v", source, err)
		return nil
	}
	if len(extensions) == 0 {
		return nil
	}
	return extensions
}

func (r *OpenSandboxRuntime) ensureDirectory(ctx context.Context, dir string, mode int) error {
	err := r.sandbox.CreateDirectory(ctx, dir, mode)
	if err == nil {
		return nil
	}
	quoted := shellquote.Quote(dir)
	result, execErr := r.runCommand(ctx, "/", "mkdir -p "+quoted+" && test -d "+quoted+" && test -w "+quoted, 30)
	if execErr != nil {
		return err
	}
	if result.ExitCode == 0 {
		logging.WarnContextf(ctx, "OpenSandboxRuntime: directory API failed for %s, continuing after shell verification: %v", dir, err)
		return nil
	}
	if result.Stderr != "" {
		return fmt.Errorf("%w; shell verification failed with exit code %d: %s", err, result.ExitCode, result.Stderr)
	}
	return fmt.Errorf("%w; shell verification failed with exit code %d", err, result.ExitCode)
}

func (r *OpenSandboxRuntime) ensureDirectories(ctx context.Context, dirs []string) error {
	if len(dirs) == 0 {
		return nil
	}
	var command strings.Builder
	command.WriteString("mkdir -p")
	for _, dir := range dirs {
		command.WriteByte(' ')
		command.WriteString(shellquote.Quote(dir))
	}
	result, err := r.runCommand(ctx, "/", command.String(), 30)
	if err != nil {
		return fmt.Errorf("mkdir -p failed: %w", err)
	}
	if result.ExitCode == 0 {
		return nil
	}
	if result.Stderr != "" {
		return fmt.Errorf("mkdir -p failed with exit code %d: %s", result.ExitCode, result.Stderr)
	}
	return fmt.Errorf("mkdir -p failed with exit code %d", result.ExitCode)
}

func (r *OpenSandboxRuntime) runCommand(ctx context.Context, cwd, command string, timeoutSec int64) (ExecResult, error) {
	exec, err := r.sandbox.RunCommandWithOpts(ctx, opensandbox.RunCommandRequest{
		Command: command,
		Cwd:     cwd,
		Timeout: timeoutSec * 1000,
	}, nil)
	result := executionToResult(exec)
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = -1
	}
	return result, err
}

func (r *OpenSandboxRuntime) ensureCreated() error {
	if r.sandbox == nil {
		return errors.New("opensandbox runtime is not created")
	}
	return nil
}

func (r *OpenSandboxRuntime) execCwd(cwd string) string {
	if cwd == "" {
		return r.workspace
	}
	return r.remotePath(cwd)
}

func (r *OpenSandboxRuntime) remotePath(p string) string {
	if p == "" || p == "." {
		return r.workspace
	}
	clean := cleanRemotePath(p)
	if strings.HasPrefix(clean, "/") {
		return clean
	}
	return path.Join(r.workspace, clean)
}

func cleanRemotePath(p string) string {
	return path.Clean(filepath.ToSlash(p))
}

func durationSecondsPtr(d time.Duration) *int {
	if d <= 0 {
		return nil
	}
	seconds := int(d.Seconds())
	seconds = max(seconds, 1)
	return &seconds
}

func executionToResult(exec *opensandbox.Execution) ExecResult {
	if exec == nil {
		return ExecResult{ExitCode: -1}
	}
	result := ExecResult{
		Stdout:   joinOutputMessages(exec.Stdout),
		Stderr:   joinOutputMessages(exec.Stderr),
		ExitCode: 0,
	}
	if exec.ExitCode != nil {
		result.ExitCode = *exec.ExitCode
	}
	if exec.Error != nil && result.Stderr == "" {
		result.Stderr = strings.Join(exec.Error.Traceback, "\n")
		if result.Stderr == "" {
			result.Stderr = exec.Error.Value
		}
	}
	return result
}

func remoteRelativePath(root, file string) (string, error) {
	root = strings.TrimSuffix(cleanRemotePath(root), "/")
	file = cleanRemotePath(file)
	if file == root {
		return ".", nil
	}
	prefix := root + "/"
	if !strings.HasPrefix(file, prefix) {
		return "", fmt.Errorf("sandbox search result %s is outside source directory %s", file, root)
	}
	return strings.TrimPrefix(file, prefix), nil
}

func safeLocalTarget(root, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." {
		return root, nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe sandbox file path: %s", rel)
	}
	return filepath.Join(root, clean), nil
}

func safeFileMode(rawMode int) os.FileMode {
	if rawMode <= 0 {
		return noneFileMode
	}
	if rawMode > 777 {
		return noneFileMode
	}
	digitModes := [...]os.FileMode{0, 1, 2, 3, 4, 5, 6, 7}
	var mode os.FileMode
	shift := 0
	for rawMode > 0 {
		digit := rawMode % 10
		if digit > 7 {
			return noneFileMode
		}
		mode |= digitModes[digit] << shift
		shift += 3
		rawMode /= 10
	}
	return mode
}

func joinOutputMessages(messages []opensandbox.OutputMessage) string {
	var b strings.Builder
	total := 0
	for _, msg := range messages {
		total += len(msg.Text)
	}
	b.Grow(total)
	for _, msg := range messages {
		b.WriteString(msg.Text)
	}
	return b.String()
}
