// Package config loads the global TOML configuration for perch.
// The global config file is at $XDG_CONFIG_HOME/perch/config.toml.
// If XDG_CONFIG_HOME is not set, perch uses ~/.config/perch/config.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/miniature-pug/perch/internal/registry"
)

// configFileName is the name of the global configuration file.
const configFileName = "config.toml"

// ── Internal decode target ────────────────────────────────────────────────────

// globalConfig is the TOML target for $XDG_CONFIG_HOME/perch/config.toml.
type globalConfig struct {
	Roots []string `toml:"roots"`
}

// ── Public API ────────────────────────────────────────────────────────────────

// Config is the merged, public configuration used across perch.
// After Load returns, callers must treat Config as read-only.
type Config struct {
	// Roots is the list of directories perch scans for git repos.
	Roots []string
}

// ── Loader ────────────────────────────────────────────────────────────────────

// DefaultGlobalPath returns the path of the global config.toml file.
// It follows the XDG Base Directory spec: $XDG_CONFIG_HOME/perch/config.toml.
// When XDG_CONFIG_HOME is unset, it falls back to ~/.config/perch/config.toml.
// registry.DefaultConfigDir resolves the XDG path, so the app directory name
// "perch" stays defined in one place.
func DefaultGlobalPath() string {
	return filepath.Join(registry.DefaultConfigDir(), configFileName)
}

// Load builds a Config from the global config.toml file at globalPath.
// An empty globalPath, or a global config file that does not exist, is not
// an error. Load fills the gap with defaults.
// When the global config sets no roots, Load uses projectStartDir as the
// single default root.
func Load(globalPath string, projectStartDir string) (*Config, error) {
	gc, err := loadGlobal(globalPath)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}

	if len(gc.Roots) > 0 {
		// A relative root resolves against the directory that holds the
		// config file, so it does not depend on the launch cwd.
		base := projectStartDir
		if globalPath != "" {
			base = filepath.Dir(globalPath)
		}
		expanded, err := expandRoots(gc.Roots, base)
		if err != nil {
			return nil, err
		}
		cfg.Roots = expanded
	}
	if len(cfg.Roots) == 0 {
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

// expandRoots normalises each configured root:
//   - empty or whitespace-only entries are dropped;
//   - environment variables ($HOME, ${XDG_DATA_HOME}) are expanded;
//   - a bare "~" or a leading "~/" becomes the user's home directory;
//   - a relative path is made absolute against base (when base is non-empty);
//   - the result is passed through filepath.Clean.
//
// expandRoots resolves HOME lazily: it calls os.UserHomeDir only when a root
// needs expansion. If os.UserHomeDir fails, expandRoots returns an error so
// the caller knows the path is unusable.
func expandRoots(roots []string, base string) ([]string, error) {
	out := make([]string, 0, len(roots))
	var home string
	for _, r := range roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if strings.Contains(r, "$") {
			r = os.ExpandEnv(r)
			if r == "" {
				continue
			}
		}
		if r == "~" || strings.HasPrefix(r, "~/") {
			if home == "" {
				var err error
				home, err = os.UserHomeDir()
				if err != nil {
					return nil, fmt.Errorf("config: expand root %q: cannot resolve home directory: %w", r, err)
				}
			}
			r = filepath.Join(home, strings.TrimPrefix(r, "~"))
		}
		if !filepath.IsAbs(r) && base != "" {
			r = filepath.Join(base, r)
		}
		out = append(out, filepath.Clean(r))
	}
	return out, nil
}
