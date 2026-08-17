package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// SourceCalls is the provenance label for call-catalog hits.
const SourceCalls = "calls"

// CallSearcher is what the provider needs from the call catalog: relevance
// search over the caller's own recorded calls, resolution of one by the
// reference its result handed back, and a note that this session saw it.
//
// The last is not bookkeeping. Reuse of a recorded query is credited to a
// session that found the record and then ran what it holds; without the
// sighting, a later identical query is just an identical query, and the record
// gets no credit for having led to it (#1321).
type CallSearcher interface {
	Search(ctx context.Context, q callrecord.SearchQuery) ([]callrecord.Scored, error)
	GetByEventID(ctx context.Context, eventID, userID string) (*callrecord.Record, error)
	RecordFetch(ctx context.Context, recordID string, by callrecord.Fetcher) error
}

// CallsProvider exposes the caller's own recorded data-access calls to the
// router: the queries and API invocations they have already run, with what each
// was for and what came of it.
//
// It is per-user. A recorded call carries a statement written against data the
// caller could reach, and the fact that a question was asked at all is the
// caller's own; neither is another caller's to search.
type CallsProvider struct {
	calls CallSearcher
}

// NewCallsProvider builds the calls provider over a call catalog.
func NewCallsProvider(calls CallSearcher) *CallsProvider {
	return &CallsProvider{calls: calls}
}

// Name returns the provenance label.
func (*CallsProvider) Name() string { return SourceCalls }

// Scope marks this provider per-user; the router supplies the caller identity
// and must skip it when that identity is absent.
func (*CallsProvider) Scope() Scope { return ScopePerUser }

// Search returns the caller's recorded calls ranked by relevance. It fails
// closed on a missing caller id rather than searching across everyone's calls.
func (p *CallsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Caller.UserID == "" {
		return nil, nil
	}
	scored, err := p.calls.Search(ctx, callrecord.SearchQuery{
		Text:      q.Intent,
		Embedding: q.Embedding,
		UserID:    q.Caller.UserID,
		Limit:     q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("call search: %w", err)
	}

	hits := make([]Hit, 0, len(scored))
	for i := range scored {
		rec := scored[i].Record
		hits = append(hits, Hit{
			Text:      callHitText(rec),
			Source:    SourceCalls,
			Ref:       rec.EventID,
			Score:     scored[i].Score,
			Reference: knowledgepage.CallRef(rec.EventID),
		})
	}
	return hits, nil
}

// Fetch dereferences an mcp:call:<event_id> reference to the full record: the
// statement or request line, what it was for, what came of it, and how many
// other sessions have re-run it. It owns only the call reference form; any
// other reference is declined (owned=false).
//
// Fetching is also what makes a later re-run count as reuse, so the sighting is
// recorded here rather than at the point of the re-run: by then, nothing would
// say the record was what led to it.
func (p *CallsProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetCall {
		// Not a call reference: decline so the Router tries the next
		// provider. The parse error is intentionally not propagated.
		return nil, false, nil //nolint:nilerr // a non-call reference is a decline, not a failure
	}
	if caller.UserID == "" {
		return nil, true, ErrNotFound
	}

	rec, err := p.calls.GetByEventID(ctx, parsed.CallID, caller.UserID)
	if errors.Is(err, callrecord.ErrNotFound) {
		return nil, true, ErrNotFound
	}
	if err != nil {
		return nil, true, fmt.Errorf("getting call %s: %w", parsed.CallID, err)
	}

	p.noteSighting(ctx, rec.ID, caller)
	return &Document{
		Reference: knowledgepage.CallRef(rec.EventID),
		Source:    SourceCalls,
		Title:     callTitle(*rec),
		Content:   rec,
	}, true, nil
}

// noteSighting records that this session read the record. Best-effort: failing
// to note a sighting costs the record a reuse credit it might later have
// earned, and must never cost the caller the record they asked for.
func (p *CallsProvider) noteSighting(ctx context.Context, recordID string, caller Caller) {
	if caller.SessionID == "" {
		return
	}
	if err := p.calls.RecordFetch(ctx, recordID, callrecord.Fetcher{
		SessionID: caller.SessionID,
		UserID:    caller.UserID,
	}); err != nil {
		slog.Debug("calls: fetch sighting not recorded", "record_id", recordID, "error", err)
	}
}

// callTitle names a record in one line: what it was for, or what it addressed
// when its caller stated no purpose.
func callTitle(rec callrecord.Record) string {
	if purpose := strings.TrimSpace(rec.Purpose); purpose != "" {
		return purpose
	}
	if rec.Kind == callrecord.KindAPI {
		return strings.TrimSpace(rec.Method + " " + rec.Path)
	}
	return rec.ToolName + " on " + rec.Connection
}

// callHitText renders a record as a knowledge snippet.
//
// The outcome and the reuse count are on the hit rather than behind a fetch
// because they are what an agent chooses between two candidate queries on: one
// that answered a question and that other people have since re-run is worth
// more than one that merely ran, and neither is worth a round trip to find out.
func callHitText(rec callrecord.Record) string {
	lines := []string{callTitle(rec)}
	if rec.Statement != "" {
		lines = append(lines, rec.Statement)
	}
	lines = append(lines, callStanding(rec))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// callStanding is the one-line standing of a record: its outcome, how it came
// to be satisfied, and how many other sessions have re-run it.
func callStanding(rec callrecord.Record) string {
	standing := "Outcome: " + rec.Outcome
	if rec.SatisfiedBy != "" {
		standing += " (" + rec.SatisfiedBy + ")"
	}
	if rec.ReuseCount == 1 {
		standing += "; re-run by 1 later session"
	} else if rec.ReuseCount > 1 {
		standing += fmt.Sprintf("; re-run by %d later sessions", rec.ReuseCount)
	}
	if rec.PromotedURN != "" {
		standing += "; promoted to " + rec.PromotedURN
	}
	return standing
}

// Verify interface compliance.
var (
	_ Provider = (*CallsProvider)(nil)
	_ Fetcher  = (*CallsProvider)(nil)
)
