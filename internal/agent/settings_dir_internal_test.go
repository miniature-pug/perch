package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/hooklistener"
)

// TestWriteSettings_PrefersXDGRuntimeDir: the per-session settings file
// lives under $XDG_RUNTIME_DIR (per-user, never age-cleaned) when it is set.
func TestWriteSettings_PrefersXDGRuntimeDir(t *testing.T) {
	rt := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv("TMPDIR", t.TempDir())
	l, err := hooklistener.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	m := NewClaudeMonitorWithListener(NewClaude(), l)
	if _, err := m.Prepare(t.Context(), "ws", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Teardown() }()
	if !strings.HasPrefix(m.HookSettingsPath(), rt+string(os.PathSeparator)) {
		t.Errorf("settings path %q not under XDG_RUNTIME_DIR %q", m.HookSettingsPath(), rt)
	}
	if !strings.Contains(filepath.Base(filepath.Dir(m.HookSettingsPath())), "-"+strconv.Itoa(os.Getpid())+"-") {
		t.Errorf("settings dir %q does not carry the owner pid", m.HookSettingsPath())
	}
}

// TestSweepStaleSettingsDirs removes a crashed perch's leftovers (dead pid,
// or a pid-less name older than a day) and keeps live ones and others'.
func TestSweepStaleSettingsDirs(t *testing.T) {
	root := t.TempDir()
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Skip("cannot spawn a process")
	}
	deadPid := dead.Process.Pid
	mk := func(name string, age time.Duration) string {
		p := filepath.Join(root, name)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-age)
		_ = os.Chtimes(p, old, old)
		return p
	}
	deadDir := mk(settingsDirPrefix+strconv.Itoa(deadPid)+"-abc", time.Minute)
	liveDir := mk(settingsDirPrefix+strconv.Itoa(os.Getpid())+"-abc", 48*time.Hour)
	oldAnon := mk(settingsDirPrefix+"xyz", 48*time.Hour)
	newAnon := mk(settingsDirPrefix+"qrs", time.Minute)
	other := mk("unrelated-"+strconv.Itoa(deadPid), 48*time.Hour)

	sweepStaleSettingsDirs([]string{root})

	for _, p := range []string{deadDir, oldAnon} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("stale dir %s kept", filepath.Base(p))
		}
	}
	for _, p := range []string{liveDir, newAnon, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("dir %s wrongly removed", filepath.Base(p))
		}
	}
}
