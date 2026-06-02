// Package attach is a stub pending deletion in Task 0.2.
package attach

import "context"

type Candidate struct {
	Project, Branch, Tool, TmuxSession, TmuxWindow, LiveTarget string
	IsLive                                                      bool
}
type Result struct {
	Count     int
	Matched   *Candidate
	Ambiguous []Candidate
}
type Deps struct{}

func Gather(_ context.Context, _ Deps) ([]Candidate, error) { return nil, nil }
func Resolve(_ string, _ []Candidate) Result                 { return Result{} }
func FormatAmbiguous(_ []Candidate) string                   { return "" }
