package discover

import "testing"

// TestFrecencyScore_BucketBoundaries pins the multiplier that frecencyScore
// selects on each side of every time-bucket boundary. The buckets use
// strict-less-than comparisons: d < frecencyHour means "recent". So the
// boundary value itself falls into the next, older bucket. The test injects
// a fixed now, so the assertions are exact, not relative.
func TestFrecencyScore_BucketBoundaries(t *testing.T) {
	const now int64 = 10_000_000 // arbitrary fixed "now"
	const rank = 10.0

	cases := []struct {
		name     string
		age      int64 // seconds since lastAccessed (now - lastAccessed)
		wantMult float64
	}{
		{"just-below hour boundary", frecencyHour - 1, frecencyMultRecent},
		{"exactly hour boundary", frecencyHour, frecencyMultToday},
		{"just-below day boundary", frecencyDay - 1, frecencyMultToday},
		{"exactly day boundary", frecencyDay, frecencyMultWeek},
		{"just-below week boundary", frecencyWeek - 1, frecencyMultWeek},
		{"exactly week boundary", frecencyWeek, frecencyMultOld},
		{"well past a week", frecencyWeek * 3, frecencyMultOld},
		{"accessed exactly now", 0, frecencyMultRecent},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := frecencyScore(rank, now-tc.age, now)
			want := rank * tc.wantMult
			if got != want {
				t.Errorf("frecencyScore(rank=%v, age=%ds) = %v, want %v (mult %v)",
					rank, tc.age, got, want, tc.wantMult)
			}
		})
	}
}

// TestFrecencyScore_MoreRecentRanksHigher asserts that, with equal rank, a
// more recently accessed project scores at least as high as a less recent
// one. When the two fall into different time buckets, the more recent one
// scores strictly higher.
func TestFrecencyScore_MoreRecentRanksHigher(t *testing.T) {
	const now int64 = 10_000_000
	const rank = 5.0

	recent := frecencyScore(rank, now-(frecencyHour/2), now) // within the hour
	older := frecencyScore(rank, now-(frecencyDay*2), now)   // a couple of days old
	if !(recent > older) {
		t.Errorf("more-recent score %v must exceed older score %v", recent, older)
	}
}

// TestFrecencyScore_MoreFrequentRanksHigher asserts that, within the same time
// bucket, a higher rank (visit frequency) yields a higher score.
func TestFrecencyScore_MoreFrequentRanksHigher(t *testing.T) {
	const now int64 = 10_000_000
	var age int64 = frecencyHour / 2 // both within the same (recent) bucket

	frequent := frecencyScore(20.0, now-age, now)
	rare := frecencyScore(2.0, now-age, now)
	if !(frequent > rare) {
		t.Errorf("higher-rank score %v must exceed lower-rank score %v in the same bucket", frequent, rare)
	}
}

// TestSortedPaths_OrdersByFrecency exercises the public ordering: more-recent and
// more-frequent projects sort ahead, with alphabetical tie-breaking.
func TestSortedPaths_OrdersByFrecency(t *testing.T) {
	const now int64 = 10_000_000
	projects := map[string]ProjectStat{
		"/old":    {Rank: 10, LastAccessed: now - frecencyWeek*2}, // 0.25× = 2.5
		"/recent": {Rank: 10, LastAccessed: now - frecencyHour/2}, // 4.0×  = 40
		"/today":  {Rank: 10, LastAccessed: now - frecencyDay/2},  // 2.0×  = 20
	}
	got := SortedPaths(projects, now)
	want := []string{"/recent", "/today", "/old"}
	if len(got) != len(want) {
		t.Fatalf("SortedPaths len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SortedPaths = %v, want %v", got, want)
		}
	}
}
