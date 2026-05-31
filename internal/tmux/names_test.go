package tmux

import (
	"testing"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Dot and colon replaced.
		{"next.js", "next-js"},
		{"release-1.2", "release-1-2"},
		{"a:b", "a-b"},
		// Slash kept as-is.
		{"a/b", "a/b"},
		// Whitespace trimmed/collapsed.
		{"  spaced name ", "spaced-name"},
		// All separators collapse to empty → unnamed.
		{"...", "unnamed"},
		// Empty string → unnamed.
		{"", "unnamed"},
		// Repeated separators collapse.
		{"a...b", "a-b"},
		{"a:::b", "a-b"},
		// Mixed kept/replaced.
		{"my-project_v1.0", "my-project_v1-0"},
		// Leading/trailing dashes stripped.
		{".hidden", "hidden"},
		{"trailing.", "trailing"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := sanitize(tc.input)
			if got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSessionName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/home/user/next.js", "next-js"},
		{"/projects/my-app", "my-app"},
		{"/repos/release-1.2", "release-1-2"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := SessionName(tc.path)
			if got != tc.want {
				t.Errorf("SessionName(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestWindowName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"main", "main"},
		{"feat/my-feature", "feat/my-feature"},
		{"release-1.2", "release-1-2"},
		{"feature:branch", "feature-branch"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := WindowName(tc.input)
			if got != tc.want {
				t.Errorf("WindowName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
