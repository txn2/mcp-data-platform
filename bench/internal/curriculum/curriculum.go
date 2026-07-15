// Package curriculum defines the cold-start knowledge-growth schema (issue
// #963): an ordered sequence of teaching lessons that, run against an empty
// enrichment layer, progressively promote knowledge into the platform's sinks
// (DataHub entity descriptions and portal knowledge pages). A fixed evaluation
// suite is re-run after each lesson so the harness can plot a learning curve —
// answer accuracy and enrichment coverage as a function of accumulated,
// promoted knowledge.
//
// A curriculum is the teach-and-promote half of the S5 lifecycle (issue #944)
// generalized into a sequence: each lesson states a fact conversationally,
// captures it as an insight, and a reviewer promotes it to a sink, exactly as a
// protocol's teach+promote stages do. The difference is what is measured — not
// a single teach-once-answer-forever transition, but the whole eval set's
// accuracy climbing from the empty floor toward the fully-documented ceiling as
// lessons accumulate. Ground truth for the eval set is the committed S3 task
// suite, whose answers are computed from the seeded dataset (never hand-typed),
// exactly as the S1-S3 and S5 truths are.
package curriculum

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

// Curriculum is one cold-start knowledge-growth script: an ordered lesson
// sequence plus the fixed eval suite re-run at every checkpoint. It is pure
// generated data over a fixed runner, like the task and protocol sets.
type Curriculum struct {
	// ID is unique across the curriculum set, e.g. "cs-traps".
	ID string `yaml:"id" json:"id"`
	// Title is a human label for the report.
	Title string `yaml:"title" json:"title"`
	// EvalSuite names the committed task suite re-run at each checkpoint
	// (e.g. "s3", the knowledge-trap suite). The runner loads it from the tasks
	// directory, so the eval ground truth stays generated, never duplicated here.
	EvalSuite string `yaml:"eval_suite" json:"eval_suite"`
	// Lessons are the ordered teaching steps. The learning curve is measured at
	// the empty baseline and after each lesson is promoted, so lesson order is
	// the curve's x-axis: earlier lessons unlock the traps that depend only on
	// them, later multi-fact traps flip once all their lessons have landed.
	Lessons []Lesson `yaml:"lessons" json:"lessons"`
}

// Lesson is one teach-and-promote step. It carries everything the runner needs
// to state the fact, verify capture, and promote it to a sink — the same fields
// a protocol's teach+promote path uses.
type Lesson struct {
	// ID is unique within the curriculum, e.g. "cs-units-cents".
	ID string `yaml:"id" json:"id"`
	// Title is a human label for the curve breakdown.
	Title string `yaml:"title" json:"title"`
	// TrapClass names the S3 trap class this lesson primarily unlocks
	// (e.g. "units_cents"), so the report can attribute a curve step to a class.
	TrapClass string `yaml:"trap_class" json:"trap_class"`
	// Fact is the domain fact taught, verified as a captured insight, and — for
	// the datahub sink — promoted as the entity description detail.
	Fact string `yaml:"fact" json:"fact"`
	// EntityURN anchors the captured insight and is the datahub-sink apply target.
	EntityURN string `yaml:"entity_urn" json:"entity_urn"`
	// Sink selects the promotion destination: protocol.SinkDataHub or
	// protocol.SinkKnowledgePage, the two knowledge-delivery channels.
	Sink string `yaml:"sink" json:"sink"`
	// Page is the knowledge_page payload, required when Sink is a page sink.
	Page *protocol.PagePayload `yaml:"page,omitempty" json:"page,omitempty"`
	// BudgetToolCalls caps tool calls in the teach episode.
	BudgetToolCalls int `yaml:"budget_tool_calls" json:"budget_tool_calls"`
	// Teach is the fact-capture episode (prompt states the fact, agent saves it).
	Teach protocol.TeachStage `yaml:"teach" json:"teach"`
}

// Validate rejects a malformed curriculum at load time so a broken set fails
// before any session is spent on it.
func (c Curriculum) Validate() error {
	switch {
	case c.ID == "":
		return errors.New("curriculum with empty id")
	case c.Title == "":
		return fmt.Errorf("curriculum %s: empty title", c.ID)
	case c.EvalSuite == "":
		return fmt.Errorf("curriculum %s: empty eval_suite", c.ID)
	case len(c.Lessons) == 0:
		return fmt.Errorf("curriculum %s: no lessons", c.ID)
	}
	seen := map[string]bool{}
	for _, l := range c.Lessons {
		if err := l.validate(c.ID); err != nil {
			return err
		}
		if seen[l.ID] {
			return fmt.Errorf("curriculum %s: duplicate lesson id %s", c.ID, l.ID)
		}
		seen[l.ID] = true
	}
	return nil
}

// validate checks one lesson's required fields and sink payload.
func (l Lesson) validate(curriculumID string) error {
	switch {
	case l.ID == "":
		return fmt.Errorf("curriculum %s: lesson with empty id", curriculumID)
	case l.Title == "":
		return fmt.Errorf("curriculum %s: lesson %s empty title", curriculumID, l.ID)
	case l.TrapClass == "":
		return fmt.Errorf("curriculum %s: lesson %s empty trap_class", curriculumID, l.ID)
	case l.Fact == "":
		return fmt.Errorf("curriculum %s: lesson %s empty fact", curriculumID, l.ID)
	case l.EntityURN == "":
		return fmt.Errorf("curriculum %s: lesson %s empty entity_urn", curriculumID, l.ID)
	case l.BudgetToolCalls <= 0:
		return fmt.Errorf("curriculum %s: lesson %s budget_tool_calls must be positive", curriculumID, l.ID)
	case l.Teach.Prompt == "":
		return fmt.Errorf("curriculum %s: lesson %s empty teach prompt", curriculumID, l.ID)
	}
	return l.validateSink(curriculumID)
}

// validateSink checks the sink is known and its payload is present, mirroring
// the protocol sink contract so a lesson promotes exactly as a protocol does.
func (l Lesson) validateSink(curriculumID string) error {
	switch l.Sink {
	case protocol.SinkDataHub:
		return nil
	case protocol.SinkKnowledgePage:
		if l.Page == nil || l.Page.Slug == "" || l.Page.Title == "" || l.Page.Body == "" {
			return fmt.Errorf("curriculum %s: lesson %s knowledge_page sink requires a complete page payload", curriculumID, l.ID)
		}
		return nil
	default:
		return fmt.Errorf("curriculum %s: lesson %s unknown sink %q", curriculumID, l.ID, l.Sink)
	}
}

// Load reads every *.yaml curriculum in dir (sorted by filename), validating
// each and rejecting duplicate IDs.
func Load(dir string) ([]Curriculum, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read curriculum dir: %w", err)
	}
	var curricula []Curriculum
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- operator-supplied curriculum dir
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var c Curriculum
		if err := yaml.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("duplicate curriculum id %s", c.ID)
		}
		seen[c.ID] = true
		curricula = append(curricula, c)
	}
	if len(curricula) == 0 {
		return nil, fmt.Errorf("no curricula found in %s", dir)
	}
	return curricula, nil
}

// Hash returns the canonical SHA-256 of the curriculum set (sorted by ID, JSON
// encoded) for the run manifest, mirroring task.Hash and protocol.Hash.
func Hash(curricula []Curriculum) string {
	sorted := make([]Curriculum, len(curricula))
	copy(sorted, curricula)
	slices.SortFunc(sorted, func(a, b Curriculum) int { return strings.Compare(a.ID, b.ID) })
	raw, err := json.Marshal(sorted)
	if err != nil {
		// Curriculum is a plain data struct; marshal cannot fail on validated input.
		panic(fmt.Sprintf("marshal curriculum set: %v", err))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
