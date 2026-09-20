package scriptlist

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestParseBool_TellsAbsentFromFalse covers the distinction the listing's
// `enabled` parameter turns on (#1795): an unset parameter must not narrow the
// listing to the disabled scripts, which is what a bare strconv.ParseBool of
// "" would have done.
func TestParseBool_TellsAbsentFromFalse(t *testing.T) {
	for _, tc := range []struct {
		in    string
		value bool
		ok    bool
	}{
		{"true", true, true},
		{"1", true, true},
		{"false", false, true},
		{"0", false, true},
		{"", false, false},
		{"yes", false, false},
		{"maybe", false, false},
	} {
		value, ok := parseBool(tc.in)
		assert.Equal(t, tc.ok, ok, "parseBool(%q) recognized", tc.in)
		assert.Equal(t, tc.value, value, "parseBool(%q) value", tc.in)
	}
}

// TestFilter_OrderingAndScope holds the two halves the query string
// carries beyond the narrowing axes.
func TestFilter_OrderingAndScope(t *testing.T) {
	const stranger = "carol@example.com"

	sorted := Filter(stranger, false, url.Values{"sort": {"name"}, "dir": {"asc"}})
	assert.Equal(t, script.SortName, sorted.Sort)
	assert.False(t, sorted.Desc, "dir=asc reads ascending")

	descending := Filter(stranger, false, url.Values{"sort": {"name"}})
	assert.True(t, descending.Desc, "a named column with no direction reads descending")

	// An unrecognized column leaves the ordering to the store, which is most
	// recently updated first — NOT the named column at the caller's direction.
	unknown := Filter(stranger, false, url.Values{"sort": {"last_run"}, "dir": {"asc"}})
	assert.Empty(t, unknown.Sort)
	assert.False(t, unknown.Desc)

	// Scope decides the population; a non-admin's default is their own.
	mine := Filter(stranger, false, nil)
	assert.Equal(t, "carol@example.com", mine.OwnerEmail)
	all := Filter(stranger, false, url.Values{"scope": {"all"}})
	assert.Empty(t, all.OwnerEmail, "scope=all lifts the owner predicate")

	// Naming an author selects the population rather than being intersected
	// with the default scope, which would answer the empty set for every
	// author but the caller.
	theirs := Filter(stranger, false, url.Values{"owner": {"dana@example.com"}})
	assert.Equal(t, "dana@example.com", theirs.OwnerEmail)
}
