package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miniature-pug/perch/internal/config"
)

// writeFile writes content to a file at path for tests.
// It creates parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// initGitDir creates a bare .git directory so Load treats the dir as a repo root.
func initGitDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
}

// TestRootsFromGlobal verifies that Load reads roots from the global config
// and returns them in Config.Roots.
func TestRootsFromGlobal(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `roots = ["`+tmp+`"]`)

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Roots) != 1 || cfg.Roots[0] != tmp {
		t.Errorf("Roots = %v; want [%s]", cfg.Roots, tmp)
	}
}

// TestDefaultsRootsToStartDir verifies that when the global config sets no
// roots, Load falls back to the projectStartDir argument.
func TestDefaultsRootsToStartDir(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	cfg, err := config.Load("", tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Roots) != 1 || cfg.Roots[0] != tmp {
		t.Errorf("Roots = %v; want [%s]", cfg.Roots, tmp)
	}
}

// TestMissingGlobal verifies that an absent global config.toml is not an error
// and that Load still returns usable defaults.
func TestMissingGlobal(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	cfg, err := config.Load(filepath.Join(tmp, "nonexistent", "config.toml"), tmp)
	if err != nil {
		t.Fatalf("missing global should not error: %v", err)
	}
	// Falls back to projectStartDir.
	if len(cfg.Roots) != 1 || cfg.Roots[0] != tmp {
		t.Errorf("Roots = %v; want [%s] (fallback to startDir)", cfg.Roots, tmp)
	}
}

// TestMalformedGlobal verifies that a syntactically invalid global config returns an error.
func TestMalformedGlobal(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `roots = [[[broken toml`)

	_, err := config.Load(globalPath, tmp)
	if err == nil {
		t.Error("expected error for malformed global config.toml")
	}
}

// TestRootsHomeDirExpansion verifies that Load expands roots entries with a
// leading ~/.
func TestRootsHomeDirExpansion(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `roots = ["~/projects"]`)

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Roots) == 0 {
		t.Fatal("Roots is empty")
	}
	if strings.HasPrefix(cfg.Roots[0], "~") {
		t.Errorf("Roots[0] = %q; ~ was not expanded", cfg.Roots[0])
	}
}

// TestDefaultGlobalPath verifies XDG discovery. DefaultGlobalPath uses
// XDG_CONFIG_HOME when set, and falls back to ~/.config/perch/config.toml
// when unset.
func TestDefaultGlobalPath(t *testing.T) {
	t.Run("XDG_CONFIG_HOME set", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmp)
		got := config.DefaultGlobalPath()
		want := filepath.Join(tmp, "perch", "config.toml")
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("XDG_CONFIG_HOME unset falls back to ~/.config", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		got := config.DefaultGlobalPath()
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".config", "perch", "config.toml")
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})
}

// TestExpandRootsNoTildeHomeMissing verifies that when no root contains a leading ~/,
// a missing HOME directory is harmless and Load succeeds.
func TestExpandRootsNoTildeHomeMissing(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	// Use an absolute root (no tilde). HOME resolution must not run.
	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `roots = ["`+tmp+`"]`)

	// Unset HOME so os.UserHomeDir would fail if called.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", tmp) // prevent DefaultGlobalPath from needing HOME

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("non-tilde roots with missing HOME should not error: %v", err)
	}
	if len(cfg.Roots) == 0 || cfg.Roots[0] != tmp {
		t.Errorf("Roots = %v; want [%s]", cfg.Roots, tmp)
	}
}

// TestExpandRootsTildeHomeMissing verifies that when a root needs ~/
// expansion but os.UserHomeDir fails, Load returns a clear error.
func TestExpandRootsTildeHomeMissing(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `roots = ["~/projects"]`)

	// Force HOME to be empty so os.UserHomeDir fails.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", tmp)

	_, err := config.Load(globalPath, tmp)
	if err == nil {
		t.Fatal("expected error when ~/ root cannot be expanded due to missing HOME, got nil")
	}
	if !strings.Contains(err.Error(), "home") && !strings.Contains(err.Error(), "HOME") {
		t.Errorf("error %q does not mention home directory resolution", err.Error())
	}
}

// TestUnknownKeysIgnored verifies that Load silently drops unknown TOML
// keys, including removed fields like sort_order, refresh_ms, and
// blacklist, and still succeeds.
func TestUnknownKeysIgnored(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `
roots        = ["`+tmp+`"]
sort_order   = ["running","frecency"]
refresh_ms   = 2000
blacklist    = ["**/node_modules"]

[default_session]
agent = "opencode"

[theme]
accent = "#FF0000"

[agents]
claude   = "/usr/local/bin/claude"
`)

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("Load with old config keys must not error: %v", err)
	}

	if len(cfg.Roots) != 1 || cfg.Roots[0] != tmp {
		t.Errorf("Roots = %v; want [%s]", cfg.Roots, tmp)
	}
}
