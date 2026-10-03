package envsync

import "testing"

// TestComputeDelta_PathStaysBounded: the drawer's login rc prepends to a
// PATH that already carries the previous reload's prepends; the captured
// value is deduplicated (first occurrence wins), so repeated reloads do not
// keep growing it.
func TestComputeDelta_PathStaysBounded(t *testing.T) {
	baseline := baselineMap([]string{"PATH=/usr/bin:/bin"})
	first := computeDelta(baseline, []string{"PATH=/home/u/bin:/usr/bin:/bin"})
	second := computeDelta(baseline, []string{"PATH=/home/u/bin:/home/u/bin:/usr/bin:/bin"})
	if !equalStringSlices(first, second) || !equalStringSlices(first, []string{"PATH=/home/u/bin:/usr/bin:/bin"}) {
		t.Errorf("PATH grew across reloads: %v then %v", first, second)
	}
	if got := computeDelta(baseline, []string{"PATH=/usr/bin:/bin:/usr/bin"}); got != nil {
		t.Errorf("a PATH equal to the baseline once deduplicated must not be captured: %v", got)
	}
}
