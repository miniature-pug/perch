package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/miniature-pug/perch/app"
	"github.com/miniature-pug/perch/internal/desktop"
)

const fakeBin = "/opt/perch/bin/perch"

// stubDesktopSeams replaces every desktop seam and restores them on cleanup.
// The defaults pretend to be a regular user running the binary at fakeBin.
func stubDesktopSeams(t *testing.T) {
	t.Helper()
	oSelf, oInstall, oIcon, oNeeds, oMissing, oEuid :=
		selfPath, installDesktop, installDesktopIcon, desktopNeedsUpdate, desktopIconMissing, geteuid
	t.Cleanup(func() {
		selfPath, installDesktop, installDesktopIcon = oSelf, oInstall, oIcon
		desktopNeedsUpdate, desktopIconMissing, geteuid = oNeeds, oMissing, oEuid
	})
	selfPath = func() (string, error) { return fakeBin, nil }
	geteuid = func() int { return 1000 }
	t.Setenv("SUDO_USER", "")
	t.Setenv("PERCH_NO_DESKTOP_ENTRY", "")
}

// noRefresh keeps desktop.Install from running gtk-update-icon-cache and
// update-desktop-database against the test's data home.
func noRefresh(o desktop.Options) desktop.Options {
	o.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	return o
}

func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("desktop entries are Linux only")
	}
}

func TestInstallDesktop_WritesEntryAndIconUnderXDGDataHome(t *testing.T) {
	requireLinux(t)
	stubDesktopSeams(t)
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	installDesktop = func(o desktop.Options) error { return desktop.Install(noRefresh(o)) }

	out, errOut, code := callRun([]string{"install-desktop"})
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	entry, err := os.ReadFile(filepath.Join(dataHome, "applications", "perch.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), `Exec="`+fakeBin+`"`) {
		t.Errorf("perch.desktop does not launch %s:\n%s", fakeBin, entry)
	}
	icon, err := os.ReadFile(filepath.Join(dataHome, "icons", "hicolor", "512x512", "apps", "perch.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(icon, app.AppIcon()) || !bytes.HasPrefix(icon, []byte("\x89PNG")) {
		t.Errorf("installed icon is not the embedded app icon (%d bytes)", len(icon))
	}
	for _, want := range []string{"perch.desktop", "perch.png", "Exec=" + fakeBin} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestInstallDesktop_PassesBinaryAndIcon(t *testing.T) {
	requireLinux(t)
	stubDesktopSeams(t)
	var got desktop.Options
	installDesktop = func(o desktop.Options) error { got = o; return nil }
	if _, _, code := callRun([]string{"install-desktop"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got.BinPath != fakeBin || len(got.Icon) == 0 {
		t.Errorf("Install got BinPath %q and %d icon bytes", got.BinPath, len(got.Icon))
	}
}

func TestInstallDesktop_Failures(t *testing.T) {
	requireLinux(t)
	t.Run("extra argument", func(t *testing.T) {
		stubDesktopSeams(t)
		installDesktop = func(desktop.Options) error { t.Error("installed"); return nil }
		_, errOut, code := callRun([]string{"install-desktop", "x"})
		if code != 2 || !strings.Contains(errOut, "Usage: perch install-desktop") {
			t.Errorf("exit %d, stderr %q", code, errOut)
		}
	})
	t.Run("install error reaches stderr", func(t *testing.T) {
		stubDesktopSeams(t)
		installDesktop = func(desktop.Options) error { return errors.New("path contains %") }
		out, errOut, code := callRun([]string{"install-desktop"})
		if code != 1 || !strings.Contains(errOut, "path contains %") || out != "" {
			t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
	t.Run("binary path unknown", func(t *testing.T) {
		stubDesktopSeams(t)
		selfPath = func() (string, error) { return "", errors.New("no /proc") }
		_, errOut, code := callRun([]string{"install-desktop"})
		if code != 1 || !strings.Contains(errOut, "no /proc") {
			t.Errorf("exit %d, stderr %q", code, errOut)
		}
	})
	t.Run("under sudo", func(t *testing.T) {
		stubDesktopSeams(t)
		geteuid = func() int { return 0 }
		t.Setenv("SUDO_USER", "alice")
		installDesktop = func(desktop.Options) error { t.Error("installed as root"); return nil }
		_, errOut, code := callRun([]string{"install-desktop"})
		if code != 1 || !strings.Contains(errOut, "sudo") {
			t.Errorf("exit %d, stderr %q", code, errOut)
		}
	})
}

// install.sh asks a binary whether it has the subcommand by grepping the
// usage that an unknown, non-existent path prints:
//
//	"$BIN" /nonexistent/.perch-usage-probe 2>&1 | grep -q 'perch install-desktop'
func TestUsage_AdvertisesInstallDesktopForInstallSh(t *testing.T) {
	var buf bytes.Buffer
	printUsage(&buf)
	if !strings.Contains(buf.String(), "perch install-desktop") {
		t.Errorf("usage lacks the install.sh probe string:\n%s", buf.String())
	}
	_, errOut, code := callRun([]string{"/nonexistent/.perch-usage-probe"})
	if code != 2 || !strings.Contains(errOut, "perch install-desktop") {
		t.Errorf("probe run: exit %d, stderr %q", code, errOut)
	}
	if sh, err := os.ReadFile(filepath.Join("..", "..", "install.sh")); err == nil &&
		!strings.Contains(string(sh), "grep -q 'perch install-desktop'") {
		t.Error("install.sh no longer probes for 'perch install-desktop'; update this test and printUsage together")
	}
}

// ── ensureDesktopEntry ────────────────────────────────────────────────────────

type desktopCalls struct{ install, icon atomic.Int32 }

func stubEnsure(t *testing.T, needsUpdate, iconMissing bool) *desktopCalls {
	t.Helper()
	stubDesktopSeams(t)
	c := &desktopCalls{}
	desktopNeedsUpdate = func(string) bool { return needsUpdate }
	desktopIconMissing = func() bool { return iconMissing }
	installDesktop = func(desktop.Options) error { c.install.Add(1); return nil }
	installDesktopIcon = func(desktop.Options) error { c.icon.Add(1); return nil }
	return c
}

func TestEnsureDesktopEntry(t *testing.T) {
	requireLinux(t)
	for _, tc := range []struct {
		name                    string
		needsUpdate, iconMissed bool
		wantInstall, wantIcon   int32
	}{
		{"missing or dangling entry installs everything", true, true, 1, 0},
		{"missing or dangling entry, icon present", true, false, 1, 0},
		{"entry for another working binary, icon missing: icon only", false, true, 0, 1},
		{"entry for another working binary, icon present: nothing", false, false, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := stubEnsure(t, tc.needsUpdate, tc.iconMissed)
			var log bytes.Buffer
			<-ensureDesktopEntry(&log)
			if c.install.Load() != tc.wantInstall || c.icon.Load() != tc.wantIcon {
				t.Errorf("install %d icon %d, want %d %d", c.install.Load(), c.icon.Load(), tc.wantInstall, tc.wantIcon)
			}
			if log.Len() != 0 {
				t.Errorf("unexpected log output %q", log.String())
			}
		})
	}
}

func TestEnsureDesktopEntry_ErrorsAreLoggedNotFatal(t *testing.T) {
	requireLinux(t)
	c := stubEnsure(t, true, false)
	installDesktop = func(desktop.Options) error { c.install.Add(1); return errors.New("disk full") }
	var log bytes.Buffer
	<-ensureDesktopEntry(&log)
	if !strings.Contains(log.String(), "disk full") {
		t.Errorf("log = %q, want the install error", log.String())
	}
	c = stubEnsure(t, false, true)
	installDesktopIcon = func(desktop.Options) error { return errors.New("read-only") }
	log.Reset()
	<-ensureDesktopEntry(&log)
	if !strings.Contains(log.String(), "read-only") {
		t.Errorf("log = %q, want the icon error", log.String())
	}
}

func TestEnsureDesktopEntry_PanicIsContained(t *testing.T) {
	requireLinux(t)
	stubEnsure(t, true, false)
	installDesktop = func(desktop.Options) error { panic("boom") }
	var log bytes.Buffer
	<-ensureDesktopEntry(&log)
	if !strings.Contains(log.String(), "boom") {
		t.Errorf("log = %q", log.String())
	}
}

func TestEnsureDesktopEntry_Skips(t *testing.T) {
	requireLinux(t)
	skip := map[string]func(t *testing.T){
		"root": func(*testing.T) { geteuid = func() int { return 0 } },
		"opt-out": func(t *testing.T) {
			t.Setenv("PERCH_NO_DESKTOP_ENTRY", "1")
		},
		"go run binary": func(*testing.T) {
			selfPath = func() (string, error) { return "/home/u/.cache/go-build/ab/perch", nil }
		},
		"temp dir binary": func(*testing.T) {
			selfPath = func() (string, error) { return filepath.Join(os.TempDir(), "x", "perch"), nil }
		},
		"unknown path": func(*testing.T) {
			selfPath = func() (string, error) { return "", errors.New("no /proc") }
		},
	}
	for name, setup := range skip {
		t.Run(name, func(t *testing.T) {
			c := stubEnsure(t, true, true)
			setup(t)
			<-ensureDesktopEntry(&bytes.Buffer{})
			if c.install.Load()+c.icon.Load() != 0 {
				t.Error("installed although it must be skipped")
			}
		})
	}
}

// A hand-made entry that launches another working binary is never
// overwritten by the automatic install; only the missing icon is added.
func TestEnsureDesktopEntry_NeverOverwritesHandMadeEntry(t *testing.T) {
	requireLinux(t)
	stubDesktopSeams(t)
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	desktopNeedsUpdate, desktopIconMissing = desktop.NeedsUpdate, desktop.IconMissing
	installDesktop = func(o desktop.Options) error { return desktop.Install(noRefresh(o)) }
	installDesktopIcon = func(o desktop.Options) error { return desktop.InstallIcon(noRefresh(o)) }

	entryPath := filepath.Join(dataHome, "applications", "perch.desktop")
	handMade := "[Desktop Entry]\nType=Application\nName=My perch\nExec=/bin/sh -c perch\nIcon=my-own-icon\n"
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, []byte(handMade), 0o644); err != nil {
		t.Fatal(err)
	}
	<-ensureDesktopEntry(&bytes.Buffer{})
	got, _ := os.ReadFile(entryPath)
	if string(got) != handMade {
		t.Errorf("hand-made entry was rewritten:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dataHome, "icons", "hicolor", "512x512", "apps", "perch.png")); err != nil {
		t.Errorf("the missing icon was not installed: %v", err)
	}

	// A dangling entry (its Exec target is gone) is replaced.
	dangling := "[Desktop Entry]\nType=Application\nName=perch\nExec=/nonexistent/perch\n"
	if err := os.WriteFile(entryPath, []byte(dangling), 0o644); err != nil {
		t.Fatal(err)
	}
	<-ensureDesktopEntry(&bytes.Buffer{})
	got, _ = os.ReadFile(entryPath)
	if !strings.Contains(string(got), `Exec="`+fakeBin+`"`) {
		t.Errorf("dangling entry was not replaced:\n%s", got)
	}
}
