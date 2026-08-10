package model

import (
	"testing"
)

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
