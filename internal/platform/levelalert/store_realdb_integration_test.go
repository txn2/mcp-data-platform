//go:build integration

package levelalert

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestEngineRealDB walks a condition from warning to critical to resolved
// through alert_conditions, and holds a condition's start across passes at
// the same level.
func TestEngineRealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	store := NewPostgresStore(db)
	sender := &fakeSender{}
	e := New(store, sender)
	f := Finding{Key: "webhook:spool_age:a", Title: "spool age on a"}

	f.Level = Warning
	_, err := e.Apply(ctx, "webhook", []string{"ops"}, []Finding{f})
	require.NoError(t, err)
	first, err := store.List(ctx, "webhook")
	require.NoError(t, err)
	require.Len(t, first, 1)

	e.now = func() time.Time { return time.Now().Add(time.Minute) }
	_, err = e.Apply(ctx, "webhook", []string{"ops"}, []Finding{f})
	require.NoError(t, err)
	again, err := store.List(ctx, "webhook")
	require.NoError(t, err)
	assert.True(t, first[0].Since.Equal(again[0].Since), "a condition at the same level keeps when it began")

	f.Level = Critical
	_, err = e.Apply(ctx, "webhook", []string{"ops"}, []Finding{f})
	require.NoError(t, err)
	crit, err := store.List(ctx, "webhook")
	require.NoError(t, err)
	assert.Equal(t, Critical, crit[0].Level)
	assert.True(t, crit[0].Since.After(first[0].Since), "a new level starts again")

	res, err := e.Apply(ctx, "webhook", []string{"ops"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Resolved)
	none, err := store.List(ctx, "webhook")
	require.NoError(t, err)
	assert.Empty(t, none)
	got := keys(sender.sent)
	require.Len(t, got, 4)
	for i, want := range []string{"webhook:spool_age:a:warning:", "webhook:spool_age:a:warning:", "webhook:spool_age:a:critical:", "webhook:spool_age:a:resolved:"} {
		assert.True(t, strings.HasPrefix(got[i], want), "%s does not start %s", got[i], want)
	}
	assert.Equal(t, got[0], got[1], "one episode repeats under one key")

	require.NoError(t, Locked(ctx, db, 4713219999, func(context.Context) error { return nil }))
}
