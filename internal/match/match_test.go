package match_test

import (
	"testing"

	"github.com/Miniature-Pug/perch/internal/match"
)

func TestMatchAny(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		path     string
		want     bool
	}{
		{
			name:     "**/archive/** matches nested",
			patterns: []string{"**/archive/**"},
			path:     "/home/u/proj/archive/old/x",
			want:     true,
		},
		{
			name:     "**/archive/** no match",
			patterns: []string{"**/archive/**"},
			path:     "/home/u/proj/src/x",
			want:     false,
		},
		{
			name:     "* single segment (basename semantics)",
			patterns: []string{"*.tmp"},
			path:     "foo.tmp",
			want:     true,
		},
		{
			name:     "empty patterns never match",
			patterns: nil,
			path:     "/anything",
			want:     false,
		},
		{
			name:     "invalid pattern is ignored (not a match, not a panic)",
			patterns: []string{"[", "**/x/**"},
			path:     "/a/x/b",
			want:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := match.MatchAny(tc.patterns, tc.path)
			if got != tc.want {
				t.Errorf("MatchAny(%v, %q) = %v, want %v", tc.patterns, tc.path, got, tc.want)
			}
		})
	}
}
