// Package judge is the benchmark's LLM judge for judgment-call rubric items
// (#943): the deterministic graders score numbers and entities, but "did the
// answer carry the required caveat" is a judgment call. The judge model is
// pinned and the rubric versioned (bench/judge/rubric.yaml); the judge's
// agreement with human labels is measured over a committed calibration set
// (bench/judge/calibration.yaml) and published with any judged scores, so a
// reader knows how much to trust them.
package judge

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// judgeSystem is the fixed judge instruction. It is part of the versioned
// rubric contract: changing it must bump the rubric version.
const judgeSystem = "You are a strict grader for a data-analysis benchmark. " +
	"You are given a scoring criterion and an analyst's final answer. Decide whether the answer satisfies the criterion. " +
	"Judge only what the answer states, not what it might have intended. Respond with exactly one word: YES or NO."

// Criterion is one judged rubric item: a single yes/no question about an answer.
type Criterion struct {
	Question string `yaml:"question"`
}

// Rubric is the versioned judge rubric with its pinned model.
type Rubric struct {
	Version  string               `yaml:"version"`
	Model    string               `yaml:"model"`
	Criteria map[string]Criterion `yaml:"criteria"`
}

// LoadRubric reads and validates a rubric file.
func LoadRubric(path string) (*Rubric, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied rubric path
	if err != nil {
		return nil, fmt.Errorf("read rubric: %w", err)
	}
	var r Rubric
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse rubric %s: %w", path, err)
	}
	if r.Version == "" || r.Model == "" || len(r.Criteria) == 0 {
		return nil, fmt.Errorf("rubric %s: version, model, and at least one criterion are required", path)
	}
	return &r, nil
}

// Verdict is one judged outcome.
type Verdict struct {
	RubricID string `json:"rubric_id"`
	Pass     bool   `json:"pass"`
	Raw      string `json:"raw"` // the judge's raw response, for audit
}

// Judge scores one answer against one rubric criterion using the adapter. The
// caller is responsible for building the adapter with the rubric's pinned model
// (benchrun's calibrate path constructs it from rubric.Model); Judge itself does
// not enforce the model, so it stays testable with a scripted adapter.
func (r *Rubric) Judge(ctx context.Context, adapter llm.Adapter, rubricID, answer string) (Verdict, error) {
	crit, ok := r.Criteria[rubricID]
	if !ok {
		return Verdict{}, fmt.Errorf("unknown rubric id %q", rubricID)
	}
	user := fmt.Sprintf("Criterion: %s\n\nAnalyst's final answer:\n%s\n\nDoes the answer satisfy the criterion? Answer YES or NO.",
		crit.Question, answer)
	msg, _, err := adapter.Complete(ctx, judgeSystem, []llm.Message{{Role: "user", Text: user}}, nil)
	if err != nil {
		return Verdict{RubricID: rubricID}, fmt.Errorf("judge completion: %w", err)
	}
	pass, ok := parseYesNo(msg.Text)
	if !ok {
		return Verdict{RubricID: rubricID, Raw: msg.Text}, fmt.Errorf("judge did not answer YES/NO: %.120q", msg.Text)
	}
	return Verdict{RubricID: rubricID, Pass: pass, Raw: msg.Text}, nil
}

// parseYesNo finds the first standalone YES or NO token in the judge's response,
// tolerating punctuation and surrounding prose.
func parseYesNo(text string) (pass, ok bool) {
	for _, f := range strings.FieldsFunc(strings.ToUpper(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		switch f {
		case "YES":
			return true, true
		case "NO":
			return false, true
		}
	}
	return false, false
}
