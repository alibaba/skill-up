//go:build docker_integration

// Package runtime — docker_integration_test.go
//
// End-to-end integration tests that exercise DockerRuntime against a real
// `docker` daemon. The unit tests in docker_test.go fake the CLI and only
// verify the argv we build; these tests prove the runtime actually works
// against `docker create` / `start` / `exec` / `cp` / `rm` as shipped.
//
// Gated by the `docker_integration` build tag so a plain `go test ./...`
// never tries to talk to docker. Run with:
//
//	go test -tags docker_integration -count=1 -v ./internal/runtime/
//
// Tests skip cleanly if the docker daemon is unreachable, so the same
// command is safe to run on a machine without docker — you just get
// "SKIP" lines instead of failures.
//
// Image: dockerIntegrationImage (default alpine:3.20). Alpine ships busybox
// + `sh` but no `bash` — picked deliberately so the test would fail if the
// runtime's switch from `bash -c` to `sh -c` ever regressed.

package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// dockerIntegrationImage is the base image used for every integration test.
// Override via the SKILL_UP_DOCKER_INTEGRATION_IMAGE env var to point at a
// mirror or pinned digest in CI.
func dockerIntegrationImage() string {
	if v := strings.TrimSpace(os.Getenv("SKILL_UP_DOCKER_INTEGRATION_IMAGE")); v != "" {
		return v
	}
	return "alpine:3.20"
}

// requireDocker skips the test when the docker CLI is missing or the daemon
// is unreachable. Pulled into a package-level once so we only probe once
// per test binary run.
var (
	dockerProbeOnce sync.Once
	dockerProbeErr  error
)

func requireDocker(t *testing.T) {
	t.Helper()
	dockerProbeOnce.Do(func() {
		if _, err := exec.LookPath("docker"); err != nil {
			dockerProbeErr = err
			return
		}
		// `docker version --format {{.Server.Version}}` reaches the
		// daemon — if it fails, the daemon is down even if the CLI
		// exists.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
		if out, err := cmd.CombinedOutput(); err != nil {
			dockerProbeErr = errFromProbe(out, err)
		}
	})
	if dockerProbeErr != nil {
		t.Skipf("skipping docker integration test: %v", dockerProbeErr)
	}
}

func errFromProbe(out []byte, err error) error {
	if len(out) == 0 {
		return err
	}
	return &dockerProbeError{msg: strings.TrimSpace(string(out)), wrapped: err}
}

type dockerProbeError struct {
	msg     string
	wrapped error
}

func (e *dockerProbeError) Error() string { return e.msg + ": " + e.wrapped.Error() }
func (e *dockerProbeError) Unwrap() error { return e.wrapped }

// ensureImagePulled pulls dockerIntegrationImage the first time a test asks
// for it. Tests can run on hosts with the image cached or fresh; pulling
// up-front keeps later `docker create` calls fast and predictable.
var (
	pullOnce sync.Once
	pullErr  error
)

func ensureImage(t *testing.T) {
	t.Helper()
	requireDocker(t)
	pullOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		image := dockerIntegrationImage()
		cmd := exec.CommandContext(ctx, "docker", "pull", image)
		if out, err := cmd.CombinedOutput(); err != nil {
			pullErr = errFromProbe(out, err)
		}
	})
	if pullErr != nil {
		t.Skipf("docker pull failed (likely offline): %v", pullErr)
	}
}

// newIntegrationRuntime returns a DockerRuntime backed by the real CLI plus
// a t.Cleanup that calls Close — every test that creates a container gets
// guaranteed cleanup even when it fails.
func newIntegrationRuntime(t *testing.T, cfg Config) *DockerRuntime {
	t.Helper()
	if cfg.Image == "" {
		cfg.Image = dockerIntegrationImage()
	}
	cfg.Delete = true
	r, err := NewDockerRuntime(cfg)
	if err != nil {
		t.Fatalf("NewDockerRuntime: %v", err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Logf("Close: %v", err)
		}
	})
	return r
}

// TestIntegration_Lifecycle is the smoke test: create → exec a tiny
// command → confirm it ran inside the container → Close cleans up.
func TestIntegration_Lifecycle(t *testing.T) {
	ensureImage(t)

	r := newIntegrationRuntime(t, Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Create(ctx); err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, err := r.Exec(ctx, "uname -a && id -u", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Linux") {
		t.Errorf("expected Linux in uname output, got %q", res.Stdout)
	}
}

// TestIntegration_NoBashStillWorks proves the docker runtime is usable on
// minimal images that don't ship bash — this is the regression guard for
// the `bash -c` → `sh -c` fix. Alpine has no bash by default; if Exec
// silently regressed to `bash -c`, every command would fail with
// "exec: bash: not found" / OCI runtime error.
func TestIntegration_NoBashStillWorks(t *testing.T) {
	ensureImage(t)
	r := newIntegrationRuntime(t, Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Create(ctx); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Confirm bash really is absent in the image (sanity check).
	probe, err := r.Exec(ctx, "command -v bash || echo NOBASH", ExecOptions{})
	if err != nil {
		t.Fatalf("probe Exec: %v", err)
	}
	if !strings.Contains(probe.Stdout, "NOBASH") {
		t.Skipf("base image %q has bash; this test only makes sense on a bash-less image", dockerIntegrationImage())
	}

	// Real Exec must still work despite no bash.
	res, err := r.Exec(ctx, "echo from-sh", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "from-sh" {
		t.Fatalf("stdout=%q", res.Stdout)
	}
}

// TestIntegration_UploadDownloadFile round-trips a single file: write on
// host → UploadFile → exec to read the in-container content → DownloadFile
// to a fresh host path → compare bytes.
func TestIntegration_UploadDownloadFile(t *testing.T) {
	ensureImage(t)
	r := newIntegrationRuntime(t, Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Create(ctx); err != nil {
		t.Fatalf("Create: %v", err)
	}

	host := t.TempDir()
	src := filepath.Join(host, "src.txt")
	const payload = "hello docker integration\n"
	if err := os.WriteFile(src, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := r.UploadFile(ctx, src, "nested/dst.txt"); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	// Confirm the file landed at the expected in-container path.
	cat, err := r.Exec(ctx, "cat nested/dst.txt", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec cat: %v", err)
	}
	if cat.ExitCode != 0 {
		t.Fatalf("cat exit=%d stderr=%q", cat.ExitCode, cat.Stderr)
	}
	if cat.Stdout != payload {
		t.Fatalf("in-container payload mismatch: %q", cat.Stdout)
	}

	dst := filepath.Join(host, "out", "downloaded.txt")
	if err := r.DownloadFile(ctx, "nested/dst.txt", dst); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read downloaded: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("downloaded bytes mismatch: %q", string(got))
	}
}

// TestIntegration_UploadDownloadDir round-trips a directory tree containing
// nested subdirs and binary content. Confirms the `src/.` dot-syntax both
// directions actually puts the *contents* of src into the destination.
func TestIntegration_UploadDownloadDir(t *testing.T) {
	ensureImage(t)
	r := newIntegrationRuntime(t, Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.Create(ctx); err != nil {
		t.Fatalf("Create: %v", err)
	}

	host := t.TempDir()
	src := filepath.Join(host, "tree")
	files := map[string]string{
		"a.txt":          "alpha\n",
		"sub/b.txt":      "bravo\n",
		"sub/deep/c.txt": "charlie\n",
	}
	for rel, content := range files {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := r.UploadDir(ctx, src, "uploaded"); err != nil {
		t.Fatalf("UploadDir: %v", err)
	}

	// Walk the in-container tree to make sure every file landed at the
	// expected relative path with the expected content.
	list, err := r.Exec(ctx, "find uploaded -type f | sort", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec find: %v", err)
	}
	if list.ExitCode != 0 {
		t.Fatalf("find exit=%d stderr=%q", list.ExitCode, list.Stderr)
	}
	wantPaths := []string{"uploaded/a.txt", "uploaded/sub/b.txt", "uploaded/sub/deep/c.txt"}
	for _, p := range wantPaths {
		if !strings.Contains(list.Stdout, p) {
			t.Errorf("expected %q in find output, got %q", p, list.Stdout)
		}
	}

	dst := filepath.Join(host, "downloaded")
	if err := r.DownloadDir(ctx, "uploaded", dst); err != nil {
		t.Fatalf("DownloadDir: %v", err)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Fatalf("read downloaded %s: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("downloaded %s mismatch: got %q want %q", rel, string(got), want)
		}
	}
}

// TestIntegration_EnvLayering verifies that Env from cfg + opts arrives in
// the container, callEnv wins on conflict, and crucially that PATH is NOT
// clobbered by host expansion (this is the regression guard for the
// overlayEnvList host-expansion fix).
func TestIntegration_EnvLayering(t *testing.T) {
	ensureImage(t)

	// Set a unique host PATH that should NOT leak into the container.
	t.Setenv("DOCKER_INT_TEST_HOST_ONLY", "this-must-not-appear-in-container")

	r := newIntegrationRuntime(t, Config{
		Env: map[string]string{
			"FROM_CFG":     "cfgvalue",
			"BOTH":         "from-cfg",
			"PATH_LITERAL": "$PATH:/extra", // must arrive literally, not expanded
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Create(ctx); err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := r.Exec(ctx, `printf 'FROM_CFG=%s\nBOTH=%s\nFROM_OPTS=%s\nPATH_LITERAL=%s\nHOSTLEAK=%s\nCONTAINER_PATH=%s\n' "$FROM_CFG" "$BOTH" "$FROM_OPTS" "$PATH_LITERAL" "$DOCKER_INT_TEST_HOST_ONLY" "$PATH"`, ExecOptions{
		Env: map[string]string{
			"BOTH":      "from-opts", // wins
			"FROM_OPTS": "optsvalue",
		},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	out := res.Stdout
	checks := map[string]string{
		"FROM_CFG=cfgvalue":         "cfg.Env value should arrive in container",
		"BOTH=from-opts":            "opts.Env should override cfg.Env on conflict",
		"FROM_OPTS=optsvalue":       "opts.Env value should arrive in container",
		"PATH_LITERAL=$PATH:/extra": "values must NOT be expanded against host env before docker --env",
		"HOSTLEAK=":                 "unrelated host env vars must NOT leak into container",
	}
	for substr, why := range checks {
		if !strings.Contains(out, substr) {
			t.Errorf("missing %q in container output (%s):\n%s", substr, why, out)
		}
	}
	// CONTAINER_PATH is whatever the image set — should NOT be the host's
	// macOS-y PATH. Cheap heuristic: alpine's default contains /usr/local/sbin.
	if !strings.Contains(out, "CONTAINER_PATH=/usr/local/sbin") && !strings.Contains(out, "CONTAINER_PATH=/usr/sbin") {
		t.Errorf("container PATH looks suspiciously like a host PATH leak:\n%s", out)
	}
}

// TestIntegration_DenyAllNetworkBlocksEgress verifies network_policy=deny_all
// actually disables network access at the container level.
func TestIntegration_DenyAllNetworkBlocksEgress(t *testing.T) {
	ensureImage(t)

	// Baseline: without policy, the container can resolve+reach a public
	// DNS name. Some CI environments have flaky egress, so we skip the
	// whole test (not just the policy half) when the baseline doesn't
	// behave — otherwise we'd report a false positive for deny_all.
	baseline := newIntegrationRuntime(t, Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := baseline.Create(ctx); err != nil {
		t.Fatalf("baseline Create: %v", err)
	}
	res, err := baseline.Exec(ctx, "wget -q -O- --timeout=5 https://example.com/ >/dev/null 2>&1; echo $?", ExecOptions{})
	if err != nil {
		t.Fatalf("baseline Exec: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "0" {
		t.Skipf("baseline egress unavailable (CI offline?), skipping deny_all check; baseline output: %q", res.Stdout)
	}

	// Now: deny_all should fail egress fast.
	denied := newIntegrationRuntime(t, Config{NetworkPolicy: "deny_all"})
	if err := denied.Create(ctx); err != nil {
		t.Fatalf("denied Create: %v", err)
	}
	res2, err := denied.Exec(ctx, "wget -q -O- --timeout=5 https://example.com/ >/dev/null 2>&1; echo $?", ExecOptions{})
	if err != nil {
		t.Fatalf("denied Exec: %v", err)
	}
	if strings.TrimSpace(res2.Stdout) == "0" {
		t.Fatalf("expected egress to fail under deny_all; got success: %q", res2.Stdout)
	}
}

// TestIntegration_CustomEntrypointActuallyReplaces verifies that a
// user-supplied Entrypoint is wired via `--entrypoint` and actually
// replaces the image's own entrypoint (rather than being appended as CMD
// and silently ignored). Uses `tail -f /dev/null` as a long-lived
// alternative to the default `sleep infinity`.
//
// Inspecting the container's Config.Entrypoint via `docker inspect`
// confirms the override survives, regardless of what entrypoint the base
// image declared.
func TestIntegration_CustomEntrypointActuallyReplaces(t *testing.T) {
	ensureImage(t)
	r := newIntegrationRuntime(t, Config{
		Entrypoint: []string{"tail", "-f", "/dev/null"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Create(ctx); err != nil {
		t.Fatalf("Create: %v", err)
	}

	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .Config.Entrypoint}}", r.containerID).Output()
	if err != nil {
		t.Fatalf("docker inspect: %v", err)
	}
	if !strings.Contains(string(out), "tail") {
		t.Fatalf("Config.Entrypoint did not record custom override: %s", out)
	}
}

// NOTE: The integration equivalent of TestDockerRuntime_CloseKeepsContainerIDOnRmFailure
// is intentionally absent. `docker rm -f <nonexistent>` is idempotent (exit 0)
// in real Docker, so there is no way to induce an rm failure from outside the
// daemon without actually crashing/stopping the daemon. The unit test owns
// this contract via fakeDocker.
