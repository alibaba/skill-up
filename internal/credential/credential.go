package credential

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
)

// Standard environment variable names for agent configuration.
const (
	EnvOpenAIAPIKey             = "OPENAI_API_KEY" //nolint:gosec // false positive: constant name, not a credential
	EnvOpenAIBaseURL            = "OPENAI_BASE_URL"
	EnvAnthropicAPIKey          = "ANTHROPIC_API_KEY" //nolint:gosec // false positive: constant name, not a credential
	EnvAnthropicBaseURL         = "ANTHROPIC_BASE_URL"
	EnvQoderPersonalAccessToken = "QODER_PERSONAL_ACCESS_TOKEN" //nolint:gosec // false positive: constant name, not a credential
)

const apiKeyMaskLen = 4 // Length for API key masking

// DefaultConfPath returns the default credentials file path,
// ~/.skill-up/credentials.yaml. It returns "" when the home directory
// cannot be determined, in which case the file layer is silently skipped.
func DefaultConfPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}

	return filepath.Join(home, ".skill-up", "credentials.yaml")
}

// Resolver resolves credentials for model providers.
type Resolver struct {
	mu       sync.RWMutex
	confPath string
	creds    map[string]*config.APIKeyConfig
}

// NewResolver creates a new credential resolver.
func NewResolver(confPath string) *Resolver {
	return &Resolver{
		confPath: confPath,
		creds:    make(map[string]*config.APIKeyConfig),
	}
}

// Load loads credentials from .env file and config file.
// Environment-variable priority is handled in agent_init.go after provider resolution.
func (r *Resolver) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		logging.Warnf("Failed to load .env file: %v", err)
	}

	if r.confPath != "" {
		if err := r.loadFromFile(r.confPath); err != nil {
			// Distinguish between file not found (acceptable) and parse errors (problematic)
			if os.IsNotExist(err) {
				logging.Debugf("Credential config file not found at %s, will use environment variables or CLI arguments.", r.confPath)
			} else {
				logging.Warnf("Failed to parse credential config file %s: %v. Check file format.", r.confPath, err)
			}
		}
	}

	r.logEffectiveConfig()

	return nil
}

// Get returns the credential for the given provider.
func (r *Resolver) Get(provider string) (*config.APIKeyConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cred, ok := r.creds[provider]

	return cred, ok
}

// MaskAPIKey masks the middle portion of an API key, showing first 2 and last 2 chars.
func MaskAPIKey(key string) string {
	if len(key) <= apiKeyMaskLen {
		return "****"
	}

	return key[:2] + "****" + key[len(key)-2:]
}

// logEffectiveConfig prints the currently loaded credentials (API_KEY masked).
func (r *Resolver) logEffectiveConfig() {
	if len(r.creds) == 0 {
		logging.Debugf("No credentials loaded from config file.")

		return
	}
	for name, cred := range r.creds {
		if cred.APIKey != "" {
			logging.Debugf("CREDENTIAL_DISCOVERED provider=%s source=resolver api_key=%s", name, MaskAPIKey(cred.APIKey))
		}
		if cred.BaseURL != "" {
			logging.Debugf("CREDENTIAL_DISCOVERED provider=%s source=resolver base_url=%s", name, cred.BaseURL)
		}
	}
}

type configFile struct {
	SchemaVersion string                    `yaml:"schema_version"`
	Providers     map[string]ProviderConfig `yaml:"providers"`
}

// ProviderConfig holds configuration for a provider's endpoints.
// Supports both flat format (api_key, base_url directly) and nested format (openai, anthropic).
type ProviderConfig struct {
	APIKey  string `yaml:"api_key,omitempty"`
	BaseURL string `yaml:"base_url,omitempty"`

	OpenAI    *ProviderEndpointConfig `yaml:"openai,omitempty"`
	Anthropic *ProviderEndpointConfig `yaml:"anthropic,omitempty"`
}

// ProviderEndpointConfig holds configuration for a single endpoint.
type ProviderEndpointConfig struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key,omitempty"`
}

func (r *Resolver) loadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	for name, p := range cfg.Providers {
		cred := &config.APIKeyConfig{
			Provider: name,
		}

		if p.APIKey != "" {
			cred.APIKey = p.APIKey
		}
		if p.BaseURL != "" {
			cred.BaseURL = p.BaseURL
		}

		if p.OpenAI != nil {
			if cred.BaseURL == "" {
				cred.BaseURL = p.OpenAI.BaseURL
			}
			if cred.APIKey == "" && p.OpenAI.APIKey != "" {
				cred.APIKey = p.OpenAI.APIKey
			}
		}
		if p.Anthropic != nil {
			if cred.APIKey == "" && p.Anthropic.APIKey != "" {
				cred.APIKey = p.Anthropic.APIKey
			}
		}
		r.creds[name] = cred
	}

	return nil
}
