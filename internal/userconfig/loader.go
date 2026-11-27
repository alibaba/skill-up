// Discovery layers for user config (lowest to highest precedence):
//
//  1. embed     — the empty Config{} (no vendor defaults baked in).
//  2. user      — $SKILL_UP_CONFIG, else $XDG_CONFIG_HOME/skill-up/config.yaml,
//                 else ~/.config/skill-up/config.yaml. Missing = silent skip.
//  3. project   — .skill-up.yaml in WorkingDir. Missing = silent skip.
//  4. explicit  — opts.ExplicitPath. Missing = ERROR.
//
// Higher-precedence layers overlay non-zero fields onto lower layers; map
// fields are merged key-wise rather than replaced.
// Only the user and explicit layers are trusted for environment expansion.

package userconfig

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigEnvVar overrides the user-layer config path.
const ConfigEnvVar = "SKILL_UP_CONFIG"

// ProjectConfigFile is the per-project config file name (in WorkingDir).
const ProjectConfigFile = ".skill-up.yaml"

// UserConfigDir is the directory under $XDG_CONFIG_HOME / ~/.config.
const UserConfigDir = "skill-up"

// UserConfigFile is the default user-layer file name.
const UserConfigFile = "config.yaml"

const (
	sourceEmbed    = "embed"
	sourceUser     = "user"
	sourceProject  = "project"
	sourceExplicit = "explicit"
)

// LoadOptions controls config discovery.
type LoadOptions struct {
	ExplicitPath string
	WorkingDir   string
	HomeDir      string
	Env          func(string) string
	// Warnings receives non-fatal configuration issues.
	// Defaults to os.Stderr when nil.
	Warnings io.Writer
}

// Source identifies a discovery layer that contributed to the merged result.
type Source struct {
	Kind string
	Path string
}

// LoadEffective merges all discovery layers in order of increasing precedence
// and returns the merged Config and the list of layers that actually
// contributed.
func LoadEffective(opts LoadOptions) (Config, []Source, error) {
	getenv := opts.Env
	if getenv == nil {
		getenv = os.Getenv
	}

	warn := opts.Warnings
	if warn == nil {
		warn = os.Stderr
	}

	cfg := Config{}
	sources := []Source{{Kind: sourceEmbed}}

	// Optional user-file read/parse errors must not block unrelated commands.
	sources = mergeOptionalLayer(&cfg, sources, sourceUser, resolveUserConfigPath(opts, getenv), warn)
	projectPath := resolveProjectConfigPath(opts)
	// A project file deliberately selected via SKILL_UP_CONFIG already
	// contributed as a user layer; do not downgrade its provenance by
	// loading it a second time through automatic discovery.
	if len(sources) == 1 || !sameConfigFile(sources[1].Path, projectPath) {
		sources = mergeOptionalLayer(&cfg, sources, sourceProject, projectPath, warn)
	}

	if opts.ExplicitPath != "" {
		loaded, ok, err := loadConfigFile(opts.ExplicitPath)
		if err != nil {
			return Config{}, nil, err
		}
		if !ok {
			return Config{}, nil, fmt.Errorf("config file not found: %s", opts.ExplicitPath)
		}
		cfg.overlay(loaded, sourceExplicit)
		sources = append(sources, Source{Kind: sourceExplicit, Path: opts.ExplicitPath})
	}

	if err := cfg.Validate(); err != nil {
		paths := make([]string, 0, len(sources))
		for _, s := range sources {
			if s.Path != "" {
				paths = append(paths, s.Path)
			}
		}
		if len(paths) > 0 {
			return Config{}, nil, fmt.Errorf("config validation failed (loaded from %v): %w", paths, err)
		}
		return Config{}, nil, err
	}

	return cfg, sources, nil
}

// mergeOptionalLayer loads an optional discovered layer,
// downgrading parse errors to warnings on warn and appending to sources when
// the file existed and parsed cleanly.
func mergeOptionalLayer(cfg *Config, sources []Source, kind, path string, warn io.Writer) []Source {
	if path == "" {
		return sources
	}
	loaded, ok, err := loadConfigFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(warn, "warning: ignoring %s config %s: %v\n", kind, path, err)
		return sources
	}
	if !ok {
		return sources
	}
	cfg.overlay(loaded, kind)
	return append(sources, Source{Kind: kind, Path: path})
}

func sameConfigFile(a, b string) bool {
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	return err == nil && os.SameFile(left, right)
}

func resolveUserConfigPath(opts LoadOptions, getenv func(string) string) string {
	if v := getenv(ConfigEnvVar); v != "" {
		return v
	}
	return DefaultConfigPath(opts.HomeDir, getenv)
}

func resolveProjectConfigPath(opts LoadOptions) string {
	dir := opts.WorkingDir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		dir = wd
	}
	return filepath.Join(dir, ProjectConfigFile)
}

// LoadFile reads and validates a single config file. Returns an error if the
// file is missing, unparseable, or fails Validate. Unlike LoadEffective it
// does not consult any discovery layers — it is meant for callers (e.g. the
// `init` subcommand) that want to vet a specific file in isolation.
func LoadFile(path string) (Config, error) {
	cfg, ok, err := loadConfigFile(path)
	if err != nil {
		return Config{}, err
	}
	if !ok {
		return Config{}, fmt.Errorf("config file not found: %s", path)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

func loadConfigFile(path string) (Config, bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is user-supplied by design
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, false, nil
		}
		return Config{}, false, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, true, nil
}
