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
	rows         map[string]*Request
	failGet      error
	failIns      error
	failList     error
	failDecide   error
	failApproved error
}

func newMem() *memStore { return &memStore{rows: map[string]*Request{}} }

func (m *memStore) Insert(_ context.Context, r *Request) error {
	if m.failIns != nil {
		return m.failIns
	}
	for _, x := range m.rows {
		if r.SubjectKind != SubjectCall && x.Status == StatusPending && x.ScriptID == r.ScriptID && x.Purpose == r.Purpose && x.Version == r.Version &&
			x.SiteReleaseID == r.SiteReleaseID && x.Target == r.Target && x.SiteID == r.SiteID {
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
	if m.failList != nil {
		return nil, m.failList
	}
	out := []Request{}
	for _, r := range m.rows {
		if (f.Status == "" || r.Status == f.Status) && (f.Requester == "" || r.RequesterEmail == f.Requester) &&
			(f.ScriptID == "" || r.ScriptID == f.ScriptID) && (f.SiteID == "" || r.SiteID == f.SiteID) &&
			(f.Purpose == "" || r.Purpose == f.Purpose) {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *memStore) Decide(_ context.Context, id string, d Decision) (*Request, error) {
	if m.failDecide != nil {
		return nil, m.failDecide
	}
	r, ok := m.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	if r.Status != StatusPending {
		return nil, ErrDecided
	}
	at := d.At
	r.Status, r.DecidedBy, r.DecisionComment, r.SelfApproved, r.DecidedAt = d.Status, d.By, d.Comment, d.SelfApproved, &at
	r.DecidedRoles, r.CallsApproved, r.Until = d.Roles, d.CallsApproved, d.Until
	c := *r
	return &c, nil
}

func (m *memStore) ReleaseApproved(_ context.Context, releaseID, target, digest string) (bool, error) {
	if m.failApproved != nil {
		return false, m.failApproved
	}
	for _, r := range m.rows {
		if r.SiteReleaseID == releaseID && r.Target == target && r.SourceSHA256 == digest && r.Status == StatusApproved {
			return true, nil
		}
	}
	return false, nil
}

func (m *memStore) EnvironmentApproved(_ context.Context, siteID, target, stamp string) (bool, error) {
	if m.failApproved != nil {
		return false, m.failApproved
	}
	for _, r := range m.rows {
		if r.SubjectKind == SubjectSiteEnvironment && r.SiteID == siteID && r.Target == target && r.SourceSHA256 == stamp && r.Status == StatusApproved {
			return true, nil
		}
	}
	return false, nil
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

var prodRelease = SubmitRelease{ReleaseID: "srel_1", Site: "marketing", Number: 3, Target: "prod", Digest: "digest-3", Reason: " launch "}

// TestSubmitRelease is a promotion into an environment that requires approval:
// the request names the release, its target and the digest it is fixed by, and
// each administrator is told once.
func TestSubmitRelease(t *testing.T) {
	svc, st, n := service()
	ctx := context.Background()

	r, err := svc.SubmitRelease(ctx, jane, prodRelease)
	require.NoError(t, err)
	assert.Equal(t, SubjectSiteRelease, r.SubjectKind)
	assert.Equal(t, PurposeSitePromote, r.Purpose)
	assert.Equal(t, 3, r.Version, "the release's number")
	assert.Equal(t, "digest-3", r.SourceSHA256, "fixed by the release's manifest digest")
	assert.Equal(t, "srel_1", r.SiteReleaseID)
	assert.Equal(t, "prod", r.Target)
	assert.Equal(t, "marketing", r.SiteName)
	assert.Empty(t, r.ScriptID, "a site release names no script")
	assert.Equal(t, jane.Email, r.RequesterEmail)
	assert.Equal(t, jane.Persona, r.RequesterPersona)
	assert.Equal(t, "launch", r.Reason, "the reason is trimmed")
	assert.Equal(t, StatusPending, r.Status)
	assert.Equal(t, [][]string{{"admin@example.com"}}, n.requested)
	assert.Len(t, st.rows, 1)
}

func TestSubmitRelease_OneAlreadyWaitingIsReturned(t *testing.T) {
	svc, st, n := service()
	ctx := context.Background()
	first, err := svc.SubmitRelease(ctx, jane, prodRelease)
	require.NoError(t, err)

	again, err := svc.SubmitRelease(ctx, bob, prodRelease)
	require.NoError(t, err, "a second promotion waits on the same request")
	assert.Equal(t, first.ID, again.ID)
	assert.Equal(t, jane.Email, again.RequesterEmail, "it stays the first requester's")
	assert.Len(t, st.rows, 1)
	assert.Len(t, n.requested, 1, "the administrators are told once")

	other := prodRelease
	other.Target = "stage"
	second, err := svc.SubmitRelease(ctx, jane, other)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, second.ID, "another target is another request")
}

func TestSubmitRelease_Refusals(t *testing.T) {
	ctx := context.Background()
	var refused *RefusedError

	t.Run("no email", func(t *testing.T) {
		svc, st, _ := service()
		_, err := svc.SubmitRelease(ctx, Actor{}, prodRelease)
		require.ErrorAs(t, err, &refused)
		assert.Empty(t, st.rows)
	})
	t.Run("a long reason", func(t *testing.T) {
		svc, _, _ := service()
		in := prodRelease
		in.Reason = strings.Repeat("x", MaxReason+1)
		_, err := svc.SubmitRelease(ctx, jane, in)
		require.ErrorAs(t, err, &refused)
	})
	t.Run("the store fails", func(t *testing.T) {
		svc, st, n := service()
		st.failIns = errors.New("down")
		_, err := svc.SubmitRelease(ctx, jane, prodRelease)
		require.ErrorContains(t, err, "filing the request")
		assert.Empty(t, n.requested)
	})
	t.Run("the waiting request cannot be read", func(t *testing.T) {
		svc, st, _ := service()
		_, err := svc.SubmitRelease(ctx, jane, prodRelease)
		require.NoError(t, err)
		st.failList = errors.New("down")
		_, err = svc.SubmitRelease(ctx, jane, prodRelease)
		require.ErrorContains(t, err, "reading the waiting requests")
	})
	t.Run("the insert says one waits and none is found", func(t *testing.T) {
		svc, st, _ := service()
		st.failIns = ErrPending
		_, err := svc.SubmitRelease(ctx, jane, prodRelease)
		require.ErrorIs(t, err, ErrPending)
	})
}

func TestSubmitRelease_NoNotifierConfigured(t *testing.T) {
	svc, _, _ := service()
	svc.Notifier = nil
	r, err := svc.SubmitRelease(context.Background(), jane, prodRelease)
	require.NoError(t, err)
	assert.NotEmpty(t, r.ID)
}

func TestReleaseApproved(t *testing.T) {
	svc, st, _ := service()
	ctx := context.Background()
	r, err := svc.SubmitRelease(ctx, jane, prodRelease)
	require.NoError(t, err)
	assert.False(t, svc.ReleaseApproved(ctx, "srel_1", "prod", "digest-3"), "waiting is not approved")

	_, err = svc.Decide(ctx, admin, r.ID, DecisionApprove, "")
	require.NoError(t, err)
	assert.True(t, svc.ReleaseApproved(ctx, "srel_1", "prod", "digest-3"))
	assert.False(t, svc.ReleaseApproved(ctx, "srel_1", "prod", "other-digest"), "an approval names the digest shown")
	assert.False(t, svc.ReleaseApproved(ctx, "srel_1", "stage", "digest-3"), "an approval names the target")
	assert.False(t, svc.ReleaseApproved(ctx, "srel_2", "prod", "digest-3"), "an approval names the release")

	st.failApproved = errors.New("down")
	assert.False(t, svc.ReleaseApproved(ctx, "srel_1", "prod", "digest-3"), "a failure to read promotes nothing")
}

func TestReleaseApproved_ADenialApprovesNothing(t *testing.T) {
	svc, _, _ := service()
	ctx := context.Background()
	r, err := svc.SubmitRelease(ctx, jane, prodRelease)
	require.NoError(t, err)
	_, err = svc.Decide(ctx, admin, r.ID, DecisionDeny, "not yet")
	require.NoError(t, err)
	assert.False(t, svc.ReleaseApproved(ctx, "srel_1", "prod", "digest-3"))
}

func TestRecordReleaseSelfApproval(t *testing.T) {
	ctx := context.Background()

	t.Run("not an administrator", func(t *testing.T) {
		svc, st, _ := service()
		require.ErrorIs(t, svc.RecordReleaseSelfApproval(ctx, jane, prodRelease), ErrNotAllowed)
		assert.Empty(t, st.rows)
	})
	t.Run("files and approves, and says it was self-approved", func(t *testing.T) {
		svc, st, n := service()
		require.NoError(t, svc.RecordReleaseSelfApproval(ctx, admin, prodRelease))
		require.Len(t, st.rows, 1)
		for _, r := range st.rows {
			assert.Equal(t, StatusApproved, r.Status)
			assert.True(t, r.SelfApproved)
			assert.Equal(t, admin.Email, r.DecidedBy)
			assert.Equal(t, admin.Email, r.RequesterEmail)
			assert.Equal(t, "promoted by an administrator", r.Reason)
			require.NotNil(t, r.DecidedAt)
		}
		assert.True(t, svc.ReleaseApproved(ctx, "srel_1", "prod", "digest-3"))
		assert.Empty(t, n.requested, "no one is asked for a decision already made")
	})
	t.Run("a request already waiting is the one decided", func(t *testing.T) {
		svc, st, _ := service()
		waiting, err := svc.SubmitRelease(ctx, jane, prodRelease)
		require.NoError(t, err)
		require.NoError(t, svc.RecordReleaseSelfApproval(ctx, admin, prodRelease))
		assert.Len(t, st.rows, 1, "no second request")
		got, err := st.Get(ctx, waiting.ID)
		require.NoError(t, err)
		assert.Equal(t, StatusApproved, got.Status)
		assert.True(t, got.SelfApproved)
		assert.Equal(t, jane.Email, got.RequesterEmail, "the person who asked is kept")
	})
	t.Run("the store fails", func(t *testing.T) {
		svc, st, _ := service()
		st.failIns = errors.New("down")
		require.ErrorContains(t, svc.RecordReleaseSelfApproval(ctx, admin, prodRelease), "recording the approval")
	})
	t.Run("the waiting request cannot be read", func(t *testing.T) {
		svc, st, _ := service()
		_, err := svc.SubmitRelease(ctx, jane, prodRelease)
		require.NoError(t, err)
		st.failList = errors.New("down")
		require.ErrorContains(t, svc.RecordReleaseSelfApproval(ctx, admin, prodRelease), "reading the waiting requests")
	})
	t.Run("the decision fails", func(t *testing.T) {
		svc, st, _ := service()
		boom := errors.New("down")
		st.failDecide = boom
		require.ErrorIs(t, svc.RecordReleaseSelfApproval(ctx, admin, prodRelease), boom)
		assert.False(t, svc.ReleaseApproved(ctx, "srel_1", "prod", "digest-3"))
	})
}

// countingScripts is a Scripts that counts every read, so a test can say none
// happened.
type countingScripts struct {
	*fakeScripts
	reads int
}

func (c *countingScripts) GetVersion(ctx context.Context, id string, v int) (*script.Version, error) {
	c.reads++
	return c.fakeScripts.GetVersion(ctx, id, v)
}

// TestDecide_ASiteReleaseReadsNoScript: a release is immutable and promoting
// compares the digest, so approving one does not look a script version up.
func TestDecide_ASiteReleaseReadsNoScript(t *testing.T) {
	svc, _, n := service()
	counting := &countingScripts{fakeScripts: scripts()}
	svc.Scripts = counting
	ctx := context.Background()

	r, err := svc.SubmitRelease(ctx, jane, prodRelease)
	require.NoError(t, err)
	got, err := svc.Decide(ctx, admin, r.ID, DecisionApprove, "ship it")
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, got.Status)
	assert.Equal(t, "ship it", got.DecisionComment)
	assert.False(t, got.SelfApproved)
	assert.Zero(t, counting.reads, "a site release has no script to read")
	assert.Equal(t, []string{StatusApproved}, n.decided, "the requester is told")

	byAdmin, err := svc.SubmitRelease(ctx, admin, SubmitRelease{ReleaseID: "srel_2", Site: "marketing", Number: 4, Target: "prod", Digest: "d4"})
	require.NoError(t, err)
	mine, err := svc.Decide(ctx, admin, byAdmin.ID, DecisionApprove, "")
	require.NoError(t, err)
	assert.True(t, mine.SelfApproved, "an administrator deciding their own request is recorded as such")
	assert.Zero(t, counting.reads)

	_, err = svc.Decide(ctx, jane, r.ID, DecisionApprove, "")
	require.ErrorIs(t, err, ErrNotAllowed)
}

var prodPublic = SubmitEnvironment{SiteID: "site_1", Site: "docs", Target: "prod", Stamp: "public:t0", Reason: "  ready  "}

// Making an environment public is a request about the environment, fixed by
// the access stamp it was asked against, told to the approvers (#2604).
func TestSubmitEnvironment(t *testing.T) {
	svc, st, n := service()
	ctx := context.Background()

	r, err := svc.SubmitEnvironment(ctx, jane, prodPublic)
	require.NoError(t, err)
	assert.Equal(t, SubjectSiteEnvironment, r.SubjectKind)
	assert.Equal(t, PurposeSitePublic, r.Purpose)
	assert.Equal(t, "public:t0", r.SourceSHA256)
	assert.Equal(t, "site_1", r.SiteID)
	assert.Equal(t, "docs", r.SiteName)
	assert.Equal(t, "prod", r.Target)
	assert.Empty(t, r.SiteReleaseID, "it names no release")
	assert.Equal(t, "ready", r.Reason)
	assert.Equal(t, StatusPending, r.Status)
	assert.Equal(t, [][]string{{"admin@example.com"}}, n.requested)

	again, err := svc.SubmitEnvironment(ctx, bob, prodPublic)
	require.NoError(t, err, "a second ask waits on the same request")
	assert.Equal(t, r.ID, again.ID)
	assert.Len(t, st.rows, 1)
	assert.Len(t, n.requested, 1, "the administrators are told once")

	other := prodPublic
	other.Target = "stage"
	second, err := svc.SubmitEnvironment(ctx, jane, other)
	require.NoError(t, err)
	assert.NotEqual(t, r.ID, second.ID, "another environment is another request")

	assert.False(t, svc.EnvironmentApproved(ctx, "site_1", "prod", "public:t0"), "pending is not approved")
	_, err = svc.Decide(ctx, admin, r.ID, DecisionApprove, "")
	require.NoError(t, err, "an environment request is approved without reading a script")
	assert.True(t, svc.EnvironmentApproved(ctx, "site_1", "prod", "public:t0"))
	assert.False(t, svc.EnvironmentApproved(ctx, "site_1", "prod", "public:t1"), "an approval holds for the access it was asked against")
	assert.False(t, svc.EnvironmentApproved(ctx, "site_2", "prod", "public:t0"), "and for that site")
}

func TestSubmitEnvironment_Refusals(t *testing.T) {
	ctx := context.Background()
	var refused *RefusedError
	t.Run("no email", func(t *testing.T) {
		svc, st, _ := service()
		_, err := svc.SubmitEnvironment(ctx, Actor{}, prodPublic)
		require.ErrorAs(t, err, &refused)
		assert.Empty(t, st.rows)
	})
	t.Run("a long reason", func(t *testing.T) {
		svc, _, _ := service()
		in := prodPublic
		in.Reason = strings.Repeat("x", MaxReason+1)
		_, err := svc.SubmitEnvironment(ctx, jane, in)
		require.ErrorAs(t, err, &refused)
	})
	t.Run("the store fails", func(t *testing.T) {
		svc, st, n := service()
		st.failIns = errors.New("down")
		_, err := svc.SubmitEnvironment(ctx, jane, prodPublic)
		require.ErrorContains(t, err, "filing the request")
		assert.Empty(t, n.requested)
	})
	t.Run("the waiting request cannot be read", func(t *testing.T) {
		svc, st, _ := service()
		_, err := svc.SubmitEnvironment(ctx, jane, prodPublic)
		require.NoError(t, err)
		st.failList = errors.New("down")
		_, err = svc.SubmitEnvironment(ctx, bob, prodPublic)
		require.ErrorContains(t, err, "reading the waiting requests")
	})
	t.Run("the waiting request is gone", func(t *testing.T) {
		svc, st, _ := service()
		st.failIns = ErrPending
		_, err := svc.SubmitEnvironment(ctx, bob, prodPublic)
		require.ErrorIs(t, err, ErrPending, "a conflict with no request to show is said as one")
	})
	t.Run("an approval that cannot be read approves nothing", func(t *testing.T) {
		svc, st, _ := service()
		st.failApproved = errors.New("down")
		assert.False(t, svc.EnvironmentApproved(ctx, "site_1", "prod", "public:t0"))
	})
}
