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
		Description: "Yesterday's sales by region", OwnerEmail: "jane@example.com",
		Status: script.StatusActive, Enabled: true,
		Params:  []script.Param{{Name: "report_date", Required: true}},
		Version: 3,
	}
}

func TestScriptsProvider_Metadata(t *testing.T) {
	p := NewScriptsProvider(&fakeScriptSearcher{})

	assert.Equal(t, SourceScripts, p.Name())
	// Per-user: a definition is everyone signed in's to read, so a caller the
	// platform cannot name has nothing here to find.
	assert.Equal(t, ScopePerUser, p.Scope())
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

// TestScriptsProvider_ForwardsTheQuery proves the intent and the limit reach
// the store, and no owner does: every signed-in caller ranks every script in
// service (#2027).
func TestScriptsProvider_ForwardsTheQuery(t *testing.T) {
	s := &fakeScriptSearcher{}

	_, err := NewScriptsProvider(s).Search(context.Background(), Query{
		Intent: "sales report",
		Limit:  7,
		Caller: Caller{Email: "jane@example.com", Persona: "acting", Personas: []string{"analyst", "engineer"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "sales report", s.got.QueryText)
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
// document is the contract, carried both as prose and structured, followed by
// the script's source (#2027).
func TestScriptsProvider_FetchReturnsTheContractDocument(t *testing.T) {
	c := runnableContract()
	c.Source = "def main():\n    # churn is ninety days\n    pass"
	s := &fakeScriptSearcher{contract: c}

	doc, owned, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:script_1",
		Caller{Email: "jane@example.com"})

	require.NoError(t, err)
	assert.True(t, owned)
	require.NotNil(t, doc)
	assert.Equal(t, "script_1", s.gotGetID)
	assert.Equal(t, "mcp:script:script_1", doc.Reference)
	assert.Equal(t, SourceScripts, doc.Source)
	assert.Equal(t, "Daily Sales", doc.Title)
	assert.Contains(t, doc.Body, "Runs: version 3, the latest saved version")
	assert.Contains(t, doc.Body, "Produced: nothing recorded.")
	assert.Contains(t, doc.Body, "Source (version 3):\n```python\ndef main():\n    # churn is ninety days\n    pass\n```")
	content, ok := doc.Content.(scriptDocument)
	require.True(t, ok)
	assert.Equal(t, *c, content.Contract, "the owner reads the contract whole")
	assert.Empty(t, content.Outputs)
	assert.NotNil(t, content.Outputs, "an empty list is [], never null")
}

// TestScriptsProvider_FetchServesAnotherPersonsScript proves a script's
// definition is everyone signed in's to read (#2027): a caller who does not
// own it gets the contract and the source, and not its last run, whose outputs
// name assets that may not be shared with them.
func TestScriptsProvider_FetchServesAnotherPersonsScript(t *testing.T) {
	c := runnableContract()
	c.Source = "print(1)\n"
	c.LastRun = &script.ContractRun{Version: 3, Outputs: []script.ContractOutput{{Name: "private-report"}}}
	s := &fakeScriptSearcher{contract: c}

	doc, owned, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:script_1",
		Caller{Email: "bob@example.com"})

	require.NoError(t, err)
	assert.True(t, owned)
	require.NotNil(t, doc)
	assert.Contains(t, doc.Body, "print(1)")
	assert.NotContains(t, doc.Body, "private-report")
	assert.Contains(t, doc.Body, "shown to the script's owner and administrators")
	content, ok := doc.Content.(scriptDocument)
	require.True(t, ok)
	assert.Nil(t, content.LastRun)
	assert.True(t, content.RunsWithheld)

	admin, _, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:script_1",
		Caller{Email: "root@example.com", IsAdmin: true})
	require.NoError(t, err)
	assert.Contains(t, admin.Body, "private-report", "an administrator reads it whole")
}

// TestScriptsProvider_FetchRefusesAnUnidentifiedCaller keeps the definition to
// signed-in readers.
func TestScriptsProvider_FetchRefusesAnUnidentifiedCaller(t *testing.T) {
	s := &fakeScriptSearcher{contract: runnableContract()}

	_, owned, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:script_1", Caller{})

	assert.True(t, owned)
	require.ErrorIs(t, err, ErrNotFound)
	assert.Zero(t, s.getCounted)
}

// fakeScriptOutputs stages what a script produced for one caller.
type fakeScriptOutputs struct {
	open   []ScriptOutput
	hidden int
	more   bool
	err    error
	caller Caller
}

func (f *fakeScriptOutputs) Outputs(_ context.Context, _ string, c Caller) (ScriptOutputSet, error) {
	f.caller = c
	return ScriptOutputSet{Open: f.open, Hidden: f.hidden, More: f.more}, f.err
}

// TestScriptsProvider_FetchListsOpenableOutputsAndCountsTheRest proves the
// produced list is the reader's: what they can open is named and referenced,
// and the rest is a count with no name (#2027).
func TestScriptsProvider_FetchListsOpenableOutputsAndCountsTheRest(t *testing.T) {
	s := &fakeScriptSearcher{contract: runnableContract()}
	outs := &fakeScriptOutputs{
		open:   []ScriptOutput{{Kind: "asset", ID: "a1", Name: "Shared Report", Reference: "mcp:asset:a1"}},
		hidden: 2,
	}
	p := NewScriptsProvider(s)
	p.SetOutputs(outs)

	doc, _, err := p.Fetch(context.Background(), "mcp:script:script_1", Caller{Email: "bob@example.com"})

	require.NoError(t, err)
	assert.Equal(t, "bob@example.com", outs.caller.Email)
	assert.Contains(t, doc.Body, "Produced: Shared Report (mcp:asset:a1); 2 more you cannot open.")
	assert.Equal(t, []DocumentRef{{Reference: "mcp:asset:a1", Type: "asset"}}, doc.References)
	content, ok := doc.Content.(scriptDocument)
	require.True(t, ok)
	assert.Equal(t, 2, content.OutputsHidden)

	outs.open, outs.hidden = nil, 3
	doc, _, err = p.Fetch(context.Background(), "mcp:script:script_1", Caller{Email: "bob@example.com"})
	require.NoError(t, err)
	assert.Contains(t, doc.Body, "Produced: 3 more you cannot open.")
	assert.Empty(t, doc.References)

	outs.more = true
	doc, _, err = p.Fetch(context.Background(), "mcp:script:script_1", Caller{Email: "bob@example.com"})
	require.NoError(t, err)
	assert.Contains(t, doc.Body, "Produced: 3 more you cannot open; these are its most recent outputs, and it wrote older ones too.")
	outs.more = false

	outs.err = errors.New("down")
	doc, _, err = p.Fetch(context.Background(), "mcp:script:script_1", Caller{Email: "bob@example.com"})
	require.NoError(t, err, "a failed outputs read does not fail the fetch")
	assert.Contains(t, doc.Body, "Produced: what this script produced could not be read.")
	unavailable, ok := doc.Content.(scriptDocument)
	require.True(t, ok)
	assert.True(t, unavailable.OutputsUnavailable)

	outs.err = nil
	outs.open = []ScriptOutput{{Kind: "resource", ID: "r1", Reference: "mcp:resource:r1"}}
	outs.hidden = 0
	doc, _, err = p.Fetch(context.Background(), "mcp:script:script_1", Caller{Email: "bob@example.com"})
	require.NoError(t, err)
	assert.Contains(t, doc.Body, "Produced: r1 (mcp:resource:r1).", "an output with no name is shown by its id")
}

// TestScriptsProvider_FetchMissingIsNotFound proves a stale reference is a
// normal answer rather than a failure, so a deleted script reads as "that
// reference is gone".
func TestScriptsProvider_FetchMissingIsNotFound(t *testing.T) {
	s := &fakeScriptSearcher{}

	_, owned, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:gone", Caller{Email: "jane@example.com"})

	assert.True(t, owned)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestScriptsProvider_FetchStoreError(t *testing.T) {
	s := &fakeScriptSearcher{getErr: errors.New("down")}

	_, owned, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:script_1", Caller{Email: "jane@example.com"})

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

	doc, _, err := NewScriptsProvider(s).Fetch(context.Background(), "mcp:script:script_1",
		Caller{Email: "jane@example.com"})

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
