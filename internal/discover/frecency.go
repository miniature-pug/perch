package discover

import "sort"

// ProjectStat holds the two fields that drive frecency ranking.
// Inlined from internal/state after that package's deletion.
type ProjectStat struct {
	Rank         float64 `json:"rank"`
	LastAccessed int64   `json:"last_accessed"`
}

const (
	frecencyHour = 3_600
	frecencyDay  = 86_400
	frecencyWeek = 604_800

	frecencyMultRecent = 4.0  // accessed within the last hour
	frecencyMultToday  = 2.0  // accessed within the last day
	frecencyMultWeek   = 0.5  // accessed within the last week
	frecencyMultOld    = 0.25 // accessed more than a week ago
)

func frecencyScore(rank float64, lastAccessed, now int64) float64 {
	switch d := now - lastAccessed; {
	case d < frecencyHour:
		return rank * frecencyMultRecent
	case d < frecencyDay:
		return rank * frecencyMultToday
	case d < frecencyWeek:
		return rank * frecencyMultWeek
	default:
		return rank * frecencyMultOld
	}
}

// SortedPaths returns project paths sorted by descending frecency score.
// Ties break alphabetically. Matches the contract of the deleted state.SortedPaths.
func SortedPaths(projects map[string]ProjectStat, now int64) []string {
	paths := make([]string, 0, len(projects))
	for k := range projects {
		paths = append(paths, k)
	}
	sort.Slice(paths, func(i, j int) bool {
		si := frecencyScore(projects[paths[i]].Rank, projects[paths[i]].LastAccessed, now)
		sj := frecencyScore(projects[paths[j]].Rank, projects[paths[j]].LastAccessed, now)
		if si != sj {
			return si > sj
		}
		return paths[i] < paths[j]
	})
	return paths
}
