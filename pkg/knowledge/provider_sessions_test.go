package knowledge

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
)

const (
	testSession    = "dps_9f2c1a4b8e7d6c5a"
	testSessionRef = "mcp:session:" + testSession
)

// fakeSessions is a session read model with canned answers, recording the
// scopes the provider read with so the tests can assert the caller was carried
// into the store rather than checked afterwards.
type fakeSessions struct {
	summary  *sessionview.Summary
	timeline []sessionview.TimelineEntry
	assets   []sessionview.AssetRef
	insights []sessionview.InsightRef
	matches  []sessionview.Match
	searchQ  sessionview.SearchQuery
	getScope sessionview.Scope
	searchEr error
	getErr   error
}

func (*fakeSessions) List(context.Context, sessionview.Filter) ([]sessionview.Summary, error) {
	return nil, nil
}

func (*fakeSessions) Count(context.Context, sessionview.Filter) (int, error) { return 0, nil }

func (f *fakeSessions) Get(_ context.Context, scope sessionview.Scope) (*sessionview.Summary, error) {
	f.getScope = scope
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.summary == nil || f.summary.SessionID != scope.SessionID {
		return nil, sessionview.ErrNotFound
	}
	return f.summary, nil
}

func (f *fakeSessions) Timeline(_ context.Context, _ sessionview.Scope) ([]sessionview.TimelineEntry, int, error) {
	return f.timeline, len(f.timeline), nil
}

func (f *fakeSessions) Assets(context.Context, string) ([]sessionview.AssetRef, error) {
	return f.assets, nil
}

func (f *fakeSessions) Insights(context.Context, string) ([]sessionview.InsightRef, error) {
	return f.insights, nil
}

func (f *fakeSessions) Search(_ context.Context, q sessionview.SearchQuery) ([]sessionview.Match, error) {
	f.searchQ = q
	return f.matches, f.searchEr
}

// fakeSessionCalls is a call catalog that answers with the records it holds for
// the event ids it was asked about.
type fakeSessionCalls struct {
	records []callrecord.Record
	filter  callrecord.Filter
	err     error
}

func (f *fakeSessionCalls) List(_ context.Context, filter callrecord.Filter) ([]callrecord.Record, error) {
	f.filter = filter
	return f.records, f.err
}

func ranSession() *fakeSessions {
	at := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	return &fakeSessions{
		summary: &sessionview.Summary{
			SessionID: testSession, Kind: sessionview.KindAgent,
			UserID: "u1", UserEmail: "analyst@example.com", Persona: "analyst",
			StartedAt: at, LastActiveAt: at.Add(5 * time.Minute),
			CallCount: 3, FailureCount: 1,
		},
		timeline: []sessionview.TimelineEntry{
			{EventID: "evt-1", Timestamp: at, ToolName: "search", Purpose: "Finding the revenue table."},
			{
				EventID: "evt-2", Timestamp: at.Add(time.Minute), ToolName: "trino_query",
				Purpose: "Summing Q3 revenue by region.", Connection: "acme", Success: true, DurationMS: 143,
			},
			{EventID: "evt-3", Timestamp: at.Add(2 * time.Minute), ToolName: "save_asset"},
		},
		assets: []sessionview.AssetRef{
			{ID: "ast_1", Name: "Q3 revenue by region", ContentType: "text/csv", CreatedAt: at},
		},
		insights: []sessionview.InsightRef{
			{ID: "ins_1", Category: "correction", Text: "revenue excludes returns.", Status: "pending", CreatedAt: at},
		},
	}
}

func TestSessionsProviderIsPerUser(t *testing.T) {
	t.Parallel()

	p := NewSessionsProvider(&fakeSessions{})
	if p.Scope() != ScopePerUser {
		t.Error("a session is the record of one caller's work: the provider must be per-user")
	}
	if p.Name() != SourceSessions {
		t.Errorf("Name() = %q", p.Name())
	}
	if !slices.Contains(KnownSources(), SourceSessions) {
		t.Error("sessions must be a known source, or a caller narrowing to it is told it is a typo")
	}
}

func TestSessionsSearchFailsClosedWithoutACaller(t *testing.T) {
	t.Parallel()

	sessions := ranSession()
	sessions.matches = []sessionview.Match{{SessionID: testSession, Score: 1}}

	hits, err := NewSessionsProvider(sessions).Search(context.Background(), Query{Intent: "revenue"})
	if err != nil || len(hits) != 0 {
		t.Errorf("a search with no caller must return nothing, got %d hits (%v)", len(hits), err)
	}
	if sessions.searchQ.UserID != "" {
		t.Error("the read model must not be searched at all without a caller")
	}
}

// An entity-keyed query names catalog entities. A session links to none, so it
// must stay out of that arm rather than answer with its whole history.
func TestSessionsSearchSkipsAnEntityOnlyQuery(t *testing.T) {
	t.Parallel()

	sessions := ranSession()
	hits, err := NewSessionsProvider(sessions).Search(context.Background(), Query{
		EntityURNs: []string{"urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)"},
		Caller:     Caller{UserID: "u1"},
	})
	if err != nil || len(hits) != 0 {
		t.Errorf("an entity-only query must not reach the session search, got %d hits (%v)", len(hits), err)
	}
	if sessions.searchQ.Text != "" {
		t.Error("the read model must not be searched without an intent")
	}
}

func TestSessionsSearchBuildsHits(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	sessions := &fakeSessions{matches: []sessionview.Match{{
		SessionID: testSession, Kind: sessionview.KindAgent,
		StartedAt: at, LastActiveAt: at.Add(time.Hour),
		CallCount: 4, FailureCount: 1,
		Purposes:   []string{"Summing Q3 revenue by region for the board deck."},
		AssetNames: []string{"Q3 revenue by region"},
		Score:      0.75,
	}}}

	hits, err := NewSessionsProvider(sessions).Search(context.Background(), Query{
		Intent: "board deck revenue",
		Caller: Caller{UserID: "u1"},
		Limit:  7,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(hits))
	}
	if sessions.searchQ.UserID != "u1" || sessions.searchQ.Text != "board deck revenue" || sessions.searchQ.Limit != 7 {
		t.Errorf("the query was not carried into the read model: %+v", sessions.searchQ)
	}

	hit := hits[0]
	if hit.Source != SourceSessions || hit.Ref != testSession {
		t.Errorf("hit provenance = %q / %q", hit.Source, hit.Ref)
	}
	if hit.Reference != testSessionRef {
		t.Errorf("Reference = %q, want the fetchable session reference", hit.Reference)
	}
	if hit.Score != 0.75 {
		t.Errorf("Score = %v", hit.Score)
	}
	for _, want := range []string{
		"Summing Q3 revenue by region for the board deck.",
		"Saved: Q3 revenue by region",
		"4 calls on 2026-08-16",
		"1 failure",
	} {
		if !strings.Contains(hit.Text, want) {
			t.Errorf("hit text is missing %q:\n%s", want, hit.Text)
		}
	}
}

// A session whose calls stated more purposes than a snippet carries must say so
// rather than silently showing the first few as though they were all of them.
func TestSessionsSearchSaysWhatTheSnippetLeftOut(t *testing.T) {
	t.Parallel()

	sessions := &fakeSessions{matches: []sessionview.Match{{
		SessionID: testSession,
		Purposes:  []string{"one.", "two.", "three.", "four.", "five."},
		CallCount: 5,
	}}}
	hits, err := NewSessionsProvider(sessions).Search(context.Background(), Query{
		Intent: "anything", Caller: Caller{UserID: "u1"},
	})
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search: %d hits, %v", len(hits), err)
	}
	if strings.Contains(hits[0].Text, "four.") {
		t.Error("the snippet must stop at the purposes it shows")
	}
	if !strings.Contains(hits[0].Text, "2 further purposes stated") {
		t.Errorf("the snippet must state what it left out:\n%s", hits[0].Text)
	}
}

// The same bound applies to what the session saved: a snippet lists a few and
// says how many more there were.
func TestSessionsSearchBoundsTheAssetsItLists(t *testing.T) {
	t.Parallel()

	sessions := &fakeSessions{matches: []sessionview.Match{{
		SessionID:  testSession,
		Purposes:   []string{"Building the board deck."},
		AssetNames: []string{"one", "two", "three", "four"},
		CallCount:  9,
	}}}
	hits, err := NewSessionsProvider(sessions).Search(context.Background(), Query{
		Intent: "board deck", Caller: Caller{UserID: "u1"},
	})
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search: %d hits, %v", len(hits), err)
	}
	if strings.Contains(hits[0].Text, "four") {
		t.Error("the snippet must stop at the assets it shows")
	}
	if !strings.Contains(hits[0].Text, "1 further asset saved") {
		t.Errorf("the snippet must state what it left out:\n%s", hits[0].Text)
	}
}

func TestSessionsSearchError(t *testing.T) {
	t.Parallel()

	sessions := &fakeSessions{searchEr: errors.New("connection reset")}
	_, err := NewSessionsProvider(sessions).Search(context.Background(), Query{
		Intent: "revenue", Caller: Caller{UserID: "u1"},
	})
	if err == nil || !strings.Contains(err.Error(), "session search") {
		t.Errorf("err = %v, want the search failure surfaced", err)
	}
}

func TestSessionsFetchDeclinesForeignReferences(t *testing.T) {
	t.Parallel()

	p := NewSessionsProvider(ranSession())
	for _, ref := range []string{
		"mcp:asset:ast_1",
		"mcp:call:evt-1",
		"urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)",
		"not a reference",
		"",
	} {
		doc, owned, err := p.Fetch(context.Background(), ref, Caller{UserID: "u1"})
		if owned || doc != nil || err != nil {
			t.Errorf("Fetch(%q) = owned %v, err %v: a reference of another form must be declined", ref, owned, err)
		}
	}
}

func TestSessionsFetchReturnsTheSessionInFull(t *testing.T) {
	t.Parallel()

	sessions := ranSession()
	calls := &fakeSessionCalls{records: []callrecord.Record{{
		EventID: "evt-2", Kind: callrecord.KindSQL, Outcome: callrecord.OutcomeSatisfied,
	}}}
	p := NewSessionsProvider(sessions)
	p.SetCalls(calls)

	doc, owned, err := p.Fetch(context.Background(), testSessionRef, Caller{UserID: "u1"})
	if !owned || err != nil {
		t.Fatalf("Fetch: owned %v, err %v", owned, err)
	}
	if sessions.getScope.UserID != "u1" || sessions.getScope.SessionID != testSession {
		t.Errorf("the read was not scoped to the caller: %+v", sessions.getScope)
	}
	if doc.Reference != testSessionRef || doc.Source != SourceSessions {
		t.Errorf("document identity = %q / %q", doc.Reference, doc.Source)
	}
	if doc.Title != "Finding the revenue table." {
		t.Errorf("Title = %q, want the first purpose the session stated", doc.Title)
	}

	recall, ok := doc.Content.(SessionRecall)
	if !ok {
		t.Fatalf("Content is %T, want SessionRecall", doc.Content)
	}
	if len(recall.Timeline) != 3 || recall.TimelineTotal != 3 {
		t.Fatalf("timeline = %d entries (total %d)", len(recall.Timeline), recall.TimelineTotal)
	}
	if recall.Timeline[1].Reference != "mcp:call:evt-2" ||
		recall.Timeline[1].Kind != callrecord.KindSQL ||
		recall.Timeline[1].Outcome != callrecord.OutcomeSatisfied {
		t.Errorf("the cataloged call was not annotated: %+v", recall.Timeline[1])
	}
	if recall.Timeline[0].Reference != "" || recall.Timeline[0].Outcome != "" {
		t.Error("a call the catalog never recorded must carry no reference to fetch")
	}
	if recall.Timeline[0].Purpose != "Finding the revenue table." {
		t.Errorf("the timeline lost the stated purpose: %+v", recall.Timeline[0])
	}
	if calls.filter.UserID != "u1" || calls.filter.SessionID != testSession {
		t.Errorf("the catalog was read unscoped: %+v", calls.filter)
	}
	if len(calls.filter.EventIDs) != 3 {
		t.Errorf("the catalog must be read by the events on the page, got %v", calls.filter.EventIDs)
	}

	// What the session produced comes back as references the agent can follow.
	if len(recall.Assets) != 1 || recall.Assets[0].Reference != "mcp:asset:ast_1" {
		t.Errorf("asset references = %+v", recall.Assets)
	}
	if len(recall.Insights) != 1 || recall.Insights[0].Reference != "mcp:insight:ins_1" {
		t.Errorf("insight references = %+v", recall.Insights)
	}
	if len(doc.References) != 2 {
		t.Fatalf("outbound references = %+v", doc.References)
	}
	if doc.References[0].Type != "asset" || doc.References[1].Type != "insight" {
		t.Errorf("outbound reference types = %+v", doc.References)
	}
}

// The session is still the session without a call catalog: the timeline lists
// what it did, with nothing to follow into.
func TestSessionsFetchWithoutACallCatalog(t *testing.T) {
	t.Parallel()

	doc, owned, err := NewSessionsProvider(ranSession()).
		Fetch(context.Background(), testSessionRef, Caller{UserID: "u1"})
	if !owned || err != nil {
		t.Fatalf("Fetch: owned %v, err %v", owned, err)
	}
	recall := doc.Content.(SessionRecall) //nolint:errcheck // asserted above
	if len(recall.Timeline) != 3 {
		t.Fatalf("timeline = %d entries", len(recall.Timeline))
	}
	for _, c := range recall.Timeline {
		if c.Reference != "" || c.Outcome != "" {
			t.Errorf("no catalog, no annotation: %+v", c)
		}
	}
}

// A catalog that cannot answer must not cost the caller the session.
func TestSessionsFetchToleratesACatalogFailure(t *testing.T) {
	t.Parallel()

	p := NewSessionsProvider(ranSession())
	p.SetCalls(&fakeSessionCalls{err: errors.New("connection reset")})

	doc, owned, err := p.Fetch(context.Background(), testSessionRef, Caller{UserID: "u1"})
	if !owned || err != nil {
		t.Fatalf("Fetch: owned %v, err %v", owned, err)
	}
	if len(doc.Content.(SessionRecall).Timeline) != 3 { //nolint:errcheck // shape asserted above
		t.Error("the timeline must survive a catalog that could not be read")
	}
}

func TestSessionsFetchNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		caller   Caller
		ref      string
		sessions *fakeSessions
	}{
		{"no caller", Caller{}, testSessionRef, ranSession()},
		{"another caller's session", Caller{UserID: "u2"}, testSessionRef, &fakeSessions{}},
		{"an id that never ran", Caller{UserID: "u1"}, "mcp:session:dps_never", ranSession()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, owned, err := NewSessionsProvider(tt.sessions).
				Fetch(context.Background(), tt.ref, tt.caller)
			if !owned {
				t.Fatal("a session reference is this provider's to answer, even when it resolves to nothing")
			}
			if doc != nil || !errors.Is(err, ErrNotFound) {
				t.Errorf("doc = %v, err = %v, want ErrNotFound", doc, err)
			}
		})
	}
}

func TestSessionsFetchReadFailure(t *testing.T) {
	t.Parallel()

	sessions := ranSession()
	sessions.getErr = errors.New("connection reset")

	_, owned, err := NewSessionsProvider(sessions).
		Fetch(context.Background(), testSessionRef, Caller{UserID: "u1"})
	if !owned {
		t.Fatal("the reference is still this provider's")
	}
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want the read failure surfaced rather than reported as not-found", err)
	}
}

// A session none of whose calls stated a purpose is still openable, and says
// which session it is.
func TestSessionsFetchTitlesAPurposelessSession(t *testing.T) {
	t.Parallel()

	sessions := ranSession()
	for i := range sessions.timeline {
		sessions.timeline[i].Purpose = ""
	}
	sessions.assets = nil
	sessions.insights = nil

	doc, _, err := NewSessionsProvider(sessions).
		Fetch(context.Background(), testSessionRef, Caller{UserID: "u1"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doc.Title != "Session "+testSession {
		t.Errorf("Title = %q", doc.Title)
	}
	if doc.References != nil {
		t.Errorf("a session that produced nothing declares no outbound references: %+v", doc.References)
	}
}
