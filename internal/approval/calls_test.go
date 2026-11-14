package approval

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

var (
	carol   = Actor{Email: "carol@example.com", Persona: "finance"}
	finance = Actor{Email: "dana@example.com", Persona: "finance-leads"}
)

func heldCall(approvers ...string) CallRequest {
	return CallRequest{
		Tool: "api_invoke_endpoint", Connection: "billing-api", Operation: "addSubscribers",
		Arguments: map[string]any{"connection": "billing-api"}, Rendering: "PUT /lists/7/subscribers (3 items)",
		Handlers: []HandlerAsk{{ID: "h1", Script: "bulk-guard"}}, Approvers: approvers, Calls: 2, Reason: "the quarterly import",
	}
}

// TestSubmitCall files a held call, told to each person its policies named
// once (never the requester), and to the administrators when none was named.
func TestSubmitCall(t *testing.T) {
	svc, _, n := service()
	r, err := svc.SubmitCall(context.Background(), jane, heldCall("Carol@example.com", "carol@example.com", "jane@example.com", "finance-leads"))
	require.NoError(t, err)
	assert.Equal(t, SubjectCall, r.SubjectKind)
	assert.Equal(t, PurposeCallAllowance, r.Purpose)
	assert.Equal(t, 2, r.CallsAsked)
	assert.Equal(t, []string{"carol@example.com"}, n.requested[0], "each person once, never the requester; a persona is not mailed")

	_, err = svc.SubmitCall(context.Background(), jane, heldCall())
	require.NoError(t, err)
	assert.Equal(t, []string{"admin@example.com"}, n.requested[1], "no person named: the administrators")

	one := heldCall()
	one.Calls = 0
	r, err = svc.SubmitCall(context.Background(), jane, one)
	require.NoError(t, err)
	assert.Equal(t, 1, r.CallsAsked, "no count asks for one call")

	for name, in := range map[string]CallRequest{
		"no tool":           {Connection: "billing-api"},
		"too many calls":    {Tool: "t", Connection: "c", Calls: MaxCalls + 1},
		"a reason too long": {Tool: "t", Connection: "c", Reason: string(make([]byte, MaxReason+1))},
	} {
		_, err := svc.SubmitCall(context.Background(), jane, in)
		var refusal *RefusedError
		require.ErrorAs(t, err, &refusal, name)
	}
	_, err = svc.SubmitCall(context.Background(), Actor{}, heldCall())
	require.Error(t, err, "a request belongs to a person")

	st := newMem()
	st.failIns = errors.New("down")
	svc.Store = st
	_, err = svc.SubmitCall(context.Background(), jane, heldCall())
	require.Error(t, err)
}

// TestDecideCall: a person or persona a policy named, or an administrator,
// decides; the requester, and someone not named, never do; an approval
// grants at most the calls asked, until a deadline within bounds.
func TestDecideCall(t *testing.T) {
	ctx := context.Background()
	svc, _, n := service()
	r, err := svc.SubmitCall(ctx, jane, heldCall("carol@example.com", "finance-leads"))
	require.NoError(t, err)

	_, err = svc.DecideAllowance(ctx, jane, r.ID, Ruling{Decision: DecisionApprove, Comment: "", Allowance: Allowance{}})
	require.ErrorIs(t, err, ErrNotAllowed, "the requester never decides their own")
	_, err = svc.DecideAllowance(ctx, bob, r.ID, Ruling{Decision: DecisionApprove, Comment: "", Allowance: Allowance{}})
	require.ErrorIs(t, err, ErrNotFound, "someone not named cannot read it, so cannot decide it")
	_, err = svc.Get(ctx, bob, r.ID)
	require.ErrorIs(t, err, ErrNotFound)
	got, err := svc.Get(ctx, finance, r.ID)
	require.NoError(t, err, "a persona the policy named reads it")
	assert.Equal(t, r.ID, got.ID)

	_, err = svc.DecideAllowance(ctx, carol, r.ID, Ruling{Decision: DecisionApprove, Comment: "", Allowance: Allowance{Calls: 3}})
	var refusal *RefusedError
	require.ErrorAs(t, err, &refusal, "more calls than were asked")
	past := svc.now().Add(-time.Minute)
	_, err = svc.DecideAllowance(ctx, carol, r.ID, Ruling{Decision: DecisionApprove, Comment: "", Allowance: Allowance{Until: &past}})
	require.ErrorAs(t, err, &refusal, "a deadline in the past")
	far := svc.now().Add(MaxAllowance + time.Hour)
	_, err = svc.DecideAllowance(ctx, carol, r.ID, Ruling{Decision: DecisionApprove, Comment: "", Allowance: Allowance{Until: &far}})
	require.ErrorAs(t, err, &refusal, "a deadline too far")

	d, err := svc.DecideAllowance(ctx, carol, r.ID, Ruling{Decision: DecisionApprove, Comment: "ok", Allowance: Allowance{}})
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, d.Status)
	assert.Equal(t, 2, d.CallsApproved, "no count grants what was asked")
	require.NotNil(t, d.Until)
	assert.Equal(t, svc.now().Add(DefaultAllowance), *d.Until)
	assert.Equal(t, []string{StatusApproved}, n.decided, "the requester is told")

	r2, err := svc.SubmitCall(ctx, jane, heldCall("carol@example.com"))
	require.NoError(t, err)
	until := svc.now().Add(2 * time.Hour)
	d, err = svc.DecideAllowance(ctx, admin, r2.ID, Ruling{Decision: DecisionApprove, Comment: "", Allowance: Allowance{Calls: 1, Until: &until}})
	require.NoError(t, err, "an administrator decides any call request")
	assert.Equal(t, 1, d.CallsApproved)
	assert.Equal(t, until, *d.Until)

	r3, err := svc.SubmitCall(ctx, jane, heldCall("carol@example.com"))
	require.NoError(t, err)
	_, err = svc.DecideAllowance(ctx, finance, r3.ID, Ruling{Decision: DecisionDeny, Comment: "", Allowance: Allowance{}})
	require.ErrorIs(t, err, ErrNotFound, "a persona the policy did not name")
	d, err = svc.DecideAllowance(ctx, carol, r3.ID, Ruling{Decision: DecisionDeny, Comment: "no", Allowance: Allowance{}})
	require.NoError(t, err)
	assert.Equal(t, StatusDenied, d.Status)
	assert.Zero(t, d.CallsApproved, "a denial grants nothing")

	own, err := svc.SubmitCall(ctx, carol, heldCall("carol@example.com"))
	require.NoError(t, err)
	_, err = svc.DecideAllowance(ctx, carol, own.ID, Ruling{Decision: DecisionApprove, Comment: "", Allowance: Allowance{}})
	require.ErrorIs(t, err, ErrNotAllowed, "named, but it is their own call")
}

type spends struct {
	id  string
	ok  bool
	err error
}

func (s spends) Spend(context.Context, string, string, string) (id string, ok bool, err error) {
	return s.id, s.ok, s.err
}

// TestSpendAllowance reads an approved allowance's call, and none when the
// store has none or fails.
func TestSpendAllowance(t *testing.T) {
	ctx := context.Background()
	var none *Service
	_, ok := none.SpendAllowance(ctx, "jane@example.com", "c", "o")
	assert.False(t, ok)
	svc := &Service{Allowances: spends{id: "r1", ok: true}}
	id, ok := svc.SpendAllowance(ctx, "jane@example.com", "c", "o")
	assert.True(t, ok)
	assert.Equal(t, "r1", id)
	_, ok = svc.SpendAllowance(ctx, "", "c", "o")
	assert.False(t, ok, "a caller with no address holds no allowance")
	svc.Allowances = spends{err: errors.New("down")}
	_, ok = svc.SpendAllowance(ctx, "jane@example.com", "c", "o")
	assert.False(t, ok, "a failed read spends nothing; the policy decides again")
}

// TestPostgresStore_Spend takes one call from the oldest usable allowance.
func TestPostgresStore_Spend(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	st := NewPostgresStore(db)
	q := regexp.QuoteMeta("UPDATE approval_requests SET calls_used = calls_used + 1")
	mock.ExpectQuery(q).WithArgs("jane@example.com", "billing-api", "addSubscribers").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("r1"))
	id, ok, err := st.Spend(context.Background(), "jane@example.com", "billing-api", "addSubscribers")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "r1", id)
	mock.ExpectQuery(q).WillReturnError(sql.ErrNoRows)
	_, ok, err = st.Spend(context.Background(), "jane@example.com", "billing-api", "addSubscribers")
	require.NoError(t, err)
	assert.False(t, ok)
	mock.ExpectQuery(q).WillReturnError(errors.New("down"))
	_, _, err = st.Spend(context.Background(), "jane@example.com", "billing-api", "addSubscribers")
	require.Error(t, err)
}

// TestHandlerVersions: an administrator's save approves the version with
// their roles; anyone else's files one request an administrator decides,
// and the approved version runs meanwhile.
func TestHandlerVersions(t *testing.T) {
	ctx := context.Background()
	svc, st, n := service()
	sc := &script.Script{ID: "s1", Name: "intake", Version: 2}
	var none *Service
	_, ok := none.HandlerRunAs(ctx, "s1")
	assert.False(t, ok)
	assert.Zero(t, none.PendingHandlerVersion(ctx, "s1"))
	require.NoError(t, none.HandlerVersionSaved(ctx, jane, sc, 2))

	_, ok = svc.HandlerRunAs(ctx, "s1")
	assert.False(t, ok, "nothing approved runs nothing")
	require.ErrorIs(t, svc.ApproveForHandlers(ctx, jane, sc, 1), ErrNotAllowed)

	boss := Actor{Email: "admin@example.com", Admin: true, Roles: []string{"admin", "finance"}}
	require.NoError(t, svc.HandlerVersionSaved(ctx, boss, sc, 1))
	run, ok := svc.HandlerRunAs(ctx, "s1")
	require.True(t, ok)
	assert.Equal(t, RunAs{Version: 1, Approver: "admin@example.com", Roles: []string{"admin", "finance"}, SelfApproved: true}, run)
	require.NoError(t, svc.ApproveForHandlers(ctx, boss, sc, 1), "a version already approved records nothing")

	require.NoError(t, svc.HandlerVersionSaved(ctx, jane, sc, 2))
	require.NoError(t, svc.HandlerVersionSaved(ctx, jane, sc, 2), "a second save of the same version names the waiting request")
	assert.Equal(t, 2, svc.PendingHandlerVersion(ctx, "s1"))
	assert.Len(t, n.requested, 2)
	run, _ = svc.HandlerRunAs(ctx, "s1")
	assert.Equal(t, 1, run.Version, "the approved version keeps running")

	require.NoError(t, svc.ApproveForHandlers(ctx, boss, sc, 0), "0 is the latest version")
	run, _ = svc.HandlerRunAs(ctx, "s1")
	assert.Equal(t, 2, run.Version)

	err := svc.HandlerVersionSaved(ctx, jane, sc, 9)
	var refusal *RefusedError
	require.ErrorAs(t, err, &refusal, "a version that does not exist")

	st.failList = errors.New("down")
	assert.Zero(t, svc.PendingHandlerVersion(ctx, "s1"))
	st.failList = nil
	st.failIns = errors.New("down")
	require.Error(t, svc.HandlerVersionSaved(ctx, jane, &script.Script{ID: "s1", Name: "intake", Version: 3}, 3))
}

// TestHandlerRunAs_AFailedReadRunsNothing: a version that cannot be read
// runs nothing.
func TestHandlerRunAs_AFailedReadRunsNothing(t *testing.T) {
	svc := &Service{Store: failingLatest{memStore: newMem()}}
	_, ok := svc.HandlerRunAs(context.Background(), "s1")
	assert.False(t, ok)
}

type failingLatest struct{ *memStore }

func (failingLatest) LatestApproved(context.Context, string, string) (*Request, error) {
	return nil, errors.New("down")
}

// TestKinds lists each request under its kind.
func TestKinds(t *testing.T) {
	assert.Equal(t, KindCall, kindOf(SubjectCall, PurposeCallAllowance))
	assert.Equal(t, KindHandlerVersion, kindOf(SubjectScriptVersion, PurposeCallHandler))
	assert.Equal(t, KindForm, kindOf(SubjectScriptVersion, PurposeFormPublic))
	assert.Equal(t, KindSite, kindOf(SubjectSiteRelease, ""))
}

// TestPostgresStore_PendingRollup reads the approvals queue for its alert.
func TestPostgresStore_PendingRollup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	st := NewPostgresStore(db)
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("FROM approval_requests WHERE status = 'pending'")).
		WillReturnRows(sqlmock.NewRows([]string{"n", "s", "m"}).AddRow(3, 1, at))
	q, err := st.PendingRollup(context.Background(), at)
	require.NoError(t, err)
	assert.Equal(t, []any{3, 1, at}, []any{q.Pending, q.Stale, *q.Oldest})
	mock.ExpectQuery("FROM approval_requests").WillReturnRows(sqlmock.NewRows([]string{"n", "s", "m"}).AddRow(0, 0, nil))
	q, err = st.PendingRollup(context.Background(), at)
	require.NoError(t, err)
	assert.Nil(t, q.Oldest)
	mock.ExpectQuery("FROM approval_requests").WillReturnError(errors.New("down"))
	_, err = st.PendingRollup(context.Background(), at)
	require.Error(t, err)
}
