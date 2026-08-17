package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// SourceSessions is the provenance label for session hits.
const SourceSessions = "sessions"

// SessionReader is what the provider needs from the session read model: search
// over the caller's own sessions, and the scoped reads that open one. It is the
// same read model the portal's My Sessions surface uses (#1318, #1319), so an
// agent recalling a session and a person opening one are reading the same
// thing.
type SessionReader interface {
	sessionview.Store
	Search(ctx context.Context, q sessionview.SearchQuery) ([]sessionview.Match, error)
}

// SessionCallReader resolves the cataloged records of named audit events, so a
// session's timeline can say what each call was and what came of it. It is
// optional: without a call catalog the timeline still lists what the session
// did, only without an outcome or a call reference to follow.
type SessionCallReader interface {
	List(ctx context.Context, f callrecord.Filter) ([]callrecord.Record, error)
}

// SessionsProvider exposes the caller's own sessions to the router: the units
// of work they ran, what each was for, and what it left behind.
//
// It is per-user, and for the same reason the call catalog is: a session is the
// record of one person's work, and the questions they asked are theirs. The
// scoping is enforced in the store's SQL rather than here, so a session
// belonging to someone else is not found rather than refused.
type SessionsProvider struct {
	sessions SessionReader
	calls    SessionCallReader
}

// NewSessionsProvider builds the sessions provider over a session read model.
func NewSessionsProvider(sessions SessionReader) *SessionsProvider {
	return &SessionsProvider{sessions: sessions}
}

// SetCalls wires the call catalog a fetched timeline is annotated from. A nil
// reader leaves the timeline unannotated rather than absent.
func (p *SessionsProvider) SetCalls(calls SessionCallReader) { p.calls = calls }

// Name returns the provenance label.
func (*SessionsProvider) Name() string { return SourceSessions }

// Scope marks this provider per-user; the router supplies the caller identity
// and must skip it when that identity is absent.
func (*SessionsProvider) Scope() Scope { return ScopePerUser }

// Search returns the caller's own sessions ranked against the intent. It fails
// closed on a missing caller id rather than searching across everyone's work,
// and it stays out of an entity-keyed query: a session is found by what its
// caller said they were doing, and it links to no catalog entity of its own.
func (p *SessionsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Caller.UserID == "" || strings.TrimSpace(q.Intent) == "" {
		return nil, nil
	}
	matches, err := p.sessions.Search(ctx, sessionview.SearchQuery{
		Text:   q.Intent,
		UserID: q.Caller.UserID,
		Limit:  q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("session search: %w", err)
	}

	hits := make([]Hit, 0, len(matches))
	for i := range matches {
		hits = append(hits, Hit{
			Text:      sessionHitText(matches[i]),
			Source:    SourceSessions,
			Ref:       matches[i].SessionID,
			Score:     matches[i].Score,
			Reference: knowledgepage.SessionRef(matches[i].SessionID),
		})
	}
	return hits, nil
}

// Fetch dereferences an mcp:session:<id> reference to the session in full: what
// its calls were for, what it produced, and the order it happened in. It owns
// only the session reference form; any other reference is declined
// (owned=false).
func (p *SessionsProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetSession {
		// Not a session reference: decline so the Router tries the next
		// provider. The parse error is intentionally not propagated.
		return nil, false, nil //nolint:nilerr // a non-session reference is a decline, not a failure
	}
	if caller.UserID == "" {
		return nil, true, ErrNotFound
	}

	detail, err := sessionview.Load(ctx, p.sessions, sessionview.Scope{
		SessionID: parsed.SessionID,
		UserID:    caller.UserID,
	})
	if errors.Is(err, sessionview.ErrNotFound) {
		return nil, true, ErrNotFound
	}
	if err != nil {
		return nil, true, fmt.Errorf("getting session %s: %w", parsed.SessionID, err)
	}

	recall := p.recall(ctx, *detail, caller.UserID)
	return &Document{
		Reference:  recall.Reference,
		Source:     SourceSessions,
		Title:      sessionTitle(*detail),
		Content:    recall,
		References: recall.outboundRefs(),
	}, true, nil
}

// SessionRecall is one session as an agent reads it back: the summary, what the
// session produced as references it can follow, and the calls it made in order.
type SessionRecall struct {
	Reference string `json:"reference" example:"mcp:session:dps_9f2c1a4b8e7d6c5a"`
	SessionID string `json:"session_id" example:"dps_9f2c1a4b8e7d6c5a"`
	// Kind is the session id's origin: an agent handle, one portal-initiated
	// run, one managed-script run, or a transport-derived id.
	Kind         string           `json:"kind" example:"agent"`
	Persona      string           `json:"persona,omitempty" example:"data-engineer"`
	StartedAt    time.Time        `json:"started_at"`
	LastActiveAt time.Time        `json:"last_active_at"`
	CallCount    int              `json:"call_count" example:"5"`
	FailureCount int              `json:"failure_count" example:"1"`
	Assets       []SessionAsset   `json:"assets"`
	Insights     []SessionInsight `json:"insights"`
	Timeline     []SessionCall    `json:"timeline"`
	// TimelineTotal is the session's full call count, which exceeds
	// len(Timeline) when the session made more calls than one page holds.
	TimelineTotal int `json:"timeline_total" example:"5"`
}

// SessionAsset is an asset the session saved, with the reference that opens it.
type SessionAsset struct {
	Reference   string    `json:"reference" example:"mcp:asset:ast_7c1e"`
	ID          string    `json:"id" example:"ast_7c1e"`
	Name        string    `json:"name" example:"Q3 revenue by region"`
	ContentType string    `json:"content_type" example:"text/csv"`
	CreatedAt   time.Time `json:"created_at"`
}

// SessionInsight is an insight the session captured, with the reference that
// reads it in full.
type SessionInsight struct {
	Reference string    `json:"reference" example:"mcp:insight:ins_3a9f"`
	ID        string    `json:"id" example:"ins_3a9f"`
	Category  string    `json:"category" example:"data_quality"`
	Text      string    `json:"text" example:"orders.amount is null for canceled rows."`
	Status    string    `json:"status" example:"pending"`
	CreatedAt time.Time `json:"created_at"`
}

// SessionCall is one call the session made.
//
// Reference, Kind and Outcome are present only for a call the catalog recorded
// — a query or an API invocation (#1321). Every other call is still listed: it
// is part of what the session did, and hiding it would misrepresent the order
// of the work. A caller reads the presence of the reference as the answer to
// whether there is a record to fetch.
type SessionCall struct {
	Reference    string    `json:"reference,omitempty" example:"mcp:call:a1b2c3d4e5f6"`
	EventID      string    `json:"event_id" example:"a1b2c3d4e5f6"`
	Timestamp    time.Time `json:"timestamp"`
	ToolName     string    `json:"tool_name" example:"trino_query"`
	Purpose      string    `json:"purpose,omitempty" example:"Sizing Q3 revenue by region for the board deck."`
	Kind         string    `json:"kind,omitempty" example:"sql"`
	Connection   string    `json:"connection,omitempty" example:"acme-warehouse"`
	Success      bool      `json:"success" example:"true"`
	ErrorMessage string    `json:"error_message,omitempty"`
	DurationMS   int64     `json:"duration_ms" example:"143"`
	Outcome      string    `json:"outcome,omitempty" example:"satisfied"`
}

// recall assembles the agent-facing session from the read model, annotating the
// timeline from the call catalog when one is wired.
func (p *SessionsProvider) recall(ctx context.Context, d sessionview.Detail, userID string) SessionRecall {
	timeline := make([]SessionCall, 0, len(d.Timeline))
	for i := range d.Timeline {
		timeline = append(timeline, sessionCallOf(d.Timeline[i]))
	}
	annotate(timeline, p.records(ctx, d.SessionID, userID, timeline))

	return SessionRecall{
		Reference:     knowledgepage.SessionRef(d.SessionID),
		SessionID:     d.SessionID,
		Kind:          string(d.Kind),
		Persona:       d.Persona,
		StartedAt:     d.StartedAt,
		LastActiveAt:  d.LastActiveAt,
		CallCount:     d.CallCount,
		FailureCount:  d.FailureCount,
		Assets:        sessionAssets(d.Assets),
		Insights:      sessionInsights(d.Insights),
		Timeline:      timeline,
		TimelineTotal: d.TimelineTotal,
	}
}

// records reads the cataloged records of exactly the calls on this page of the
// timeline. Reading them by event id rather than by session is what keeps a
// long session's page from pulling in its whole history.
//
// A catalog failure is not the caller's problem: the session is still what it
// was, so the error is logged and the timeline comes back unannotated rather
// than the fetch failing.
func (p *SessionsProvider) records(ctx context.Context, sessionID, userID string, timeline []SessionCall) []callrecord.Record {
	if p.calls == nil || len(timeline) == 0 {
		return nil
	}
	ids := make([]string, 0, len(timeline))
	for i := range timeline {
		ids = append(ids, timeline[i].EventID)
	}
	found, err := p.calls.List(ctx, callrecord.Filter{
		UserID:    userID,
		SessionID: sessionID,
		EventIDs:  ids,
		Limit:     len(ids),
	})
	if err != nil {
		slog.Debug("sessions: call records not read", "session_id", sessionID, "error", err)
		return nil
	}
	return found
}

// annotate stamps each timeline entry that has a cataloged record with the
// record's kind, its outcome, and the reference that fetches it.
func annotate(timeline []SessionCall, records []callrecord.Record) {
	if len(records) == 0 {
		return
	}
	byEvent := make(map[string]callrecord.Record, len(records))
	for i := range records {
		byEvent[records[i].EventID] = records[i]
	}
	for i := range timeline {
		rec, ok := byEvent[timeline[i].EventID]
		if !ok {
			continue
		}
		timeline[i].Reference = knowledgepage.CallRef(rec.EventID)
		timeline[i].Kind = rec.Kind
		timeline[i].Outcome = rec.Outcome
	}
}

// sessionCallOf projects one timeline entry onto the agent-facing call.
func sessionCallOf(e sessionview.TimelineEntry) SessionCall {
	return SessionCall{
		EventID:      e.EventID,
		Timestamp:    e.Timestamp,
		ToolName:     e.ToolName,
		Purpose:      e.Purpose,
		Connection:   e.Connection,
		Success:      e.Success,
		ErrorMessage: e.ErrorMessage,
		DurationMS:   e.DurationMS,
	}
}

// sessionAssets projects the assets a session saved, each with the reference
// that fetches it.
func sessionAssets(in []sessionview.AssetRef) []SessionAsset {
	out := make([]SessionAsset, 0, len(in))
	for _, a := range in {
		out = append(out, SessionAsset{
			Reference:   knowledgepage.AssetRef(a.ID),
			ID:          a.ID,
			Name:        a.Name,
			ContentType: a.ContentType,
			CreatedAt:   a.CreatedAt,
		})
	}
	return out
}

// sessionInsights projects the insights a session captured, each with the
// reference that fetches it.
func sessionInsights(in []sessionview.InsightRef) []SessionInsight {
	out := make([]SessionInsight, 0, len(in))
	for _, i := range in {
		out = append(out, SessionInsight{
			Reference: knowledgepage.InsightRef(i.ID),
			ID:        i.ID,
			Category:  i.Category,
			Text:      i.Text,
			Status:    i.Status,
			CreatedAt: i.CreatedAt,
		})
	}
	return out
}

// outboundRefs are the links the session declares: what it produced. The calls
// are deliberately not among them — they are already on the timeline, where
// their order and their purpose give them meaning, and repeating a hundred of
// them as a flat list would bury the two things the session left behind.
func (r SessionRecall) outboundRefs() []DocumentRef {
	refs := make([]DocumentRef, 0, len(r.Assets)+len(r.Insights))
	for _, a := range r.Assets {
		refs = append(refs, DocumentRef{Reference: a.Reference, Type: knowledgepage.RefTargetAsset})
	}
	for _, i := range r.Insights {
		refs = append(refs, DocumentRef{Reference: i.Reference, Type: knowledgepage.RefTargetInsight})
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
}

// sessionTitle names a session in one line: the first thing its caller said
// they were doing, or the session's own id when no call stated a purpose.
func sessionTitle(d sessionview.Detail) string {
	for i := range d.Timeline {
		if purpose := strings.TrimSpace(d.Timeline[i].Purpose); purpose != "" {
			return purpose
		}
	}
	return "Session " + d.SessionID
}

// maxHitPurposes and maxHitAssets bound what a search snippet carries. A
// snippet is for choosing between sessions, and a long session's twentieth
// purpose or asset does not help with that; the whole list is one fetch away.
// Neither bound is silent — the standing line states what was left out.
const (
	maxHitPurposes = 3
	maxHitAssets   = 3
)

// sessionHitText renders a session as a knowledge snippet: what it was for,
// what it produced, and how much it did.
//
// The purposes lead because they are the session's own words and the reason the
// hit matched at all. The standing follows because it is what a caller chooses
// on: a session that saved something is a session whose work survived.
func sessionHitText(m sessionview.Match) string {
	lines := make([]string, 0, 3)
	if len(m.Purposes) > 0 {
		lines = append(lines, strings.Join(firstOf(m.Purposes, maxHitPurposes), standingSep))
	}
	if len(m.AssetNames) > 0 {
		lines = append(lines, "Saved: "+strings.Join(firstOf(m.AssetNames, maxHitAssets), ", "))
	}
	lines = append(lines, sessionStanding(m))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// firstOf returns at most n leading entries.
func firstOf(in []string, n int) []string {
	if len(in) > n {
		return in[:n]
	}
	return in
}

// standingSep joins the clauses of a standing line.
const standingSep = "; "

// sessionStanding is the one-line standing of a session: how much it did, how
// much of it failed, when it was last active, and what the snippet above left
// out.
func sessionStanding(m sessionview.Match) string {
	standing := plural(m.CallCount, "call") + " on " + m.LastActiveAt.UTC().Format(time.DateOnly)
	if m.FailureCount > 0 {
		standing += standingSep + plural(m.FailureCount, "failure")
	}
	if extra := len(m.Purposes) - maxHitPurposes; extra > 0 {
		standing += standingSep + plural(extra, "further purpose") + " stated"
	}
	if extra := len(m.AssetNames) - maxHitAssets; extra > 0 {
		standing += standingSep + plural(extra, "further asset") + " saved"
	}
	return standing
}

// plural renders a count with its noun, pluralized by adding an "s" — which
// every noun this is called with takes.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// Verify interface compliance.
var (
	_ Provider = (*SessionsProvider)(nil)
	_ Fetcher  = (*SessionsProvider)(nil)
)
