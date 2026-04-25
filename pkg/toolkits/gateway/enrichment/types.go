// Package enrichment implements the cross-enrichment rule engine for the
// gateway toolkit. Rules declaratively describe how proxied upstream tool
// responses are augmented with context pulled from other platform sources
// (Trino, DataHub, other gateway connections).
package enrichment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrRuleNotFound is returned when Get or Delete target a missing rule id.
var ErrRuleNotFound = errors.New("enrichment: rule not found")

// Source kinds accepted in enrich_action.source. Operators cannot choose
// arbitrary kinds — only values in this list are dispatchable.
const (
	SourceTrino   = "trino"
	SourceDataHub = "datahub"
)

// Predicate kinds accepted in when_predicate.kind. An empty object is
// treated as PredicateAlways.
const (
	PredicateAlways           = "always"
	PredicateResponseContains = "response_contains"
)

// Merge kinds accepted in merge_strategy.kind. An empty object is treated
// as MergePath with path "enrichment".
const (
	MergePath = "path"
)

// Rule is a single enrichment configuration row. Fields map 1:1 to the
// gateway_enrichment_rules table.
type Rule struct {
	ID             string    `json:"id"`
	ConnectionName string    `json:"connection_name"`
	ToolName       string    `json:"tool_name"`
	WhenPredicate  Predicate `json:"when_predicate"`
	EnrichAction   Action    `json:"enrich_action"`
	MergeStrategy  Merge     `json:"merge_strategy"`
	Description    string    `json:"description,omitempty"`
	Enabled        bool      `json:"enabled"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Predicate decides whether a rule fires for a given tool call. v1
// supports "always" (default) and "response_contains" (fires when every
// listed JSON key path is present in the response).
type Predicate struct {
	Kind  string   `json:"kind,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

// Action describes the read-only operation an enrichment rule invokes.
// Source + Operation must resolve to an entry in the source adapter
// allowlist at evaluation time. Parameters carry source-specific inputs
// and JSONPath bindings for value substitution.
type Action struct {
	Source     string         `json:"source"`
	Operation  string         `json:"operation"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// Merge tells the engine how to attach the source result to the upstream
// response. Path mode places the result under response[path].
type Merge struct {
	Kind string `json:"kind,omitempty"`
	Path string `json:"path,omitempty"`
}

// Store abstracts persistence for enrichment rules. Empty filter args
// (connection, tool) mean "no constraint"; enabledOnly=true skips disabled rows.
type Store interface {
	List(ctx context.Context, connection, tool string, enabledOnly bool) ([]Rule, error)
	Get(ctx context.Context, id string) (*Rule, error)
	Create(ctx context.Context, r Rule) (Rule, error)
	Update(ctx context.Context, r Rule) (Rule, error)
	Delete(ctx context.Context, id string) error
}

// Validate checks the rule's shape. Call before persisting to give
// operators actionable errors up-front.
func (r Rule) Validate() error {
	if err := validateRequiredFields(r); err != nil {
		return err
	}
	if err := validateSource(r.EnrichAction); err != nil {
		return err
	}
	if err := validatePredicate(r.WhenPredicate); err != nil {
		return err
	}
	return validateMerge(r.MergeStrategy)
}

func validateRequiredFields(r Rule) error {
	if r.ConnectionName == "" {
		return errors.New("enrichment: connection_name is required")
	}
	if r.ToolName == "" {
		return errors.New("enrichment: tool_name is required")
	}
	if r.EnrichAction.Source == "" {
		return errors.New("enrichment: enrich_action.source is required")
	}
	if r.EnrichAction.Operation == "" {
		return errors.New("enrichment: enrich_action.operation is required")
	}
	return nil
}

func validateSource(a Action) error {
	switch a.Source {
	case SourceTrino, SourceDataHub:
		return nil
	default:
		return fmt.Errorf("enrichment: source %q is not in the allowlist", a.Source)
	}
}

func validatePredicate(p Predicate) error {
	switch p.Kind {
	case "", PredicateAlways, PredicateResponseContains:
	default:
		return fmt.Errorf("enrichment: predicate kind %q is not supported", p.Kind)
	}
	if p.Kind == PredicateResponseContains && len(p.Paths) == 0 {
		return errors.New("enrichment: response_contains predicate requires paths")
	}
	return nil
}

func validateMerge(m Merge) error {
	switch m.Kind {
	case "", MergePath:
		return nil
	default:
		return fmt.Errorf("enrichment: merge kind %q is not supported", m.Kind)
	}
}

// GenerateID returns a 32-character hex id suitable for use as a rule
// primary key.
func GenerateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate rule id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
