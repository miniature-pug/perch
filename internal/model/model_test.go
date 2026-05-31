package model

import (
	"encoding/json"
	"testing"
)

// windowJSONKeys is the exact set of JSON field names the Window wire format must have.
// This list is the authoritative contract — any accidental tag rename or addition
// will cause TestWindowWireContract to fail.
var windowJSONKeys = map[string]struct{}{
	"pane_key":     {},
	"tool":         {},
	"session_id":   {},
	"tree":         {},
	"tmux_session": {},
	"tmux_window":  {},
	"boot_id":      {},
	"updated":      {},
}

// TestWindowWireContract marshals a Window, verifies the produced JSON contains
// exactly the expected keys (no more, no less), and then round-trips through
// Unmarshal to confirm the value survives intact.
func TestWindowWireContract(t *testing.T) {
	original := Window{
		PaneKey:     "%17",
		Tool:        ToolOpencode,
		SessionID:   "ses_18a0abcd",
		Tree:        "/abs/path/workflows__worktrees/feat-aligner",
		TmuxSession: "workflows",
		TmuxWindow:  "feat-aligner",
		BootID:      "1748470000",
		Updated:     1748476800,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	// Decode into a raw map to inspect keys without type coercion.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal into map: %v", err)
	}

	// Assert no unexpected keys.
	for k := range raw {
		if _, ok := windowJSONKeys[k]; !ok {
			t.Errorf("unexpected JSON key %q in marshalled Window", k)
		}
	}
	// Assert no missing keys.
	for k := range windowJSONKeys {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing expected JSON key %q in marshalled Window", k)
		}
	}

	// Assert tool serializes as the bare string "opencode", not a quoted integer.
	var toolVal string
	if err := json.Unmarshal(raw["tool"], &toolVal); err != nil {
		t.Fatalf("unmarshal tool field: %v", err)
	}
	if toolVal != "opencode" {
		t.Errorf("tool field: got %q, want %q", toolVal, "opencode")
	}

	// Full round-trip: unmarshal back into a Window and compare field by field.
	var got Window
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal into Window: %v", err)
	}
	if got != original {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, original)
	}
}

func TestToolValid(t *testing.T) {
	cases := []struct {
		tool  Tool
		valid bool
	}{
		{ToolClaude, true},
		{ToolOpencode, true},
		{"", false},
		{"gemini", false},
		{"CLAUDE", false},
		{"openCode", false},
	}
	for _, tc := range cases {
		got := tc.tool.Valid()
		if got != tc.valid {
			t.Errorf("Tool(%q).Valid() = %v, want %v", tc.tool, got, tc.valid)
		}
	}
}
