package approval

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// memStore is a Store in memory.
type memStore struct {
	rows    map[string]*Request
	failGet error
	failIns error
}

func newMem() *memStore { return &memStore{rows: map[string]*Request{}} }

func (m *memStore) Insert(_ context.Context, r *Request) error {
	if m.failIns != nil {
		return m.failIns
	}
	for _, x := range m.rows {
		if x.Status == StatusPending && x.ScriptID == r.ScriptID && x.Purpose == r.Purpose && x.Version == r.Version {
			return ErrPending
		}
	}
	r.Status, r.CreatedAt = StatusPending, time.Now()
	c := *r
	m.rows[r.ID] = &c
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*Request, error) {
	if m.failGet != nil {
		return nil, m.failGet
	}
	r, ok := m.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *r
	return &c, nil
}

func (m *memStore) List(_ context.Context, f Filter) ([]Request, error) {
	out := []Request{}
	for _, r := range m.rows {
		if (f.Status == "" || r.Status == f.Status) && (f.Requester == "" || r.RequesterEmail == f.Requester) &&
			(f.ScriptID == "" || r.ScriptID == f.ScriptID) {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *memStore) Decide(_ context.Context, id string, d Decision) (*Request, error) {
	r, ok := m.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	if r.Status != StatusPending {
		return nil, ErrDecided
	}
	at := d.At
	r.Status, r.DecidedBy, r.DecisionComment, r.SelfApproved, r.DecidedAt = d.Status, d.By, d.Comment, d.SelfApproved, &at
	c := *r
	return &c, nil
}

func (m *memStore) LatestApproved(_ context.Context, scriptID, purpose string) (*Request, error) {
	var best *Request
	for _, r := range m.rows {
		if r.ScriptID == scriptID && r.Purpose == purpose && r.Status == StatusApproved && (best == nil || r.Version > best.Version) {
			best = r
		}
	}
	if best == nil {
		return nil, ErrNotFound
	}
	return best, nil
}

// fakeScripts holds one form script with three versions.
type fakeScripts struct{ versions map[int]string }

const (
	formV1 = "def form_spec(req):\n    return {}\n\ndef form_submit(req, values):\n    return None\n"
	formV2 = formV1 + "# a second version\n"
)

func scripts() *fakeScripts {
	return &fakeScripts{versions: map[int]string{1: formV1, 2: formV2, 3: "def main():\n    pass\n"}}
}

func (*fakeScripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	if id != "s1" {
		return nil, errors.New("no such script")
	}
	return &script.Script{ID: "s1", Name: "intake", OwnerEmail: "jane@example.com", Version: 2}, nil
}

func (f *fakeScripts) GetByName(_ context.Context, owner, name string) (*script.Script, error) {
	if owner == "jane@example.com" && name == "intake" {
		return f.GetByID(context.Background(), "s1")
	}
	return nil, errors.New("no such script")
}

func (f *fakeScripts) GetVersion(_ context.Context, _ string, v int) (*script.Version, error) {
	src, ok := f.versions[v]
	if !ok {
		return nil, errors.New("no such version")
	}
	return &script.Version{Version: v, Source: src}, nil
}

type notices struct {
	requested [][]string
	decided   []string
}

func (n *notices) Requested(_ context.Context, _ *Request, approvers []string) {
	n.requested = append(n.requested, approvers)
}
func (n *notices) Decided(_ context.Context, r *Request) { n.decided = append(n.decided, r.Status) }

var (
	jane  = Actor{Email: "jane@example.com", Persona: "analyst"}
	bob   = Actor{Email: "bob@example.com"}
	admin = Actor{Email: "admin@example.com", Admin: true}
)

func service() (*Service, *memStore, *notices) {
	st, n := newMem(), &notices{}
	return &Service{
		Store: st, Scripts: scripts(), IsForm: func(src string) bool { return strings.Contains(src, "def form_spec") },
		Approvers: func(context.Context) []string { return []string{"admin@example.com"} }, Notifier: n,
		Now: func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) },
	}, st, n
}

// TestSubmitAndDecide is #2494's approval loop: the owner asks for a version,
// every administrator is told once, a non-administrator deciding it (the
// requester included) is refused, an administrator approves it, and the
// approved version is what Approved reads.
func TestSubmitAndDecide(t *testing.T) {
	svc, _, n := service()
	ctx := context.Background()

	r, err := svc.Submit(ctx, jane, Submit{Script: "intake", Version: 1, Reason: "launch"})
	require.NoError(t, err)
	assert.Equal(t, SubjectScriptVersion, r.SubjectKind)
	assert.Equal(t, PurposeFormPublic, r.Purpose)
	assert.Equal(t, SourceHash(formV1), r.SourceSHA256)
	assert.Equal(t, "analyst", r.RequesterPersona)
	assert.Zero(t, r.Changes.ApprovedVersion, "a first request has nothing approved before it")
	assert.Equal(t, [][]string{{"admin@example.com"}}, n.requested, "each administrator is told once")

	_, err = svc.Submit(ctx, jane, Submit{Script: "s1", Version: 1})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused, "one request waits per version")

	for _, who := range []Actor{jane, bob} {
		_, err = svc.Decide(ctx, who, r.ID, DecisionApprove, "")
		require.ErrorIs(t, err, ErrNotAllowed, who.Email)
	}
	assert.Zero(t, svc.Approved(ctx, "s1", PurposeFormPublic))

	got, err := svc.Decide(ctx, admin, r.ID, DecisionApprove, "fine")
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, got.Status)
	assert.False(t, got.SelfApproved)
	assert.Equal(t, []string{StatusApproved}, n.decided, "the requester is told")
	assert.Equal(t, 1, svc.Approved(ctx, "s1", PurposeFormPublic))

	_, err = svc.Decide(ctx, admin, r.ID, DecisionDeny, "")
	require.ErrorIs(t, err, ErrDecided)

	// The next request names what changed since the approved version.
	r2, err := svc.Submit(ctx, jane, Submit{Script: "intake"})
	require.NoError(t, err)
	assert.Equal(t, 2, r2.Version, "the latest version when none is named")
	assert.Equal(t, 1, r2.Changes.ApprovedVersion)
	assert.Equal(t, 1, r2.Changes.Added)
	assert.Contains(t, r2.Changes.Diff, "+# a second version")
	denied, err := svc.Decide(ctx, admin, r2.ID, DecisionDeny, "not yet")
	require.NoError(t, err)
	assert.Equal(t, StatusDenied, denied.Status)
	assert.Equal(t, 1, svc.Approved(ctx, "s1", PurposeFormPublic), "a denial approves nothing")
}

func TestSubmitRefusals(t *testing.T) {
	svc, st, _ := service()
	ctx := context.Background()
	var refused *RefusedError
	cases := []struct {
		name  string
		actor Actor
		in    Submit
	}{
		{"no email", Actor{}, Submit{Script: "s1"}},
		{"unknown purpose", jane, Submit{Script: "s1", Purpose: "other"}},
		{"long reason", jane, Submit{Script: "s1", Reason: strings.Repeat("x", MaxReason+1)}},
		{"no script named", jane, Submit{}},
		{"no such script", jane, Submit{Script: "nope"}},
		{"no such version", jane, Submit{Script: "s1", Version: 9}},
		{"not a form", jane, Submit{Script: "s1", Version: 3}},
	}
	for _, tc := range cases {
		_, err := svc.Submit(ctx, tc.actor, tc.in)
		require.ErrorAs(t, err, &refused, tc.name)
	}
	_, err := svc.Submit(ctx, bob, Submit{Script: "s1"})
	require.ErrorIs(t, err, ErrNotAllowed, "only the owner or an administrator asks")
	_, err = svc.Submit(ctx, admin, Submit{Script: "s1", Version: 1})
	require.NoError(t, err, "an administrator asks for anyone's script")

	st.failIns = errors.New("down")
	_, err = svc.Submit(ctx, jane, Submit{Script: "s1", Version: 2})
	require.ErrorContains(t, err, "down")
}

func TestDecideRefusals(t *testing.T) {
	svc, st, n := service()
	ctx := context.Background()
	r, err := svc.Submit(ctx, admin, Submit{Script: "s1", Version: 1})
	require.NoError(t, err)
	var refused *RefusedError
	_, err = svc.Decide(ctx, admin, r.ID, "maybe", "")
	require.ErrorAs(t, err, &refused)
	_, err = svc.Decide(ctx, admin, r.ID, DecisionApprove, strings.Repeat("x", MaxReason+1))
	require.ErrorAs(t, err, &refused)
	_, err = svc.Decide(ctx, admin, "apr_none", DecisionApprove, "")
	require.ErrorIs(t, err, ErrNotFound)

	// A version whose source changed under the request is not approved.
	fs, ok := svc.Scripts.(*fakeScripts)
	require.True(t, ok)
	fs.versions[1] = "changed"
	_, err = svc.Decide(ctx, admin, r.ID, DecisionApprove, "")
	require.ErrorAs(t, err, &refused)
	delete(fs.versions, 1)
	_, err = svc.Decide(ctx, admin, r.ID, DecisionApprove, "")
	require.ErrorAs(t, err, &refused)
	fs.versions[1] = formV1

	// An administrator's own request is self-approved and sends no notice.
	got, err := svc.Decide(ctx, admin, r.ID, DecisionApprove, "")
	require.NoError(t, err)
	assert.True(t, got.SelfApproved)
	assert.Empty(t, n.decided)

	st.failGet = errors.New("down")
	assert.Zero(t, (&Service{}).Approved(ctx, "s1", PurposeFormPublic))
}

func TestListAndGetAreScopedToTheRequester(t *testing.T) {
	svc, _, _ := service()
	ctx := context.Background()
	mine, err := svc.Submit(ctx, jane, Submit{Script: "s1", Version: 1})
	require.NoError(t, err)
	_, err = svc.Submit(ctx, admin, Submit{Script: "s1", Version: 2})
	require.NoError(t, err)

	got, err := svc.List(ctx, jane, Filter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, mine.ID, got[0].ID)
	all, err := svc.List(ctx, admin, Filter{})
	require.NoError(t, err)
	assert.Len(t, all, 2)
	_, err = svc.List(ctx, Actor{}, Filter{})
	require.Error(t, err)

	_, err = svc.Get(ctx, bob, mine.ID)
	require.ErrorIs(t, err, ErrNotFound, "another person's request reads as none")
	r, err := svc.Get(ctx, jane, mine.ID)
	require.NoError(t, err)
	assert.Equal(t, mine.ID, r.ID)
	_, err = svc.Get(ctx, admin, "apr_none")
	require.ErrorIs(t, err, ErrNotFound)
}

// TestRecordSelfApproval is an administrator binding a form public: the
// version served is recorded as self-approved, a pending request for it is
// the one decided, and a version already approved records nothing.
func TestRecordSelfApproval(t *testing.T) {
	svc, st, _ := service()
	ctx := context.Background()
	sc, err := svc.Scripts.GetByID(ctx, "s1")
	require.NoError(t, err)
	require.ErrorIs(t, svc.RecordSelfApproval(ctx, jane, sc, 1), ErrNotAllowed)
	var refused *RefusedError
	require.ErrorAs(t, svc.RecordSelfApproval(ctx, admin, sc, 9), &refused)

	pending, err := svc.Submit(ctx, jane, Submit{Script: "s1", Version: 2})
	require.NoError(t, err)
	require.NoError(t, svc.RecordSelfApproval(ctx, admin, sc, 2))
	got, err := st.Get(ctx, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, got.Status, "the waiting request is the one decided")
	assert.True(t, got.SelfApproved)
	assert.Equal(t, 2, svc.Approved(ctx, "s1", PurposeFormPublic))

	before := len(st.rows)
	require.NoError(t, svc.RecordSelfApproval(ctx, admin, sc, 1), "an older version is already covered")
	assert.Len(t, st.rows, before)
}

func TestChangesSinceAnApprovedVersionThatIsGone(t *testing.T) {
	svc, st, _ := service()
	ctx := context.Background()
	st.rows["apr_old"] = &Request{ID: "apr_old", ScriptID: "s1", Purpose: PurposeFormPublic, Version: 7, Status: StatusApproved}
	c, err := svc.changesSince(ctx, "s1", PurposeFormPublic, &script.Version{Version: 2, Source: formV2})
	require.NoError(t, err)
	assert.Equal(t, Changes{ApprovedVersion: 7}, c, "a deleted version is named with no diff")
}

func TestNewIDAndHash(t *testing.T) {
	assert.True(t, strings.HasPrefix(NewID(), "apr_"))
	assert.NotEqual(t, NewID(), NewID())
	assert.Len(t, SourceHash("x"), 64)
	assert.Equal(t, "x", (&RefusedError{Msg: "x"}).Error())
}
