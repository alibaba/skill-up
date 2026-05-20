// Discovery layers for user config (lowest to highest precedence):
//
//  1. embed     — the empty Config{} (no vendor defaults baked in).
//  2. user      — $SKILL_EVAL_CONFIG, else $XDG_CONFIG_HOME/skill-up/config.yaml,
//                 else ~/.config/skill-up/config.yaml. Missing = silent skip.
//  3. project   — $WorkingDir/.skill-up.yaml. Missing = silent skip.
//  4. explicit  — opts.ExplicitPath. Missing = ERROR.
//
// Higher-precedence layers overlay non-zero fields onto lower layers; map
// fields are merged key-wise rather than replaced.

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
const ConfigEnvVar = "SKILL_EVAL_CONFIG"

// ProjectConfigFile is the per-project config file name (in WorkingDir).
const ProjectConfigFile = ".skill-up.yaml"

// UserConfigDir is the directory under $XDG_CONFIG_HOME / ~/.config.
const UserConfigDir = "skill-up"

// UserConfigFile is the default user-layer file name.
const UserConfigFile = "config.yaml"

// LoadOptions controls config discovery.
type LoadOptions struct {
	ExplicitPath string
	WorkingDir   string
	HomeDir      string
	Env          func(string) string
	// Warnings receives one line per non-fatal issue (e.g. a corrupt user or
	// project layer file). Defaults to os.Stderr when nil.
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
	sources := []Source{{Kind: "embed"}}

	// Auto-discovered layers (user, project) downgrade parse/validate errors to
	// warnings so a corrupt ~/.config/skill-up/config.yaml does not block
	// unrelated commands (run/validate/init/...). Only --config, which the user
	// explicitly opted into, treats errors as fatal.
	sources = mergeOptionalLayer(&cfg, sources, "user", resolveUserConfigPath(opts, getenv), warn)
	sources = mergeOptionalLayer(&cfg, sources, "project", resolveProjectConfigPath(opts), warn)

	if opts.ExplicitPath != "" {
		loaded, ok, err := loadConfigFile(opts.ExplicitPath)
		if err != nil {
			return Config{}, nil, err
		}
		if !ok {
			return Config{}, nil, fmt.Errorf("config file not found: %s", opts.ExplicitPath)
		}
		cfg.overlay(loaded)
		sources = append(sources, Source{Kind: "explicit", Path: opts.ExplicitPath})
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

// mergeOptionalLayer loads a single auto-discovered layer (user or project),
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
	cfg.overlay(loaded)
	return append(sources, Source{Kind: kind, Path: path})
}

func resolveUserConfigPath(opts LoadOptions, getenv func(string) string) string {
	if v := getenv(ConfigEnvVar); v != "" {
		return v
	}
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, UserConfigDir, UserConfigFile)
	}
	home := opts.HomeDir
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = h
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", UserConfigDir, UserConfigFile)
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
