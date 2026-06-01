package trust

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEmptyStoreEntrusts nothing.
func TestEmptyStore_TrustsNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if s.Trusted("/some/path/.perch.toml", "abc123") {
		t.Error("empty store must trust nothing")
	}
}

// TestApprove_ThenTrusted verifies the happy path.
func TestApprove_ThenTrusted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	configPath := "/repo/.perch.toml"
	hash := Hash([]byte("post_create = [\"echo hi\"]"))

	if err := s.Approve(configPath, hash); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !s.Trusted(configPath, hash) {
		t.Error("Trusted must return true after Approve with the same hash")
	}
}

// TestApprove_WrongHash verifies that a different hash is rejected.
func TestApprove_WrongHash(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	configPath := "/repo/.perch.toml"
	hash := Hash([]byte("original content"))
	if err := s.Approve(configPath, hash); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	wrongHash := Hash([]byte("edited content"))
	if s.Trusted(configPath, wrongHash) {
		t.Error("Trusted must return false for a different hash (simulates edited config)")
	}
}

// TestApprove_UnknownPath verifies an unknown path is not trusted.
func TestApprove_UnknownPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	hash := Hash([]byte("content"))
	if err := s.Approve("/approved/repo/.perch.toml", hash); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	if s.Trusted("/other/repo/.perch.toml", hash) {
		t.Error("Trusted must return false for an unknown path")
	}
}

// TestMalformedFile verifies that a malformed trust file returns an error.
func TestMalformedFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	if err := os.WriteFile(path, []byte("not valid json {{{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load must return an error for a malformed trust file")
	}
}

// TestApprove_FileMode verifies the trust file is written with mode 0600.
func TestApprove_FileMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := s.Approve("/repo/.perch.toml", Hash([]byte("x"))); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("trust file mode = %04o; want 0600", mode)
	}
}

// TestApprove_ReplacesPriorHash verifies that Approve replaces a prior hash for the same path.
func TestApprove_ReplacesPriorHash(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	configPath := "/repo/.perch.toml"
	hash1 := Hash([]byte("v1"))
	hash2 := Hash([]byte("v2"))

	if err := s.Approve(configPath, hash1); err != nil {
		t.Fatalf("Approve(hash1): %v", err)
	}
	if err := s.Approve(configPath, hash2); err != nil {
		t.Fatalf("Approve(hash2): %v", err)
	}

	if s.Trusted(configPath, hash1) {
		t.Error("after re-approve, old hash must no longer be trusted")
	}
	if !s.Trusted(configPath, hash2) {
		t.Error("after re-approve, new hash must be trusted")
	}
}

// TestApprove_Atomic verifies no .tmp file is left behind after a successful Approve.
func TestApprove_Atomic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Approve("/repo/.perch.toml", Hash([]byte("content"))); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("stale .tmp file left behind: %s", e.Name())
		}
	}
}

// TestLoad_Roundtrip verifies that a store persisted by Approve can be re-loaded.
func TestLoad_Roundtrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	configPath := "/repo/.perch.toml"
	hash := Hash([]byte("hello"))

	s1, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s1.Approve(configPath, hash); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	s2, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Approve: %v", err)
	}
	if !s2.Trusted(configPath, hash) {
		t.Error("re-loaded store must trust the previously approved config@hash")
	}
}

// TestLoad_NonExistentReadError verifies that a non-ErrNotExist error from os.ReadFile
// is surfaced rather than swallowed (e.g. permission denied).
func TestLoad_PermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read any file; permission test skipped")
	}
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	// Write a valid file then remove read permission.
	if err := os.WriteFile(path, []byte("{}"), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load must return an error for a permission-denied file")
	}
}

// TestApprove_MkdirAllError verifies that Approve returns an error when the
// parent directory cannot be created (path component is a regular file).
func TestApprove_MkdirAllError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// Create a regular file where the subdirectory should be.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Use a path whose parent directory cannot be created.
	path := filepath.Join(blocker, "trust.json")
	s := &Store{path: path, entries: make(map[string]string)}

	err := s.Approve("/repo/.perch.toml", Hash([]byte("x")))
	if err == nil {
		t.Error("Approve must return an error when parent dir cannot be created")
	}
}

// TestApprove_CreateTempError verifies that Approve returns an error when temp
// file creation fails (directory made read-only after MkdirAll).
func TestApprove_CreateTempError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write to read-only dirs; test skipped")
	}
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	// Load (creates empty store), then make dir read-only so CreateTemp fails.
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := os.Chmod(dir, 0o400); err != nil {
		t.Fatalf("Chmod dir: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()

	err = s.Approve("/repo/.perch.toml", Hash([]byte("x")))
	if err == nil {
		t.Error("Approve must return an error when CreateTemp fails")
	}
}

// TestApprove_RenameError verifies that Approve returns an error when the atomic
// rename fails (target path already exists as a directory).
func TestApprove_RenameError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	// Load creates an empty store with path=path. Now create path as a directory
	// so the rename into it fails.
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Create a directory at the target path so os.Rename fails.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	defer func() { _ = os.RemoveAll(path) }()

	err = s.Approve("/repo/.perch.toml", Hash([]byte("x")))
	if err == nil {
		t.Error("Approve must return an error when rename fails")
	}
}

// TestHash_Deterministic verifies that Hash returns a stable, non-empty value.
func TestHash_Deterministic(t *testing.T) {
	content := []byte("post_create = [\"echo hi\"]")
	h1 := Hash(content)
	h2 := Hash(content)
	if h1 != h2 {
		t.Error("Hash is not deterministic")
	}
	if h1 == "" {
		t.Error("Hash returned empty string")
	}
	if len(h1) != 64 {
		t.Errorf("Hash length = %d; want 64 (hex sha256)", len(h1))
	}
}
