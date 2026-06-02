package tui

import (
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/config"
)

// TestConfig_CfgField verifies that tui.Config.GlobalCfg threads the loaded
// *config.Config through to m.cfg on the Model, and that RefreshMs still
// drives the status tick interval (the two fields are independent paths, as
// in production main.go both are set from the loaded config).
func TestConfig_CfgField(t *testing.T) {
	c := &config.Config{RefreshMs: 250}
	cfg := Config{
		GlobalCfg: c,
		RefreshMs: 250, // mirrors main.go: set both fields from the loaded config
	}

	// Replicate Run's wiring without starting the Bubble Tea program.
	ldr := loader{GlobalCfg: cfg.GlobalCfg}
	m := New(nil).WithLoader(ldr).WithRefresh(time.Duration(cfg.RefreshMs) * time.Millisecond)

	if m.cfg == nil {
		t.Error("m.cfg should be non-nil when tui.Config.GlobalCfg is set")
	}
	want := 250 * time.Millisecond
	if m.refresh != want {
		t.Errorf("m.refresh = %v, want %v", m.refresh, want)
	}
}

// TestConfig_NilCfg verifies that a tui.Config with no GlobalCfg set leaves m.cfg
// nil (test / scaffold mode) without panicking.
func TestConfig_NilCfg(t *testing.T) {
	m := New(nil).WithLoader(loader{}) // no GlobalCfg set
	if m.cfg != nil {
		t.Errorf("m.cfg should be nil when loader.GlobalCfg is not set; got %v", m.cfg)
	}
	// refresh should still default to 1 s (WithRefresh not called explicitly)
	if m.refresh != time.Second {
		t.Errorf("m.refresh = %v, want 1s default", m.refresh)
	}
}

// TestConfig_FrameFields verifies that the FrameSession and PlaceholderPane
// fields exist on Config and that Run seeds the Model when both are non-empty.
// We exercise the seeding logic directly via the model constructor path used
// in Run rather than calling Run (which would start a real TUI program).
func TestConfig_FrameFields(t *testing.T) {
	cfg := Config{
		FrameSession:    "perch",
		PlaceholderPane: "%2",
	}
	// Replicate Run's seeding logic without starting the Bubble Tea program.
	m := New(nil)
	if cfg.FrameSession != "" && cfg.PlaceholderPane != "" {
		m.frameSession = cfg.FrameSession
		m.placeholderPaneID = cfg.PlaceholderPane
	}
	if !m.inFrame() {
		t.Error("inFrame() should return true when FrameSession+PlaceholderPane set")
	}
	if m.frameSession != "perch" {
		t.Errorf("frameSession=%q, want perch", m.frameSession)
	}
	if m.placeholderPaneID != "%2" {
		t.Errorf("placeholderPaneID=%q, want %%2", m.placeholderPaneID)
	}
}

// TestConfig_EmptyFrameFields verifies that an empty FrameSession/PlaceholderPane
// leaves the Model in direct-TUI mode (inFrame() == false).
func TestConfig_EmptyFrameFields(t *testing.T) {
	cfg := Config{
		FrameSession:    "",
		PlaceholderPane: "",
	}
	m := New(nil)
	if cfg.FrameSession != "" && cfg.PlaceholderPane != "" {
		m.frameSession = cfg.FrameSession
		m.placeholderPaneID = cfg.PlaceholderPane
	}
	if m.inFrame() {
		t.Error("inFrame() should return false when FrameSession+PlaceholderPane are empty")
	}
}

// TestConfig_PartialFrameFields verifies that setting only one of
// FrameSession/PlaceholderPane is treated as direct-TUI mode (not in-frame).
func TestConfig_PartialFrameFields(t *testing.T) {
	for _, tc := range []struct {
		session     string
		placeholder string
	}{
		{"perch", ""},
		{"", "%2"},
	} {
		m := New(nil)
		if tc.session != "" && tc.placeholder != "" {
			m.frameSession = tc.session
			m.placeholderPaneID = tc.placeholder
		}
		if m.inFrame() {
			t.Errorf("inFrame() should be false for partial frame fields session=%q placeholder=%q",
				tc.session, tc.placeholder)
		}
	}
}
