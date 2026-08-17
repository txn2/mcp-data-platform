package callrecord

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNotPromotable is returned when a record is not in a state a reviewer can
// act on: it never answered anything, or it has already been promoted or
// declined.
var ErrNotPromotable = errors.New("only a satisfied record that has not already been promoted or rejected can be promoted")

// ErrNoPromotionTarget is returned when the platform has nowhere to promote a
// record to: no DataHub connection for a query, no endpoint identity for an API
// call. It is a configuration answer, not a validation failure, and the caller
// reports it as such rather than telling the reviewer they did something wrong.
var ErrNoPromotionTarget = errors.New("no promotion target is configured for this kind of record")

// CuratedQueryWriter is the DataHub write path a promoted query takes. It is
// the platform's existing curated-query write (pkg/toolkits/knowledge's
// DataHubWriter satisfies it), narrowed to the one method promotion needs, so a
// promoted record and an apply_knowledge proposal reach DataHub the same way
// rather than through two write paths that could drift.
type CuratedQueryWriter interface {
	CreateCuratedQuery(ctx context.Context, datasetURNs []string, name, sql, description string) (string, error)
}

// ExampleWriter saves a promoted API call as an example on its endpoint, so the
// next agent reading that endpoint's schema sees a request that is known to
// have worked. It is the API catalog's counterpart to a DataHub Query entity:
// an endpoint has no catalog entity of its own to attach a query to.
type ExampleWriter interface {
	SaveExample(ctx context.Context, ex Example) (string, error)
}

// Example is one endpoint invocation worth keeping.
type Example struct {
	Connection  string
	OperationID string
	Method      string
	Path        string
	Name        string
	Description string
	// CallRecordID is the record the example was promoted from, so the
	// endpoint can lead back to the call that produced it.
	CallRecordID string
	CreatedBy    string
}

// Promoter turns a satisfied record into something the whole platform can see.
type Promoter struct {
	store    Store
	queries  CuratedQueryWriter
	examples ExampleWriter
}

// NewPromoter builds the promotion path. Either writer may be nil: a
// deployment with no DataHub cannot promote a query, and one with no example
// store cannot promote an API call, and each refuses only its own kind.
func NewPromoter(store Store, queries CuratedQueryWriter, examples ExampleWriter) *Promoter {
	return &Promoter{store: store, queries: queries, examples: examples}
}

// Promote publishes one record and records what it became.
//
// The scope is the caller's: an owner promotes their own record from the
// portal, and a reviewer promotes any record from the operator surface. Both
// reach the same code, so what promotion means does not depend on which page it
// was started from.
func (p *Promoter) Promote(ctx context.Context, scope Scope, actor string) (*Record, error) {
	rec, err := p.store.Get(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("reading call record: %w", err)
	}
	if !rec.Promotable() {
		return nil, ErrNotPromotable
	}

	urn, err := p.publish(ctx, *rec, actor)
	if err != nil {
		return nil, err
	}
	if err := p.store.Promote(ctx, rec.ID, Promotion{URN: urn, Actor: actor}); err != nil {
		return nil, fmt.Errorf("recording the promotion: %w", err)
	}
	return p.reread(ctx, scope)
}

// publish writes the record to wherever its kind belongs and returns the
// reference it now has there.
func (p *Promoter) publish(ctx context.Context, rec Record, actor string) (string, error) {
	if rec.Kind == KindAPI {
		return p.publishExample(ctx, rec, actor)
	}
	return p.publishQuery(ctx, rec)
}

// publishQuery creates the DataHub Query entity for a promoted statement,
// against every dataset the query read rather than one of them: a query that
// joins three tables belongs to all three, and DataHub's own write path already
// accepts the set.
func (p *Promoter) publishQuery(ctx context.Context, rec Record) (string, error) {
	if p.queries == nil {
		return "", ErrNoPromotionTarget
	}
	if rec.Statement == "" {
		return "", fmt.Errorf("the record kept no statement to promote: %w", ErrNotPromotable)
	}
	urn, err := p.queries.CreateCuratedQuery(ctx, rec.Targets, PromotedName(rec), rec.Statement, PromotedDescription(rec))
	if err != nil {
		return "", fmt.Errorf("creating catalog query: %w", err)
	}
	return urn, nil
}

// publishExample saves a promoted API call as an example on its endpoint.
func (p *Promoter) publishExample(ctx context.Context, rec Record, actor string) (string, error) {
	if p.examples == nil {
		return "", ErrNoPromotionTarget
	}
	if rec.OperationID == "" && rec.Path == "" {
		return "", fmt.Errorf("the record names no endpoint: %w", ErrNotPromotable)
	}
	ref, err := p.examples.SaveExample(ctx, Example{
		Connection:   rec.Connection,
		OperationID:  rec.OperationID,
		Method:       rec.Method,
		Path:         rec.Path,
		Name:         PromotedName(rec),
		Description:  PromotedDescription(rec),
		CallRecordID: rec.ID,
		CreatedBy:    actor,
	})
	if err != nil {
		return "", fmt.Errorf("saving endpoint example: %w", err)
	}
	return ref, nil
}

// Reject records that a reviewer declined the record, so the queue does not
// offer it again.
func (p *Promoter) Reject(ctx context.Context, scope Scope, actor, note string) (*Record, error) {
	rec, err := p.store.Get(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("reading call record: %w", err)
	}
	if !rec.Promotable() {
		return nil, ErrNotPromotable
	}
	if err := p.store.Reject(ctx, rec.ID, Rejection{Actor: actor, Note: note}); err != nil {
		return nil, fmt.Errorf("recording the rejection: %w", err)
	}
	return p.reread(ctx, scope)
}

// reread returns the record as it stands after a decision, so the caller
// answers with what was written rather than with what it sent.
func (p *Promoter) reread(ctx context.Context, scope Scope) (*Record, error) {
	rec, err := p.store.Get(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("re-reading call record: %w", err)
	}
	return rec, nil
}

// maxPromotedNameLen bounds the name a promoted record carries into the
// catalog. A purpose is one sentence, and a catalog list is unreadable when an
// entry is a paragraph.
const maxPromotedNameLen = 120

// PromotedName is what the promoted record is called in the catalog: the
// purpose its caller stated, which is the only human sentence a call ever
// carries. A record with no purpose falls back to what it addressed, so an
// entry is never nameless.
func PromotedName(rec Record) string {
	if name := strings.TrimSpace(rec.Purpose); name != "" {
		return truncate(name, maxPromotedNameLen)
	}
	switch {
	case rec.OperationID != "":
		return rec.OperationID
	case rec.Path != "":
		return truncate(strings.TrimSpace(rec.Method+" "+rec.Path), maxPromotedNameLen)
	default:
		return truncate(rec.ToolName+" on "+rec.Connection, maxPromotedNameLen)
	}
}

// PromotedDescription is what the catalog entry says about itself: the purpose,
// and where the call came from. The session is named because a promoted query
// is evidence, and evidence is worth being able to walk back to.
func PromotedDescription(rec Record) string {
	parts := []string{}
	if purpose := strings.TrimSpace(rec.Purpose); purpose != "" {
		parts = append(parts, purpose)
	}
	if rec.SessionID != "" {
		parts = append(parts, "Recorded from session "+rec.SessionID+".")
	}
	if rec.ReuseCount > 0 {
		parts = append(parts, fmt.Sprintf("Re-run by %d later session(s).", rec.ReuseCount))
	}
	return strings.Join(parts, " ")
}

// truncate shortens s to n runes, marking that it was cut.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
