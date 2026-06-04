// Package config implements global TOML configuration loading for perch.
// The global config file lives at $XDG_CONFIG_HOME/perch/config.toml
// (falling back to ~/.config/perch/config.toml).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// configFileName is the name of the global configuration file.
const configFileName = "config.toml"

// ── Internal decode target ────────────────────────────────────────────────────

// globalConfig is the TOML target for $XDG_CONFIG_HOME/perch/config.toml.
type globalConfig struct {
	Roots []string `toml:"roots"`
}

// ── Public API ────────────────────────────────────────────────────────────────

// Config is the merged, public configuration consumed by the rest of perch.
// Callers should treat it as read-only after Load returns.
type Config struct {
	// Roots is the list of directories perch scans for git repos.
	Roots []string
}

// ── Loader ────────────────────────────────────────────────────────────────────

// DefaultGlobalPath returns the canonical path for the global config.toml
// following the XDG Base Directory spec: $XDG_CONFIG_HOME/perch/config.toml,
// falling back to ~/.config/perch/config.toml when XDG_CONFIG_HOME is unset.
func DefaultGlobalPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: resolve home dir for default global path: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "perch", configFileName), nil
}

// Load assembles a Config from the global config.toml at globalPath.
// An empty globalPath or a non-existent file is not an error; defaults fill
// the gap. projectStartDir is accepted for API compatibility but is no longer
// used after the worktree package was removed — project config keys are
// silently ignored.
func Load(globalPath string, projectStartDir string) (*Config, error) {
	gc, err := loadGlobal(globalPath)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}

	if len(gc.Roots) > 0 {
		expanded, err := expandRoots(gc.Roots)
		if err != nil {
			return nil, err
		}
		cfg.Roots = expanded
	} else {
		// Default roots to the start directory so callers get something useful.
		if projectStartDir != "" {
			cfg.Roots = []string{projectStartDir}
		}
	}

	return cfg, nil
}

// loadGlobal reads the global config file. A missing file returns a zero globalConfig.
func loadGlobal(globalPath string) (*globalConfig, error) {
	gc := &globalConfig{}
	if globalPath == "" {
		return gc, nil
	}
	_, err := os.Stat(globalPath)
	if errors.Is(err, os.ErrNotExist) {
		return gc, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: stat global config %q: %w", globalPath, err)
	}
	if _, err := toml.DecodeFile(globalPath, gc); err != nil {
		return nil, fmt.Errorf("config: parse global config %q: %w", globalPath, err)
	}
	return gc, nil
}

// expandRoots replaces a leading ~/ with the user's home directory.
// HOME resolution is lazy: os.UserHomeDir is only called when at least one root
// actually needs expansion. If it fails in that case, an error is returned so
// the caller knows the path cannot be made usable.
func expandRoots(roots []string) ([]string, error) {
	out := make([]string, len(roots))
	var home string
	for i, r := range roots {
		if strings.HasPrefix(r, "~/") {
			if home == "" {
				var err error
				home, err = os.UserHomeDir()
				if err != nil {
					return nil, fmt.Errorf("config: expand root %q: cannot resolve home directory: %w", r, err)
				}
			}
			out[i] = filepath.Join(home, r[2:])
		} else {
			out[i] = r
		}
	}
	return out, nil
}
