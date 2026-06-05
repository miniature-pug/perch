package app

import "testing"

// TestDefaultsSyncedWithFrontend is the Go half of a cross-language sync guard. It
// PINS the Go-side settings defaults and pty event-name prefixes to their literal
// values.
//
// MIRROR: frontend/src/lib/constants.ts (DEFAULT_THEME/DEFAULT_DENSITY/DEFAULT_FONT)
// and frontend/src/lib/wails.ts (EVT_PTY_DATA_PREFIX/EVT_PTY_EXIT_PREFIX) — a
// frontend test pins the same literals. If you change one side, both tests fail
// until re-synced. These literals are a wire/UX contract: the pty prefixes form the
// exact Wails event names the frontend subscribes to (a drift silently breaks pty
// output/exit delivery), and the defaults must match so a fresh install renders the
// same theme/density/font on either side.
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
