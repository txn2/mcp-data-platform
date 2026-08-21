package mentionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
	userdir "github.com/txn2/mcp-data-platform/pkg/user"
)

// fakeAudience serves a fixed audience and records what it was asked.
type fakeAudience struct {
	people  []mention.Person
	members []string
	// eligibleByTarget answers per target id, for the cases where one caller
	// belongs to one target's audience and not another's.
	eligibleByTarget map[string][]string
	listErr          error
	elErr            error
	gotList          mention.ListOptions
	gotTarget        mention.Target
}

func (f *fakeAudience) List(_ context.Context, t mention.Target, opts mention.ListOptions) ([]mention.Person, error) {
	f.gotTarget, f.gotList = t, opts
	return f.people, f.listErr
}

func (f *fakeAudience) Eligible(_ context.Context, t mention.Target, emails []string) ([]string, error) {
	if f.elErr != nil {
		return nil, f.elErr
	}
	members := f.members
	if f.eligibleByTarget != nil {
		members = f.eligibleByTarget[t.ID]
	}
	var out []string
	for _, e := range emails {
		for _, m := range members {
			if strings.EqualFold(e, m) {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// fakeDirectory is an in-memory known-users store.
type fakeDirectory struct {
	users []userdir.User
	err   error
	// gotFilter records what the store was asked for, so a narrowing is
	// asserted on the query rather than on the fake's answer.
	gotFilter userdir.Filter
}

func (f *fakeDirectory) List(_ context.Context, filter userdir.Filter) ([]userdir.User, int, error) {
	f.gotFilter = filter
	return f.users, len(f.users), f.err
}

// fakeThreads answers the mentions worklist.
type fakeThreads struct {
	threads.ThreadStore
	rows      []threads.ThreadWithMeta
	total     int
	err       error
	gotFilter threads.ThreadFilter
}

func (f *fakeThreads) ListThreads(_ context.Context, filter threads.ThreadFilter) ([]threads.ThreadWithMeta, int, error) {
	f.gotFilter = filter
	return f.rows, f.total, f.err
}

// serve builds the handler over deps and runs one request through the
// registered routes, so every test exercises the real mux wiring.
func serve(t *testing.T, deps Deps, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(deps).Register(mux, func(h http.Handler) http.Handler { return h })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, target, http.NoBody))
	return rec
}

func callerIs(id *Identity) func(*http.Request) *Identity {
	return func(*http.Request) *Identity { return id }
}

var member = &Identity{UserID: "u1", Email: "me@example.com"}

func TestListMentionCandidates(t *testing.T) {
	audience := &fakeAudience{
		members: []string{"me@example.com"},
		people:  []mention.Person{{Email: "bob@example.com", FirstName: "Bob"}},
	}
	rec := serve(t, Deps{Audience: audience, Caller: callerIs(member)},
		"/api/v1/portal/mention-candidates?target_type=asset&target_id=asset_1&q=bo&limit=5")

	require.Equal(t, http.StatusOK, rec.Code)
	var body mentionCandidatesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, []mention.Person{{Email: "bob@example.com", FirstName: "Bob"}}, body.Candidates)
	assert.Equal(t, mention.Target{Type: "asset", ID: "asset_1"}, audience.gotTarget)
	assert.Equal(t, "bo", audience.gotList.Query)
	assert.Equal(t, 5, audience.gotList.Limit)
	assert.Equal(t, "me@example.com", audience.gotList.Exclude, "the caller is never offered as a mention target")
}

// Listing who can see an item is as sensitive as its share list, so a caller
// outside the audience is refused rather than shown the members.
func TestListMentionCandidates_NonMemberRefused(t *testing.T) {
	audience := &fakeAudience{members: []string{"someone.else@example.com"}}
	rec := serve(t, Deps{Audience: audience, Caller: callerIs(member)},
		"/api/v1/portal/mention-candidates?target_type=asset&target_id=asset_1")

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, audience.gotTarget.Type, "the audience is never listed for a refused caller")
}

func TestListMentionCandidates_AdminSeesAnyAudience(t *testing.T) {
	audience := &fakeAudience{people: []mention.Person{{Email: "bob@example.com"}}}
	admin := &Identity{UserID: "u9", Email: "admin@example.com", IsAdmin: true}
	rec := serve(t, Deps{Audience: audience, Caller: callerIs(admin)},
		"/api/v1/portal/mention-candidates?target_type=asset&target_id=asset_1")

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestListMentionCandidates_UnknownTargetType(t *testing.T) {
	audience := &fakeAudience{members: []string{"me@example.com"}, listErr: mention.ErrUnknownTarget}
	rec := serve(t, Deps{Audience: audience, Caller: callerIs(member)},
		"/api/v1/portal/mention-candidates?target_type=dataset&target_id=x")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestListMentionCandidates_ListError(t *testing.T) {
	audience := &fakeAudience{members: []string{"me@example.com"}, listErr: errors.New("boom")}
	rec := serve(t, Deps{Audience: audience, Caller: callerIs(member)},
		"/api/v1/portal/mention-candidates?target_type=asset&target_id=a")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestListMentionCandidates_Unauthenticated(t *testing.T) {
	rec := serve(t, Deps{Audience: &fakeAudience{}, Caller: callerIs(nil)},
		"/api/v1/portal/mention-candidates?target_type=asset&target_id=a")

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMentionWorklist(t *testing.T) {
	store := &fakeThreads{rows: []threads.ThreadWithMeta{{Thread: threads.Thread{ID: "thr_1"}}}, total: 1}
	rec := serve(t, Deps{Threads: store, Caller: callerIs(member)},
		"/api/v1/portal/worklist/mentions?limit=10&offset=20")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "me@example.com", store.gotFilter.MentionedEmail, "the worklist is self-scoped to the caller")
	assert.Equal(t, 10, store.gotFilter.Limit)
	assert.Equal(t, 20, store.gotFilter.Offset)

	var body struct {
		Data  []threads.ThreadWithMeta `json:"data"`
		Total int                      `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Total)
	require.Len(t, body.Data, 1)
	assert.Equal(t, "thr_1", body.Data[0].ID)
}

func TestMentionWorklist_EmptyRendersAsList(t *testing.T) {
	rec := serve(t, Deps{Threads: &fakeThreads{}, Caller: callerIs(member)},
		"/api/v1/portal/worklist/mentions")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`, "an empty inbox is a list, not null")
}

func TestMentionWorklist_StoreError(t *testing.T) {
	rec := serve(t, Deps{Threads: &fakeThreads{err: errors.New("boom")}, Caller: callerIs(member)},
		"/api/v1/portal/worklist/mentions")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestListDirectoryUsers(t *testing.T) {
	dir := &fakeDirectory{users: []userdir.User{
		{Email: "bob@example.com", FirstName: "Bob", LastName: "Jones", Confirmed: true},
	}}
	rec := serve(t, Deps{Directory: dir, Caller: callerIs(member)},
		"/api/v1/portal/users?q=bo")

	require.Equal(t, http.StatusOK, rec.Code)
	var body directoryUsersResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, []directoryUser{
		{Email: "bob@example.com", FirstName: "Bob", LastName: "Jones", Confirmed: true},
	}, body.Users)
	assert.Equal(t, 1, body.Total)
}

// The picker that hands a script over asks for the people who have actually
// signed in, and that narrowing is the store's (#1407): filtering the answer
// would let the row cap fill with people who never have.
func TestListDirectoryUsers_ConfirmedOnly(t *testing.T) {
	dir := &fakeDirectory{}
	rec := serve(t, Deps{Directory: dir, Caller: callerIs(member)},
		"/api/v1/portal/users?confirmed=true")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, dir.gotFilter.ConfirmedOnly)
}

func TestListDirectoryUsers_ListsEveryoneByDefault(t *testing.T) {
	dir := &fakeDirectory{}
	rec := serve(t, Deps{Directory: dir, Caller: callerIs(member)}, "/api/v1/portal/users")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, dir.gotFilter.ConfirmedOnly)
}

func TestListDirectoryUsers_StoreError(t *testing.T) {
	rec := serve(t, Deps{Directory: &fakeDirectory{err: errors.New("boom")}, Caller: callerIs(member)},
		"/api/v1/portal/users")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestListDirectoryUsers_Unauthenticated(t *testing.T) {
	rec := serve(t, Deps{Directory: &fakeDirectory{}, Caller: callerIs(nil)},
		"/api/v1/portal/users")

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// A dependency the deployment does not have leaves its route unregistered
// rather than serving an error page.
func TestRegister_SkipsRoutesWithoutTheirDependency(t *testing.T) {
	mux := http.NewServeMux()
	New(Deps{Caller: callerIs(member)}).Register(mux, func(h http.Handler) http.Handler { return h })

	for _, path := range []string{
		"/api/v1/portal/users",
		"/api/v1/portal/mention-candidates",
		"/api/v1/portal/worklist/mentions",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(
			context.Background(), http.MethodGet, path, http.NoBody))
		assert.Equal(t, http.StatusNotFound, rec.Code, path)
	}
}

func TestRegister_WithoutIdentityAccessorRegistersNothing(t *testing.T) {
	mux := http.NewServeMux()
	New(Deps{Directory: &fakeDirectory{}, Audience: &fakeAudience{}}).
		Register(mux, func(h http.Handler) http.Handler { return h })

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/api/v1/portal/users", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// Every route runs inside the wrapper the composition root supplies, which is
// where portal authentication lives.
func TestRegister_WrapsEveryRoute(t *testing.T) {
	mux := http.NewServeMux()
	wrapped := 0
	New(Deps{Directory: &fakeDirectory{}, Audience: &fakeAudience{}, Threads: &fakeThreads{}, Caller: callerIs(member)}).
		Register(mux, func(h http.Handler) http.Handler {
			wrapped++
			return h
		})
	assert.Equal(t, 3, wrapped)
}

// A knowledge page and the standalone channel are open to any authenticated
// user, so their audience needs no membership test -- and a caller whose
// directory row has not landed yet still gets their picker.
func TestListMentionCandidates_OpenTargetsSkipTheMembershipTest(t *testing.T) {
	for _, targetType := range []string{mention.TargetKnowledgePage, mention.TargetStandalone} {
		audience := &fakeAudience{people: []mention.Person{{Email: "bob@example.com"}}}
		rec := serve(t, Deps{Audience: audience, Caller: callerIs(member)},
			"/api/v1/portal/mention-candidates?target_type="+targetType+"&target_id=kp_1")

		assert.Equal(t, http.StatusOK, rec.Code, targetType)
		assert.Equal(t, targetType, audience.gotTarget.Type)
	}
}

// A mention is durable, a share is not: once the caller can no longer open the
// item, its threads must leave their inbox instead of going on surfacing the
// item's title.
func TestMentionWorklist_DropsThreadsTheCallerCanNoLongerOpen(t *testing.T) {
	store := &fakeThreads{
		rows: []threads.ThreadWithMeta{
			{Thread: threads.Thread{ID: "thr_live", TargetType: mention.TargetAsset, AssetID: "asset_live"}},
			{Thread: threads.Thread{ID: "thr_revoked", TargetType: mention.TargetAsset, AssetID: "asset_revoked"}},
		},
		total: 2,
	}
	// The caller is still in asset_live's audience and no longer in the other's.
	audience := &fakeAudience{eligibleByTarget: map[string][]string{"asset_live": {member.Email}}}
	rec := serve(t, Deps{Threads: store, Audience: audience, Caller: callerIs(member)},
		"/api/v1/portal/worklist/mentions")

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Data  []threads.ThreadWithMeta `json:"data"`
		Total int                      `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.Equal(t, "thr_live", body.Data[0].ID)
	assert.Equal(t, 1, body.Total, "the total must not count rows the page dropped")
}

// Losing the audience lookup must not empty someone's inbox.
func TestMentionWorklist_KeepsRowsWhenTheAccessCheckFails(t *testing.T) {
	store := &fakeThreads{
		rows:  []threads.ThreadWithMeta{{Thread: threads.Thread{ID: "thr_1", TargetType: mention.TargetAsset, AssetID: "a1"}}},
		total: 1,
	}
	rec := serve(t, Deps{
		Threads:  store,
		Audience: &fakeAudience{elErr: errors.New("database down")},
		Caller:   callerIs(member),
	}, "/api/v1/portal/worklist/mentions")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "thr_1")
}

// A failed target lookup is the caller's bad id or our outage, never a denial:
// answering 403 would tell them something untrue and hide the real cause.
func TestListMentionCandidates_LookupFailureIsNotADenial(t *testing.T) {
	rec := serve(t, Deps{
		Audience: &fakeAudience{elErr: errors.New("invalid input syntax for type uuid")},
		Caller:   callerIs(member),
	}, "/api/v1/portal/mention-candidates?target_type=prompt&target_id=not-a-uuid")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "do not have access")
}

func TestListMentionCandidates_UnknownTargetTypeOnTheAccessCheck(t *testing.T) {
	rec := serve(t, Deps{
		Audience: &fakeAudience{elErr: mention.ErrUnknownTarget},
		Caller:   callerIs(member),
	}, "/api/v1/portal/mention-candidates?target_type=dataset&target_id=x")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
