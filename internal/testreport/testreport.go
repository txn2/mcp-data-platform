// Package testreport is what a saved script version keeps of its tests
// (#1972): each test_* function's outcome at the save and the share of the
// version's statements the tests reach. It is its own package so a version
// record in pkg/script carries it without growing that package's public
// surface, and so the save gate that produces it and the store that keeps it
// share one type.
package testreport

// Report is a saved version's tests.
type Report struct {
	Tests    []Outcome `json:"tests"`
	Passed   int       `json:"passed" example:"3"`
	Failed   int       `json:"failed" example:"0"`
	Coverage Coverage  `json:"coverage"`
}

// Outcome is one test_* function's result at the save.
type Outcome struct {
	Name    string `json:"name" example:"test_empty_window"`
	Passed  bool   `json:"passed"`
	Line    int    `json:"line,omitempty" example:"120"`
	Failure string `json:"failure,omitempty"`
}

// Coverage is the statements the tests reach, and the lines of the ones they
// do not.
type Coverage struct {
	Statements  int     `json:"statements" example:"140"`
	Covered     int     `json:"covered" example:"126"`
	Percent     float64 `json:"percent" example:"90"`
	MissedLines []int   `json:"missed_lines"`
}
