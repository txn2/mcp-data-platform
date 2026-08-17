package sessionview

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterFromQuery_ReadsEveryFacet(t *testing.T) {
	q := url.Values{
		"kind":         {"script"},
		"has_assets":   {"true"},
		"has_failures": {"1"},
		"start_time":   {"2026-08-16T00:00:00Z"},
		"end_time":     {"2026-08-16T23:59:59Z"},
		"page":         {"3"},
		"per_page":     {"10"},
	}

	got := FilterFromQuery(q)
	assert.Equal(t, KindScript, got.Kind)
	assert.True(t, got.HasAssets)
	assert.True(t, got.HasFailures)
	require.NotNil(t, got.StartTime)
	assert.Equal(t, 2026, got.StartTime.Year())
	require.NotNil(t, got.EndTime)
	assert.Equal(t, 10, got.Limit)
	assert.Equal(t, 20, got.Offset, "page 3 at 10 per page starts at row 20")
}

// The caller is the one facet this parser does not read. Each surface assigns
// it — the operator from a query parameter, the portal from the authenticated
// session — so a portal request carrying user_id must arrive here as unscoped
// and be overwritten rather than honored.
func TestFilterFromQuery_LeavesTheCallerUnset(t *testing.T) {
	got := FilterFromQuery(url.Values{"user_id": {"someone-else"}})
	assert.Empty(t, got.UserID)
	assert.Empty(t, got.SessionID)
}

// A malformed flag is "no filter stated", not "only the narrow set": a bad
// value must never silently select a subset of the sessions.
func TestFilterFromQuery_MalformedValuesAreUnset(t *testing.T) {
	got := FilterFromQuery(url.Values{
		"has_assets":   {"maybe"},
		"has_failures": {""},
		"start_time":   {"last tuesday"},
		"per_page":     {"lots"},
	})
	assert.False(t, got.HasAssets)
	assert.False(t, got.HasFailures)
	assert.Nil(t, got.StartTime)
	assert.Equal(t, DefaultPerPage, got.Limit)
}

func TestClampPerPage(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"unstated", 0, DefaultPerPage},
		{"negative", -5, DefaultPerPage},
		{"over the cap", 5000, MaxPerPage},
		{"at the cap", MaxPerPage, MaxPerPage},
		{"stated", 10, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ClampPerPage(tt.limit))
		})
	}
}
