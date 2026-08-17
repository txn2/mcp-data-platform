package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
)

// fakeCalls is a call catalog with canned answers, recording what the provider
// asked of it.
type fakeCalls struct {
	scored  []callrecord.Scored
	record  *callrecord.Record
	getErr  error
	searchQ callrecord.SearchQuery
	fetched []callrecord.Fetcher
	fetchID string
}

func (f *fakeCalls) Search(_ context.Context, q callrecord.SearchQuery) ([]callrecord.Scored, error) {
	f.searchQ = q
	return f.scored, nil
}

func (f *fakeCalls) GetByEventID(_ context.Context, eventID, userID string) (*callrecord.Record, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.record == nil || (userID != "" && f.record.UserID != userID) {
		return nil, callrecord.ErrNotFound
	}
	rec := *f.record
	rec.EventID = eventID
	return &rec, nil
}

func (f *fakeCalls) RecordFetch(_ context.Context, recordID string, by callrecord.Fetcher) error {
	f.fetchID = recordID
	f.fetched = append(f.fetched, by)
	return nil
}

func satisfiedRecord() *callrecord.Record {
	return &callrecord.Record{
		ID: "call-1", EventID: "evt-1", Reference: "mcp:call:evt-1",
		Kind: callrecord.KindSQL, ToolName: "trino_query", Connection: "acme",
		Statement: "SELECT region FROM sales.orders",
		Purpose:   "Sizing Q3 revenue by region.",
		UserID:    "u1", Outcome: callrecord.OutcomeSatisfied,
		SatisfiedBy: callrecord.SatisfiedByCapture, ReuseCount: 2,
	}
}

func TestCallsProviderIsPerUser(t *testing.T) {
	t.Parallel()

	p := NewCallsProvider(&fakeCalls{})
	if p.Scope() != ScopePerUser {
		t.Error("a recorded call is the caller's own: the provider must be per-user")
	}
	if p.Name() != SourceCalls {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestCallsSearchFailsClosedWithoutACaller(t *testing.T) {
	t.Parallel()

	calls := &fakeCalls{scored: []callrecord.Scored{{Record: *satisfiedRecord(), Score: 0.9}}}
	hits, err := NewCallsProvider(calls).Search(context.Background(), Query{Intent: "revenue"})
	if err != nil || len(hits) != 0 {
		t.Errorf("a search with no caller must return nothing, got %d hits (%v)", len(hits), err)
	}
	if calls.searchQ.UserID != "" {
		t.Error("the catalog must not be searched at all without a caller")
	}
}

func TestCallsSearchCarriesTheStandingOnTheHit(t *testing.T) {
	t.Parallel()

	calls := &fakeCalls{scored: []callrecord.Scored{{Record: *satisfiedRecord(), Score: 0.9}}}
	hits, err := NewCallsProvider(calls).Search(context.Background(), Query{
		Intent: "revenue", Caller: Caller{UserID: "u1"}, Limit: 5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected one hit, got %d", len(hits))
	}

	// An agent chooses between candidate queries on the outcome and the
	// reuse count, and neither is worth a round trip to find out.
	if !strings.Contains(hits[0].Text, "satisfied") {
		t.Errorf("hit text %q must carry the outcome", hits[0].Text)
	}
	if !strings.Contains(hits[0].Text, "re-run by 2 later sessions") {
		t.Errorf("hit text %q must carry the reuse count", hits[0].Text)
	}
	if hits[0].Reference != "mcp:call:evt-1" || hits[0].Source != SourceCalls {
		t.Errorf("hit = %+v", hits[0])
	}
	if calls.searchQ.UserID != "u1" || calls.searchQ.Limit != 5 {
		t.Errorf("the search must carry the caller and the limit, got %+v", calls.searchQ)
	}
}

func TestCallsFetchDeclinesAnotherScheme(t *testing.T) {
	t.Parallel()

	p := NewCallsProvider(&fakeCalls{record: satisfiedRecord()})
	for _, ref := range []string{"mcp:asset:ast-1", "urn:li:dataset:(x,y,PROD)", "nonsense"} {
		doc, owned, err := p.Fetch(context.Background(), ref, Caller{UserID: "u1"})
		if owned || doc != nil || err != nil {
			t.Errorf("%q must be declined so the router tries the next provider", ref)
		}
	}
}

func TestCallsFetchIsScopedToTheCaller(t *testing.T) {
	t.Parallel()

	p := NewCallsProvider(&fakeCalls{record: satisfiedRecord()})

	if _, owned, err := p.Fetch(context.Background(), "mcp:call:evt-1", Caller{}); !owned || !errors.Is(err, ErrNotFound) {
		t.Errorf("an anonymous caller must get not-found, got owned=%v err=%v", owned, err)
	}
	if _, _, err := p.Fetch(context.Background(), "mcp:call:evt-1", Caller{UserID: "someone-else"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("another caller's record must be not-found, got %v", err)
	}
}

func TestCallsFetchRecordsTheSighting(t *testing.T) {
	t.Parallel()

	calls := &fakeCalls{record: satisfiedRecord()}
	doc, owned, err := NewCallsProvider(calls).Fetch(context.Background(), "mcp:call:evt-1", Caller{
		UserID: "u1", SessionID: "dps_reader",
	})
	if err != nil || !owned || doc == nil {
		t.Fatalf("Fetch: owned=%v err=%v", owned, err)
	}
	if doc.Title != "Sizing Q3 revenue by region." || doc.Source != SourceCalls {
		t.Errorf("document = %+v", doc)
	}

	// Reuse is credited to a session that found the record and then ran what
	// it holds, so the sighting has to be recorded at the fetch.
	if len(calls.fetched) != 1 || calls.fetched[0].SessionID != "dps_reader" {
		t.Fatalf("the fetch must be recorded against the reading session, got %+v", calls.fetched)
	}
	if calls.fetchID != "call-1" {
		t.Errorf("the sighting must name the record id, got %q", calls.fetchID)
	}
}

func TestCallsFetchWithoutASessionRecordsNothing(t *testing.T) {
	t.Parallel()

	calls := &fakeCalls{record: satisfiedRecord()}
	if _, _, err := NewCallsProvider(calls).Fetch(context.Background(), "mcp:call:evt-1", Caller{UserID: "u1"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(calls.fetched) != 0 {
		t.Error("a read with no session credits nothing later, so nothing is recorded")
	}
}

func TestCallsFetchReportsAReadFailure(t *testing.T) {
	t.Parallel()

	calls := &fakeCalls{getErr: errors.New("catalog down")}
	_, owned, err := NewCallsProvider(calls).Fetch(context.Background(), "mcp:call:evt-1", Caller{UserID: "u1"})
	if !owned || err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("a read failure must surface as a failure, got owned=%v err=%v", owned, err)
	}
}

func TestCallTitleFallsBackToWhatWasAddressed(t *testing.T) {
	t.Parallel()

	if got := callTitle(callrecord.Record{Kind: callrecord.KindAPI, Method: "GET", Path: "/v1/orders"}); got != "GET /v1/orders" {
		t.Errorf("title = %q, want the request line", got)
	}
	if got := callTitle(callrecord.Record{ToolName: "trino_query", Connection: "acme"}); got != "trino_query on acme" {
		t.Errorf("title = %q, want the tool and connection", got)
	}
}

func TestCallStandingNamesOneReuseInTheSingular(t *testing.T) {
	t.Parallel()

	rec := callrecord.Record{Outcome: callrecord.OutcomeSatisfied, ReuseCount: 1, PromotedURN: "urn:li:query:x"}
	got := callStanding(rec)
	if !strings.Contains(got, "1 later session") || strings.Contains(got, "1 later sessions") {
		t.Errorf("standing = %q", got)
	}
	if !strings.Contains(got, "promoted to urn:li:query:x") {
		t.Errorf("standing %q must say what the record became", got)
	}
}
