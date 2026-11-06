//go:build integration

package approval

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epstore"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const realForm = "def form_spec(req):\n    return {}\n\ndef form_submit(req, values):\n    return None\n"

// TestRealDB_TheQueueOnPostgres is the queue on a migrated database: one
// request waits per version, a decision is made once, the highest approved
// version is what the binding stamp reads, and the request goes with its
// script.
func TestRealDB_TheQueueOnPostgres(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	scripts := scriptstore.New(db)
	sc := &script.Script{Name: "intake", Source: realForm, OwnerEmail: "jane@example.com", Enabled: true}
	require.NoError(t, scripts.Create(ctx, sc, script.Author{Email: "jane@example.com"}))
	sc.Source, sc.Version = realForm+"# two\n", sc.Version+1
	require.NoError(t, scripts.UpdateWithVersion(ctx, sc, script.Author{Email: "jane@example.com"}))

	svc := &Service{Store: NewPostgresStore(db), Scripts: scripts, IsForm: func(string) bool { return true }}
	jane, admin := Actor{Email: "jane@example.com"}, Actor{Email: "admin@example.com", Admin: true}
	r1, err := svc.Submit(ctx, jane, Submit{Script: "intake", Version: 1, Reason: "launch"})
	require.NoError(t, err)
	_, err = svc.Submit(ctx, jane, Submit{Script: sc.ID, Version: 1})
	require.Error(t, err, "one request waits per version")
	got, err := svc.Get(ctx, jane, r1.ID)
	require.NoError(t, err)
	assert.Equal(t, "intake", got.ScriptName)
	assert.Equal(t, StatusPending, got.Status)

	bindings := epstore.New(db)
	require.NoError(t, bindings.Set(ctx, &epbinding.Binding{
		ScriptID: sc.ID, Name: "intake", Methods: []string{"GET", "POST"}, AuthMode: epbinding.AuthPublic,
		TimeoutSeconds: 10, MaxMemoryBytes: 64 << 20, MaxBodyBytes: 1 << 20, RateLimit: 600, CallerRateLimit: 120, Enabled: true,
		CheckedMethods: []string{}, Callers: []string{}, AllowedOrigins: []string{},
	}))
	st, err := bindings.Stamp(ctx, "intake")
	require.NoError(t, err)
	assert.Zero(t, st.ApprovedVersion, "nothing approved yet")

	decided, err := svc.Decide(ctx, admin, r1.ID, DecisionApprove, "fine")
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, decided.Status)
	require.NotNil(t, decided.DecidedAt)
	_, err = svc.Decide(ctx, admin, r1.ID, DecisionDeny, "")
	require.ErrorIs(t, err, ErrDecided)
	st, err = bindings.Stamp(ctx, "intake")
	require.NoError(t, err)
	assert.Equal(t, 1, st.ApprovedVersion, "the stamp reads the approved version")

	r2, err := svc.Submit(ctx, jane, Submit{Script: "intake", Version: 2})
	require.NoError(t, err)
	assert.Equal(t, 1, r2.Changes.ApprovedVersion)
	assert.Equal(t, 1, r2.Changes.Added)
	require.NoError(t, svc.RecordSelfApproval(ctx, admin, sc, 2))
	assert.Equal(t, 2, svc.Approved(ctx, sc.ID, PurposeFormPublic))
	self, err := svc.Get(ctx, admin, r2.ID)
	require.NoError(t, err)
	assert.True(t, self.SelfApproved, "the waiting request is the one an administrator's binding decided")

	mine, err := svc.List(ctx, jane, Filter{Status: StatusApproved, ScriptID: sc.ID, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, mine, 2)

	_, err = scripts.Delete(ctx, sc.ID)
	require.NoError(t, err)
	all, err := svc.List(ctx, admin, Filter{ScriptID: sc.ID})
	require.NoError(t, err)
	assert.Empty(t, all, "a request goes with its script")
}
