// Package trust records which project .perch.toml files the user has approved to
// run shell hooks. A repo's hooks are never executed until its config is approved
// once; editing the config re-requires approval (the content hash changes).
package trust

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Hash returns the hex sha256 of a .perch.toml's raw bytes.
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum)
}

// Store is a set of approved configs: absolute config path → approved content hash.
type Store struct {
	path    string
	entries map[string]string
}

// Load reads the trust file at path. A missing file yields an empty Store (no
// error). A malformed file is an error (callers may treat as "nothing trusted").
func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Store{path: path, entries: make(map[string]string)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("trust: read %s: %w", path, err)
	}

	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("trust: parse %s: %w", path, err)
	}
	if entries == nil {
		entries = make(map[string]string)
	}
	return &Store{path: path, entries: entries}, nil
}

// Trusted reports whether configPath was approved with this exact content hash.
func (s *Store) Trusted(configPath, hash string) bool {
	return s.entries[configPath] == hash
}

// Approve records configPath@hash and atomically writes the trust file (0600,
// temp+rename, mkdir 0700). Replaces any prior hash for the same path.
func (s *Store) Approve(configPath, hash string) error {
	s.entries[configPath] = hash

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("trust: mkdir %s: %w", dir, err)
	}

	data, err := json.Marshal(s.entries)
	if err != nil {
		return fmt.Errorf("trust: marshal trust store: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "*.tmp")
	if err != nil {
		return fmt.Errorf("trust: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("trust: chmod temp file %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("trust: write temp file %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("trust: close temp file %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("trust: rename %s → %s: %w", tmpName, s.path, err)
	}
	ok = true
	return nil
}
