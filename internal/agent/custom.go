package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

const (
	customTransportLocal       = "local"
	customTransportHTTP        = "http"
	customResponseSessionJSON  = "session_result"
	customResponseText         = "text"
	customDefaultInputFile     = "inputs/messages.json"
	customDefaultOutputFile    = "outputs/session-result.json"
	customSessionResultMissing = "custom engine returned a result without exit_code"
	// customStaleClearedMarker is printed by the pre-run cleanup when it
	// actually deletes a stale output file.
	customStaleClearedMarker = "__skill_up_stale_output_cleared__"
	// customArtifactReadTimeout bounds the post-timeout artifact read, which
	// runs on a fresh context detached from the (possibly canceled) run context.
	customArtifactReadTimeout = 30 * time.Second
	// maxInlineArtifactBytes caps a single inline artifacts.files entry's
	// rendered size (after base64 decode). A misconfigured or malicious
	// custom engine that returns hundreds of MB of inline content per case
	// would otherwise thrash memory and the workspace.
	maxInlineArtifactBytes = 50 * 1024 * 1024
	// maxInlineArtifactTotal caps the sum of inline artifact sizes across a
	// single SessionResult.
	maxInlineArtifactTotal = 200 * 1024 * 1024
	// maxHTTPResponseBytes caps how much of an http transport response body is
	// read, so a misbehaving or malicious agent service cannot exhaust memory
	// with an unbounded SessionResult payload.
	maxHTTPResponseBytes = 64 * 1024 * 1024
	// customArtifactDownloadTimeout bounds a single best-effort url artifact
	// download so a slow or hung remote cannot stall report assembly.
	customArtifactDownloadTimeout = 60 * time.Second
)

// maxArtifactDownloadBytes caps how many bytes are read from a declared
// artifacts.files[].url before the download is abandoned. Sized larger than
// maxHTTPResponseBytes (a single JSON SessionResult) because a downloadable
// artifact may be a whole report or archive, while still bounding what a
// misbehaving engine can pull into the report directory. It is a var, not a
// const, only so the size-cap test can lower it without transferring hundreds
// of megabytes.
var maxArtifactDownloadBytes int64 = 256 * 1024 * 1024

// CustomAgent implements Agent for user-defined engines configured via
// engine.custom. The local transport is fully supported; the http transport
// supports JSON request/response, with multipart file upload still pending.
// Result artifacts declared via a `url` are downloaded for any transport. See
// docs/design/custom-engine.md.
//
// CustomAgent embeds BaseAgent directly (not CLIAgent) so it inherits only
// shared agent infrastructure — Name, credential helpers, exec-options merge,
// installSkillDefault. Run / Install / InstallMCP / InstallSkill / Check /
// CheckCredentials are all defined explicitly, so future additions to
// CLIAgent do not accidentally leak in with the wrong semantics for a custom
// engine.
type CustomAgent struct {
	BaseAgent
}

// NewCustomAgent creates a CustomAgent from a resolved agent Config. The
// caller must populate cfg.Custom; factory dispatch guarantees this.
func NewCustomAgent(cfg Config) *CustomAgent {
	return &CustomAgent{BaseAgent: NewBaseAgent(cfg)}
}

// Install is a no-op: a custom engine's command is provided and managed by the
// user, not installed by skill-up.
func (a *CustomAgent) Install(_ context.Context, _ Runtime) error { return nil }

// InstallMCP is a no-op for custom engines: a custom engine discovers MCP
// servers from the runtime environment itself. Declared servers are logged so
// an eval is not silently missing expected MCP wiring, but no installation is
// attempted (unlike the inherited CLIAgent.InstallMCP, which would error).
func (a *CustomAgent) InstallMCP(ctx context.Context, _ Runtime, mcpCfg runtime.MCPConfig) error {
	if len(mcpCfg.Servers) > 0 {
		logging.InfoContextf(ctx, "CustomAgent %q: %d MCP server(s) declared; a custom engine manages MCP itself, skipping installation", a.Name(), len(mcpCfg.Servers))
	}
	return nil
}

// InstallSkill installs the skill into the runtime under the configured
// SkillPath. A custom engine has no template-driven install command, so the
// default installer is sufficient.
func (a *CustomAgent) InstallSkill(ctx context.Context, rt Runtime, skillCfg runtime.SkillConfig) error {
	return a.installSkillDefault(ctx, rt, skillCfg)
}

// Check is a no-op for custom engines.
func (a *CustomAgent) Check(_ context.Context, _ Runtime) error { return nil }

// CheckCredentials is a no-op: a custom engine references credentials
// explicitly via ${api_key}, so skill-up does not pre-validate them.
func (a *CustomAgent) CheckCredentials(_ context.Context) error { return nil }

// customTransport executes a prepared custom-engine run and returns a uniform
// transportOutcome that the shared assembler (CustomAgent.assembleResult) maps
// to a *SessionResult. The local transport reads result files and the process
// exit; a future http transport will map an HTTP response body and status onto
// the same shape.
//
// run's returned error is a SETUP error raised before the engine executes (e.g.
// the command failed to render, or the input file could not be written): the
// run is abandoned and CustomAgent.Run reports an errorResult. An
// execution-phase failure — the engine ran but exited non-zero, timed out, or
// kept stdout open past WaitDelay — is carried in transportOutcome.execErr
// instead, so a partial result can still be assembled.
type customTransport interface {
	run(ctx context.Context, rt Runtime, opts ExecOptions, prep *customRunPrep) (*transportOutcome, error)
}

// customRunPrep is the transport-agnostic state CustomAgent.Run builds once,
// before dispatching to a transport. Every transport consumes the same prepared
// session input and template variable set. start is captured at preparation
// time so the reported duration spans preparation through result assembly.
type customRunPrep struct {
	custom     *config.CustomEngineConfig
	vars       map[string]string
	sessJSON   []byte
	timeoutSec int
	messages   []transcript.Message
	start      time.Time
}

// transportOutcome is what a transport hands back to the shared assembler.
// exitCode and stderr are the process exit code and stderr for the local
// transport (a future http transport maps HTTP status and error body onto
// them). raw is the result payload graded for session_result responses; stdout
// is the text-format final-message source (the local transport keeps them
// distinct so a bookkeeping output_file is never graded as a text answer).
// frameworkFiles are the framework-written paths (input/output files) to record
// in GeneratedFiles so the workspace-diff collector excludes them.
type transportOutcome struct {
	raw            string
	stdout         string
	exitCode       int
	stderr         string
	execErr        error
	frameworkFiles []string
}

// Run executes the custom engine for a single case: it builds the shared
// session input, selects the transport, runs it, and assembles the result.
func (a *CustomAgent) Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (*SessionResult, error) {
	custom := a.Cfg.Custom
	if custom == nil {
		return a.errorResult(0), errors.New("custom engine config is missing")
	}
	transport, err := a.selectTransport(custom)
	if err != nil {
		return a.errorResult(0), err
	}
	prep, err := a.prepareRun(rt, opts, messages, custom)
	if err != nil {
		return a.errorResult(0), err
	}
	outcome, err := transport.run(ctx, rt, opts, prep)
	if err != nil {
		return a.errorResult(0), err
	}
	return a.assembleResult(ctx, rt, opts, prep, outcome)
}

// selectTransport maps engine.custom.transport to its implementation: the
// local transport runs a command in the runtime, the http transport calls a
// remote agent service. Both produce a transportOutcome that CustomAgent.Run
// hands to the shared assembleResult.
func (a *CustomAgent) selectTransport(custom *config.CustomEngineConfig) (customTransport, error) {
	switch custom.Transport {
	case customTransportLocal:
		return &localTransport{a: a}, nil
	case customTransportHTTP:
		return &httpTransport{a: a}, nil
	default:
		return nil, fmt.Errorf("custom engine transport %q is not supported", custom.Transport)
	}
}

// prepareRun builds the transport-agnostic run state: the effective timeout, the
// full template variable set, and the marshaled SessionInput. start is captured
// here so the reported duration spans preparation through result assembly,
// matching the pre-refactor behavior.
func (a *CustomAgent) prepareRun(rt Runtime, opts ExecOptions, messages []transcript.Message, custom *config.CustomEngineConfig) (*customRunPrep, error) {
	start := time.Now()
	// engine.custom.timeout_seconds is the per-engine deadline; opts.TimeoutSec
	// is the outer case deadline (the evaluator already wraps ctx with it).
	// When both are positive, the effective deadline is the smaller of the two
	// — exceeding the case deadline would have the case context kill the process
	// while ${timeout_seconds} and SessionInput still advertised a longer
	// timeout to the agent, producing premature termination with inconsistent
	// metadata. Take the min so the value handed to the agent reflects the real
	// wall-clock budget.
	timeoutSec := custom.TimeoutSeconds
	switch {
	case timeoutSec <= 0:
		timeoutSec = opts.TimeoutSec
	case opts.TimeoutSec > 0 && opts.TimeoutSec < timeoutSec:
		timeoutSec = opts.TimeoutSec
	}

	// Build the full template variable set first: kwargs may reference built-in
	// variables (e.g. ${case_id}), and the I/O path templates may in turn
	// reference ${kwargs.<key>}, so kwargs must be resolved before paths.
	baseVars := a.buildBaseVars(rt, opts, messages, timeoutSec)

	renderedKwargs, err := renderTemplateMap(custom.Kwargs, baseVars)
	if err != nil {
		return nil, fmt.Errorf("render custom.kwargs: %w", err)
	}

	sess := a.buildSessionInput(rt, opts, messages, renderedKwargs, timeoutSec)
	// Marshal the session input once and share the bytes between the template
	// expansion (${session_input}) and the on-disk input file, avoiding a second
	// copy of what for long transcripts can be tens of MB per case.
	sessJSON, err := json.Marshal(sess)
	if err != nil {
		return nil, fmt.Errorf("marshal session input: %w", err)
	}
	vars, err := a.completeTemplateVars(baseVars, renderedKwargs, sess, sessJSON)
	if err != nil {
		return nil, err
	}

	return &customRunPrep{
		custom:     custom,
		vars:       vars,
		sessJSON:   sessJSON,
		timeoutSec: timeoutSec,
		messages:   messages,
		start:      start,
	}, nil
}

// clearStaleOutputFile removes a pre-existing output file before the run and
// reports whether one was actually deleted, so a fixture/previous-run file is
// not parsed as this invocation's result and the deletion can be excluded from
// workspace diffs.
func (a *CustomAgent) clearStaleOutputFile(ctx context.Context, rt Runtime, outputFile string) bool {
	// Re-resolve symlinks now: resolveCustomIOFiles validated the path
	// pre-run, but a not-yet-existing parent (e.g. outputs/new/result.json)
	// could be planted as an outward symlink by a concurrent case or any
	// other process sharing the workspace between then and now. Without
	// this re-check, the `rm -f` below would follow the planted link out
	// of the workspace.
	safe, err := workspacePath(rt, outputFile)
	if err != nil {
		logging.WarnContextf(ctx, "CustomAgent: refusing to clear output file %s before run: %v", outputFile, err)
		return false
	}
	outputFile = safe
	q := shellQuote(outputFile)
	cmd := "if [ -e " + q + " ]; then rm -f -- " + q + " && printf %s " + shellQuote(customStaleClearedMarker) + "; fi"
	result, err := rt.Exec(ctx, cmd, ExecOptions{})
	if err != nil {
		logging.DebugContextf(ctx, "CustomAgent: could not clear stale output file %s: %v", outputFile, err)
		return false
	}
	return strings.Contains(result.Stdout, customStaleClearedMarker)
}

// buildLocalExec renders the command, args and exec options for the local run.
func (a *CustomAgent) buildLocalExec(ctx context.Context, rt Runtime, opts ExecOptions, custom *config.CustomEngineConfig, vars map[string]string, timeoutSec int) (string, ExecOptions, error) {
	local := custom.Local
	command, err := renderTemplate(local.Command, vars)
	if err != nil {
		return "", ExecOptions{}, fmt.Errorf("render local.command: %w", err)
	}
	if a.containsAPIKey(command) {
		return "", ExecOptions{}, errSecretInCommand("local.command")
	}
	parts := []string{shellQuote(command)}
	for i, raw := range local.Args {
		arg, rErr := renderTemplate(raw, vars)
		if rErr != nil {
			return "", ExecOptions{}, fmt.Errorf("render local.args[%d]: %w", i, rErr)
		}
		if a.containsAPIKey(arg) {
			return "", ExecOptions{}, errSecretInCommand(fmt.Sprintf("local.args[%d]", i))
		}
		parts = append(parts, shellQuote(arg))
	}

	cwd := rt.Workspace()
	if local.Cwd != "" {
		rendered, rErr := renderTemplate(local.Cwd, vars)
		if rErr != nil {
			return "", ExecOptions{}, fmt.Errorf("render local.cwd: %w", rErr)
		}
		// Resolve and confine cwd to the runtime workspace so local transport
		// behaves consistently across runtimes (NoneRuntime would otherwise
		// pass it through relative to the skill-up process) and so a
		// template-driven cwd cannot escape the workspace.
		resolved, wpErr := workspacePath(rt, rendered)
		if wpErr != nil {
			return "", ExecOptions{}, fmt.Errorf("local.cwd: %w", wpErr)
		}
		cwd = resolved
	}

	envVars, err := renderTemplateMap(custom.Env, vars)
	if err != nil {
		return "", ExecOptions{}, fmt.Errorf("render custom.env: %w", err)
	}

	execOpts := ExecOptions{Cwd: cwd, TimeoutSec: timeoutSec, ArtifactDir: opts.ArtifactDir}
	execOpts = a.mergeExecOptionsEnv(ctx, execOpts, envVars, a.buildAgentObservabilityAttrs(nil))
	return strings.Join(parts, " "), execOpts, nil
}

// assembleResult maps a transport's outcome onto a *SessionResult, applying the
// shared exit-code and error semantics. It is transport-agnostic: the local and
// (future) http transports differ only in how they produce the
// transportOutcome. The raw-result read happens inside the transport, so this
// shared step never touches the output file or the process directly.
func (a *CustomAgent) assembleResult(ctx context.Context, rt Runtime, opts ExecOptions, prep *customRunPrep, o *transportOutcome) (*SessionResult, error) {
	durationMs := time.Since(prep.start).Milliseconds()

	res, parseErr := a.buildResult(ctx, rt, opts, prep.custom, o.raw, o.stdout, o.exitCode, o.stderr, durationMs, prep.messages)

	if o.execErr != nil {
		switch {
		case parseErr != nil:
			// Nothing usable was produced before the failure.
			res = a.errorResult(o.exitCode)
			res.DurationMs = durationMs
			res.Transcript = minimalCustomTranscript(prep.messages, "")
		case res.ExitCode == 0:
			// The run was interrupted; a parsed exit_code 0 must not let the
			// evaluator treat an interrupted run as a success.
			res.ExitCode = o.exitCode
			if res.ExitCode == 0 {
				res.ExitCode = 1
			}
		}
		if res.Stderr == "" {
			res.Stderr = a.maskAPIKey(o.stderr)
		} else {
			res.Stderr = a.maskAPIKey(res.Stderr)
		}
		a.appendFrameworkFiles(res, o.frameworkFiles)
		return res, fmt.Errorf("custom engine run failed: %w", o.execErr)
	}

	a.appendFrameworkFiles(res, o.frameworkFiles)
	// A non-zero process exit is a failed run even when the engine's JSON
	// reports exit_code 0 (e.g. a wrapper that crashed after printing output)
	// or never emitted parseable JSON at all. Reflect the real process
	// exit/stderr so reports surface the actual command failure.
	if o.exitCode != 0 {
		res.ExitCode = o.exitCode
		if res.Stderr == "" {
			res.Stderr = a.maskAPIKey(o.stderr)
		} else {
			res.Stderr = a.maskAPIKey(res.Stderr)
		}
		return res, fmt.Errorf("custom engine command exited %d: %s", o.exitCode, a.maskAPIKey(o.stderr))
	}
	if parseErr != nil {
		res.Stderr = a.maskAPIKey(res.Stderr)
		return res, parseErr
	}
	if res.ExitCode != 0 {
		res.Stderr = a.maskAPIKey(res.Stderr)
		return res, fmt.Errorf("custom engine run failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	res.Stderr = a.maskAPIKey(res.Stderr)
	return res, nil
}

// buildResult parses raw engine output into a SessionResult according to the
// configured response_format. The text format never errors and always grades
// stdout (never the output file); session_result returns a parse error when
// the payload is missing or malformed.
func (a *CustomAgent) buildResult(ctx context.Context, rt Runtime, opts ExecOptions, custom *config.CustomEngineConfig, raw, stdout string, exitCode int, stderr string, durationMs int64, messages []transcript.Message) (*SessionResult, error) {
	if customResponseFormat(custom) == customResponseText {
		// Per the contract, text responses come from stdout; an output file
		// produced for bookkeeping must not be graded as the final answer.
		// Mask before constructing the transcript so the assistant-reply
		// message embedded in it never carries an unmasked value.
		finalMsg := a.maskAPIKey(strings.TrimSpace(stdout))
		return &SessionResult{
			Engine:       a.Name(),
			ExitCode:     exitCode,
			DurationMs:   durationMs,
			FinalMessage: finalMsg,
			Stderr:       a.maskAPIKey(stderr),
			Transcript:   minimalCustomTranscript(messages, finalMsg),
			Artifacts:    &SessionArtifacts{},
		}, nil
	}
	return a.parseSessionResult(ctx, rt, opts, raw, durationMs, messages)
}

// appendFrameworkFiles records the framework-written files in GeneratedFiles so
// the workspace-diff collector excludes them (they would otherwise show up as
// user changes) and they are archived for debugging. The transport decides
// which files belong in the slice — for the local transport, the input file
// always, and the output file only when it was produced or cleared.
func (a *CustomAgent) appendFrameworkFiles(res *SessionResult, files []string) {
	if res.Artifacts == nil {
		res.Artifacts = &SessionArtifacts{}
	}
	res.Artifacts.GeneratedFiles = append(res.Artifacts.GeneratedFiles, files...)
}

// readRawResult reads the result payload from the output file, or from stdout.
// Per the contract, the output file is read only when local.output_file is
// explicitly configured; when it is unset the result comes from stdout and the
// default ${output_file} path is left untouched (it may be ordinary fixture
// input). The boolean reports whether the configured output file was produced
// (exists in the runtime) — independent of whether it supplied the payload —
// so an empty produced file is still excluded from workspace diffs.
func (a *CustomAgent) readRawResult(ctx context.Context, rt Runtime, custom *config.CustomEngineConfig, result ExecResult, outputFile string) (raw string, produced bool) {
	// Re-resolve symlinks now that the engine has run. workspacePath was
	// called pre-run, but the engine could have created a not-yet-existing
	// parent (e.g. outputs/newdir) as a symlink pointing outside the
	// workspace between then and now. Without this re-check, the
	// DownloadFile below would follow that symlink.
	if outputFile != "" {
		safe, err := workspacePath(rt, outputFile)
		if err != nil {
			logging.WarnContextf(ctx, "CustomAgent: refusing to read output file %s after run: %v", outputFile, err)
			return strings.TrimSpace(result.Stdout), false
		}
		outputFile = safe
	}
	if custom.Local.OutputFile == "" || outputFile == "" {
		return result.Stdout, false
	}

	// A per-call temp file avoids collisions when parallel cases share the
	// same output_file basename.
	tmpFile, err := os.CreateTemp("", "skill-up-custom-result-*")
	if err != nil {
		logging.WarnContextf(ctx, "CustomAgent: cannot create temp file for output_file %s, falling back to stdout: %v", outputFile, err)
		return result.Stdout, false
	}
	tmp := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmp) }()

	if err := rt.DownloadFile(ctx, outputFile, tmp); err != nil {
		logging.WarnContextf(ctx, "CustomAgent: cannot read output_file %s, falling back to stdout: %v", outputFile, err)
		return result.Stdout, false
	}
	// The output file exists in the runtime; it is "produced" even if empty.
	data, err := os.ReadFile(tmp)
	if err != nil {
		logging.WarnContextf(ctx, "CustomAgent: cannot read output_file %s, falling back to stdout: %v", outputFile, err)
		return result.Stdout, true
	}
	if strings.TrimSpace(string(data)) == "" {
		return result.Stdout, true
	}
	return string(data), true
}

// parsedSessionResult mirrors the SessionResult JSON contract but keeps
// exit_code as a pointer so a missing field can be distinguished from 0.
type parsedSessionResult struct {
	Engine       string                `json:"engine"`
	Model        string                `json:"model"`
	ExitCode     *int                  `json:"exit_code"`
	DurationMs   int64                 `json:"duration_ms"`
	Turns        int                   `json:"turns"`
	InputTokens  int                   `json:"input_tokens"`
	OutputTokens int                   `json:"output_tokens"`
	FinalMessage string                `json:"final_message"`
	Stderr       string                `json:"stderr"`
	Transcript   transcript.Transcript `json:"transcript"`
	Artifacts    *SessionArtifacts     `json:"artifacts"`
}

func (a *CustomAgent) parseSessionResult(ctx context.Context, rt Runtime, opts ExecOptions, raw string, durationMs int64, messages []transcript.Message) (*SessionResult, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return a.errorResult(0), errors.New("custom engine returned an empty result")
	}
	var parsed parsedSessionResult
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return a.errorResult(0), fmt.Errorf("custom engine result is not valid JSON: %w", err)
	}
	if parsed.ExitCode == nil {
		return a.errorResult(0), errors.New(customSessionResultMissing)
	}

	artifacts := parsed.Artifacts
	if artifacts == nil {
		artifacts = &SessionArtifacts{}
	}
	if err := validateArtifactFiles(artifacts.Files); err != nil {
		return a.errorResult(0), err
	}
	a.collectArtifacts(ctx, rt, opts, artifacts)

	// Mask everything the engine returned before deriving FinalMessage from
	// it — the engine may have echoed a custom.env value or the configured
	// API key into either final_message or transcript content, and those
	// strings flow into judges, reports, and CI logs from here.
	finalMsg := a.maskAPIKey(parsed.FinalMessage)
	trans := a.maskTranscript(parsed.Transcript)
	if finalMsg == "" && len(trans) > 0 {
		// The engine provided a transcript but omitted final_message; derive
		// it from the (already-masked) last assistant reply so judges and
		// reports do not grade or display a blank answer.
		finalMsg = trans.FinalAssistantMessage()
	}
	if len(trans) == 0 {
		// Build the documented minimal transcript so judges still receive the
		// conversation when the engine omits an explicit transcript.
		trans = minimalCustomTranscript(messages, finalMsg)
	}

	turns := parsed.Turns
	if turns == 0 {
		// Default missing turns from the transcript so an otherwise
		// successful custom run does not report 0 turns to judges/reports.
		turns = deriveTurns(trans)
	}

	// Engine and Model are engine-supplied untrusted strings that flow into
	// result.json, judges, and OTel span attributes. Mask them too so a
	// custom engine that echoes a credential into its self-identification
	// fields cannot bypass the masking discipline applied to FinalMessage /
	// Stderr / Transcript content above.
	res := &SessionResult{
		Engine:       firstNonEmpty(a.maskAPIKey(parsed.Engine), a.Name()),
		Model:        firstNonEmpty(a.maskAPIKey(parsed.Model), formatAgentModel(a.Cfg.ModelProvider, a.Cfg.ModelName)),
		ExitCode:     *parsed.ExitCode,
		DurationMs:   parsed.DurationMs,
		Turns:        turns,
		InputTokens:  parsed.InputTokens,
		OutputTokens: parsed.OutputTokens,
		FinalMessage: finalMsg,
		Stderr:       a.maskAPIKey(parsed.Stderr),
		Transcript:   trans,
		Artifacts:    artifacts,
	}
	if res.DurationMs == 0 {
		res.DurationMs = durationMs
	}
	return res, nil
}

// deriveTurns infers a turn count from a transcript. It prefers the highest
// explicit Message.Turn value; when no message carries Turn (the
// design-example transcript only sets role/content) it falls back to the
// assistant-reply count so a successful run does not report turns=0.
func deriveTurns(trans transcript.Transcript) int {
	maxTurn, assistantCount := 0, 0
	for _, m := range trans {
		if m.Turn > maxTurn {
			maxTurn = m.Turn
		}
		if m.Role == transcript.RoleAssistant {
			assistantCount++
		}
	}
	if maxTurn > 0 {
		return maxTurn
	}
	return assistantCount
}

// minimalCustomTranscript builds a fallback transcript from the input messages
// plus the engine's final assistant reply. It returns nil when there is nothing
// to record.
func minimalCustomTranscript(messages []transcript.Message, finalMsg string) transcript.Transcript {
	trans := make(transcript.Transcript, 0, len(messages)+1)
	maxTurn := 0
	for _, m := range messages {
		trans = append(trans, m)
		if m.Turn > maxTurn {
			maxTurn = m.Turn
		}
	}
	if finalMsg != "" {
		if maxTurn == 0 {
			maxTurn = 1
		}
		trans = append(trans, transcript.Message{
			Role:    transcript.RoleAssistant,
			Content: finalMsg,
			Turn:    maxTurn,
		})
	}
	if len(trans) == 0 {
		return nil
	}
	return trans
}

// validateArtifactFiles enforces the contract documented in
// docs/design/custom-engine.md for every artifacts.files entry:
//   - name is required (validateArtifactName)
//   - exactly one of path/url/content/content_base64 is set
//     (validateArtifactSource)
//   - inline content stays within the per-file and aggregate size caps
//     (validateArtifactSize)
//
// Each sub-check is split out so callers and future readers can see the
// distinct invariants this function enforces without unpacking one nested
// loop.
func validateArtifactFiles(files []ArtifactFile) error {
	var totalInline int64
	for i, f := range files {
		if err := validateArtifactName(i, f); err != nil {
			return err
		}
		if err := validateArtifactSource(i, f); err != nil {
			return err
		}
		size, err := validateArtifactSize(i, f, totalInline)
		if err != nil {
			return err
		}
		totalInline += size
	}
	return nil
}

// validateArtifactName enforces the required `name` field. An empty name
// otherwise causes inline entries to be silently dropped by
// writeInlineArtifact and renamed-path entries to be archived under the
// source basename — users notice the missing artifact too late.
func validateArtifactName(i int, f ArtifactFile) error {
	if strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("artifacts.files[%d]: name is required", i)
	}
	return nil
}

// validateArtifactSource enforces that exactly one of path/url/content/
// content_base64 is set. Without this, the collect-step switch would
// silently drop conflicting sources (e.g. both content and path).
func validateArtifactSource(i int, f ArtifactFile) error {
	sources := 0
	if f.Path != "" {
		sources++
	}
	if f.URL != "" {
		sources++
	}
	if f.Content != "" {
		sources++
	}
	if f.ContentBase64 != "" {
		sources++
	}
	if sources != 1 {
		return fmt.Errorf("artifacts.files[%d] (%q): must set exactly one of path/url/content/content_base64 (got %d)", i, f.Name, sources)
	}
	return nil
}

// validateArtifactSize enforces the per-file and aggregate inline content
// caps, returning the (estimated) size contributed by this entry so the
// caller can update its running total. Base64 expands ~4/3; estimating the
// decoded size from the encoded length lets us reject oversize payloads
// without paying to decode them first. Exact size is re-verified after
// decode in writeInlineArtifact.
func validateArtifactSize(i int, f ArtifactFile, runningTotal int64) (int64, error) {
	var size int64
	switch {
	case f.Content != "":
		size = int64(len(f.Content))
	case f.ContentBase64 != "":
		size = int64(len(f.ContentBase64)) * 3 / 4
	}
	if size > maxInlineArtifactBytes {
		return 0, fmt.Errorf("artifacts.files[%d] (%q): inline content size %d exceeds per-file limit %d", i, f.Name, size, maxInlineArtifactBytes)
	}
	if runningTotal+size > maxInlineArtifactTotal {
		return 0, fmt.Errorf("artifacts.files: total inline content size exceeds %d bytes after entry %d (%q)", maxInlineArtifactTotal, i, f.Name)
	}
	return size, nil
}

// collectArtifacts folds structured artifacts.files entries into the report
// pipeline: path entries register the original workspace path (so the
// workspace-diff collector excludes it) and, when the declared name differs
// from the basename, additionally archive a copy under that name; inline
// content is written to the case artifact directory; url-based artifacts are
// downloaded into the case artifact directory (best-effort).
func (a *CustomAgent) collectArtifacts(ctx context.Context, rt Runtime, opts ExecOptions, artifacts *SessionArtifacts) {
	// Drop any engine-supplied generated_files paths that escape the
	// workspace. On the none runtime an absolute path is the host path, so an
	// untrusted custom engine could return /etc/passwd here to have the
	// evaluator copy host files into the report.
	artifacts.GeneratedFiles = a.filterWorkspacePaths(ctx, rt, artifacts.GeneratedFiles, "generated_files")

	for _, f := range artifacts.Files {
		switch {
		case f.Path != "":
			// Confine the path to the workspace before registering or
			// archiving — same threat model as generated_files above.
			safePath, err := workspacePath(rt, f.Path)
			if err != nil {
				logging.WarnContextf(ctx, "CustomAgent: dropping artifact %q with path %q: %v", f.Name, f.Path, err)
				continue
			}
			// Keep the safe (resolved) path: the archiver downloads it (under
			// its basename) and the workspace-diff collector excludes it.
			artifacts.GeneratedFiles = append(artifacts.GeneratedFiles, safePath)
			if renamed := a.archiveRenamedPathArtifact(ctx, rt, opts, ArtifactFile{Name: f.Name, Path: safePath}); renamed != "" {
				artifacts.GeneratedFiles = append(artifacts.GeneratedFiles, renamed)
			}
		case f.Content != "" || f.ContentBase64 != "":
			if path := a.writeInlineArtifact(ctx, opts, f); path != "" {
				artifacts.GeneratedFiles = append(artifacts.GeneratedFiles, path)
			}
		case f.URL != "":
			if path := a.downloadURLArtifact(ctx, opts, f); path != "" {
				artifacts.GeneratedFiles = append(artifacts.GeneratedFiles, path)
			}
		}
	}
}

// filterWorkspacePaths drops any path that does not resolve inside the runtime
// workspace, logging each dropped entry so a misconfigured or malicious engine
// cannot exfiltrate host files through the report pipeline.
func (a *CustomAgent) filterWorkspacePaths(ctx context.Context, rt Runtime, paths []string, field string) []string {
	if len(paths) == 0 {
		return paths
	}
	out := paths[:0]
	for _, p := range paths {
		safe, err := workspacePath(rt, p)
		if err != nil {
			logging.WarnContextf(ctx, "CustomAgent: dropping %s entry %q: %v", field, p, err)
			continue
		}
		out = append(out, safe)
	}
	return out
}

// archiveRenamedPathArtifact materializes a path-based artifact into the case
// artifact directory under its declared name when that name differs from the
// file's basename, so the archiver (which keys on basename) preserves it. It
// returns the host copy path, or an empty string when no rename is needed.
func (a *CustomAgent) archiveRenamedPathArtifact(ctx context.Context, rt Runtime, opts ExecOptions, f ArtifactFile) string {
	name := filepath.Base(f.Name)
	if f.Name == "" || opts.ArtifactDir == "" || name == filepath.Base(f.Path) {
		return ""
	}
	// f.Path has already passed workspacePath in collectArtifacts, but we
	// re-check here so a follow-up symlink racing the DownloadFile cannot
	// follow a planted parent symlink out of the workspace.
	safePath, err := workspacePath(rt, f.Path)
	if err != nil {
		logging.WarnContextf(ctx, "CustomAgent: refusing to archive artifact %q from %s: %v", name, f.Path, err)
		return ""
	}
	dest := filepath.Join(opts.ArtifactDir, name)
	if err := rt.DownloadFile(ctx, safePath, dest); err != nil {
		logging.WarnContextf(ctx, "CustomAgent: cannot archive artifact %q from %s: %v", name, f.Path, err)
		return ""
	}
	return dest
}

// writeInlineArtifact materializes an inline artifact into the case artifact
// directory and returns its path so the caller can register it for archiving.
// It returns an empty string when nothing was written.
func (a *CustomAgent) writeInlineArtifact(ctx context.Context, opts ExecOptions, f ArtifactFile) string {
	if opts.ArtifactDir == "" || f.Name == "" {
		return ""
	}
	content := f.Content
	if f.ContentBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(f.ContentBase64)
		if err != nil {
			logging.WarnContextf(ctx, "CustomAgent: artifact %q has invalid content_base64: %v", f.Name, err)
			return ""
		}
		content = string(decoded)
	}
	// validateArtifactFiles already rejects oversized inline content using a
	// size estimate; re-check here after the actual decode so a base64 payload
	// that decodes to slightly more than estimated still cannot slip through.
	if int64(len(content)) > maxInlineArtifactBytes {
		logging.WarnContextf(ctx, "CustomAgent: artifact %q exceeds inline size limit %d after decode (%d bytes); dropping", f.Name, maxInlineArtifactBytes, len(content))
		return ""
	}
	path, err := writeLocalArtifact(opts.ArtifactDir, f.Name, content)
	if err != nil {
		logging.WarnContextf(ctx, "CustomAgent: cannot write inline artifact %q: %v", f.Name, err)
		return ""
	}
	return path
}

// downloadURLArtifact fetches a declared artifacts.files[].url and writes the
// body into the case artifact directory under f.Name, returning the local path.
//
// It is best-effort, mirroring the rest of artifact handling: a non-http(s)
// scheme, a transport error, a non-2xx status, or an over-cap body is logged as
// a warning and yields an empty string so the run still succeeds. Only the exact
// URL the engine declared is fetched — SSRF is out of scope by the same posture
// as the http transport, where the engine/operator is the trust boundary. A URL
// that embeds the configured API key is refused up front (it would otherwise be
// transmitted to the engine-declared endpoint and its proxies), and every error
// string — which net/http builds with the request URL — is passed through
// maskAPIKey so the configured api_key and engine.custom.env secrets cannot
// survive into the log. The download writes to the artifact output dir, never
// the workspace, so it bypasses the workspacePath confinement used for f.Path.
func (a *CustomAgent) downloadURLArtifact(ctx context.Context, opts ExecOptions, f ArtifactFile) string {
	if opts.ArtifactDir == "" || f.Name == "" {
		return ""
	}
	parsed, err := url.Parse(f.URL)
	if err != nil {
		logging.WarnContextf(ctx, "CustomAgent: artifact %q has an invalid url: %v", f.Name, a.maskAPIKey(err.Error()))
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		logging.WarnContextf(ctx, "CustomAgent: artifact %q url scheme %q is not http(s); skipping", f.Name, parsed.Scheme)
		return ""
	}
	// A URL embedding the configured API key would transmit it to the
	// engine-declared endpoint (and into its request logs and proxies), the
	// same leak errSecretInURL guards against for the http transport. The engine
	// is untrusted, so refuse rather than fetch.
	if a.containsAPIKey(f.URL) {
		logging.WarnContextf(ctx, "CustomAgent: artifact %q url embeds the API key; skipping download", f.Name)
		return ""
	}

	reqCtx, cancel := context.WithTimeout(ctx, customArtifactDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, f.URL, nil)
	if err != nil {
		logging.WarnContextf(ctx, "CustomAgent: cannot build request for artifact %q: %v", f.Name, a.maskAPIKey(err.Error()))
		return ""
	}

	// url is engine-declared (SessionResult.artifacts.files[].url); fetching the
	// exact declared endpoint is the documented contract and the engine/operator
	// is the trust boundary, the same posture as the http transport. Redirects
	// are NOT followed: a 3xx would let the artifact host (or the engine) swap
	// the archived bytes for a Location pointing at a different — possibly
	// internal — endpoint, breaking the "exact declared URL" boundary and the
	// up-front api-key/scheme checks. ErrUseLastResponse returns the 3xx as-is so
	// it falls through to the non-2xx skip path below.
	client := &http.Client{
		Timeout: customArtifactDownloadTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req) //nolint:gosec // intentional engine-declared dynamic URL
	if err != nil {
		// The net/http error embeds the request URL; mask it so a credential a
		// user mistakenly put in the URL cannot leak into the warning log.
		logging.WarnContextf(ctx, "CustomAgent: cannot download artifact %q: %v", f.Name, a.maskAPIKey(err.Error()))
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logging.WarnContextf(ctx, "CustomAgent: artifact %q url returned status %d; skipping", f.Name, resp.StatusCode)
		return ""
	}

	// Read one byte past the cap so an over-limit body is rejected rather than
	// silently truncated into the report.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArtifactDownloadBytes+1))
	if err != nil {
		logging.WarnContextf(ctx, "CustomAgent: cannot read artifact %q body: %v", f.Name, a.maskAPIKey(err.Error()))
		return ""
	}
	if int64(len(body)) > maxArtifactDownloadBytes {
		logging.WarnContextf(ctx, "CustomAgent: artifact %q exceeds download size limit %d bytes; dropping", f.Name, maxArtifactDownloadBytes)
		return ""
	}

	path, err := writeLocalArtifact(opts.ArtifactDir, f.Name, string(body))
	if err != nil {
		logging.WarnContextf(ctx, "CustomAgent: cannot write downloaded artifact %q: %v", f.Name, err)
		return ""
	}
	return path
}

// containsAPIKey reports whether s embeds the configured API key value, used
// to keep credentials out of the rendered command line.
func (a *CustomAgent) containsAPIKey(s string) bool {
	return a.Cfg.APIKey != "" && strings.Contains(s, a.Cfg.APIKey)
}

// maskTranscript returns a new transcript with each message's Content passed
// through maskAPIKey, so secrets the custom engine echoes inside the
// transcript (which both judges and the report consume) do not survive into
// downstream consumers. The slice is copied so callers cannot accidentally
// reuse the pre-mask version.
func (a *CustomAgent) maskTranscript(t transcript.Transcript) transcript.Transcript {
	if len(t) == 0 {
		return t
	}
	out := make(transcript.Transcript, len(t))
	for i, m := range t {
		m.Content = a.maskAPIKey(m.Content)
		out[i] = m
	}
	return out
}

// maskAPIKey scrubs known credential-shaped values from s before it is
// captured into SessionResult. Two sources are masked:
//
//  1. The configured model-layer API key (a.Cfg.APIKey) — masked even when
//     the custom engine never sees it, so a stray echo from a wrapper that
//     inspects the parent process env cannot leak it.
//  2. Every value passed through engine.custom.env (a.Cfg.Custom.Env) —
//     these are the user-declared secret channel, so anything the agent
//     echoes back from $MY_TOKEN must not survive into the report.
//
// Values shorter than 8 characters are skipped so a plausibly-non-secret
// value like a flag or a model name does not collapse every short word in
// the output into ***REDACTED***.
func (a *CustomAgent) maskAPIKey(s string) string {
	if s == "" {
		return s
	}
	const redacted = "***REDACTED***"
	const minMaskLen = 8
	if a.Cfg.APIKey != "" && strings.Contains(s, a.Cfg.APIKey) {
		s = strings.ReplaceAll(s, a.Cfg.APIKey, redacted)
	}
	if a.Cfg.Custom != nil {
		for _, v := range a.Cfg.Custom.Env {
			if len(v) < minMaskLen {
				continue
			}
			if strings.Contains(s, v) {
				s = strings.ReplaceAll(s, v, redacted)
			}
		}
	}
	return s
}

// errSecretInCommand reports a rendered command/arg that would leak a credential.
func errSecretInCommand(field string) error {
	return fmt.Errorf(
		"engine.custom.%s renders the API key into the command line, which would expose it in process listings and traces; reference ${api_key} from engine.custom.env instead",
		field,
	)
}

func (a *CustomAgent) errorResult(exitCode int) *SessionResult {
	if exitCode == 0 {
		exitCode = 1
	}
	return &SessionResult{
		Engine:    a.Name(),
		ExitCode:  exitCode,
		Artifacts: &SessionArtifacts{},
	}
}

// SessionInput is the documented contract handed to a Custom Engine for each
// case (see docs/design/custom-engine.md). Both the local transport (written
// to ${input_file}) and the planned http transport (sent as the request body
// or multipart payload field) use the same shape, so it is exported here as
// a public type rather than buried in a transport implementation.
type SessionInput struct {
	CaseID         string            `json:"case_id,omitempty"`
	Variant        string            `json:"variant,omitempty"`
	Workspace      string            `json:"workspace,omitempty"`
	Model          string            `json:"model,omitempty"`
	Kwargs         map[string]string `json:"kwargs,omitempty"`
	Messages       []SessionMessage  `json:"messages"`
	MaxTurns       int               `json:"max_turns,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

// SessionMessage is one entry in SessionInput.Messages: a role/content pair
// matching the design's transcript element.
type SessionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (a *CustomAgent) buildSessionInput(rt Runtime, opts ExecOptions, messages []transcript.Message, kwargs map[string]string, timeoutSec int) SessionInput {
	msgs := make([]SessionMessage, 0, len(messages))
	for _, m := range messages {
		msgs = append(msgs, SessionMessage{Role: string(m.Role), Content: m.Content})
	}
	meta := opts.AgentMeta()
	return SessionInput{
		CaseID:         meta.CaseID,
		Variant:        meta.Variant,
		Workspace:      rt.Workspace(),
		Model:          formatAgentModel(a.Cfg.ModelProvider, a.Cfg.ModelName),
		Kwargs:         kwargs,
		Messages:       msgs,
		MaxTurns:       meta.MaxTurns,
		TimeoutSeconds: timeoutSec,
	}
}

// buildBaseVars builds the scalar template variables that do not depend on
// custom.kwargs (so kwargs can themselves reference them) or on the session
// input. input_file/output_file start at their defaults and are overwritten
// once resolveCustomIOFiles has run.
func (a *CustomAgent) buildBaseVars(rt Runtime, opts ExecOptions, messages []transcript.Message, timeoutSec int) map[string]string {
	workspace := rt.Workspace()
	meta := opts.AgentMeta()
	return map[string]string{
		"workspace":       workspace,
		"prompt":          singleTurnPrompt(messages),
		"input_file":      filepath.Join(workspace, customDefaultInputFile),
		"output_file":     filepath.Join(workspace, customDefaultOutputFile),
		"model":           formatAgentModel(a.Cfg.ModelProvider, a.Cfg.ModelName),
		"model_provider":  a.Cfg.ModelProvider,
		"model_name":      a.Cfg.ModelName,
		"api_key":         a.Cfg.APIKey,
		"case_id":         meta.CaseID,
		"variant":         meta.Variant,
		"max_turns":       strconv.Itoa(meta.MaxTurns),
		"timeout_seconds": strconv.Itoa(timeoutSec),
	}
}

// completeTemplateVars extends the base variables with the rendered kwargs
// and the session-input-derived structured values. sessJSON is the
// pre-marshaled session input the caller already produced, shared here to
// avoid a second copy of (potentially large) transcript payloads.
func (a *CustomAgent) completeTemplateVars(baseVars, renderedKwargs map[string]string, sess SessionInput, sessJSON []byte) (map[string]string, error) {
	msgsJSON, err := json.Marshal(sess.Messages)
	if err != nil {
		return nil, fmt.Errorf("marshal messages: %w", err)
	}
	kwargsJSON, err := json.Marshal(orEmptyMap(renderedKwargs))
	if err != nil {
		return nil, fmt.Errorf("marshal kwargs: %w", err)
	}

	vars := make(map[string]string)
	maps.Copy(vars, baseVars)
	vars["messages"] = string(msgsJSON)
	vars["messages_json"] = string(msgsJSON)
	vars["session_input"] = string(sessJSON)
	vars["session_input_json"] = string(sessJSON)
	vars["kwargs"] = string(kwargsJSON)
	vars["kwargs_json"] = string(kwargsJSON)
	for k, v := range renderedKwargs {
		vars["kwargs."+k] = v
	}
	return vars, nil
}

// resolveCustomIOFiles renders the input/output file paths, applying defaults.
// A non-absolute configured path is resolved against the runtime workspace so
// it is consistent regardless of local.cwd (the command sees the same path the
// runtime upload/download APIs key on).
func resolveCustomIOFiles(rt Runtime, custom *config.CustomEngineConfig, vars map[string]string) (inputFile, outputFile string, err error) {
	inputFile = filepath.Join(rt.Workspace(), customDefaultInputFile)
	if custom.Local.InputFile != "" {
		rendered, rErr := renderTemplate(custom.Local.InputFile, vars)
		if rErr != nil {
			return "", "", fmt.Errorf("render local.input_file: %w", rErr)
		}
		if inputFile, err = workspacePath(rt, rendered); err != nil {
			return "", "", fmt.Errorf("local.input_file: %w", err)
		}
	}
	outputFile = filepath.Join(rt.Workspace(), customDefaultOutputFile)
	if custom.Local.OutputFile != "" {
		rendered, rErr := renderTemplate(custom.Local.OutputFile, vars)
		if rErr != nil {
			return "", "", fmt.Errorf("render local.output_file: %w", rErr)
		}
		if outputFile, err = workspacePath(rt, rendered); err != nil {
			return "", "", fmt.Errorf("local.output_file: %w", err)
		}
	}
	return inputFile, outputFile, nil
}

// workspacePath resolves a path against the runtime workspace and confines it
// to that workspace. A relative path is joined with the workspace root; an
// absolute path is accepted only if it already points inside the workspace.
// Any path that escapes the workspace (via "..", an unrelated absolute root,
// etc.) is rejected — otherwise a template-driven local.cwd / input_file /
// output_file could let clearStaleOutputFile remove arbitrary host files.
//
// The check also follows symlinks (on the workspace root and on every
// existing ancestor of the supplied path) before enforcing the bound, so a
// symlink planted under the workspace that points outside cannot be used as
// a side door. The leaf is allowed not to exist yet (output files are
// created by the agent), but every existing ancestor is resolved.
func workspacePath(rt Runtime, p string) (string, error) {
	if p == "" {
		return p, nil
	}
	ws, err := filepath.EvalSymlinks(filepath.Clean(rt.Workspace()))
	if err != nil {
		// Workspace must exist; if EvalSymlinks fails fall back to the
		// lexical root so we still reject obvious escapes.
		ws = filepath.Clean(rt.Workspace())
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(ws, p))
	}
	// Resolve the deepest existing ancestor so a symlinked intermediate
	// directory cannot smuggle the final path out of the workspace. The leaf
	// itself may not exist yet (output files); we walk up until EvalSymlinks
	// succeeds and re-attach the unresolved suffix.
	resolved := resolveExistingPrefix(abs)
	rel, err := filepath.Rel(ws, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the runtime workspace", p)
	}
	// Return the resolved-prefix path. Returning the lexical `abs` instead
	// would re-introduce a TOCTOU window: the custom command could create a
	// not-yet-existing parent as a symlink after this check, and a later
	// DownloadFile / rm against `abs` would follow it. Because EvalSymlinks
	// is re-run on every call, repeating workspacePath at each use site
	// (readRawResult, archiveRenamedPathArtifact, collectArtifacts) closes
	// the post-engine TOCTOU window — the second call sees whatever the
	// engine just planted on disk.
	return resolved, nil
}

// resolveExistingPrefix returns abs with its longest existing ancestor passed
// through filepath.EvalSymlinks and the trailing unresolved segments
// re-appended. When no ancestor exists or EvalSymlinks fails for an unrelated
// reason, the input is returned unchanged so the caller can still apply the
// lexical workspace bound.
func resolveExistingPrefix(abs string) string {
	suffix := ""
	cur := abs
	for {
		if resolvedAncestor, err := filepath.EvalSymlinks(cur); err == nil {
			if suffix == "" {
				return resolvedAncestor
			}
			return filepath.Join(resolvedAncestor, suffix)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		suffix = filepath.Join(filepath.Base(cur), suffix)
		cur = parent
	}
}

func customResponseFormat(custom *config.CustomEngineConfig) string {
	if custom.ResponseFormat == customResponseText {
		return customResponseText
	}
	return customResponseSessionJSON
}

func singleTurnPrompt(messages []transcript.Message) string {
	if len(messages) != 1 {
		return ""
	}
	if messages[0].Role != transcript.RoleUser {
		return ""
	}
	return messages[0].Content
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func orEmptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// renderTemplateMap renders every value of m, returning a new map.
func renderTemplateMap(m map[string]string, vars map[string]string) (map[string]string, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		rv, err := renderTemplate(v, vars)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
}

// renderTemplate resolves ${X}, ${X:-default} and ${X?message} references
// against vars, falling back to process environment variables.
func renderTemplate(s string, vars map[string]string) (string, error) {
	if s == "" || !strings.Contains(s, "${") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		open := strings.Index(s[i:], "${")
		if open < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+open])
		start := i + open
		closeIdx := strings.IndexByte(s[start:], '}')
		if closeIdx < 0 {
			b.WriteString(s[start:])
			break
		}
		value, err := resolveTemplateToken(s[start+2:start+closeIdx], vars)
		if err != nil {
			return "", err
		}
		b.WriteString(value)
		i = start + closeIdx + 1
	}
	return b.String(), nil
}

func resolveTemplateToken(inner string, vars map[string]string) (string, error) {
	// Share the ${X} / ${X:-default} / ${X?msg} grammar with the config-load-time
	// resolver (config.resolveEnvToken) so the two cannot drift apart.
	tok := config.ParseTemplateToken(inner)

	if v, ok := vars[tok.Name]; ok {
		// A present-but-empty built-in value (e.g. an unconfigured api_key)
		// is treated as unset so ${X:-default} and ${X?msg} still apply.
		if v != "" || (!tok.HasDefault && !tok.HasErrForm) {
			return v, nil
		}
	}
	if v := os.Getenv(tok.Name); v != "" {
		return v, nil
	}
	if tok.HasDefault {
		return tok.Default, nil
	}
	if tok.HasErrForm {
		return "", tok.RequiredErr()
	}
	// kwargs.<key> references that were not provided resolve to empty.
	if strings.HasPrefix(tok.Name, "kwargs.") {
		return "", nil
	}
	return "", fmt.Errorf("unresolved template variable %q", tok.Name)
}
