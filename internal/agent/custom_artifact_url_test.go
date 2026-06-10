package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

// TestCustomAgent_CollectArtifacts_URLDownloaded exercises the happy path: a
// declared artifacts.files[].url is fetched and written into the artifact dir,
// then registered in GeneratedFiles with the served content.
func TestCustomAgent_CollectArtifacts_URLDownloaded(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "remote-body")
	}))
	defer srv.Close()

	rt := newCustomTestRuntime(t)
	artifactDir := t.TempDir()
	ag := customLocalAgent(&config.CustomEngineConfig{Transport: "http"})
	artifacts := &SessionArtifacts{
		Files: []ArtifactFile{{Name: "remote-report.html", URL: srv.URL}},
	}
	ag.collectArtifacts(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, artifacts)

	if !containsBasename(artifacts.GeneratedFiles, "remote-report.html") {
		t.Fatalf("generated_files = %v, want the downloaded artifact registered", artifacts.GeneratedFiles)
	}
	data, err := os.ReadFile(filepath.Join(artifactDir, "remote-report.html"))
	if err != nil || string(data) != "remote-body" {
		t.Fatalf("downloaded artifact = %q (err %v), want remote-body", data, err)
	}
}

// TestCustomAgent_RunHTTP_URLArtifactNon2xxSucceeds proves artifact download is
// best-effort end to end: the engine returns a successful SessionResult that
// declares a url pointing at a 500 endpoint; the run still succeeds and the
// artifact is simply skipped.
func TestCustomAgent_RunHTTP_URLArtifactNon2xxSucceeds(t *testing.T) {
	t.Parallel()
	artSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	defer artSrv.Close()

	runSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"exit_code":0,"final_message":"ok","artifacts":{"files":[{"name":"a.txt","url":%q}]}}`,
			artSrv.URL,
		))
	}))
	defer runSrv.Close()

	rt := newCustomTestRuntime(t)
	artifactDir := t.TempDir()

	out := captureStdout(t, func() {
		res, err := httpAgent(httpEngine(runSrv.URL), "").Run(
			context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, userMessages())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.ExitCode != 0 || res.FinalMessage != "ok" {
			t.Fatalf("unexpected result: %#v", res)
		}
		if containsBasename(res.Artifacts.GeneratedFiles, "a.txt") {
			t.Fatalf("generated_files = %v, want the failed download skipped", res.Artifacts.GeneratedFiles)
		}
	})
	if !strings.Contains(out, "returned status 500") {
		t.Fatalf("logs = %q, want a non-2xx warning", out)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("a.txt should not have been written (stat err = %v)", err)
	}
}

// TestCustomAgent_CollectArtifacts_URLUnreachableSkipped covers a transport
// error: the url's host is unreachable (server already closed). The artifact is
// dropped with a warning and the run is unaffected.
func TestCustomAgent_CollectArtifacts_URLUnreachableSkipped(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL
	srv.Close() // nothing listens on deadURL now

	rt := newCustomTestRuntime(t)
	artifactDir := t.TempDir()
	ag := customLocalAgent(&config.CustomEngineConfig{Transport: "http"})
	artifacts := &SessionArtifacts{Files: []ArtifactFile{{Name: "gone.txt", URL: deadURL}}}

	out := captureStdout(t, func() {
		ag.collectArtifacts(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, artifacts)
	})
	if containsBasename(artifacts.GeneratedFiles, "gone.txt") {
		t.Fatalf("generated_files = %v, want the unreachable url skipped", artifacts.GeneratedFiles)
	}
	if !strings.Contains(out, "cannot download artifact") {
		t.Fatalf("logs = %q, want a download-failure warning", out)
	}
}

// TestCustomAgent_CollectArtifacts_URLNonHTTPSchemeSkipped rejects a non-http(s)
// scheme (e.g. file://) before any request is made.
func TestCustomAgent_CollectArtifacts_URLNonHTTPSchemeSkipped(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	artifactDir := t.TempDir()
	ag := customLocalAgent(&config.CustomEngineConfig{Transport: "http"})
	artifacts := &SessionArtifacts{Files: []ArtifactFile{{Name: "p.txt", URL: "file:///etc/passwd"}}}

	out := captureStdout(t, func() {
		ag.collectArtifacts(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, artifacts)
	})
	if containsBasename(artifacts.GeneratedFiles, "p.txt") {
		t.Fatalf("generated_files = %v, want the file:// url skipped", artifacts.GeneratedFiles)
	}
	if !strings.Contains(out, "is not http(s)") {
		t.Fatalf("logs = %q, want a scheme-rejection warning", out)
	}
}

// TestCustomAgent_CollectArtifacts_URLSizeCapEnforced lowers the download cap so
// an over-limit body is rejected rather than written into the report.
func TestCustomAgent_CollectArtifacts_URLSizeCapEnforced(t *testing.T) {
	// Not parallel: mutates the package-level cap.
	orig := maxArtifactDownloadBytes
	maxArtifactDownloadBytes = 8
	defer func() { maxArtifactDownloadBytes = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("z", 64)) // 64 > 8-byte cap
	}))
	defer srv.Close()

	rt := newCustomTestRuntime(t)
	artifactDir := t.TempDir()
	ag := customLocalAgent(&config.CustomEngineConfig{Transport: "http"})
	artifacts := &SessionArtifacts{Files: []ArtifactFile{{Name: "big.bin", URL: srv.URL}}}

	out := captureStdout(t, func() {
		ag.collectArtifacts(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, artifacts)
	})
	if containsBasename(artifacts.GeneratedFiles, "big.bin") {
		t.Fatalf("generated_files = %v, want the oversize download dropped", artifacts.GeneratedFiles)
	}
	if !strings.Contains(out, "exceeds download size limit") {
		t.Fatalf("logs = %q, want a size-cap warning", out)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, "big.bin")); !os.IsNotExist(err) {
		t.Fatalf("big.bin should not have been written (stat err = %v)", err)
	}
}

// TestCustomAgent_CollectArtifacts_URLWithAPIKeyRefused ensures a url that
// embeds the configured API key is refused before any request is made, so the
// key is never transmitted to the engine-declared endpoint, and the refusal
// warning does not leak the key.
func TestCustomAgent_CollectArtifacts_URLWithAPIKeyRefused(t *testing.T) {
	t.Parallel()
	const secret = "sk-super-secret-token-value"
	rt := newCustomTestRuntime(t)
	artifactDir := t.TempDir()
	ag := NewCustomAgent(Config{Name: "my-agent", APIKey: secret, Custom: &config.CustomEngineConfig{Transport: "http"}})
	artifacts := &SessionArtifacts{
		Files: []ArtifactFile{{Name: "x.txt", URL: "http://example.com/?token=" + secret}},
	}

	out := captureStdout(t, func() {
		ag.collectArtifacts(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, artifacts)
	})
	if containsBasename(artifacts.GeneratedFiles, "x.txt") {
		t.Fatalf("generated_files = %v, want the api-key url refused", artifacts.GeneratedFiles)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("logs leaked the api key: %q", out)
	}
	if !strings.Contains(out, "embeds the API key") {
		t.Fatalf("logs = %q, want an api-key refusal warning", out)
	}
}

// TestCustomAgent_CollectArtifacts_URLSecretMaskedInLog ensures an
// engine.custom.env secret a user embeds in the artifact url (which, unlike the
// api_key, is not refused up front) is scrubbed from the transport-error
// warning, which net/http builds with the full request URL.
func TestCustomAgent_CollectArtifacts_URLSecretMaskedInLog(t *testing.T) {
	// Not parallel: relies on the shared log-capture mutex via captureStdout.
	const secret = "env-super-secret-token-value"
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL + "/?token=" + secret
	srv.Close() // force a transport error whose message embeds the url

	rt := newCustomTestRuntime(t)
	artifactDir := t.TempDir()
	ag := NewCustomAgent(Config{Name: "my-agent", Custom: &config.CustomEngineConfig{
		Transport: "http",
		Env:       map[string]string{"MY_TOKEN": secret},
	}})
	artifacts := &SessionArtifacts{Files: []ArtifactFile{{Name: "x.txt", URL: deadURL}}}

	out := captureStdout(t, func() {
		ag.collectArtifacts(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, artifacts)
	})
	if strings.Contains(out, secret) {
		t.Fatalf("logs leaked the env secret: %q", out)
	}
	if !strings.Contains(out, "***REDACTED***") {
		t.Fatalf("logs = %q, want the masked url in the warning", out)
	}
}
