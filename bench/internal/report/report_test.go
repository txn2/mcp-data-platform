package report

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleResults() *Results {
	r := &Results{Manifest: Manifest{
		StartedAt: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC),
		Arm:       "a2", Model: "scripted", LLMProvider: "scripted", K: 2,
		GitCommit: "abcdef1234567890", TaskSetHash: "hash1234567890", PlatformVersion: "1.0.0",
	}}
	r.Attempts = []Attempt{
		{TaskID: "t1", Suite: "s1", Attempt: 1, Correct: true, ToolCalls: 2, WallMS: 100},
		{TaskID: "t1", Suite: "s1", Attempt: 2, Correct: true, ToolCalls: 4, WallMS: 200},
		{TaskID: "t2", Suite: "s3", Attempt: 1, Correct: true, ToolCalls: 3, WallMS: 300},
		{TaskID: "t2", Suite: "s3", Attempt: 2, Correct: false, ToolCalls: 9, WallMS: 400, ToolErrors: 2},
	}
	r.Aggregate()
	return r
}

func TestAggregate(t *testing.T) {
	r := sampleResults()
	if len(r.Tasks) != 2 || len(r.Suites) != 2 {
		t.Fatalf("aggregates: %d tasks %d suites", len(r.Tasks), len(r.Suites))
	}
	t1 := r.Tasks[0]
	if t1.TaskID != "t1" || !t1.PassK || t1.PassRate != 1.0 || t1.Graded != 2 {
		t.Errorf("t1 summary wrong: %+v", t1)
	}
	t2 := r.Tasks[1]
	if t2.PassK || t2.Correct != 1 {
		t.Errorf("t2 summary wrong: %+v", t2)
	}
	for _, s := range r.Suites {
		switch s.Suite {
		case "s1":
			if s.Accuracy != 1.0 || s.PassKRate != 1.0 || s.MedianToolCalls != 2 {
				t.Errorf("s1 summary wrong: %+v", s)
			}
		case "s3":
			if s.Accuracy != 0.5 || s.PassKRate != 0.0 || s.ToolErrors != 2 {
				t.Errorf("s3 summary wrong: %+v", s)
			}
		}
	}
}

func TestWriteAndLoadJSON(t *testing.T) {
	r := sampleResults()
	path := filepath.Join(t.TempDir(), "results.json")
	if err := r.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.Arm != "a2" || len(loaded.Attempts) != 4 {
		t.Errorf("round trip lost data: %+v", loaded.Manifest)
	}
}

func TestHarnessFailuresExcludedFromGrading(t *testing.T) {
	r := sampleResults()
	// A harness failure on an otherwise-perfect task must not dent accuracy,
	// but must also deny pass^k (the task was not graded k times).
	r.Attempts = []Attempt{
		r.Attempts[0], // t1 attempt 1, correct
		{TaskID: "t1", Suite: "s1", Attempt: 2, Error: "audit read-back: missing"},
	}
	r.Aggregate()
	t1 := r.Tasks[0]
	if t1.Graded != 1 || t1.Correct != 1 || t1.PassRate != 1.0 {
		t.Errorf("failure folded into grading: %+v", t1)
	}
	if t1.PassK {
		t.Errorf("pass^k claimed with a harness failure: %+v", t1)
	}
	if t1.HarnessFailures != 1 {
		t.Errorf("harness failure not counted: %+v", t1)
	}
	for _, s := range r.Suites {
		if s.Suite == "s1" && (s.Accuracy != 1.0 || s.Graded != 1 || s.HarnessFailures != 1) {
			t.Errorf("s1 suite mixed failure into accuracy: %+v", s)
		}
	}
}

func TestHumanSummary(t *testing.T) {
	r := sampleResults()
	r.Attempts = append(r.Attempts, Attempt{TaskID: "t3", Suite: "s3", Attempt: 1, Error: "audit read-back: missing"})
	r.Aggregate()
	sum := r.HumanSummary()
	for _, needle := range []string{"arm=a2", "s1", "s3", "t1", "harness failures (excluded from grading)", "audit read-back"} {
		if !strings.Contains(sum, needle) {
			t.Errorf("summary missing %q:\n%s", needle, sum)
		}
	}
}

func TestPercentile(t *testing.T) {
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("empty percentile = %v", got)
	}
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(vals, 0.5); got != 5 {
		t.Errorf("median = %v, want 5", got)
	}
	if got := percentile(vals, 0.9); got != 9 {
		t.Errorf("p90 = %v, want 9", got)
	}
}
