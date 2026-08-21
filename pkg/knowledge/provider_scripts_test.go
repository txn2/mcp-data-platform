package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeScriptSearcher records what the provider asked for and returns what the
// test staged.
type fakeScriptSearcher struct {
	scored     []script.ScoredScript
	searchErr  error
	got        script.SearchQuery
	searched   bool
	contract   *script.Contract
	getErr     error
	gotGetID   string
	getCounted int
}

func (f *fakeScriptSearcher) Search(_ context.Context, q script.SearchQuery) ([]script.ScoredScript, error) {
	f.searched = true
	f.got = q
	return f.scored, f.searchErr
}

func (f *fakeScriptSearcher) Contract(_ context.Context, id string) (*script.Contract, error) {
	f.gotGetID = id
	f.getCounted++
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.contract, nil
}

// runnableContract is a visible, runnable script's contract: an active,
// enabled script whose latest saved version is 3 and whose run gate refuses
// nothing.
func runnableContract() *script.Contract {
	return &script.Contract{
		ID: "script_1", Name: "daily-sales", DisplayName: "Daily Sales",
		Description: "Yesterday's sales by region", Scope: script.ScopeGlobal,
		Status: script.StatusActive, Enabled: true,
		Params:  []script.Param{{Name: "report_date", Required: true}},
		Version: 3,
	}
}

func TestScriptsProvider_Metadata(t *testing.T) {
	p := NewScriptsProvider(&fakeScriptSearcher{})

	assert.Equal(t, SourceScripts, p.Name())
	// Shared: global scripts are visible to everyone, and the store predicate
	// self-filters persona and personal ones to the caller.
	assert.Equal(t, ScopeShared, p.Scope())
}

// TestScriptsProvider_NoIntentSkips proves the provider is text-path only: an
// entity-keyed query must not cost a script query, since scripts carry no
// catalog entities.
func TestScriptsProvider_NoIntentSkips(t *testing.T) {
	s := &fakeScriptSearcher{}

	hits, err := NewScriptsProvider(s).Search(context.Background(), Query{EntityURNs: []string{"urn:x"}})

	require.NoError(t, err)
	assert.Nil(t, hits)
	assert.False(t, s.searched)
}

// TestScriptsProvider_ForwardsCallerVisibility proves the caller's identity and
// persona MEMBERSHIP reach the store, which is where visibility is applied. A
// provider that filtered afterwards would have already paid for rows the caller
// may not see.
func TestScriptsProvider_ForwardsCallerVisibility(t *testing.T) {
	s := &fakeScriptSearcher{}

	_, err := NewScriptsProvider(s).Search(context.Background(), Query{
		Intent: "sales report",
		Limit:  7,
		Caller: Caller{Email: "jane@example.com", Persona: "acting", Personas: []string{"analyst", "engineer"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "sales report", s.got.QueryText)
	assert.Equal(t, "jane@example.com", s.got.OwnerEmail)
	assert.Equal(t, []string{"analyst", "engineer"}, s.got.Personas,
		"visibility scopes on membership, never on the persona a request claims to act as")
	assert.Equal(t, 7, s.got.Limit)
}

// TestScriptsProvider_ForwardsTheQueryVector proves the router's embedding
// reaches the script store, which is the only thing that turns the scripts
// source's ranking hybrid. A provider that dropped it would leave scripts the
// one kind found by wording alone while every other source ranked semantically.
func TestScriptsProvider_ForwardsTheQueryVector(t *testing.T) {
	s := &fakeScriptSearcher{}

	_, err := NewScriptsProvider(s).Search(context.Background(), Query{
		Intent:    "what refreshes the regional sales numbers",
		Embedding: []float32{0.1, 0.2},
	})

	require.NoError(t, err)
	assert.Equal(t, []float32{0.1, 0.2}, s.got.Embedding)
}

// TestScriptsProvider_LexicalWhenTheRouterHasNoVector pins the degraded path: a
// deployment with no embedding provider passes no vector, and the store must be
// asked for exactly the lexical ranking it has always done.
func TestScriptsProvider_LexicalWhenTheRouterHasNoVector(t *testing.T) {
	s := &fakeScriptSearcher{}

	_, err := NewScriptsProvider(s).Search(context.Background(), Query{Intent: "sales"})

	require.NoError(t, err)
	assert.Nil(t, s.got.Embedding)
}

// TestScriptsProvider_HitTextIsTheEmbeddedText proves the snippet a caller reads
// is the document the vector was built from: both are script.IndexText, so a
// result cannot be ranked on text nobody is shown.
func TestScriptsProvider_HitTextIsTheEmbeddedText(t *testing.T) {
	sc := script.Script{
		ID: "script_1", Name: "daily-sales", DisplayName: "Daily Sales",
		Description: "Yesterday's sales by region", Tags: []string{"revenue"},
		Status: script.StatusActive, Enabled: true,
	}
	s := &fakeScriptSearcher{scored: []script.ScoredScript{{Score: 0.9, Script: sc}}}

	hits, err := NewScriptsProvider(s).Search(context.Background(), Query{Intent: "sales"})

	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, script.IndexText(&sc), hits[0].Text)
	assert.Contains(t, hits[0].Text, "revenue", "tags are part of the document, as they are for prompts")
}

// TestScriptsProvider_HitCarriesContractAndExecutionState proves a hit answers
// the two questions that decide what to do with it — what it takes, and whether
// anything will run it — plus the reference that dereferences it.
func TestScriptsProvider_HitCarriesContractAndExecutionState(t *testing.T) {
	s := &fakeScriptSearcher{scored: []script.ScoredScript{
		{Score: 0.9, Script: script.Script{
			ID: "script_1", Name: "daily-sales", DisplayName: "Daily Sales",
			Description: "Yesterday's sales by region", Status: script.StatusActive,
			OwnerEmail: "jane@example.com", Enabled: true,
			Params: []script.Param{{Name: "report_date", Required: true}},
		}},
		{Score: 0.4, Script: script.Script{
			ID: "script_2", Name: "disabled-thing", Status: script.StatusActive, Enabled: false,
		}},
	}}

	hits, err := NewScriptsProvider(s).Search(context.Background(), Query{Intent: "sales"})

	require.NoError(t, err)
	require.Len(t, hits, 2)
	assert.Equal(t, SourceScripts, hits[0].Source)
	assert.Equal(t, "script_1", hits[0].Ref)
	assert.Equal(t, "mcp:script:script_1", hits[0].Reference)
	assert.Equal(t, script.StatusActive, hits[0].Status)
	assert.Equal(t, "jane@example.com", hits[0].CapturedBy)
	assert.Contains(t, hits[0].Text, "Daily Sales")
	assert.Contains(t, hits[0].Text, "parameters: report_date (required)")
	assert.Contains(t, hits[0].Text, "Call run_script")
	assert.Contains(t, hits[1].Text, "Nothing will execute this script: the script is disabled",
		"a hit the run gate refuses must say so, not read as something to run")
}

func TestScriptsProvider_SearchError(t *testing.T) {
	s := &fakeScriptSearcher{searchErr: errors.New("boom")}

	_, err := NewScriptsProvider(s).Search(context.Background(), Query{Intent: "x"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "script search")
}

// TestScriptsProvider_FetchDeclinesForeignReferences proves ownership is
// partitioned by reference form: anything that is not mcp:script: is declined
// cheaply so the router moves on, rather than erroring.
func TestScriptsProvider_FetchDeclinesForeignReferences(t *testing.T) {
	s := &fakeScriptSearcher{}
	p := NewScriptsProvider(s)

	for _, ref := range []string{"mcp:prompt:11111111-1111-1111-1111-111111111111", "urn:li:dataset:(a,b,PROD)", "nonsense", ""} {
		doc, owned, err := p.Fetch(context.Background(), ref, Caller{})
		require.NoError(t, err, ref)
		assert.False(t, owned, ref)
		assert.Nil(t, doc, ref)
	}
	assert.Zero(t, s.getCounted, "a declined reference must not hit the store")
}

// TestScriptsProvider_FetchReturnsTheContractDocument proves the fetched
// document is the contract, carried both as prose and structured, and never the
// script's source.
func TestScriptsProvider_FetchReturnsTheContractDocument(t *testing.T) {
	s := &fakeScriptSearcher{contract: runnableContract()}

	doc, owned, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:script_1", Caller{Email: "anyone@example.com"})

	require.NoError(t, err)
	assert.True(t, owned)
	require.NotNil(t, doc)
	assert.Equal(t, "script_1", s.gotGetID)
	assert.Equal(t, "mcp:script:script_1", doc.Reference)
	assert.Equal(t, SourceScripts, doc.Source)
	assert.Equal(t, "Daily Sales", doc.Title)
	assert.Contains(t, doc.Body, "Runs: version 3, the latest saved version")
	assert.Equal(t, runnableContract(), doc.Content)
}

// TestScriptsProvider_FetchHidesWhatSearchWouldHide proves fetch re-applies the
// visibility rule the store predicate enforces. Without it, a reference would
// be a way to read a script the same caller could never have searched.
func TestScriptsProvider_FetchHidesWhatSearchWouldHide(t *testing.T) {
	c := runnableContract()
	c.Scope = script.ScopePersonal
	c.OwnerEmail = "jane@example.com"
	s := &fakeScriptSearcher{contract: c}

	doc, owned, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:script_1",
		Caller{Email: "bob@example.com"})

	assert.True(t, owned)
	assert.Nil(t, doc)
	require.ErrorIs(t, err, ErrNotFound)
}

// TestScriptsProvider_FetchMissingIsNotFound proves a stale reference is a
// normal answer rather than a failure, so a deleted script reads as "that
// reference is gone".
func TestScriptsProvider_FetchMissingIsNotFound(t *testing.T) {
	s := &fakeScriptSearcher{}

	_, owned, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:gone", Caller{})

	assert.True(t, owned)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestScriptsProvider_FetchStoreError(t *testing.T) {
	s := &fakeScriptSearcher{getErr: errors.New("down")}

	_, owned, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:script_1", Caller{})

	assert.True(t, owned)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound, "a store outage is a failure, not a missing reference")
}

// TestScriptsProvider_FetchServesARetiredScript proves the lifecycle filter is
// a RANKING rule, not an access rule: a caller holding a reference to a
// deprecated script gets the document, whose refusal says it will not run. A
// not-found would read as though the script had never existed.
func TestScriptsProvider_FetchServesARetiredScript(t *testing.T) {
	c := runnableContract()
	c.Status = script.StatusDeprecated
	c.Refusal = "the script is deprecated and must not be executed"
	s := &fakeScriptSearcher{contract: c}

	doc, _, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:script_1", Caller{})

	require.NoError(t, err)
	require.NotNil(t, doc)
	assert.Contains(t, doc.Body, "would be refused: the script is deprecated")
}

// TestScriptsSourceIsKnown proves the new source name is registered with the
// router's validator, so a caller can narrow a search to it instead of being
// told "scripts" is a typo.
func TestScriptsSourceIsKnown(t *testing.T) {
	assert.Contains(t, KnownSources(), SourceScripts)
}
