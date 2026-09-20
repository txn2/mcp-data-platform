package script_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"ok", "daily-sales_1", ""},
		{"empty", "", "name is required"},
		{"uppercase", "DailySales", "lowercase"},
		{"leading hyphen", "-daily", "lowercase"},
		{"space", "daily sales", "lowercase"},
		{"too long", string(make([]byte, 200)), "at most"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := script.ValidateName(tc.input)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestValidateSourceAndTags(t *testing.T) {
	assert.ErrorContains(t, script.ValidateSource(""), "required")
	assert.NoError(t, script.ValidateSource("x = 1"))

	oversize := make([]byte, script.MaxSourceBytes+1)
	assert.ErrorContains(t, script.ValidateSource(string(oversize)), "over the")

	assert.NoError(t, script.ValidateTags([]string{"a", "b"}))
	assert.ErrorContains(t, script.ValidateTags(make([]string, 21)), "too many tags")
	assert.ErrorContains(t, script.ValidateTags([]string{string(make([]byte, 200))}), "exceeds")
}

func TestValidateStatus(t *testing.T) {
	assert.NoError(t, script.ValidateStatus(script.StatusActive))
	assert.ErrorContains(t, script.ValidateStatus("retired"), "invalid status")
	assert.NoError(t, script.ValidateStatusTransition(script.StatusActive, script.StatusDeprecated))
	assert.ErrorContains(t, script.ValidateStatusTransition(script.StatusSuperseded, script.StatusActive), "invalid status transition")
}

// TestValidate_WholeRecord proves the record-level check refuses each field it
// covers, applied to the final state rather than to arguments as they arrive.
func TestValidate_WholeRecord(t *testing.T) {
	base := func() *script.Script {
		return &script.Script{
			Name: "daily", Source: "x = 1", Status: script.StatusActive,
		}
	}
	assert.NoError(t, base().Validate())

	refusals := []struct {
		name    string
		mutate  func(*script.Script)
		wantErr string
	}{
		{"name", func(s *script.Script) { s.Name = "" }, "name is required"},
		{"source", func(s *script.Script) { s.Source = "" }, "source is required"},
		{"params", func(s *script.Script) { s.Params = []script.Param{{Name: "Day", Type: script.ParamTypeDate}} }, "lowercase letter"},
		{"tags", func(s *script.Script) { s.Tags = make([]string, 21) }, "too many tags"},
		{"status", func(s *script.Script) { s.Status = "retired" }, "invalid status"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			sc := base()
			tc.mutate(sc)
			assert.ErrorContains(t, sc.Validate(), tc.wantErr)
		})
	}
}

func TestApplyStatusTransition_LifecycleStamps(t *testing.T) {
	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	sc := &script.Script{Status: script.StatusActive}
	require.NoError(t, sc.ApplyStatusTransition(script.StatusDeprecated, "", now))
	require.NotNil(t, sc.DeprecatedAt)
	assert.Equal(t, now, *sc.DeprecatedAt)

	// Reactivating clears the deprecation stamp, so a reactivated script does
	// not read as both active and deprecated.
	require.NoError(t, sc.ApplyStatusTransition(script.StatusActive, "", now))
	assert.Nil(t, sc.DeprecatedAt)

	require.NoError(t, sc.ApplyStatusTransition(script.StatusSuperseded, "daily-v2", now))
	assert.Equal(t, "daily-v2", sc.SupersededBy)

	// No-ops and unknown values.
	assert.NoError(t, sc.ApplyStatusTransition("", "", now))
	assert.NoError(t, sc.ApplyStatusTransition(script.StatusSuperseded, "", now))
	assert.ErrorContains(t, sc.ApplyStatusTransition("retired", "", now), "invalid status")
}

// TestOwnedBy is the one definition of script visibility, shared by the read
// path and the list predicate so a script can never be listable but unreadable.
// Both sides must be identified: an ownerless script belongs to nobody, not to
// every caller the platform cannot name.
func TestOwnedBy(t *testing.T) {
	cases := []struct {
		name  string
		sc    *script.Script
		email string
		want  bool
	}{
		{"its owner", &script.Script{OwnerEmail: "bob@example.com"}, "bob@example.com", true},
		{"anybody else", &script.Script{OwnerEmail: "jane@example.com"}, "bob@example.com", false},
		{"ownerless to an identified caller", &script.Script{}, "bob@example.com", false},
		{"ownerless to an unidentified caller", &script.Script{}, "", false},
		{"owned, caller unidentified", &script.Script{OwnerEmail: "jane@example.com"}, "", false},
		{"no script", nil, "bob@example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.sc.OwnedBy(tc.email))
		})
	}
}

// TestParseSortColumn_ReportsRatherThanSubstituting covers the whitelist the
// listing's ordering rests on (#1795). It REPORTS an unrecognized column
// rather than substituting one, because a route that is told so leaves the
// filter's Sort empty and gets the store's own default ordering — while
// substituting SortUpdatedAt would leave the DIRECTION to the caller's `dir`
// and answer oldest-first for a filter that named nothing valid.
func TestParseSortColumn_ReportsRatherThanSubstituting(t *testing.T) {
	for _, want := range script.SortColumns() {
		got, ok := script.ParseSortColumn(string(want))
		assert.True(t, ok, "%s is orderable", want)
		assert.Equal(t, want, got)
	}

	for _, bad := range []string{"", "last_run", "source_code", "updated_at; DROP TABLE scripts"} {
		_, ok := script.ParseSortColumn(bad)
		assert.False(t, ok, "%q is not an orderable column", bad)
	}
}

// TestSortColumns_ExcludesLastRun pins the one column deliberately absent: a
// run is attached to a page AFTER the query, so ordering by it would be an
// ordering over the page rather than over the listing.
func TestSortColumns_ExcludesLastRun(t *testing.T) {
	for _, col := range script.SortColumns() {
		assert.NotContains(t, string(col), "run")
	}
	assert.Len(t, script.SortColumns(), 5)
}
