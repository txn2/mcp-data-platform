package scriptlist

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	// Scope decides the population; a non-admin's default is every script
	// (#1994), and scope=mine narrows it to their own.
	assert.Empty(t, Filter(stranger, false, nil).OwnerEmail, "every script by default")
	assert.Empty(t, Filter(stranger, false, url.Values{"scope": {"all"}}).OwnerEmail)
	mine := Filter(stranger, false, url.Values{"scope": {ScopeMine}})
	assert.Equal(t, "carol@example.com", mine.OwnerEmail)
	assert.Empty(t, Filter(stranger, true, url.Values{"scope": {ScopeMine}}).OwnerEmail,
		"an administrator's listing is every script")

	// Naming an author selects the population rather than being intersected
	// with the default scope, which would answer the empty set for every
	// author but the caller.
	theirs := Filter(stranger, false, url.Values{"owner": {"dana@example.com"}})
	assert.Equal(t, "dana@example.com", theirs.OwnerEmail)
}

// TestFilter_Kind: kind=library lists libraries, kind=script the scripts
// that run, and no kind both (#1941, #1970).
func TestFilter_Kind(t *testing.T) {
	lib := Filter("jane@example.com", false, url.Values{"kind": {KindLibrary}})
	require.NotNil(t, lib.Library)
	assert.True(t, *lib.Library)
	scripts := Filter("jane@example.com", true, url.Values{"kind": {KindScript}})
	require.NotNil(t, scripts.Library)
	assert.False(t, *scripts.Library)
	assert.Nil(t, Filter("jane@example.com", false, url.Values{}).Library)
}

// TestParseKind: the two kinds and no kind are read; any other value,
// including the automation the listing named scripts by before #1970, is
// refused.
func TestParseKind(t *testing.T) {
	_, named, err := ParseKind("")
	require.NoError(t, err)
	assert.False(t, named)
	library, named, err := ParseKind(KindLibrary)
	require.NoError(t, err)
	assert.True(t, named)
	assert.True(t, library)
	library, named, err = ParseKind(KindScript)
	require.NoError(t, err)
	assert.True(t, named)
	assert.False(t, library)
	for _, bad := range []string{"automation", "Script", "other"} {
		_, named, err := ParseKind(bad)
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "unknown kind")
		assert.False(t, named)
	}
}
