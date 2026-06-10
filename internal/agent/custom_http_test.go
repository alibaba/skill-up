package agent

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/config"
)

func httpAgent(custom *config.CustomEngineConfig, apiKey string) *CustomAgent {
	return NewCustomAgent(Config{Name: "my-agent", APIKey: apiKey, Custom: custom})
}

func httpEngine(url string) *config.CustomEngineConfig {
	return &config.CustomEngineConfig{
		Transport: "http",
		HTTP:      &config.CustomHTTPConfig{URL: url},
	}
}

func TestCustomAgent_RunHTTP_SessionResult(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"done","turns":3}`)
	}))
	defer srv.Close()

	rt := newCustomTestRuntime(t)
	res, err := httpAgent(httpEngine(srv.URL), "").Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 || res.FinalMessage != "done" || res.Turns != 3 {
		t.Fatalf("unexpected result: %#v", res)
	}
}

func TestCustomAgent_RunHTTP_TextFormat(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "plain http answer")
	}))
	defer srv.Close()

	custom := httpEngine(srv.URL)
	custom.ResponseFormat = "text"
	rt := newCustomTestRuntime(t)
	res, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage != "plain http answer" {
		t.Fatalf("final_message = %q, want plain http answer", res.FinalMessage)
	}
}

func TestCustomAgent_RunHTTP_NonZeroStatusFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	defer srv.Close()

	rt := newCustomTestRuntime(t)
	res, err := httpAgent(httpEngine(srv.URL), "").Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil {
		t.Fatal("expected error for non-2xx status")
	}
	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero for http 500")
	}
}

func TestCustomAgent_RunHTTP_DefaultBodyIsSessionInput(t *testing.T) {
	t.Parallel()
	var got struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"ok"}`)
	}))
	defer srv.Close()

	rt := newCustomTestRuntime(t)
	if _, err := httpAgent(httpEngine(srv.URL), "").Run(context.Background(), rt, ExecOptions{}, userMessages()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "review the diff" {
		t.Fatalf("default body did not carry SessionInput.messages: %#v", got.Messages)
	}
}

func TestCustomAgent_RunHTTP_RequestBodyInjectsSessionInputStructure(t *testing.T) {
	t.Parallel()
	// payload references ${session_input}; it must arrive as a JSON object, not
	// a quoted string. tag is a plain string leaf.
	var raw map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &raw)
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"ok"}`)
	}))
	defer srv.Close()

	custom := httpEngine(srv.URL)
	custom.HTTP.RequestBody = map[string]any{
		"payload": "${session_input}",
		"tag":     "review",
	}
	rt := newCustomTestRuntime(t)
	if _, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(raw["payload"]) == 0 || raw["payload"][0] != '{' {
		t.Fatalf("payload was not injected as a JSON object: %s", raw["payload"])
	}
	if string(raw["tag"]) != `"review"` {
		t.Fatalf("tag = %s, want \"review\"", raw["tag"])
	}
}

func TestCustomAgent_RunHTTP_ScalarRequestBodyIsSessionInput(t *testing.T) {
	t.Parallel()
	// The documented top-level form `request_body: ${session_input}` (a scalar,
	// not a map) must POST the SessionInput JSON object, not a quoted string.
	var probe struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &probe)
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"ok"}`)
	}))
	defer srv.Close()

	custom := httpEngine(srv.URL)
	custom.HTTP.RequestBody = "${session_input}"
	rt := newCustomTestRuntime(t)
	if _, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(probe.Messages) != 1 || probe.Messages[0].Content != "review the diff" {
		t.Fatalf("scalar ${session_input} body did not carry the SessionInput: %#v", probe.Messages)
	}
}

func TestCustomAgent_RunHTTP_RequestBodyRendersKwargs(t *testing.T) {
	t.Parallel()
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"ok"}`)
	}))
	defer srv.Close()

	custom := httpEngine(srv.URL)
	custom.Kwargs = map[string]string{"profile": "strict"}
	custom.HTTP.RequestBody = map[string]any{"p": "${kwargs.profile}"}
	rt := newCustomTestRuntime(t)
	if _, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got["p"] != "strict" {
		t.Fatalf("rendered kwargs in body = %q, want strict", got["p"])
	}
}

func TestCustomAgent_RunHTTP_RendersHeaderWithAPIKey(t *testing.T) {
	t.Parallel()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"ok"}`)
	}))
	defer srv.Close()

	custom := httpEngine(srv.URL)
	custom.HTTP.Headers = map[string]string{"Authorization": "Bearer ${api_key}"}
	rt := newCustomTestRuntime(t)
	if _, err := httpAgent(custom, "secret-key-123456").Run(context.Background(), rt, ExecOptions{}, userMessages()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if auth != "Bearer secret-key-123456" {
		t.Fatalf("Authorization header = %q, want rendered api_key", auth)
	}
}

func TestCustomAgent_RunHTTP_RejectsAPIKeyInURL(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"ok"}`)
	}))
	defer srv.Close()

	custom := httpEngine(srv.URL + "?token=${api_key}")
	rt := newCustomTestRuntime(t)
	_, err := httpAgent(custom, "sk-secret-9999").Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "renders the API key into the URL") {
		t.Fatalf("expected api-key-in-url rejection, got: %v", err)
	}
}

func TestCustomAgent_RunHTTP_RejectsNonPostMethod(t *testing.T) {
	t.Parallel()
	// Mirrors validateCustomHTTP, covering the --engine override path that
	// bypasses config validation.
	custom := httpEngine("http://127.0.0.1:0")
	custom.HTTP.Method = "GET"
	rt := newCustomTestRuntime(t)
	_, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "method must be POST") {
		t.Fatalf("expected non-POST rejection, got: %v", err)
	}
}

func writeWorkspaceFile(t *testing.T, rt Runtime, rel, content string) {
	t.Helper()
	p := filepath.Join(rt.Workspace(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// captureMultipart parses a multipart request into the payload field and a
// filename -> content map for the `files` parts.
func captureMultipart(t *testing.T, r *http.Request) (payload string, files map[string]string) {
	t.Helper()
	mr, err := r.MultipartReader()
	if err != nil {
		t.Fatalf("not a multipart request: %v", err)
	}
	files = map[string]string{}
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			t.Fatalf("read multipart part: %v", perr)
		}
		data, _ := io.ReadAll(part)
		// part.FileName() returns filepath.Base for receiver-side safety; read
		// the raw disposition filename to verify the full workspace-relative
		// path is on the wire.
		_, params, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		switch part.FormName() {
		case "payload":
			payload = string(data)
		case "files":
			files[params["filename"]] = string(data)
		}
	}
	return payload, files
}

func TestCustomAgent_RunHTTP_MultipartExactFile(t *testing.T) {
	t.Parallel()
	var (
		contentType string
		payload     string
		files       map[string]string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		payload, files = captureMultipart(t, r)
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"ok"}`)
	}))
	defer srv.Close()

	rt := newCustomTestRuntime(t)
	writeWorkspaceFile(t, rt, "diff.patch", "DIFF-CONTENT")
	custom := httpEngine(srv.URL)
	custom.HTTP.Files = []config.CustomHTTPFile{{Path: "diff.patch"}}
	if _, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		t.Fatalf("Content-Type = %q, want multipart/form-data", contentType)
	}
	if files["diff.patch"] != "DIFF-CONTENT" {
		t.Fatalf("file part = %#v, want diff.patch -> DIFF-CONTENT", files)
	}
	if !strings.Contains(payload, "review the diff") {
		t.Fatalf("payload field did not carry the SessionInput: %q", payload)
	}
}

func TestCustomAgent_RunHTTP_MultipartGlobExcludesDirsAndNonMatches(t *testing.T) {
	t.Parallel()
	var files map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, files = captureMultipart(t, r)
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"ok"}`)
	}))
	defer srv.Close()

	rt := newCustomTestRuntime(t)
	writeWorkspaceFile(t, rt, "src/a.go", "A")
	writeWorkspaceFile(t, rt, "src/b.go", "B")
	writeWorkspaceFile(t, rt, "README.md", "readme")
	custom := httpEngine(srv.URL)
	custom.HTTP.Files = []config.CustomHTTPFile{{Path: "src/*.go"}}
	if _, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(files) != 2 || files["src/a.go"] != "A" || files["src/b.go"] != "B" {
		t.Fatalf("glob upload = %#v, want exactly src/a.go and src/b.go", files)
	}
	if _, ok := files["README.md"]; ok {
		t.Fatalf("non-matching README.md should not be uploaded: %#v", files)
	}
	if _, ok := files["src"]; ok {
		t.Fatalf("directory src should never be uploaded as a part: %#v", files)
	}
}

func TestCustomAgent_RunHTTP_RequiredFileMissingErrors(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	custom := httpEngine("http://127.0.0.1:0")
	custom.HTTP.Files = []config.CustomHTTPFile{{Path: "nope.txt"}} // required defaults to true
	_, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "matched no files") {
		t.Fatalf("expected required-missing error, got: %v", err)
	}
}

func TestCustomAgent_RunHTTP_OptionalMissingSkipped(t *testing.T) {
	t.Parallel()
	var files map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, files = captureMultipart(t, r)
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"ok"}`)
	}))
	defer srv.Close()

	rt := newCustomTestRuntime(t)
	optional := false
	custom := httpEngine(srv.URL)
	custom.HTTP.Files = []config.CustomHTTPFile{{Path: "nope.txt", Required: &optional}}
	if _, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("optional missing file should be skipped, got parts: %#v", files)
	}
}

func TestCustomAgent_RunHTTP_MultipartNewlineFilenameStaysOneEntry(t *testing.T) {
	t.Parallel()
	// Regression: with newline-separated `find` output a filename containing a
	// newline would split into a bogus (possibly absolute) entry. -print0 keeps
	// it a single in-workspace entry; assert it uploads intact with no escape.
	var files map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, files = captureMultipart(t, r)
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"ok"}`)
	}))
	defer srv.Close()

	rt := newCustomTestRuntime(t)
	weird := "weird\n/etc/passwd"
	writeWorkspaceFile(t, rt, weird, "IN-WORKSPACE")
	custom := httpEngine(srv.URL)
	custom.HTTP.Files = []config.CustomHTTPFile{{Path: "**/*"}}
	if _, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Exactly one part (no newline split), carrying the in-workspace content,
	// and no separate off-workspace entry. The header encodes the newline, so
	// match on content rather than the exact (encoded) filename.
	if len(files) != 1 {
		t.Fatalf("newline filename should be a single upload entry, got: %#v", files)
	}
	for name, content := range files {
		if content != "IN-WORKSPACE" {
			t.Fatalf("uploaded content = %q, want IN-WORKSPACE", content)
		}
		if name == "/etc/passwd" {
			t.Fatalf("newline split produced an off-workspace entry: %q", name)
		}
	}
}

func TestCustomAgent_RunHTTP_RejectsUnsafeFilePath(t *testing.T) {
	t.Parallel()
	rt := newCustomTestRuntime(t)
	custom := httpEngine("http://127.0.0.1:0")
	custom.HTTP.Files = []config.CustomHTTPFile{{Path: "../escape.txt"}}
	_, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil || !strings.Contains(err.Error(), "relative path inside the workspace") {
		t.Fatalf("expected unsafe-path rejection, got: %v", err)
	}
}

func TestCustomAgent_RunHTTP_TimeoutFails(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release // hang past the 1s engine timeout
		_, _ = io.WriteString(w, `{"exit_code":0}`)
	}))
	// Defers run LIFO: close(release) first unblocks the handler, then
	// srv.Close() can drain it. The reverse order would deadlock Close.
	defer srv.Close()
	defer close(release)

	custom := httpEngine(srv.URL)
	custom.TimeoutSeconds = 1
	rt := newCustomTestRuntime(t)
	start := time.Now()
	_, err := httpAgent(custom, "").Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}

func TestCustomAgent_RunHTTP_MasksAPIKeyInResponse(t *testing.T) {
	t.Parallel()
	const key = "supersecret-abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"exit_code":0,"final_message":"leaked `+key+`"}`)
	}))
	defer srv.Close()

	rt := newCustomTestRuntime(t)
	res, err := httpAgent(httpEngine(srv.URL), key).Run(context.Background(), rt, ExecOptions{}, userMessages())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(res.FinalMessage, key) {
		t.Fatalf("api key not masked in final_message: %q", res.FinalMessage)
	}
}
