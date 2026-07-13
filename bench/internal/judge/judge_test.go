package judge

import (
	"context"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// fixedJudge is a fake adapter that returns a canned verdict word for every
// completion, so the judge machinery is testable without a model.
type fixedJudge struct{ reply string }

func (f fixedJudge) Model() string { return "fixed" }
func (f fixedJudge) Complete(context.Context, string, []llm.Message, []llm.ToolDef) (llm.Message, llm.Usage, error) {
	return llm.Message{Role: "assistant", Text: f.reply}, llm.Usage{}, nil
}

func TestParseYesNo(t *testing.T) {
	cases := []struct {
		in   string
		pass bool
		ok   bool
	}{
		{"YES", true, true},
		{"NO", false, true},
		{"yes", true, true},
		{"Yes.", true, true},
		{"NO — the answer omits the caveat", false, true},
		{"The answer is YES because it mentions cents", true, true},
		{"maybe", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		pass, ok := parseYesNo(c.in)
		if pass != c.pass || ok != c.ok {
			t.Errorf("parseYesNo(%q) = %v,%v want %v,%v", c.in, pass, ok, c.pass, c.ok)
		}
	}
}

func testRubric() *Rubric {
	return &Rubric{Version: "t", Model: "fixed", Criteria: map[string]Criterion{
		"caveat-units": {Question: "Does it mention cents?"},
	}}
}

func TestJudge(t *testing.T) {
	r := testRubric()
	v, err := r.Judge(context.Background(), fixedJudge{"YES"}, "caveat-units", "amounts are in cents")
	if err != nil || !v.Pass {
		t.Errorf("Judge YES: pass=%v err=%v", v.Pass, err)
	}
	v, err = r.Judge(context.Background(), fixedJudge{"NO"}, "caveat-units", "12345")
	if err != nil || v.Pass {
		t.Errorf("Judge NO: pass=%v err=%v", v.Pass, err)
	}
	if _, err := r.Judge(context.Background(), fixedJudge{"maybe"}, "caveat-units", "x"); err == nil {
		t.Error("expected error on non-YES/NO judge reply")
	}
	if _, err := r.Judge(context.Background(), fixedJudge{"YES"}, "no-such-id", "x"); err == nil {
		t.Error("expected error on unknown rubric id")
	}
}

// TestCommittedRubricAndCalibration loads the shipped files and computes the
// deterministic agreement of degenerate always-YES / always-NO judges, which
// also proves the calibration set's label balance (12 YES / 18 NO of 30).
func TestCommittedRubricAndCalibration(t *testing.T) {
	rubric, err := LoadRubric("../../judge/rubric.yaml")
	if err != nil {
		t.Fatalf("LoadRubric: %v", err)
	}
	cal, err := LoadCalibration("../../judge/calibration.yaml", rubric)
	if err != nil {
		t.Fatalf("LoadCalibration: %v", err)
	}
	if len(cal.Items) != 30 {
		t.Fatalf("calibration items = %d, want 30", len(cal.Items))
	}
	yesRes, _ := Calibrate(context.Background(), fixedJudge{"YES"}, rubric, cal)
	if yesRes.Agreements != 12 || yesRes.Total != 30 {
		t.Errorf("always-YES agreement = %d/%d, want 12/30", yesRes.Agreements, yesRes.Total)
	}
	noRes, _ := Calibrate(context.Background(), fixedJudge{"NO"}, rubric, cal)
	if noRes.Agreements != 18 || noRes.Total != 30 {
		t.Errorf("always-NO agreement = %d/%d, want 18/30", noRes.Agreements, noRes.Total)
	}
	// The two degenerate judges must partition the set (every item is YES or NO).
	if yesRes.Agreements+noRes.Agreements != 30 {
		t.Errorf("YES+NO agreements = %d, want 30", yesRes.Agreements+noRes.Agreements)
	}
	// Every criterion in the rubric must appear in the calibration set.
	for id := range rubric.Criteria {
		if noRes.ByClass[id].Total+yesRes.ByClass[id].Total == 0 {
			t.Errorf("rubric criterion %q has no calibration items", id)
		}
	}
}

// TestCalibrateErrored counts unparseable judge replies as errors, not silent
// agreement.
func TestCalibrateErrored(t *testing.T) {
	rubric := testRubric()
	cal := &Calibration{Items: []CalItem{{RubricID: "caveat-units", Answer: "x", Human: true}}}
	res, _ := Calibrate(context.Background(), fixedJudge{"unsure"}, rubric, cal)
	if res.Errored != 1 || res.Total != 0 {
		t.Errorf("errored=%d total=%d, want 1/0", res.Errored, res.Total)
	}
}

func TestCalibrationSummary(t *testing.T) {
	rubric := testRubric()
	cal := &Calibration{Items: []CalItem{
		{RubricID: "caveat-units", Answer: "amounts in cents", Human: true},
		{RubricID: "caveat-units", Answer: "12345", Human: false},
	}}
	// The fixed-NO judge agrees with the second item, disagrees with the first.
	res, _ := Calibrate(context.Background(), fixedJudge{"NO"}, rubric, cal)
	s := res.Summary()
	for _, want := range []string{"judge calibration", "agreement 1/2", "caveat-units", "disagreement"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary missing %q:\n%s", want, s)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := LoadRubric("does-not-exist.yaml"); err == nil {
		t.Error("expected error loading missing rubric")
	}
	rubric := testRubric()
	if _, err := LoadCalibration("does-not-exist.yaml", rubric); err == nil {
		t.Error("expected error loading missing calibration")
	}
}
