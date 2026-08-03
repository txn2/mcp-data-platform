package persona

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allRuleTools is every tool the rule table names, standing in for a fully
// featured deployment.
var allRuleTools = []string{"search", "fetch", "memory_capture", "apply_knowledge", "trino_query"}

func TestCheckCoherence(t *testing.T) {
	tests := []struct {
		name       string
		allow      []string
		deny       []string
		registered []string
		wantPairs  []string // "granted->missing"
	}{
		{
			name:       "wildcard grant is coherent",
			allow:      []string{"*"},
			registered: allRuleTools,
		},
		{
			name:       "search without fetch is flagged",
			allow:      []string{"search", "trino_*"},
			registered: allRuleTools,
			wantPairs:  []string{"search->fetch"},
		},
		{
			name:       "search with fetch is coherent",
			allow:      []string{"search", "fetch", "trino_*"},
			registered: allRuleTools,
		},
		{
			name:       "fetch denied by a deny pattern is flagged",
			allow:      []string{"*"},
			deny:       []string{"fetch"},
			registered: allRuleTools,
			wantPairs:  []string{"search->fetch"},
		},
		{
			name:       "memory_capture without search is flagged",
			allow:      []string{"memory_capture", "trino_*"},
			registered: allRuleTools,
			wantPairs:  []string{"memory_capture->search"},
		},
		{
			name:       "apply_knowledge without search is flagged",
			allow:      []string{"apply_knowledge"},
			registered: allRuleTools,
			wantPairs:  []string{"apply_knowledge->search"},
		},
		{
			name:       "denying search trips both write rules and not the search rule",
			allow:      []string{"*"},
			deny:       []string{"search"},
			registered: allRuleTools,
			wantPairs:  []string{"apply_knowledge->search", "memory_capture->search"},
		},
		{
			name:       "granting nothing is coherent",
			allow:      nil,
			deny:       []string{"*"},
			registered: allRuleTools,
		},
		{
			name:       "a glob that covers both halves is coherent",
			allow:      []string{"*e*"}, // matches search, fetch, memory_capture, apply_knowledge
			registered: allRuleTools,
		},
		{
			// The rule speaks only to tools this deployment registered: a
			// deployment with no fetch is not misconfigured for lacking it.
			name:       "unregistered required tool does not fire",
			allow:      []string{"search"},
			registered: []string{"search", "memory_capture", "apply_knowledge"},
		},
		{
			name:       "unregistered granted tool does not fire",
			allow:      []string{"*"},
			deny:       []string{"search"},
			registered: []string{"search", "fetch"},
		},
		{
			name:       "empty registered set fires nothing",
			allow:      []string{"*"},
			deny:       []string{"fetch", "search"},
			registered: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Persona{Name: "subject", Tools: ToolRules{Allow: tt.allow, Deny: tt.deny}}

			got := CheckCoherence(p, tt.registered)

			pairs := make([]string, 0, len(got))
			for _, f := range got {
				pairs = append(pairs, f.Granted+"->"+f.Missing)
			}
			assert.ElementsMatch(t, tt.wantPairs, pairs)
		})
	}
}

func TestCheckCoherenceFindingContent(t *testing.T) {
	p := &Persona{Name: "analyst", Tools: ToolRules{Allow: []string{"search"}}}

	got := CheckCoherence(p, allRuleTools)

	require.Len(t, got, 1)
	f := got[0]
	assert.Equal(t, "analyst", f.Persona)
	assert.Equal(t, "search", f.Granted)
	assert.Equal(t, "fetch", f.Missing)
	assert.Contains(t, f.Why, "dereferences", "Why must state the capability that is lost")
	assert.Contains(t, f.Remedy, `"fetch"`, "Remedy must name the missing tool")
	assert.Contains(t, f.Remedy, `"analyst"`, "Remedy must name the persona")
	assert.Contains(t, f.Remedy, "tools.allow", "Remedy must name the field to edit")
}

func TestCheckCoherenceNilPersona(t *testing.T) {
	assert.Nil(t, CheckCoherence(nil, allRuleTools))
}

func TestCheckRegistryCoherence(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register(&Persona{Name: "zeta", Tools: ToolRules{Allow: []string{"search"}}}))
	// alpha trips both write rules, so it also exercises the same-persona
	// tie-break on the granted tool.
	require.NoError(t, reg.Register(&Persona{Name: "alpha", Tools: ToolRules{Allow: []string{"*"}, Deny: []string{"search"}}}))
	require.NoError(t, reg.Register(&Persona{Name: "beta", Tools: ToolRules{Allow: []string{"*"}}}))

	got := CheckRegistryCoherence(reg, allRuleTools)

	// alpha's findings sort before zeta's, and within alpha the granted tool
	// orders them. Registry.All iterates a map, so without the sort this order
	// varies between runs and a startup log is unreadable.
	require.Len(t, got, 3)
	assert.Equal(t, "alpha", got[0].Persona)
	assert.Equal(t, "apply_knowledge", got[0].Granted)
	assert.Equal(t, "alpha", got[1].Persona)
	assert.Equal(t, "memory_capture", got[1].Granted)
	assert.Equal(t, "zeta", got[2].Persona)
	assert.Equal(t, "search", got[2].Granted)
}

// TestCheckRegistryCoherenceStableOrder pins the ordering guarantee against
// Go's randomized map iteration, which a single run cannot detect.
func TestCheckRegistryCoherenceStableOrder(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"d", "a", "c", "b", "e"} {
		require.NoError(t, reg.Register(&Persona{Name: name, Tools: ToolRules{Allow: []string{"search"}}}))
	}

	want := []string{"a", "b", "c", "d", "e"}
	for range 20 {
		got := CheckRegistryCoherence(reg, allRuleTools)
		names := make([]string, 0, len(got))
		for _, f := range got {
			names = append(names, f.Persona)
		}
		require.Equal(t, want, names)
	}
}

func TestCheckRegistryCoherenceNilRegistry(t *testing.T) {
	assert.Nil(t, CheckRegistryCoherence(nil, allRuleTools))
}

// TestCoherenceRuleTableIsWellFormed guards the rule table itself: a rule whose
// Grant and Requires are equal can never fire, and an empty Why produces a
// warning an operator cannot act on.
func TestCoherenceRuleTableIsWellFormed(t *testing.T) {
	require.NotEmpty(t, coherenceRules)
	seen := map[string]bool{}
	for _, r := range coherenceRules {
		assert.NotEmpty(t, r.Grant)
		assert.NotEmpty(t, r.Requires)
		assert.NotEqual(t, r.Grant, r.Requires, "a rule requiring its own grant can never fire")
		assert.NotEmpty(t, r.Why, "rule %s->%s has no reason to log", r.Grant, r.Requires)

		key := r.Grant + "->" + r.Requires
		assert.False(t, seen[key], "duplicate rule %s would warn twice", key)
		seen[key] = true
	}
}
