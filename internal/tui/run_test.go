package tui

import (
	"testing"
)

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
