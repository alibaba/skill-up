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
	EnvOpenAIAPIKey               = "OPENAI_API_KEY" //nolint:gosec // false positive: constant name, not a credential
	EnvOpenAIBaseURL              = "OPENAI_BASE_URL"
	EnvOpenAIModel                = "OPENAI_MODEL"
	EnvAnthropicAPIKey            = "ANTHROPIC_API_KEY"    //nolint:gosec // false positive: constant name, not a credential
	EnvAnthropicAuthToken         = "ANTHROPIC_AUTH_TOKEN" //nolint:gosec // false positive: constant name, not a credential
	EnvAnthropicBaseURL           = "ANTHROPIC_BASE_URL"
	EnvQoderPersonalAccessToken   = "QODER_PERSONAL_ACCESS_TOKEN"   //nolint:gosec // false positive: constant name, not a credential
	EnvQoderCNPersonalAccessToken = "QODERCN_PERSONAL_ACCESS_TOKEN" //nolint:gosec // false positive: constant name, not a credential
	EnvQoderCNAccessToken         = "QODER_CN_ACCESS_TOKEN"         //nolint:gosec // local secret alias, not a credential value
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
	mu        sync.RWMutex
	confPath  string
	creds     map[string]*config.APIKeyConfig
	providers map[string]ProviderConfiguration
}

// NewResolver creates a new credential resolver.
func NewResolver(confPath string) *Resolver {
	return &Resolver{
		confPath:  confPath,
		creds:     make(map[string]*config.APIKeyConfig),
		providers: make(map[string]ProviderConfiguration),
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
	SchemaVersion string                        `yaml:"schema_version"`
	Providers     map[string]providerFileConfig `yaml:"providers"`
}

type providerFileConfig struct {
	APIKey  optionalFileValue `yaml:"api_key,omitempty"`
	BaseURL optionalFileValue `yaml:"base_url,omitempty"`

	OpenAI    *providerEndpointFileConfig `yaml:"openai,omitempty"`
	Anthropic *providerEndpointFileConfig `yaml:"anthropic,omitempty"`
}

func (p *providerFileConfig) UnmarshalYAML(node *yaml.Node) error {
	type plainProviderFileConfig providerFileConfig
	var decoded plainProviderFileConfig
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*p = providerFileConfig(decoded)
	return decodeOptionalFileValues(node, map[string]*optionalFileValue{
		"api_key":  &p.APIKey,
		"base_url": &p.BaseURL,
	})
}

type providerEndpointFileConfig struct {
	BaseURL optionalFileValue `yaml:"base_url"`
	APIKey  optionalFileValue `yaml:"api_key,omitempty"`
}

func (p *providerEndpointFileConfig) UnmarshalYAML(node *yaml.Node) error {
	type plainProviderEndpointFileConfig providerEndpointFileConfig
	var decoded plainProviderEndpointFileConfig
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*p = providerEndpointFileConfig(decoded)
	return decodeOptionalFileValues(node, map[string]*optionalFileValue{
		"api_key":  &p.APIKey,
		"base_url": &p.BaseURL,
	})
}

type optionalFileValue struct {
	value string
	set   bool
}

func (v *optionalFileValue) UnmarshalYAML(node *yaml.Node) error {
	v.set = true
	return node.Decode(&v.value)
}

func decodeOptionalFileValues(node *yaml.Node, fields map[string]*optionalFileValue) error {
	for i := 0; i+1 < len(node.Content); i += 2 {
		field, ok := fields[node.Content[i].Value]
		if !ok {
			continue
		}
		field.set = true
		valueNode := node.Content[i+1]
		if valueNode.Tag == "!!null" {
			field.value = ""
			continue
		}
		if err := valueNode.Decode(&field.value); err != nil {
			return err
		}
	}
	return nil
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
		providerConfig := ProviderConfiguration{
			Provider:  name,
			APIKey:    fileConnectionValue(p.APIKey),
			BaseURL:   fileConnectionValue(p.BaseURL),
			Endpoints: map[Protocol]ProviderEndpointConfig{},
		}
		if p.OpenAI != nil {
			providerConfig.Endpoints[ProtocolOpenAI] = ProviderEndpointConfig{
				APIKey:  fileConnectionValue(p.OpenAI.APIKey),
				BaseURL: fileConnectionValue(p.OpenAI.BaseURL),
			}
		}
		if p.Anthropic != nil {
			providerConfig.Endpoints[ProtocolAnthropic] = ProviderEndpointConfig{
				APIKey:  fileConnectionValue(p.Anthropic.APIKey),
				BaseURL: fileConnectionValue(p.Anthropic.BaseURL),
			}
		}
		r.providers[name] = providerConfig

		cred := &config.APIKeyConfig{
			Provider: name,
		}

		if p.APIKey.value != "" {
			cred.APIKey = p.APIKey.value
		}
		if p.BaseURL.value != "" {
			cred.BaseURL = p.BaseURL.value
		}

		if p.OpenAI != nil {
			if cred.BaseURL == "" {
				cred.BaseURL = p.OpenAI.BaseURL.value
			}
			if cred.APIKey == "" && p.OpenAI.APIKey.value != "" {
				cred.APIKey = p.OpenAI.APIKey.value
			}
		}
		if p.Anthropic != nil {
			if cred.APIKey == "" && p.Anthropic.APIKey.value != "" {
				cred.APIKey = p.Anthropic.APIKey.value
			}
		}
		r.creds[name] = cred
	}

	return nil
}

func fileConnectionValue(value optionalFileValue) ConnectionValue {
	return ConnectionValue{Value: value.value, Source: ValueSourceResolver, Set: value.set}
}
