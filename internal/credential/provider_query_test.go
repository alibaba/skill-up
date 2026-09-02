package credential

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
)

const testProviderDashscope = "dashscope"

// -- HasProvider --

func TestHasProvider_EmptyNameReturnsFalse(t *testing.T) {
	t.Parallel()

	if (*Resolver)(nil).HasProvider("") {
		t.Fatalf("nil Resolver.HasProvider(\"\") = true, want false")
	}
}

func TestHasProvider_NilReceiverFallsBackToEnv(t *testing.T) {
	// Not parallel: depends on env state for "dashscope" and the absence of
	// env footprint for the unconfigured probe.
	t.Setenv("DASHSCOPE_API_KEY", "sk-test")
	if !(*Resolver)(nil).HasProvider(testProviderDashscope) {
		t.Fatalf("nil receiver should still see env-configured provider")
	}
}

func TestHasProvider_TrueWhenResolverHasEntry(t *testing.T) {
	t.Parallel()

	r := NewResolver("")
	r.creds["custom-provider"] = &config.APIKeyConfig{
		Provider: "custom-provider",
		APIKey:   "sk-x",
	}
	if !r.HasProvider("custom-provider") {
		t.Fatalf("HasProvider returned false for resolver-known provider")
	}
}

func TestHasProvider_TrueWhenAPIKeyEnvSet(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "sk-test")
	if !(&Resolver{}).HasProvider(testProviderDashscope) {
		t.Fatalf("HasProvider returned false when DASHSCOPE_API_KEY is set")
	}
}

func TestHasProvider_TrueWhenBaseURLEnvSet(t *testing.T) {
	// Some setups only configure a base URL (e.g. private gateway behind a
	// shared org token); that alone must count as "configured".
	t.Setenv("ROUTIFY_BASE_URL", "https://routify.example.com")
	if !(&Resolver{}).HasProvider("routify") {
		t.Fatalf("HasProvider returned false when ROUTIFY_BASE_URL is set")
	}
}

func TestHasProvider_TrueWhenQoderPATEnvSet(t *testing.T) {
	// Qoder authenticates via QODER_PERSONAL_ACCESS_TOKEN (its own env,
	// NOT a general `<PROVIDER>_PERSONAL_ACCESS_TOKEN` convention) — PAT
	// presence must count as configured for qoder specifically.
	t.Setenv(EnvQoderPersonalAccessToken, "qpat-test")
	if !(&Resolver{}).HasProvider("qoder") {
		t.Fatalf("HasProvider returned false when %s is set", EnvQoderPersonalAccessToken)
	}
}

func TestHasProvider_TrueWhenQoderCNTokenEnvSet(t *testing.T) {
	for _, envName := range []string{EnvQoderCNPersonalAccessToken, EnvQoderCNAccessToken} {
		t.Run(envName, func(t *testing.T) {
			t.Setenv(EnvQoderPersonalAccessToken, "")
			t.Setenv(EnvQoderCNPersonalAccessToken, "")
			t.Setenv(EnvQoderCNAccessToken, "")
			t.Setenv(envName, "qoder-cn-test-token")
			if !(&Resolver{}).HasProvider("qoder") {
				t.Fatalf("HasProvider returned false when %s is set", envName)
			}
		})
	}
}

func TestHasProvider_QoderPATDoesNotLeakToOtherProviders(t *testing.T) {
	// The PAT probe is qoder-specific; setting QODER_PERSONAL_ACCESS_TOKEN
	// must not mark unrelated providers (or imaginary `<X>_PAT` env names)
	// as configured.
	t.Setenv(EnvQoderPersonalAccessToken, "qpat-test")
	for _, key := range []string{
		"DASHSCOPE_API_KEY",
		"DASHSCOPE_BASE_URL",
	} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	if (&Resolver{}).HasProvider(testProviderDashscope) {
		t.Fatalf("qoder PAT must not register dashscope as configured")
	}
}

func TestHasProvider_TrueForFrameworkDefaults(t *testing.T) {
	// Not parallel: explicitly unsets framework provider envs to make sure
	// the framework-default short-circuit is what returns true, not stray
	// env state.
	for _, key := range []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL",
	} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}

	for _, name := range []string{"anthropic", "Anthropic", "ANTHROPIC", "openai", "OpenAI"} {
		if !(*Resolver)(nil).HasProvider(name) {
			t.Fatalf("HasProvider(%q) returned false; framework defaults must be always-configured", name)
		}
	}
}

func TestHasProvider_FalseWhenNoEnvAndNoResolverEntry(t *testing.T) {
	for _, key := range []string{
		"UNKNOWN_VENDOR_API_KEY",
		"UNKNOWN_VENDOR_BASE_URL",
	} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}

	if (&Resolver{}).HasProvider("unknown_vendor") {
		t.Fatalf("HasProvider returned true for fully unconfigured provider")
	}
}

// -- ResolveModelRef --

func TestResolveModelRef_EmptyReturnsEmpty(t *testing.T) {
	t.Parallel()

	p, n := ResolveModelRef("", nil)
	if p != "" || n != "" {
		t.Fatalf("ResolveModelRef(\"\", nil) = (%q, %q), want (\"\",\"\")", p, n)
	}
}

func TestResolveModelRef_NoSlashReturnsBareName(t *testing.T) {
	t.Parallel()

	p, n := ResolveModelRef("claude-opus-4-7", nil)
	if p != "" || n != "claude-opus-4-7" {
		t.Fatalf("ResolveModelRef bare = (%q, %q), want (\"\", \"claude-opus-4-7\")", p, n)
	}
}

func TestResolveModelRef_SplitsWhenProviderConfigured(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "sk-test")
	p, n := ResolveModelRef("dashscope/claude-sonnet-4-6", nil)
	if p != testProviderDashscope || n != "claude-sonnet-4-6" {
		t.Fatalf("ResolveModelRef configured = (%q, %q), want (dashscope, claude-sonnet-4-6)", p, n)
	}
}

func TestResolveModelRef_CollapsesWhenProviderUnknown(t *testing.T) {
	for _, key := range []string{
		"ANTHROPIC_MODELSCOPE_API_KEY",
		"ANTHROPIC_MODELSCOPE_BASE_URL",
	} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}

	p, n := ResolveModelRef("anthropic_modelscope/deepseek-v4-pro", nil)
	// Anthropic-proxy gateways need the full identifier verbatim; the
	// unknown-provider path must NOT peel the prefix off.
	if p != "" || n != "anthropic_modelscope/deepseek-v4-pro" {
		t.Fatalf("ResolveModelRef unconfigured = (%q, %q), want (\"\", \"anthropic_modelscope/deepseek-v4-pro\")", p, n)
	}
}

func TestResolveModelRef_DebugLogsOnCollapse(t *testing.T) {
	// Capture slog output to confirm operators get visibility into the
	// silent-collapse path (otherwise a typo'd provider would produce
	// nothing more than a 400 with no breadcrumb).
	for _, key := range []string{
		"TYPOED_API_KEY",
		"TYPOED_BASE_URL",
	} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	restore := logging.SetOutputForTest(&buf)
	defer restore()
	logging.SetVerbosity(1)
	defer logging.SetVerbosity(0)

	_, _ = ResolveModelRef("typoed/some-model", nil)

	out := buf.String()
	// slog's text handler quote-escapes the msg, so check substrings rather
	// than a single literal.
	if !strings.Contains(out, "typoed/some-model") || !strings.Contains(out, "opaque model id") {
		t.Fatalf("expected debug log about opaque-model collapse, got:\n%s", out)
	}
}

func TestResolveModelRef_NoLogOnConfiguredSplit(t *testing.T) {
	// Configured splits are the common case and must stay silent.
	t.Setenv("DASHSCOPE_API_KEY", "sk-test")

	var buf bytes.Buffer
	restore := logging.SetOutputForTest(&buf)
	defer restore()
	logging.SetVerbosity(1)
	defer logging.SetVerbosity(0)

	_, _ = ResolveModelRef("dashscope/claude-sonnet-4-6", nil)

	if strings.Contains(buf.String(), "ResolveModelRef") {
		t.Fatalf("did not expect ResolveModelRef debug log on configured split, got:\n%s", buf.String())
	}
}
