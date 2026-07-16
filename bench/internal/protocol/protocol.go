// Package protocol defines the S5 lifecycle protocol schema (issue #944): a
// multi-episode script that exercises the memory-insight-knowledge lifecycle as
// a sequence of fresh sessions, and whose state transitions are verified through
// the platform's own admin APIs (insights, changesets) rather than inferred from
// transcripts.
//
// A protocol runs the canonical lifecycle once (issue #930's S5 definition):
//
//	teach    an identity states a fact conversationally and captures it
//	recall   the same identity, a fresh session, answers a question needing it
//	promote  a reviewer applies the insight via apply_knowledge (one of two sinks)
//	transfer a DIFFERENT identity, a fresh session, answers the same question
//	update   the teacher supersedes the fact with a correction; recall flips
//	abstain  a question about a fact never taught must not be fabricated
//
// Ground truth (recall answers, the flipped update answer) is computed from the
// seeded dataset by the generator (bench/seedgen), never hand-typed, exactly as
// the S1-S3 task ground truths are.
package protocol

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

	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// Promotion sinks accepted by the promote stage, mirroring the apply_knowledge
// tool's `sink` argument.
const (
	// SinkDataHub promotes the insight to the anchored entity's catalog
	// description; the fact then reaches any identity via cross-enrichment.
	SinkDataHub = "datahub"
	// SinkKnowledgePage promotes the insight to a canonical portal knowledge
	// page; the fact then reaches any identity via the search tool.
	SinkKnowledgePage = "knowledge_page"
)

// Protocol is one lifecycle script. Every field the runner needs to drive and
// verify the six stages is data here, so the ~15 committed protocols are pure
// generated data over a fixed runner.
type Protocol struct {
	// ID is unique across the protocol set, e.g. "lc-net-revenue".
	ID string `yaml:"id" json:"id"`
	// Title is a human label for the report.
	Title string `yaml:"title" json:"title"`
	// Fact is the domain fact taught in the teach stage. It is also the text the
	// runner verifies was captured as an insight and, for the datahub sink, the
	// description promoted onto the entity.
	Fact string `yaml:"fact" json:"fact"`
	// EntityURN anchors the captured insight and is the datahub-sink apply
	// target. Recall/transfer questions concern this entity.
	EntityURN string `yaml:"entity_urn" json:"entity_urn"`
	// Sink selects the promotion destination: SinkDataHub or SinkKnowledgePage.
	// The two sinks are the two teach-once-answer-forever channels the S5
	// definition requires exercising.
	Sink string `yaml:"sink" json:"sink"`
	// Page is the knowledge_page payload, required when Sink == SinkKnowledgePage.
	Page *PagePayload `yaml:"page,omitempty" json:"page,omitempty"`
	// BudgetToolCalls caps tool calls per episode (each stage is one episode).
	BudgetToolCalls int `yaml:"budget_tool_calls" json:"budget_tool_calls"`

	// Teach is the fact-capture episode.
	Teach TeachStage `yaml:"teach" json:"teach"`
	// Recall is the personal-recall episode (same identity, fresh session).
	Recall RecallStage `yaml:"recall" json:"recall"`
	// Transfer is the cross-identity episode run after promotion. Optional: a
	// protocol without it is excluded from the transfer-rate metric. Mutually
	// exclusive with Update (see Validate): promotion makes the insight applied,
	// and the platform deliberately never supersedes an applied insight (a newer
	// capture must not clobber a reviewed one), so a protocol cannot both promote
	// and then supersede the same fact.
	Transfer *RecallStage `yaml:"transfer,omitempty" json:"transfer,omitempty"`
	// Update is the supersede episode plus its post-correction recall. Optional,
	// and mutually exclusive with Transfer (see Transfer).
	Update *UpdateStage `yaml:"update,omitempty" json:"update,omitempty"`
	// Abstain is the never-taught-fact episode. Optional.
	Abstain *AbstainStage `yaml:"abstain,omitempty" json:"abstain,omitempty"`
}

// PagePayload is the knowledge_page promotion target (sink=knowledge_page).
// Summary is the fact-bearing one-liner search renders next to the title: on
// tool surfaces without a page-body fetch tool (the a3 arm), the summary is the
// ONLY channel through which a promoted page's fact reaches an agent, so a
// page payload without one is a title-only search hit that delivers nothing.
type PagePayload struct {
	Slug    string `yaml:"slug" json:"slug"`
	Title   string `yaml:"title" json:"title"`
	Summary string `yaml:"summary" json:"summary"`
	Body    string `yaml:"body" json:"body"`
}

// TeachStage is the fact-capture episode. The prompt states the fact
// conversationally and instructs the agent to record it for future sessions;
// the runner verifies an insight was captured and linked to the entity.
type TeachStage struct {
	Prompt string `yaml:"prompt" json:"prompt"`
}

// RecallStage is a question episode graded deterministically against a computed
// answer. It backs both personal recall and cross-identity transfer.
type RecallStage struct {
	Prompt  string       `yaml:"prompt" json:"prompt"`
	Grading task.Grading `yaml:"grading" json:"grading"`
}

// UpdateStage supersedes the taught fact and re-checks recall. The correction
// prompt teaches the new fact; Recall grades the flipped answer; SupersededValue
// (numeric only, optional) is the pre-update answer the post-update recall must
// NOT return, so a stale recall is caught even when the new answer is missed.
type UpdateStage struct {
	Prompt          string      `yaml:"prompt" json:"prompt"`
	Fact            string      `yaml:"fact" json:"fact"`
	Recall          RecallStage `yaml:"recall" json:"recall"`
	SupersededValue *float64    `yaml:"superseded_value,omitempty" json:"superseded_value,omitempty"`
}

// AbstainStage asks about a fact never taught. A correct outcome is an explicit
// abstention; any concrete answer is a fabrication.
type AbstainStage struct {
	Prompt string `yaml:"prompt" json:"prompt"`
}

// Validate rejects a malformed protocol at load time so a broken set fails
// before any session is spent on it.
func (p Protocol) Validate() error {
	if err := p.validateHeader(); err != nil {
		return err
	}
	if err := p.validateSink(); err != nil {
		return err
	}
	if p.Teach.Prompt == "" {
		return fmt.Errorf("protocol %s: empty teach prompt", p.ID)
	}
	if err := validateStageGrading(p.ID, "recall", p.Recall); err != nil {
		return err
	}
	return p.validateOptionalStages()
}

// validateOptionalStages checks the transfer, update, and abstain stages when
// present. Split from Validate to keep each function's branch count in bounds.
func (p Protocol) validateOptionalStages() error {
	if p.Transfer != nil && p.Update != nil {
		return fmt.Errorf("protocol %s: transfer and update are mutually exclusive (an applied insight cannot be superseded)", p.ID)
	}
	if p.Transfer != nil {
		if err := validateStageGrading(p.ID, "transfer", *p.Transfer); err != nil {
			return err
		}
	}
	if p.Update != nil {
		if err := p.validateUpdate(); err != nil {
			return err
		}
	}
	if p.Abstain != nil && p.Abstain.Prompt == "" {
		return fmt.Errorf("protocol %s: abstain stage has empty prompt", p.ID)
	}
	return nil
}

// validateHeader checks the protocol-level required fields.
func (p Protocol) validateHeader() error {
	switch {
	case p.ID == "":
		return errors.New("protocol with empty id")
	case p.Title == "":
		return fmt.Errorf("protocol %s: empty title", p.ID)
	case p.Fact == "":
		return fmt.Errorf("protocol %s: empty fact", p.ID)
	case p.EntityURN == "":
		return fmt.Errorf("protocol %s: empty entity_urn", p.ID)
	case p.BudgetToolCalls <= 0:
		return fmt.Errorf("protocol %s: budget_tool_calls must be positive", p.ID)
	}
	return nil
}

// validateSink checks the sink is known and its payload is present.
func (p Protocol) validateSink() error {
	switch p.Sink {
	case SinkDataHub:
		return nil
	case SinkKnowledgePage:
		if p.Page == nil || p.Page.Slug == "" || p.Page.Title == "" || p.Page.Body == "" {
			return fmt.Errorf("protocol %s: knowledge_page sink requires a complete page payload", p.ID)
		}
		// The summary is required, not optional: search renders a page hit as
		// title plus summary and the a3 tool surface has no page-body fetch, so a
		// promoted page with an empty summary structurally cannot deliver its
		// fact — the run would spend its budget measuring an impossible channel.
		if p.Page.Summary == "" {
			return fmt.Errorf("protocol %s: knowledge_page sink requires a non-empty page summary (search delivers the fact through it)", p.ID)
		}
		return nil
	default:
		return fmt.Errorf("protocol %s: unknown sink %q", p.ID, p.Sink)
	}
}

// validateUpdate checks the supersede stage.
func (p Protocol) validateUpdate() error {
	if p.Update.Prompt == "" {
		return fmt.Errorf("protocol %s: update stage has empty prompt", p.ID)
	}
	if p.Update.Fact == "" {
		return fmt.Errorf("protocol %s: update stage has empty fact", p.ID)
	}
	return validateStageGrading(p.ID, "update recall", p.Update.Recall)
}

// validateStageGrading reuses the task grading validation for a recall stage,
// which the deterministic graders (numeric, entity) score identically to a task.
func validateStageGrading(id, stage string, r RecallStage) error {
	if r.Prompt == "" {
		return fmt.Errorf("protocol %s: %s stage has empty prompt", id, stage)
	}
	t := task.Task{
		ID:              id + "/" + stage,
		Suite:           "s5",
		Prompt:          r.Prompt,
		Arms:            []string{"a3"},
		BudgetToolCalls: 1,
		Grading:         r.Grading,
	}
	if err := t.Validate(); err != nil {
		return fmt.Errorf("protocol %s: %s grading: %w", id, stage, err)
	}
	if r.Grading.Kind == task.GradeExecSQL {
		return fmt.Errorf("protocol %s: %s stage cannot use exec_sql grading", id, stage)
	}
	return nil
}

// Load reads every *.yaml protocol in dir (sorted by filename), validating each
// and rejecting duplicate IDs.
func Load(dir string) ([]Protocol, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read protocol dir: %w", err)
	}
	var protocols []Protocol
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- operator-supplied protocol dir
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var p Protocol
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("duplicate protocol id %s", p.ID)
		}
		seen[p.ID] = true
		protocols = append(protocols, p)
	}
	if len(protocols) == 0 {
		return nil, fmt.Errorf("no protocols found in %s", dir)
	}
	return protocols, nil
}

// Hash returns the canonical SHA-256 of the protocol set (sorted by ID, JSON
// encoded) for the run manifest, mirroring task.Hash.
func Hash(protocols []Protocol) string {
	sorted := make([]Protocol, len(protocols))
	copy(sorted, protocols)
	slices.SortFunc(sorted, func(a, b Protocol) int { return strings.Compare(a.ID, b.ID) })
	raw, err := json.Marshal(sorted)
	if err != nil {
		// Protocol is a plain data struct; marshal cannot fail on validated input.
		panic(fmt.Sprintf("marshal protocol set: %v", err))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
