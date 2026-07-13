package judge

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// CalItem is one human-labeled calibration example.
type CalItem struct {
	RubricID string `yaml:"rubric_id" json:"rubric_id"`
	Answer   string `yaml:"answer" json:"answer"`
	Human    bool   `yaml:"human" json:"human"`
}

// Calibration is the committed set of labeled items.
type Calibration struct {
	Items []CalItem `yaml:"items" json:"items"`
}

// LoadCalibration reads the calibration set, validating that every item names a
// rubric criterion.
func LoadCalibration(path string, rubric *Rubric) (*Calibration, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied calibration path
	if err != nil {
		return nil, fmt.Errorf("read calibration: %w", err)
	}
	var c Calibration
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse calibration %s: %w", path, err)
	}
	if len(c.Items) == 0 {
		return nil, fmt.Errorf("calibration %s: no items", path)
	}
	for i, it := range c.Items {
		if _, ok := rubric.Criteria[it.RubricID]; !ok {
			return nil, fmt.Errorf("calibration item %d: unknown rubric id %q", i, it.RubricID)
		}
	}
	return &c, nil
}

// Disagreement records one calibration item where judge and human differed.
type Disagreement struct {
	RubricID string `json:"rubric_id"`
	Answer   string `json:"answer"`
	Human    bool   `json:"human"`
	Judge    bool   `json:"judge"`
	Raw      string `json:"raw"`
}

// ClassAgreement is per-rubric-class agreement.
type ClassAgreement struct {
	Total      int `json:"total"`
	Agreements int `json:"agreements"`
}

// CalibrationResult is the judge-vs-human agreement over the calibration set.
type CalibrationResult struct {
	Model         string                    `json:"model"`
	RubricVersion string                    `json:"rubric_version"`
	Total         int                       `json:"total"`
	Agreements    int                       `json:"agreements"`
	AgreementRate float64                   `json:"agreement_rate"`
	Errored       int                       `json:"errored"`
	ByClass       map[string]ClassAgreement `json:"by_class"`
	Disagreements []Disagreement            `json:"disagreements"`
}

// Calibrate runs the judge over the calibration set and computes its agreement
// with the human labels. An item the judge cannot answer (YES/NO parse failure)
// is counted as an error and excluded from agreement, surfaced in Errored — a
// judge that cannot render a verdict is a judge problem, not silent agreement.
func Calibrate(ctx context.Context, adapter llm.Adapter, rubric *Rubric, cal *Calibration) (CalibrationResult, error) {
	res := CalibrationResult{Model: rubric.Model, RubricVersion: rubric.Version, ByClass: map[string]ClassAgreement{}}
	for _, it := range cal.Items {
		v, err := rubric.Judge(ctx, adapter, it.RubricID, it.Answer)
		if err != nil {
			res.Errored++
			continue
		}
		res.Total++
		class := res.ByClass[it.RubricID]
		class.Total++
		if v.Pass == it.Human {
			res.Agreements++
			class.Agreements++
		} else {
			res.Disagreements = append(res.Disagreements, Disagreement{
				RubricID: it.RubricID, Answer: it.Answer, Human: it.Human, Judge: v.Pass, Raw: v.Raw,
			})
		}
		res.ByClass[it.RubricID] = class
	}
	if res.Total > 0 {
		res.AgreementRate = float64(res.Agreements) / float64(res.Total)
	}
	return res, nil
}

// Summary renders the calibration result for a terminal and the results page.
func (r CalibrationResult) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "judge calibration: model=%s rubric=%s\n", r.Model, r.RubricVersion)
	fmt.Fprintf(&b, "  agreement %d/%d = %.1f%% (%d errored)\n", r.Agreements, r.Total, r.AgreementRate*100, r.Errored)
	classes := make([]string, 0, len(r.ByClass))
	for c := range r.ByClass {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	for _, c := range classes {
		ca := r.ByClass[c]
		fmt.Fprintf(&b, "  %-18s %d/%d\n", c, ca.Agreements, ca.Total)
	}
	if len(r.Disagreements) > 0 {
		fmt.Fprintf(&b, "  %d disagreement(s):\n", len(r.Disagreements))
		for _, d := range r.Disagreements {
			fmt.Fprintf(&b, "    [%s] human=%v judge=%v: %.80s\n", d.RubricID, d.Human, d.Judge, d.Answer)
		}
	}
	return b.String()
}
