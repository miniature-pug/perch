package tui

import (
	"reflect"
	"testing"
)

func TestPlanSwapIn(t *testing.T) {
	tests := []struct {
		name      string
		displayed string
		placehold string
		target    string
		want      []swapOp
	}{
		{
			name:      "nothing displayed: single swap into the frame",
			displayed: "",
			placehold: "%PL",
			target:    "%A",
			want:      []swapOp{{src: "%A", dst: "%PL"}},
		},
		{
			name:      "switch from A to B: send A home then bring B in",
			displayed: "%A",
			placehold: "%PL",
			target:    "%B",
			want: []swapOp{
				{src: "%A", dst: "%PL"},
				{src: "%B", dst: "%PL"},
			},
		},
		{
			name:      "target already displayed: no-op",
			displayed: "%A",
			placehold: "%PL",
			target:    "%A",
			want:      nil,
		},
		{
			name:      "empty target: no-op",
			displayed: "%A",
			placehold: "%PL",
			target:    "",
			want:      nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planSwapIn(tt.displayed, tt.placehold, tt.target)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("planSwapIn(%q,%q,%q) = %v, want %v",
					tt.displayed, tt.placehold, tt.target, got, tt.want)
			}
		})
	}
}

func TestPlanSwapHome(t *testing.T) {
	if ops := planSwapHome("", "%PL"); ops != nil {
		t.Errorf("planSwapHome with nothing displayed = %v, want nil", ops)
	}
	got := planSwapHome("%A", "%PL")
	want := []swapOp{{src: "%A", dst: "%PL"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("planSwapHome(%q,%q) = %v, want %v", "%A", "%PL", got, want)
	}
}

// The placeholder id is invariant across a swap-in: a switch A→B parks the
// placeholder in B's home but never changes its identity. This guards the
// "exactly one placeholder for the frame's lifetime" invariant at the planner
// level (the caller must keep passing the same placeholder id).
func TestPlanSwapIn_PlaceholderIsConstantTarget(t *testing.T) {
	ops := planSwapIn("%A", "%PL", "%B")
	for _, op := range ops {
		if op.dst != "%PL" {
			t.Errorf("every swap must target the single placeholder %%PL, got dst=%q", op.dst)
		}
	}
}
