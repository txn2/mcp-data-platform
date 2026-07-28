package knowledge

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// stubScope is a ConnectionScope over fixed rules: allowed names and a URN →
// connections mapping. It stands in for the persona-backed adapter in the
// knowledge package's own unit tests; the assembled-system proof lives in
// internal/platform/searchfed.
type stubScope struct {
	allowed map[string]bool
	urns    map[string][]string
}

func (s stubScope) AllowConnection(_, connection string) bool { return s.allowed[connection] }

func (s stubScope) ConnectionsForURN(urn string) []string { return s.urns[urn] }

// testPersona is the acting persona every scoped-caller test uses; the stub scope
// decides by connection name, so the name only has to be non-empty and stable.
const testPersona = "analyst"

// scopedCaller builds a caller carrying scope, the way the Router does for every
// provider arm, and returns the gate so a test can read the withheld count back.
func scopedCaller(scope ConnectionScope) (Caller, *connGate) {
	gate := &connGate{scope: scope}
	return Caller{Persona: testPersona, conn: gate}, gate
}

func TestCallerAllowsConnection(t *testing.T) {
	scope := stubScope{allowed: map[string]bool{"warehouse": true}}
	caller, _ := scopedCaller(scope)

	assert.True(t, caller.allowsConnection("warehouse"), "an allowed connection is visible")
	assert.False(t, caller.allowsConnection("payroll"), "a denied connection is hidden")
	assert.True(t, caller.allowsConnection(""),
		"an empty connection name is platform-level and always visible")

	// No scope wired: discovery is unfiltered, the pre-#1108 behavior.
	assert.True(t, Caller{}.allowsConnection("payroll"))
	unscoped, _ := scopedCaller(nil)
	assert.True(t, unscoped.allowsConnection("payroll"))
}

func TestCallerAllowsURN(t *testing.T) {
	scope := stubScope{
		allowed: map[string]bool{"warehouse": true},
		urns: map[string][]string{
			"urn:mine":   {"warehouse"},
			"urn:theirs": {"payroll"},
			"urn:both":   {"payroll", "warehouse"},
		},
	}
	caller, _ := scopedCaller(scope)

	assert.True(t, caller.allowsURN("urn:mine"))
	assert.False(t, caller.allowsURN("urn:theirs"))
	assert.True(t, caller.allowsURN("urn:both"),
		"a URN reachable through any permitted connection stays visible")
	assert.True(t, caller.allowsURN("urn:unmapped"),
		"a URN that maps to no connection stays visible rather than being hidden on a guess")
}

func TestCallerWithholdAccumulates(t *testing.T) {
	caller, gate := scopedCaller(stubScope{})
	caller.withhold(2)
	caller.withhold(3)
	caller.withhold(0)
	caller.withhold(-1)
	assert.Equal(t, 5, gate.withheld, "only positive counts accumulate")

	// An unscoped caller drops the count instead of panicking.
	assert.NotPanics(t, func() { Caller{}.withhold(1) })
}

func TestWithheldNotice(t *testing.T) {
	t.Run("empty when nothing was withheld", func(t *testing.T) {
		assert.Empty(t, WithheldNotice(nil, "analyst"))
		assert.Empty(t, WithheldNotice([]SourceCoverage{{Source: "catalog", Matched: 3}}, "analyst"))
	})

	t.Run("names the count, the sources, the reason, and the remedy", func(t *testing.T) {
		got := WithheldNotice([]SourceCoverage{
			{Source: "endpoints", Withheld: 2},
			{Source: "catalog", Matched: 1, Withheld: 3},
			{Source: "memory", Matched: 4},
		}, "analyst")
		assert.Contains(t, got, "5 results are hidden", "counts sum across sources")
		assert.Contains(t, got, "catalog and endpoints", "sources are named, sorted")
		assert.NotContains(t, got, "memory", "a source that withheld nothing is not named")
		assert.Contains(t, got, "your persona (analyst)", "the reason names the persona")
		assert.Contains(t, got, "Ask an administrator", "the denial carries the path in")
	})

	t.Run("agrees in number for a single result", func(t *testing.T) {
		got := WithheldNotice([]SourceCoverage{{Source: "catalog", Withheld: 1}}, "analyst")
		assert.Contains(t, got, "1 result is hidden")
	})

	t.Run("refers to the persona generically when none resolved", func(t *testing.T) {
		got := WithheldNotice([]SourceCoverage{{Source: "catalog", Withheld: 1}}, "")
		assert.Contains(t, got, "your persona is not granted")
	})
}

func TestConnectionsWithheldNotice(t *testing.T) {
	assert.Empty(t, ConnectionsWithheldNotice(0, "analyst"))

	one := ConnectionsWithheldNotice(1, "analyst")
	assert.Contains(t, one, "1 connection is hidden")
	assert.Contains(t, one, "your persona (analyst)")
	assert.Contains(t, one, "Ask an administrator")

	many := ConnectionsWithheldNotice(4, "analyst")
	assert.Contains(t, many, "4 connections are hidden")
}

func TestJoinAnd(t *testing.T) {
	assert.Empty(t, joinAnd(nil))
	assert.Equal(t, "a", joinAnd([]string{"a"}))
	assert.Equal(t, "a and b", joinAnd([]string{"a", "b"}))
	assert.Equal(t, "a, b, and c", joinAnd([]string{"a", "b", "c"}))
}

func TestAllocate_WithheldOnlySourceStillReported(t *testing.T) {
	// Every candidate from one source was withheld: it contributes no group, but
	// coverage must still carry it, since "nothing matched" and "matches you may
	// not see" are different answers.
	groups, cov := allocate([]sourceResult{
		{source: "memory", hits: []Hit{{Source: "memory", Ref: "m1", Score: 1}}},
		{source: "catalog", withheld: 3},
	}, 10)

	require.Len(t, groups, 1)
	assert.Equal(t, "memory", groups[0].Source)

	bySource := coverageBySource(cov)
	require.Contains(t, bySource, "catalog")
	assert.Equal(t, SourceCoverage{Source: "catalog", Matched: 0, Shown: 0, Withheld: 3}, bySource["catalog"])
	assert.Zero(t, bySource["memory"].Withheld)
}

// TestRouter_ConnectionScopeReachesProviders proves the wiring the surfaces
// depend on: a scope set on the Router arrives at every provider arm and at
// fetch, so a provider never has to be handed a boundary by its caller.
func TestRouter_ConnectionScopeReachesProviders(t *testing.T) {
	lister := &fakeConnLister{conns: []ConnectionInfo{
		{Name: "warehouse", Kind: "trino", Description: "analytics"},
		{Name: "payroll", Kind: "trino", Description: "analytics"},
	}}
	r := NewRouter(nil, nil, NewConnectionsProvider(lister))
	r.SetConnectionScope(stubScope{allowed: map[string]bool{"warehouse": true}})

	res, err := r.Search(context.Background(), Query{Intent: "analytics", Caller: Caller{Persona: "analyst"}, Limit: 10})
	require.NoError(t, err)

	require.Len(t, res.Groups, 1)
	require.Len(t, res.Groups[0].Hits, 1)
	assert.Equal(t, "warehouse", res.Groups[0].Hits[0].Ref)
	require.Len(t, res.Coverage, 1)
	assert.Equal(t, 1, res.Coverage[0].Withheld)

	// The same boundary applies to fetch, so a citation cannot read around it.
	_, err = r.Fetch(context.Background(), knowledgepage.ConnectionRef("trino", "payroll"), Caller{Persona: "analyst"})
	assert.ErrorIs(t, err, ErrNotFound)
}
