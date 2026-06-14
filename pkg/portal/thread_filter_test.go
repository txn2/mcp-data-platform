package portal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyThreadFilterConditions checks the SQL conditions for the #618 filter
// additions (standalone inclusion, unresolved, author exclusion) and the
// existing target-id OR, by building the query and inspecting the SQL/args.
func TestApplyThreadFilterConditions(t *testing.T) {
	tests := []struct {
		name      string
		filter    ThreadFilter
		wantSQL   []string
		wantArgs  []any
		absentSQL []string
	}{
		{
			name:      "empty filter adds no conditions",
			filter:    ThreadFilter{},
			absentSQL: []string{"target_type", "status", "author_id"},
		},
		{
			name:    "unresolved excludes terminal statuses",
			filter:  ThreadFilter{Unresolved: true},
			wantSQL: []string{"t.status NOT IN"},
			// resolved + wont_fix are the two terminal statuses.
			wantArgs: []any{ThreadStatusResolved, ThreadStatusWontFix},
		},
		{
			name:    "exclude author by id and email",
			filter:  ThreadFilter{ExcludeAuthorID: "u1", ExcludeAuthorEmail: "U1@Example.com"},
			wantSQL: []string{"t.author_id <>", "LOWER(t.author_email) <> LOWER("},
		},
		{
			name:    "include standalone alone matches the channel",
			filter:  ThreadFilter{IncludeStandalone: true},
			wantSQL: []string{"t.target_type ="},
			// the standalone OR term binds the standalone discriminator
			wantArgs: []any{targetTypeStandalone},
		},
		{
			name: "artifact ids OR standalone",
			filter: ThreadFilter{
				TargetAssetIDs:    []string{"a1", "a2"},
				IncludeStandalone: true,
			},
			wantSQL: []string{"t.asset_id IN", "t.target_type ="},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qb := applyThreadFilter(psq.Select("t.id").From("portal_threads t"), tt.filter)
			sql, args, err := qb.ToSql()
			require.NoError(t, err)
			for _, want := range tt.wantSQL {
				assert.Contains(t, sql, want, "sql: %s", sql)
			}
			for _, absent := range tt.absentSQL {
				assert.NotContains(t, sql, absent, "sql: %s", sql)
			}
			for _, wantArg := range tt.wantArgs {
				assert.Contains(t, args, wantArg, "args: %v", args)
			}
		})
	}
}

// TestApplyThreadFilterExcludeAuthorEmailLowercases verifies the excluded email
// is matched case-insensitively (mirrors the inclusive author filter).
func TestApplyThreadFilterExcludeAuthorEmailLowercases(t *testing.T) {
	qb := applyThreadFilter(psq.Select("t.id").From("portal_threads t"),
		ThreadFilter{ExcludeAuthorEmail: "Owner@Example.com"})
	sql, args, err := qb.ToSql()
	require.NoError(t, err)
	assert.True(t, strings.Contains(sql, "LOWER(t.author_email) <> LOWER("), "sql: %s", sql)
	assert.Contains(t, args, "Owner@Example.com")
}
