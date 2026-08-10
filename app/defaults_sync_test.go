package app

import "testing"

// TestDefaultsSyncedWithFrontend is the Go half of a cross-language sync guard.
// The test pins the Go-side settings defaults and the pty event-name prefixes
// to their literal values.
//
// MIRROR: frontend/src/lib/constants.ts (DEFAULT_THEME/DEFAULT_DENSITY/DEFAULT_FONT)
// and frontend/src/lib/wails.ts (EVT_PTY_DATA_PREFIX/EVT_PTY_EXIT_PREFIX) pin
// the same literals in a frontend test. If you change a literal on one side,
// both tests fail until you re-sync the two sides. These literals form a wire
// and UX contract. The pty prefixes are the exact Wails event names that the
// frontend subscribes to, so a drift silently breaks pty output and exit
// delivery. The defaults must also match, so a fresh install renders the same
// theme, density, and font on both sides.
func TestDefaultsSyncedWithFrontend(t *testing.T) {
	t.Parallel()

	pins := []struct {
		name string
		got  string
		want string
	}{
		{"defaultTheme", defaultTheme, "gruvbox"},
		{"defaultDensity", defaultDensity, "dense"},
		{"defaultFont", defaultFont, "geist"},
		{"ptyDataEventPrefix", ptyDataEventPrefix, "pty:data:"},
		{"ptyExitEventPrefix", ptyExitEventPrefix, "pty:exit:"},
	}
	for _, p := range pins {
		if p.got != p.want {
			t.Errorf("%s = %q, want %q — re-sync with the mirrored frontend literal", p.name, p.got, p.want)
		}
	}
}
