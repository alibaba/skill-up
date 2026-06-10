package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpTransport calls a remote (or local) HTTP agent service: it POSTs the
// SessionInput as a JSON body and parses the SessionResult from the response.
// It owns the http-specific concerns — URL/header/body rendering, the request
// body's JSON-structure injection, and status-to-outcome mapping. Shared
// session preparation and result assembly live on CustomAgent, reused via the
// back-reference.
//
// This transport currently supports JSON request/response only. Multipart file
// upload (engine.custom.http.files) and URL artifact download are follow-ups; a
// non-empty files list is rejected as not-yet-implemented.
type httpTransport struct {
	a *CustomAgent
}

// run renders the request from prep.vars, POSTs it, and maps the response onto
// a transportOutcome for CustomAgent.assembleResult. A setup failure (missing
// URL, secret in URL, unrenderable template, files configured) is returned as
// run's error so the run is abandoned; a transport/status failure is carried in
// transportOutcome.execErr so a partial SessionResult can still be assembled.
func (t *httpTransport) run(ctx context.Context, _ Runtime, _ ExecOptions, prep *customRunPrep) (*transportOutcome, error) {
	a := t.a
	custom := prep.custom
	// Guard against a nil/unsupported http block: a `--engine` override can
	// reach here unvalidated (config validation skips engine.custom for
	// built-in engine names).
	if custom.HTTP == nil || custom.HTTP.URL == "" {
		return nil, errors.New("engine.custom.http.url is required when transport is http")
	}
	if len(custom.HTTP.Files) > 0 {
		return nil, errors.New("engine.custom.http.files (file upload) is not yet implemented")
	}

	url, err := renderTemplate(custom.HTTP.URL, prep.vars)
	if err != nil {
		return nil, fmt.Errorf("render http.url: %w", err)
	}
	// A credential in the URL would leak into request logs, proxies, and
	// traces. The sanctioned channel for ${api_key} is a header or the request
	// body, so it is rejected only here.
	if a.containsAPIKey(url) {
		return nil, errSecretInURL()
	}

	// Only POST is supported. validateCustomHTTP enforces this at config load,
	// but a `--engine` override reaches here unvalidated, so re-check.
	if custom.HTTP.Method != "" && !strings.EqualFold(custom.HTTP.Method, http.MethodPost) {
		return nil, fmt.Errorf("engine.custom.http.method must be POST (got %q)", custom.HTTP.Method)
	}
	method := http.MethodPost

	body, err := t.buildRequestBody(prep)
	if err != nil {
		return nil, err
	}

	headers, err := renderTemplateMap(custom.HTTP.Headers, prep.vars)
	if err != nil {
		return nil, fmt.Errorf("render http.headers: %w", err)
	}

	// Enforce the custom timeout via a context deadline (mirrors the local
	// transport); the http.Client.Timeout is a backstop for the case where the
	// transport ignores ctx.
	reqCtx := ctx
	var timeout time.Duration
	if prep.timeoutSec > 0 {
		timeout = time.Duration(prep.timeoutSec) * time.Second
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build http request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	return t.doRequest(&http.Client{Timeout: timeout}, req), nil
}

// doRequest performs the request and maps the response onto a transportOutcome.
// It never returns a setup error — a transport/read failure or a non-2xx status
// is carried in the outcome's execErr so assembleResult can still salvage a
// partial SessionResult from the body.
func (t *httpTransport) doRequest(client *http.Client, req *http.Request) *transportOutcome {
	a := t.a
	// url is operator-supplied engine config (engine.custom.http.url); a dynamic
	// endpoint is the whole point of the http transport.
	resp, err := client.Do(req) //nolint:gosec // intentional operator-configured dynamic URL
	if err != nil {
		// The net/http error embeds the request URL; mask it like stderr so a
		// secret a user mistakenly put in the URL cannot leak into result.Error
		// / reports. (errors.New, not %w: the masked string is the payload and
		// the chain is not matched downstream.)
		return &transportOutcome{
			exitCode: 1,
			stderr:   a.maskAPIKey(err.Error()),
			execErr:  errors.New(a.maskAPIKey("custom engine http request failed: " + err.Error())),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	// Read one byte past the cap so an over-limit body surfaces a clear error
	// instead of silently truncating into an inscrutable JSON parse failure.
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBytes+1))
	if readErr != nil {
		return &transportOutcome{
			exitCode: 1,
			stderr:   a.maskAPIKey(readErr.Error()),
			execErr:  errors.New(a.maskAPIKey("read custom engine http response: " + readErr.Error())),
		}
	}
	if int64(len(respBody)) > maxHTTPResponseBytes {
		msg := fmt.Sprintf("custom engine http response exceeded %d bytes", maxHTTPResponseBytes)
		return &transportOutcome{exitCode: 1, stderr: msg, execErr: errors.New(msg)}
	}
	raw := string(respBody)

	// raw feeds session_result parsing; stdout is the text-format final-message
	// source. For http both are the response body (there is no separate stream).
	outcome := &transportOutcome{raw: raw, stdout: raw}
	// A non-2xx status is an Engine execution error per the contract; still
	// surface the body (it may carry a partial SessionResult) and let
	// assembleResult decide.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		outcome.exitCode = resp.StatusCode
		outcome.stderr = a.maskAPIKey(strings.TrimSpace(raw))
		outcome.execErr = fmt.Errorf("custom engine http status %d", resp.StatusCode)
	}
	return outcome
}

// buildRequestBody produces the JSON request body. When request_body is unset
// it defaults to the SessionInput JSON (equivalent to ${session_input});
// otherwise the configured structure is rendered (see renderBodyValue) and
// marshaled.
func (t *httpTransport) buildRequestBody(prep *customRunPrep) ([]byte, error) {
	rb := prep.custom.HTTP.RequestBody
	if rb == nil {
		return prep.sessJSON, nil
	}
	rendered, err := renderBodyValue(rb, prep.vars)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(rendered)
	if err != nil {
		return nil, fmt.Errorf("marshal http.request_body: %w", err)
	}
	return out, nil
}

// renderBodyValue resolves template references inside a request_body value. A
// string that is exactly ${session_input}/${messages}/${kwargs} is injected as
// the corresponding JSON structure (so it embeds as a JSON object/array, not a
// quoted string); any other string is rendered with the normal template
// substitution; maps and slices are walked recursively; other scalars pass
// through unchanged.
func renderBodyValue(v any, vars map[string]string) (any, error) {
	switch val := v.(type) {
	case string:
		if raw, ok := structuredInjection(val, vars); ok {
			return raw, nil
		}
		return renderTemplate(val, vars)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			r, err := renderBodyValue(item, vars)
			if err != nil {
				return nil, fmt.Errorf("request_body[%q]: %w", k, err)
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			r, err := renderBodyValue(item, vars)
			if err != nil {
				return nil, fmt.Errorf("request_body[%d]: %w", i, err)
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

// structuredInjection returns the JSON structure for the three aggregate
// template tokens when s is exactly one of them, so they embed as JSON values
// rather than strings. The backing vars entries are always valid JSON
// (completeTemplateVars marshals them).
func structuredInjection(s string, vars map[string]string) (json.RawMessage, bool) {
	switch s {
	case "${session_input}":
		return json.RawMessage(vars["session_input"]), true
	case "${messages}":
		return json.RawMessage(vars["messages"]), true
	case "${kwargs}":
		return json.RawMessage(vars["kwargs"]), true
	default:
		return nil, false
	}
}

// errSecretInURL mirrors errSecretInCommand for the http transport: a rendered
// URL that embeds the API key would expose it in request logs and traces.
func errSecretInURL() error {
	return errors.New(
		"engine.custom.http.url renders the API key into the URL, which would expose it in request logs and traces; reference ${api_key} from engine.custom.http.headers or request_body instead",
	)
}
