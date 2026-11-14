package scripthandler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestConnectionHandlers lists what applies to a connection: policies,
// transforms in their order, then watches, each with its counts; a handler
// on another connection is not listed, and only an administrator reads it.
func TestConnectionHandlers(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "limit", "jane@example.com", policySource)
	svc := w.service()
	at := time.Unix(100, 0)
	w.calls.byID = map[string]callhandler.Handler{
		"w1": {ID: "w1", ScriptID: sc.ID, Kind: KindWatch, Connection: "billing-*", CreatedAt: at},
		"t2": {ID: "t2", ScriptID: sc.ID, Kind: KindTransform, Connection: "billing-api", Order: 2, CreatedAt: at},
		"t1": {ID: "t1", ScriptID: sc.ID, Kind: KindTransform, Connection: "billing-api", Order: 1, CreatedAt: at},
		"p1": {ID: "p1", ScriptID: sc.ID, Kind: KindPolicy, Connection: "*", CreatedAt: at},
		"x1": {ID: "x1", ScriptID: sc.ID, Kind: KindPolicy, Connection: "crm", CreatedAt: at},
		"g1": {ID: "g1", ScriptID: "gone", Kind: KindPolicy, Connection: "billing-api", CreatedAt: at},
	}
	w.calls.stats = map[string]callhandler.Stat{"p1": {Matched: 4, Denied: 1}}

	_, err := svc.ConnectionHandlers(ctx, "billing-api", jane)
	require.ErrorIs(t, err, ErrNotAllowed)

	rows, err := svc.ConnectionHandlers(ctx, "billing-api", admin)
	require.NoError(t, err)
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		id, _ := r[fieldHandler].(string)
		ids = append(ids, id)
	}
	assert.Equal(t, []string{"p1", "t1", "t2", "w1"}, ids, "policies, transforms in order, then watches; another connection's and a gone script's are left out")
	assert.Equal(t, callhandler.Stat{Matched: 4, Denied: 1}, rows[0]["stats"])
	assert.Equal(t, callhandler.Stat{}, rows[1]["stats"])

	w.calls.stats = nil
	rows, err = svc.ConnectionHandlers(ctx, "billing-api", admin)
	require.NoError(t, err, "counts that cannot be read list the handlers without them")
	assert.Len(t, rows, 4)

	w.calls.err = errors.New("down")
	_, err = svc.ConnectionHandlers(ctx, "billing-api", admin)
	require.Error(t, err)

	none := &Service{}
	rows, err = none.ConnectionHandlers(ctx, "billing-api", admin)
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{}, rows, "no store lists none, never null")
}

// TestOrderTransforms sets the order transforms run in, an administrator's
// only.
func TestOrderTransforms(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	svc := w.service()
	w.calls.byID = map[string]callhandler.Handler{"a": {ID: "a", Kind: KindTransform}, "b": {ID: "b", Kind: KindTransform}}
	require.ErrorIs(t, svc.OrderTransforms(ctx, []string{"b", "a"}, jane), ErrNotAllowed)
	require.NoError(t, svc.OrderTransforms(ctx, []string{"b", "a"}, admin))
	assert.Equal(t, 0, w.calls.byID["b"].Order)
	assert.Equal(t, 1, w.calls.byID["a"].Order)
	assert.Equal(t, admin.Email, w.calls.byID["a"].UpdatedBy)
	require.Error(t, svc.OrderTransforms(ctx, []string{"none"}, admin))
	require.ErrorIs(t, (&Service{}).OrderTransforms(ctx, nil, admin), ErrInvalid)
}

// apprStore is an approval.Store in memory, enough for handler versions.
type apprStore struct{ rows []*approval.Request }

func (s *apprStore) Insert(_ context.Context, r *approval.Request) error {
	for _, x := range s.rows {
		if x.Status == approval.StatusPending && x.ScriptID == r.ScriptID && x.Purpose == r.Purpose && x.Version == r.Version {
			return approval.ErrPending
		}
	}
	r.Status = approval.StatusPending
	c := *r
	s.rows = append(s.rows, &c)
	return nil
}

func (s *apprStore) Get(_ context.Context, id string) (*approval.Request, error) {
	for _, r := range s.rows {
		if r.ID == id {
			c := *r
			return &c, nil
		}
	}
	return nil, approval.ErrNotFound
}

func (s *apprStore) List(_ context.Context, f approval.Filter) ([]approval.Request, error) {
	out := []approval.Request{}
	for _, r := range s.rows {
		if (f.Status == "" || r.Status == f.Status) && (f.ScriptID == "" || r.ScriptID == f.ScriptID) && (f.Purpose == "" || r.Purpose == f.Purpose) {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (s *apprStore) Decide(_ context.Context, id string, d approval.Decision) (*approval.Request, error) {
	for _, r := range s.rows {
		if r.ID == id {
			r.Status, r.DecidedBy, r.DecidedRoles, r.SelfApproved = d.Status, d.By, d.Roles, d.SelfApproved
			c := *r
			return &c, nil
		}
	}
	return nil, approval.ErrNotFound
}

func (s *apprStore) LatestApproved(_ context.Context, scriptID, purpose string) (*approval.Request, error) {
	var best *approval.Request
	for _, r := range s.rows {
		if r.ScriptID == scriptID && r.Purpose == purpose && r.Status == approval.StatusApproved && (best == nil || r.Version > best.Version) {
			best = r
		}
	}
	if best == nil {
		return nil, approval.ErrNotFound
	}
	return best, nil
}

func (*apprStore) ReleaseApproved(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (*apprStore) EnvironmentApproved(context.Context, string, string, string) (bool, error) {
	return false, nil
}

// apprScripts answers every version of every script with one source.
type apprScripts struct{}

func (apprScripts) GetByID(context.Context, string) (*script.Script, error) {
	return nil, approval.ErrNotFound
}

func (apprScripts) GetByName(context.Context, string, string) (*script.Script, error) {
	return nil, approval.ErrNotFound
}

func (apprScripts) GetVersion(_ context.Context, _ string, v int) (*script.Version, error) {
	return &script.Version{Version: v, Source: policySource}, nil
}

// TestVersionSaved: a save of a script holding tool-call handlers approves an
// administrator's version with their roles, and files anyone else's for an
// administrator, the approved version running meanwhile; a script holding
// none records nothing.
func TestVersionSaved(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "limit", "jane@example.com", policySource)
	st := &apprStore{}
	appr := &approval.Service{Store: st, Scripts: apprScripts{}}
	svc := w.service()
	svc.Approvals, svc.Versions = appr, ApprovalVersions{Approvals: appr}

	svc.VersionSaved(ctx, sc, scriptsave.Caller{Email: admin.Email, Admin: true, Roles: []string{"admin"}})
	assert.Empty(t, st.rows, "a script holding no handler records nothing")

	w.calls.byID = map[string]callhandler.Handler{"p1": {ID: "p1", ScriptID: sc.ID, Kind: KindPolicy, Connection: "*"}}
	svc.VersionSaved(ctx, sc, scriptsave.Caller{Email: admin.Email, Admin: true, Roles: []string{"admin", "finance"}})
	running, approver, waiting := svc.Versions.State(ctx, sc)
	assert.Equal(t, 1, running)
	assert.Equal(t, admin.Email, approver)
	assert.Zero(t, waiting)
	run, ok := appr.HandlerRunAs(ctx, sc.ID)
	require.True(t, ok)
	assert.Equal(t, []string{"admin", "finance"}, run.Roles, "it runs with the roles of the administrator who saved it")

	sc.Version = 2
	svc.VersionSaved(ctx, sc, scriptsave.Caller{Email: jane.Email, Roles: []string{"analyst"}})
	running, _, waiting = svc.Versions.State(ctx, sc)
	assert.Equal(t, 1, running, "the approved version keeps running")
	assert.Equal(t, 2, waiting)

	require.NoError(t, svc.Versions.ApproveFor(ctx, Caller{Email: admin.Email, Admin: true, Roles: []string{"admin"}}, sc, 2))
	running, _, _ = svc.Versions.State(ctx, sc)
	assert.Equal(t, 2, running)

	sc.Version = 3
	w.calls.err = errors.New("down")
	svc.VersionSaved(ctx, sc, scriptsave.Caller{Email: jane.Email})
	_, _, waiting = svc.Versions.State(ctx, sc)
	assert.Zero(t, waiting, "handlers that cannot be read record nothing")
	var none *Service
	none.VersionSaved(ctx, sc, scriptsave.Caller{})
}
