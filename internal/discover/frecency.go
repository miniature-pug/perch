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
)

func frecencyScore(rank float64, lastAccessed, now int64) float64 {
	switch d := now - lastAccessed; {
	case d < frecencyHour:
		return rank * 4.0
	case d < frecencyDay:
		return rank * 2.0
	case d < frecencyWeek:
		return rank * 0.5
	default:
		return rank * 0.25
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
