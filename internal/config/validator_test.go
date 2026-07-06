package config

import (
	"strings"
	"testing"
)

func intPtr(v int) *int { return &v }

//nolint:maintidx,funlen // table-driven test cases drive the line count; splitting hurts readability.
func TestValidator_ValidateEvalConfig(t *testing.T) {
	t.Parallel()
	validator := NewValidator()

	tests := []struct {
		name    string
		cfg     *EvalConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with none runtime",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: false,
		},
		{
			name: "valid config with opensandbox runtime image",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:  "opensandbox",
					Image: "ubuntu:24.04",
				},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: false,
		},
		{
			name: "valid config with opensandbox template",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:            "opensandbox",
					SandboxTemplate: "ubuntu:24.04",
				},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: false,
		},
		{
			name: "missing schema_version",
			cfg: &EvalConfig{
				Environment: Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: true,
			errMsg:  "schema_version is required",
		},
		{
			name: "invalid schema_version",
			cfg: &EvalConfig{
				SchemaVersion: "v1beta",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: true,
			errMsg:  "schema_version must be 'v1alpha1'",
		},
		{
			name: "missing environment.type",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: true,
			errMsg:  "environment.type is required",
		},
		{
			name: "invalid environment.type",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "invalid"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: true,
			errMsg:  "environment.type must be one of",
		},
		{
			name: "opensandbox without image or template uses runtime default",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "opensandbox"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: false,
		},
		{
			name: "missing engine.name",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: true,
			errMsg:  "engine.name is required",
		},
		{
			name: "missing engine.model.provider is valid",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Name: "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: false,
		},
		{
			name: "missing engine.model.name is valid",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: false,
		},
		{
			name: "missing both engine.model.provider and name is valid",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name:  "claude_code",
					Model: ModelConfig{},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: false,
		},
		{
			name: "empty cases.files",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{Files: []string{}},
				Judge: JudgeConfig{
					Type: "rule_based",
				},
			},
			wantErr: true,
			errMsg:  "cases.files must contain at least one case file",
		},
		{
			name: "no global judge is valid",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{},
			},
			wantErr: false,
		},
		{
			name: "retry policy max_retries above limit",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
					RetryPolicy: RetryPolicy{
						MaxRetries: maxRetryPolicyRetries + 1,
					},
				},
			},
			wantErr: true,
			errMsg:  "cases.retry_policy.max_retries must be <=",
		},
		{
			name: "invalid judge.type at eval level is ignored",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "invalid",
				},
			},
			wantErr: false,
		},
		{
			name: "script without script_path at eval level is ignored",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "script",
				},
			},
			wantErr: false,
		},
		{
			name: "agent_judge without model at eval level is ignored",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type: "agent_judge",
				},
			},
			wantErr: false,
		},
		{
			name: "agent_judge with zero threshold",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type:          "agent_judge",
					Model:         "test-model",
					Criteria:      []string{"criterion"},
					PassThreshold: float64Ptr(0.0),
				},
			},
			wantErr: false,
		},
		{
			name: "agent_judge with threshold above one at eval level is ignored",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment:   Environment{Type: "none"},
				Engine: EngineConfig{
					Name: "claude_code",
					Model: ModelConfig{
						Provider: "anthropic",
						Name:     "claude-sonnet-4-6",
					},
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
				Judge: JudgeConfig{
					Type:          "agent_judge",
					Model:         "test-model",
					Criteria:      []string{"criterion"},
					PassThreshold: float64Ptr(1.1),
				},
			},
			wantErr: false,
		},
		{
			name: "valid network_policy deny_all with opensandbox",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:          "opensandbox",
					NetworkPolicy: "deny_all",
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid network_policy allow_declared with opensandbox",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:          "opensandbox",
					NetworkPolicy: "allow_declared",
					AllowedEgress: []string{"pypi.org", "*.githubusercontent.com"},
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: false,
		},
		{
			name: "allow_declared without allowed_egress is rejected",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:          "opensandbox",
					NetworkPolicy: "allow_declared",
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: true,
			errMsg:  "network_policy: allow_declared requires a non-empty allowed_egress list",
		},
		{
			name: "allowed_egress with deny_all is rejected",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:          "opensandbox",
					NetworkPolicy: "deny_all",
					AllowedEgress: []string{"pypi.org"},
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: true,
			errMsg:  "allowed_egress is only valid with network_policy: allow_declared",
		},
		{
			name: "allowed_egress without network_policy is rejected",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:          "opensandbox",
					AllowedEgress: []string{"pypi.org"},
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: true,
			errMsg:  "allowed_egress requires network_policy: allow_declared",
		},
		{
			name: "allowed_egress entry as URL is rejected",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:          "opensandbox",
					NetworkPolicy: "allow_declared",
					AllowedEgress: []string{"https://pypi.org/simple"},
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: true,
			errMsg:  "must be a bare FQDN or wildcard domain",
		},
		{
			name: "invalid network_policy value",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:          "opensandbox",
					NetworkPolicy: "allow_all",
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: true,
			errMsg:  "network_policy must be one of: deny_all, allow_declared",
		},
		{
			name: "network_policy with none runtime is rejected",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:          "none",
					NetworkPolicy: "deny_all",
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: true,
			errMsg:  "network_policy requires environment.type opensandbox",
		},
		{
			name: "valid docker runtime type",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:  "docker",
					Image: "alpine:3.20",
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: false,
		},
		{
			name: "docker without image is rejected at validation",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type: "docker",
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: true,
			errMsg:  "environment.image is required when environment.type is docker",
		},
		{
			name: "docker with relative workspace_mount is rejected at validation",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:           "docker",
					Image:          "alpine:3.20",
					WorkspaceMount: "rel/path",
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: true,
			errMsg:  "environment.workspace_mount must be absolute when environment.type is docker",
		},
		{
			name: "docker with absolute workspace_mount is valid",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:           "docker",
					Image:          "alpine:3.20",
					WorkspaceMount: "/work",
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid docker with deny_all network policy",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:          "docker",
					Image:         "alpine:3.20",
					NetworkPolicy: "deny_all",
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: false,
		},
		{
			name: "docker with allow_declared is rejected",
			cfg: &EvalConfig{
				SchemaVersion: "v1alpha1",
				Environment: Environment{
					Type:          "docker",
					Image:         "alpine:3.20",
					NetworkPolicy: "allow_declared",
					AllowedEgress: []string{"pypi.org"},
				},
				Engine: EngineConfig{
					Name: "claude_code",
				},
				Cases: CasesConfig{
					Files: []string{"evals/cases/test.yaml"},
				},
			},
			wantErr: true,
			errMsg:  "allow_declared is not supported for environment.type docker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validator.ValidateEvalConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEvalConfig() error = %v, wantErr %v", err, tt.wantErr)

				return
			}
			if tt.wantErr && err != nil {
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateEvalConfig() error = %v, should contain %v", err, tt.errMsg)
				}
			}
		})
	}
}

// nolint:funlen // table-driven test cases drive the line count; splitting hurts readability.
func TestValidator_ValidateCaseConfig(t *testing.T) {
	t.Parallel()
	validator := NewValidator()

	tests := []struct {
		name    string
		cfg     *CaseConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid case with prompt",
			cfg: &CaseConfig{
				ID: "test-case",
				Input: Input{
					Prompt: "Say hello",
				},
			},
			wantErr: false,
		},
		{
			name: "valid case with turns",
			cfg: &CaseConfig{
				ID: "test-case",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello"},
						{Role: "user", Content: "How are you?"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing prompt and turns",
			cfg: &CaseConfig{
				ID:    "test-case",
				Input: Input{},
			},
			wantErr: true,
			errMsg:  "input.prompt or input.turns is required",
		},
		{
			name: "prompt and turns are mutually exclusive",
			cfg: &CaseConfig{
				ID: "test-case",
				Input: Input{
					Prompt: "Say hello",
					Turns:  []Turn{{Role: "user", Content: "Hi"}},
				},
			},
			wantErr: true,
			errMsg:  "input.prompt and input.turns are mutually exclusive",
		},
		{
			name: "turn with missing role",
			cfg: &CaseConfig{
				ID: "test-case",
				Input: Input{
					Turns: []Turn{
						{Content: "Hello"},
					},
				},
			},
			wantErr: true,
			errMsg:  "input.turns[0].role is required",
		},
		{
			name: "turn with non-user role",
			cfg: &CaseConfig{
				ID: "test-case",
				Input: Input{
					Turns: []Turn{
						{Role: "assistant", Content: "Hello"},
					},
				},
			},
			wantErr: true,
			errMsg:  "input.turns[0].role must be \"user\"",
		},
		{
			name: "turn with missing content",
			cfg: &CaseConfig{
				ID: "test-case",
				Input: Input{
					Turns: []Turn{
						{Role: "user"},
					},
				},
			},
			wantErr: true,
			errMsg:  "input.turns[0].content is required",
		},
		{
			name: "turn with negative timeout_seconds",
			cfg: &CaseConfig{
				ID: "test-case",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hi", TimeoutSeconds: -1},
					},
				},
			},
			wantErr: true,
			errMsg:  "input.turns[0].timeout_seconds must be non-negative",
		},
		{
			name: "case-level agent_judge with negative threshold",
			cfg: &CaseConfig{
				ID: "test-case",
				Input: Input{
					Prompt: "Say hello",
				},
				Judge: JudgeConfig{
					Type:          "agent_judge",
					Model:         "test-model",
					Criteria:      []string{"criterion"},
					PassThreshold: float64Ptr(-0.1),
				},
			},
			wantErr: true,
			errMsg:  "judge.pass_threshold must be between 0.0 and 1.0",
		},
		{
			name: "case-level judge with negative timeout_seconds",
			cfg: &CaseConfig{
				ID: "test-case",
				Input: Input{
					Prompt: "Say hello",
				},
				Judge: JudgeConfig{
					Type:           "script",
					ScriptPath:     "check.sh",
					TimeoutSeconds: intPtr(-1),
				},
			},
			wantErr: true,
			errMsg:  "judge.timeout_seconds must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validator.ValidateCaseConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCaseConfig() error = %v, wantErr %v", err, tt.wantErr)

				return
			}
			if tt.wantErr && err != nil {
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateCaseConfig() error = %v, should contain %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestValidator_ValidateAll(t *testing.T) {
	t.Parallel()
	validator := NewValidator()

	validEval := &EvalConfig{
		SchemaVersion: "v1alpha1",
		Environment:   Environment{Type: "none"},
		Engine: EngineConfig{
			Name: "claude_code",
			Model: ModelConfig{
				Provider: "anthropic",
				Name:     "claude-sonnet-4-6",
			},
		},
		Cases: CasesConfig{
			Files: []string{"evals/cases/test.yaml"},
		},
		Judge: JudgeConfig{
			Type: "rule_based",
		},
	}

	t.Run("valid eval and cases", func(t *testing.T) {
		t.Parallel()
		result := &EvalResult{
			Eval: validEval,
			Cases: []*CaseConfig{
				{ID: "test", Input: Input{Prompt: "test"}},
			},
		}
		if err := validator.ValidateAll(result); err != nil {
			t.Errorf("ValidateAll() error = %v, want nil", err)
		}
	})

	t.Run("invalid case", func(t *testing.T) {
		t.Parallel()
		result := &EvalResult{
			Eval: validEval,
			Cases: []*CaseConfig{
				{ID: "test", Input: Input{}}, // missing prompt and turns
			},
		}
		if err := validator.ValidateAll(result); err == nil {
			t.Error("ValidateAll() expected error for invalid case")
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}

	return false
}

func float64Ptr(v float64) *float64 {
	return &v
}

func TestValidator_CollectArtifacts(t *testing.T) {
	t.Parallel()
	validator := NewValidator()

	t.Run("valid globs on eval config", func(t *testing.T) {
		t.Parallel()
		cfg := &EvalConfig{
			SchemaVersion: "v1alpha1",
			Environment:   Environment{Type: "none"},
			Engine:        EngineConfig{Name: "claude_code"},
			Cases: CasesConfig{
				Files:    []string{"evals/cases/a.yaml"},
				Defaults: CaseDefaults{CollectArtifacts: []string{"**/*.json", "docs/*.md", "out/**"}},
			},
		}
		if err := validator.ValidateEvalConfig(cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty glob on eval config", func(t *testing.T) {
		t.Parallel()
		cfg := &EvalConfig{
			SchemaVersion: "v1alpha1",
			Environment:   Environment{Type: "none"},
			Engine:        EngineConfig{Name: "claude_code"},
			Cases: CasesConfig{
				Files:    []string{"evals/cases/a.yaml"},
				Defaults: CaseDefaults{CollectArtifacts: []string{"  "}},
			},
		}
		err := validator.ValidateEvalConfig(cfg)
		if err == nil || !strings.Contains(err.Error(), "cases.defaults.collect_artifacts[0] must not be empty") {
			t.Fatalf("expected empty-glob error, got %v", err)
		}
	})

	t.Run("invalid glob on case config", func(t *testing.T) {
		t.Parallel()
		cfg := &CaseConfig{
			ID:               "c1",
			Input:            Input{Prompt: "hi"},
			CollectArtifacts: []string{"[unterminated"},
		}
		err := validator.ValidateCaseConfig(cfg)
		if err == nil || !strings.Contains(err.Error(), "collect_artifacts[0] is not a valid glob pattern") {
			t.Fatalf("expected invalid-glob error, got %v", err)
		}
	})
}

//nolint:funlen // table-driven test
func TestValidator_PostCondition(t *testing.T) {
	t.Parallel()
	validator := NewValidator()

	tests := []struct {
		name    string
		cfg     *CaseConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid post_condition with must_contain_any",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", PostCondition: &PostCondition{
							MustContainAny: []string{"hi", "hello"},
						}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid post_condition with must_contain_all",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", PostCondition: &PostCondition{
							MustContainAll: []string{"greeting", "response"},
						}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid post_condition with must_not_contain",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", PostCondition: &PostCondition{
							MustNotContain: []string{"error"},
						}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid post_condition with on_fail=fail",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", PostCondition: &PostCondition{
							MustContainAny: []string{"ok"},
							OnFail:         "fail",
						}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid post_condition with on_fail=skip_remaining",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", PostCondition: &PostCondition{
							MustContainAny: []string{"ok"},
							OnFail:         "skip_remaining",
						}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "post_condition with invalid on_fail",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", PostCondition: &PostCondition{
							MustContainAny: []string{"ok"},
							OnFail:         "abort",
						}},
					},
				},
			},
			wantErr: true,
			errMsg:  "on_fail must be empty",
		},
		{
			name: "post_condition with no check fields",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", PostCondition: &PostCondition{
							OnFail: "fail",
						}},
					},
				},
			},
			wantErr: true,
			errMsg:  "must include at least one of must_contain_any, must_contain_all, or must_not_contain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validator.ValidateCaseConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("error = %v, should contain %q", err, tt.errMsg)
			}
		})
	}
}

//nolint:funlen // table-driven test
func TestValidator_CaptureRules(t *testing.T) {
	t.Parallel()
	validator := NewValidator()

	tests := []struct {
		name    string
		cfg     *CaseConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid capture with pattern",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", Capture: []CaptureRule{
							{Variable: "session_id", Pattern: `id:\s*(?P<value>[a-f0-9]+)`},
						}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid capture with jsonpath",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", Capture: []CaptureRule{
							{Variable: "name", JSONPath: "$.result.name"},
						}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "capture with empty variable",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", Capture: []CaptureRule{
							{Variable: "", Pattern: `id:\s+(\w+)`},
						}},
					},
				},
			},
			wantErr: true,
			errMsg:  "variable is required",
		},
		{
			name: "capture with invalid variable name",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", Capture: []CaptureRule{
							{Variable: "123invalid", Pattern: `id:\s+(\w+)`},
						}},
					},
				},
			},
			wantErr: true,
			errMsg:  "must match pattern",
		},
		{
			name: "capture with no extractor",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", Capture: []CaptureRule{
							{Variable: "myvar"},
						}},
					},
				},
			},
			wantErr: true,
			errMsg:  "must specify exactly one extractor",
		},
		{
			name: "capture with both extractors",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", Capture: []CaptureRule{
							{Variable: "myvar", Pattern: `(\w+)`, JSONPath: "$.x"},
						}},
					},
				},
			},
			wantErr: true,
			errMsg:  "must specify exactly one extractor: pattern or jsonpath (both set)",
		},
		{
			name: "capture with invalid regex pattern",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello", Capture: []CaptureRule{
							{Variable: "myvar", Pattern: `[invalid`},
						}},
					},
				},
			},
			wantErr: true,
			errMsg:  "pattern is invalid regex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validator.ValidateCaseConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("error = %v, should contain %q", err, tt.errMsg)
			}
		})
	}
}

//nolint:funlen // table-driven test
func TestValidator_PerTurnJudgeRules(t *testing.T) {
	t.Parallel()
	validator := NewValidator()

	tests := []struct {
		name    string
		cfg     *CaseConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid turn_response_contains",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello"},
						{Role: "user", Content: "How are you?"},
					},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
					Success: []Rule{
						{TurnResponseContains: &TurnResponseContainsRule{Turn: 1, ContainsAll: []string{"hello"}}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "turn_response_contains with turn < 1",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello"},
					},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
					Success: []Rule{
						{TurnResponseContains: &TurnResponseContainsRule{Turn: 0, ContainsAll: []string{"x"}}},
					},
				},
			},
			wantErr: true,
			errMsg:  "turn_response_contains.turn must be >= 1",
		},
		{
			name: "turn_response_contains exceeds total turns",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello"},
					},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
					Success: []Rule{
						{TurnResponseContains: &TurnResponseContainsRule{Turn: 5, ContainsAll: []string{"x"}}},
					},
				},
			},
			wantErr: true,
			errMsg:  "exceeds total turns",
		},
		{
			name: "turn_response_contains without contains_all or contains_any",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello"},
					},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
					Success: []Rule{
						{TurnResponseContains: &TurnResponseContainsRule{Turn: 1}},
					},
				},
			},
			wantErr: true,
			errMsg:  "must specify contains_all or contains_any",
		},
		{
			name: "turn_response_not_contains without not_contains",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello"},
					},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
					Success: []Rule{
						{TurnResponseNotContains: &TurnResponseNotContainsRule{Turn: 1}},
					},
				},
			},
			wantErr: true,
			errMsg:  "turn_response_not_contains.not_contains is required",
		},
		{
			name: "tool_called_in_turn without name",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello"},
					},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
					Success: []Rule{
						{ToolCalledInTurn: &ToolCalledInTurnRule{Turn: 1}},
					},
				},
			},
			wantErr: true,
			errMsg:  "tool_called_in_turn.name is required",
		},
		{
			name: "tool_not_called_in_turn without name",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello"},
					},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
					Success: []Rule{
						{ToolNotCalledInTurn: &ToolNotCalledInTurnRule{Turn: 1}},
					},
				},
			},
			wantErr: true,
			errMsg:  "tool_not_called_in_turn.name is required",
		},
		{
			name: "tool_called_in_turn with turn < 1",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello"},
					},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
					Success: []Rule{
						{ToolCalledInTurn: &ToolCalledInTurnRule{Turn: 0, Name: "write_file"}},
					},
				},
			},
			wantErr: true,
			errMsg:  "tool_called_in_turn.turn must be >= 1",
		},
		{
			name: "valid per-turn rules in failure list",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Turns: []Turn{
						{Role: "user", Content: "Hello"},
						{Role: "user", Content: "Bye"},
					},
				},
				Judge: JudgeConfig{
					Type: "rule_based",
					Failure: []Rule{
						{TurnResponseContains: &TurnResponseContainsRule{Turn: 2, ContainsAny: []string{"error"}}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "per-turn rules valid when no turns defined (prompt mode)",
			cfg: &CaseConfig{
				ID: "test",
				Input: Input{
					Prompt: "Hello",
				},
				Judge: JudgeConfig{
					Type: "rule_based",
					Success: []Rule{
						{TurnResponseContains: &TurnResponseContainsRule{Turn: 1, ContainsAll: []string{"x"}}},
					},
				},
			},
			wantErr: false, // no turns defined means turnsTotal=0, skip upper-bound check
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validator.ValidateCaseConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("error = %v, should contain %q", err, tt.errMsg)
			}
		})
	}
}
