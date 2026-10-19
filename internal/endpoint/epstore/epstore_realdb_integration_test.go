//go:build integration

package epstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func savedScript(ctx context.Context, t *testing.T, s *scriptstore.Store, name string) *script.Script {
	t.Helper()
	sc := &script.Script{Name: name, Source: "def handle(req):\n    return {\"status\": 200}\n", OwnerEmail: "jane@example.com", Enabled: true}
	require.NoError(t, s.Create(ctx, sc, script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}))
	return sc
}

// The two rules the binding table holds and nothing else can: a script has
// one binding, replaced in place, and a name serves one script.
func TestRealDB_OneBindingPerScriptAndOneScriptPerName(t *testing.T) {
	db := testdb.New(t)
	scripts, s := scriptstore.New(db), New(db)
	ctx := context.Background()
	a, b := savedScript(ctx, t, scripts, "lookup-a"), savedScript(ctx, t, scripts, "lookup-b")

	first := sample()
	first.ScriptID, first.AllowedOrigins = a.ID, []string{"https://forms.example.com"}
	require.NoError(t, s.Set(ctx, first))
	again := sample()
	again.ScriptID, again.Methods, again.Path = a.ID, []string{"POST"}, "/{id}"
	require.NoError(t, s.Set(ctx, again))
	assert.Equal(t, first.ID, again.ID, "a script's binding is replaced in place")

	got, err := s.GetByName(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, []string{"POST"}, got.Methods)
	assert.Equal(t, "/{id}", got.Path)
	assert.Equal(t, []string{}, got.AllowedOrigins, "the replacement's empty origins are stored as empty")

	st, err := s.Stamp(ctx, "orders")
	require.NoError(t, err)
	assert.False(t, st.Binding.IsZero())
	assert.False(t, st.Script.IsZero())

	clash := sample()
	clash.ScriptID = b.ID
	require.ErrorIs(t, s.Set(ctx, clash), epbinding.ErrNameTaken)

	require.NoError(t, s.SetEnabled(ctx, a.ID, false, "admin@example.com"))
	got, err = s.Get(ctx, a.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled)

	listed, err := s.List(ctx, epbinding.Filter{ScriptIDs: []string{b.ID}})
	require.NoError(t, err)
	assert.Empty(t, listed)
}

func TestRealDB_FailuresAreKeptAndSweptAndGoWithTheScript(t *testing.T) {
	db := testdb.New(t)
	scripts, s := scriptstore.New(db), New(db)
	ctx := context.Background()
	sc := savedScript(ctx, t, scripts, "lookup")
	b := sample()
	b.ScriptID = sc.ID
	require.NoError(t, s.Set(ctx, b))

	for _, status := range []int{500, 504} {
		require.NoError(t, s.RecordFailure(ctx, epbinding.Failure{EndpointID: b.ID, Version: 1, Method: "GET", Status: status, Error: "x"}))
	}
	got, err := s.Failures(ctx, b.ID, 10)
	require.NoError(t, err)
	require.Len(t, got, 2)

	n, err := s.SweepFailures(ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	require.NoError(t, s.RecordFailure(ctx, epbinding.Failure{EndpointID: b.ID, Version: 1, Method: "GET", Status: 500, Error: "x"}))
	_, err = scripts.Delete(ctx, sc.ID)
	require.NoError(t, err)
	_, err = s.Get(ctx, sc.ID)
	require.ErrorIs(t, err, epbinding.ErrNotFound, "deleting the script removes its binding")
	got, err = s.Failures(ctx, b.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, got, "and the failures its binding answered")
}
