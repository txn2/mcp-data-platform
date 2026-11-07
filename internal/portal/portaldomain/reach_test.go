package portaldomain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseScope(t *testing.T) {
	assert.Equal(t, ScopeShared, ParseScope(" Shared ", ScopeMine))
	assert.Equal(t, ScopeAll, ParseScope("all", ScopeMine))
	assert.Equal(t, ScopeMine, ParseScope("everything", ScopeMine))
	assert.Equal(t, ScopeAll, ParseScope("", ScopeAll))
}

// The identifiers are normalized as an owner's are, so the anonymous sentinel
// names nobody, and a persona alone is enough to be addressed by a share.
func TestNewReach(t *testing.T) {
	r := NewReach(" u1 ", "A@Example.com", " analyst ", "nonsense")
	assert.Equal(t, "u1", r.Owner.UserID)
	assert.Equal(t, "analyst", r.Persona)
	assert.Equal(t, ScopeAll, r.Scope, "an unknown scope reads as all")
	assert.True(t, r.Identified())

	assert.True(t, NewReach("", "", "analyst", ScopeMine).Identified())
	assert.False(t, NewReach("", " ", "", ScopeMine).Identified())
	var none *Reach
	assert.False(t, none.Identified())
}

func TestNormalizeOwners(t *testing.T) {
	got := NormalizeOwners([]string{" B@Example.com ", "c@example.com,b@example.com", "", " , "})
	assert.Equal(t, []string{"b@example.com", "c@example.com"}, got)
	assert.Empty(t, NormalizeOwners(nil))
	assert.NotNil(t, NormalizeOwners(nil))
}

func TestRankedPage(t *testing.T) {
	limit, offset := RankedPage(0, -5)
	assert.Equal(t, DefaultSearchLimit, limit)
	assert.Zero(t, offset)
	limit, offset = RankedPage(50, 100)
	assert.Equal(t, 50, limit)
	assert.Equal(t, 100, offset)
	limit, offset = RankedPage(100, 5000)
	assert.Equal(t, 100, limit)
	assert.Equal(t, MaxRankedDepth-100, offset, "paging stops at the ranked depth")
}
