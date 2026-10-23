package accessreqwire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/accessreq"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/resourceshare"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

type fakeAssets struct {
	portaldomain.AssetStore
	byID map[string]*portaldomain.Asset
}

func (f fakeAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if a, ok := f.byID[id]; ok {
		return a, nil
	}
	return nil, errors.New("not found")
}

type fakeCollections struct {
	portaldomain.CollectionStore
	byID map[string]*portaldomain.Collection
}

func (f fakeCollections) Get(_ context.Context, id string) (*portaldomain.Collection, error) {
	if c, ok := f.byID[id]; ok {
		return c, nil
	}
	return nil, errors.New("not found")
}

type fakePrompts struct {
	prompt.Store
	byID map[string]*prompt.Prompt
}

func (f fakePrompts) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	if p, ok := f.byID[id]; ok {
		return p, nil
	}
	return nil, errors.New("not found")
}

type fakeShares struct {
	portaldomain.ShareStore
	inserted  []portaldomain.Share
	insertErr error
}

func (f *fakeShares) Insert(_ context.Context, s *portaldomain.Share) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, *s)
	return nil
}

func (*fakeShares) ListByAsset(context.Context, string) ([]portaldomain.Share, error) {
	return nil, nil
}

func (*fakeShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

func (*fakeShares) GetUserCollectionPermission(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

func (*fakeShares) ListSharedPromptsWithUser(context.Context, string, string) ([]portaldomain.SharedPromptRef, error) {
	return nil, nil
}

func newStores(shares *fakeShares) stores {
	deleted := time.Now()
	st := stores{
		assets: fakeAssets{byID: map[string]*portaldomain.Asset{
			"a1":   {ID: "a1", Name: "Report", OwnerID: "o1", OwnerEmail: "owner@example.com"},
			"gone": {ID: "gone", Name: "Old", OwnerID: "o1", DeletedAt: &deleted},
		}},
		collections: fakeCollections{byID: map[string]*portaldomain.Collection{
			"c1": {ID: "c1", Name: "Quarter", OwnerID: "o1", OwnerEmail: "owner@example.com"},
		}},
		prompts: fakePrompts{byID: map[string]*prompt.Prompt{
			"p1": {ID: "p1", Name: "weekly", DisplayName: "Weekly review", OwnerEmail: "owner@example.com", Scope: prompt.ScopePersonal},
			"p2": {ID: "p2", Name: "monthly", OwnerEmail: "owner@example.com", Scope: prompt.ScopeGlobal},
		}},
		shares: shares,
	}
	st.checker = access.New(access.Config{
		Assets: st.assets, Collections: st.collections, Shares: shares, Prompts: st.prompts,
		AdminRule: func([]string) bool { return false },
	})
	return st
}

// An item a request names is read with its name and owner; a deleted or
// unknown one is no item (#2090).
func TestResolve(t *testing.T) {
	st, ctx := newStores(&fakeShares{}), context.Background()
	for _, tc := range []struct {
		targetType, id, name string
	}{
		{accessreq.TargetAsset, "a1", "Report"},
		{accessreq.TargetCollection, "c1", "Quarter"},
		{accessreq.TargetPrompt, "p1", "Weekly review"},
		{accessreq.TargetPrompt, "p2", "monthly"},
	} {
		got, err := st.resolve(ctx, tc.targetType, tc.id)
		require.NoError(t, err, tc.id)
		assert.Equal(t, tc.name, got.Name, tc.id)
		assert.Equal(t, "owner@example.com", got.OwnerEmail, tc.id)
	}
	for _, tc := range [][2]string{{accessreq.TargetAsset, "gone"}, {accessreq.TargetAsset, "none"}, {accessreq.TargetCollection, "none"}, {accessreq.TargetPrompt, "none"}, {"page", "a1"}} {
		_, err := st.resolve(ctx, tc[0], tc[1])
		assert.ErrorIs(t, err, errNoItem, tc[1])
	}
}

// The owner opens their item; a stranger opens only a prompt everyone sees.
func TestCanOpen(t *testing.T) {
	st, ctx := newStores(&fakeShares{}), context.Background()
	owner := &access.User{UserID: "o1", Email: "owner@example.com"}
	stranger := &access.User{UserID: "s1", Email: "someone@example.com"}
	assert.True(t, st.canOpen(ctx, owner, accessreq.TargetAsset, "a1"))
	assert.True(t, st.canOpen(ctx, owner, accessreq.TargetCollection, "c1"))
	assert.True(t, st.canOpen(ctx, owner, accessreq.TargetPrompt, "p1"))
	assert.False(t, st.canOpen(ctx, stranger, accessreq.TargetAsset, "a1"))
	assert.False(t, st.canOpen(ctx, stranger, accessreq.TargetCollection, "c1"))
	assert.False(t, st.canOpen(ctx, stranger, accessreq.TargetPrompt, "p1"))
	assert.True(t, st.canOpen(ctx, stranger, accessreq.TargetPrompt, "p2"))
	assert.False(t, st.canOpen(ctx, owner, accessreq.TargetAsset, "none"))
	assert.False(t, st.canOpen(ctx, owner, "page", "a1"))
}

// An approval is a person share of the item with the permission chosen,
// recorded as coming from a request.
func TestShare(t *testing.T) {
	shares := &fakeShares{}
	st, ctx := newStores(shares), context.Background()
	for _, tc := range []struct{ targetType, id string }{
		{accessreq.TargetAsset, "a1"}, {accessreq.TargetCollection, "c1"}, {accessreq.TargetPrompt, "p1"},
	} {
		id, err := st.sharer()(ctx, tc.targetType, tc.id, "owner@example.com", "reader@example.com", "editor")
		require.NoError(t, err, tc.targetType)
		assert.NotEmpty(t, id)
	}
	require.Len(t, shares.inserted, 3)
	assert.Equal(t, "a1", shares.inserted[0].AssetID)
	assert.Equal(t, "c1", shares.inserted[1].CollectionID)
	assert.Equal(t, "p1", shares.inserted[2].PromptID)
	for _, s := range shares.inserted {
		assert.Equal(t, portaldomain.OriginAccessRequest, s.Origin)
		assert.Equal(t, "reader@example.com", s.SharedWithEmail)
		assert.Equal(t, portaldomain.PermissionEditor, s.Permission)
	}

	_, err := st.sharer()(ctx, accessreq.TargetAsset, "a1", "owner@example.com", "reader@example.com", "owner")
	assert.Error(t, err, "a permission a share cannot carry")
	shares.insertErr = errors.New("down")
	_, err = st.sharer()(ctx, accessreq.TargetAsset, "a1", "owner@example.com", "reader@example.com", "viewer")
	assert.Error(t, err)
}

type recordNotices struct{ requests, decisions []notifydelivery.AccessNotice }

func (r *recordNotices) NotifyAccessRequest(_ context.Context, a notifydelivery.AccessNotice) {
	r.requests = append(r.requests, a)
}

func (r *recordNotices) NotifyAccessDecision(_ context.Context, a notifydelivery.AccessNotice) {
	r.decisions = append(r.decisions, a)
}

// The owner is told who asked and why; the requester is told the answer, with
// a link to the item when it was approved.
func TestNotices(t *testing.T) {
	rec := &recordNotices{}
	owner, requester := notices(rec, "https://platform.example.com")
	ctx := context.Background()
	req := &accessreq.Request{
		TargetType: accessreq.TargetAsset, TargetID: "a1", RequesterEmail: "reader@example.com",
		OwnerEmail: "owner@example.com", Message: "for the review",
	}
	owner(ctx, req, "Report")
	require.Len(t, rec.requests, 1)
	assert.Equal(t, notifydelivery.AccessNotice{
		To: "owner@example.com", Actor: "reader@example.com", ItemTitle: "Report", ItemID: "a1", Message: "for the review",
	}, rec.requests[0])

	req.Status, req.Permission, req.DecidedBy = accessreq.StatusApproved, "viewer", "owner@example.com"
	requester(ctx, req, "Report")
	req.Status = accessreq.StatusDeclined
	requester(ctx, req, "Report")
	require.Len(t, rec.decisions, 2)
	assert.Equal(t, "Your request was approved as viewer.", rec.decisions[0].Message)
	assert.Equal(t, "https://platform.example.com/portal/assets/a1", rec.decisions[0].Link)
	assert.Equal(t, "Your request was declined.", rec.decisions[1].Message)
	assert.Empty(t, rec.decisions[1].Link)
}

// The settings read the stored section, and the defaults when it is absent,
// unreadable or out of range.
func TestSectionSettings(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := sectionSettings{section: settingsjson.New[accessreq.Settings](db, accessreq.SettingsSection)}
	ctx := context.Background()
	cols := []string{"value", "updated_by", "updated_at"}

	mock.ExpectQuery("SELECT value").WillReturnRows(sqlmock.NewRows(cols).AddRow(`{"reveal_names":true,"expire_days":30}`, "a", time.Now()))
	assert.Equal(t, accessreq.Settings{RevealNames: true, ExpireDays: 30}, s.Read(ctx))
	mock.ExpectQuery("SELECT value").WillReturnRows(sqlmock.NewRows(cols).AddRow(`{"expire_days":0}`, "a", time.Now()))
	assert.Equal(t, accessreq.Defaults(), s.Read(ctx))
	mock.ExpectQuery("SELECT value").WillReturnError(errors.New("down"))
	assert.Equal(t, accessreq.Defaults(), s.Read(ctx))

	mock.ExpectExec("INSERT INTO platform_settings").WillReturnResult(sqlmock.NewResult(0, 1))
	assert.NoError(t, s.Write(ctx, accessreq.Settings{ExpireDays: 7}, "admin@example.com"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The routes are mounted over the stores, and the settings routes only behind
// the admin API's authentication; a platform with no database mounts none.
func TestMount(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	pass := func(h http.Handler) http.Handler { return h }
	routed := func(mux *http.ServeMux, method, path string) bool {
		_, pattern := mux.Handler(httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody))
		return pattern != ""
	}

	mux := http.NewServeMux()
	register(mux, db, newStores(&fakeShares{}), "https://platform.example.com", Deps{
		Wrap: pass, IsAdmin: func([]string) bool { return false }, AdminWrap: pass,
		Author: func(*http.Request) string { return "admin@example.com" },
	})
	assert.True(t, routed(mux, http.MethodGet, "/api/v1/portal/assets/a1/access-request"))
	assert.True(t, routed(mux, http.MethodPost, "/api/v1/portal/access-requests/r1/approve"))
	assert.True(t, routed(mux, http.MethodPut, "/api/v1/admin/settings/access-requests"))

	bare := http.NewServeMux()
	register(bare, db, newStores(&fakeShares{}), "", Deps{Wrap: pass, IsAdmin: func([]string) bool { return false }})
	assert.False(t, routed(bare, http.MethodPut, "/api/v1/admin/settings/access-requests"))

	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server: platform.ServerConfig{Name: "test"}, Semantic: platform.SemanticConfig{Provider: "noop"},
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	none := http.NewServeMux()
	Mount(none, p, Deps{Wrap: pass})
	assert.False(t, routed(none, http.MethodGet, "/api/v1/portal/access-requests"))
}

func TestCanShareReadsThePersona(t *testing.T) {
	off := false
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "staff", Roles: []string{"staff"}}))
	require.NoError(t, reg.Register(&persona.Persona{Name: "quiet", Roles: []string{"quiet"}, AllowSharing: &off}))
	can := canShare(reg)
	assert.True(t, can(&access.User{Roles: []string{"staff"}}))
	assert.False(t, can(&access.User{Roles: []string{"quiet"}}))
	assert.False(t, can(&access.User{Roles: []string{"nobody"}}))
}

type fakeResources map[string]*resource.Resource

func (f fakeResources) Get(_ context.Context, id string) (*resource.Resource, error) {
	if r, ok := f[id]; ok {
		return r, nil
	}
	return nil, errors.New("not found")
}

type fakeResourceShares struct {
	inserted []portaldomain.Share
	perm     map[string]string
}

func (f *fakeResourceShares) Insert(_ context.Context, s *portaldomain.Share) error {
	f.inserted = append(f.inserted, *s)
	return nil
}

func (f *fakeResourceShares) Permission(_ context.Context, _ string, r resourceshare.Recipient) (string, error) {
	return f.perm[r.Email], nil
}

// A file is a target too (#2258): read with its name and uploader, opened by
// its uploader, by a share, or by an administrator, and an approval writes a
// share of the file.
func TestResourceTarget(t *testing.T) {
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "staff", Roles: []string{"staff"}}))
	rs := &fakeResourceShares{perm: map[string]string{"shared@example.com": "viewer"}}
	st := newStores(&fakeShares{})
	st.personas, st.resourceShares = reg, rs
	st.isAdmin = func(roles []string) bool { return len(roles) > 0 && roles[0] == "admin" }
	st.resources = fakeResources{"r1": {
		ID: "r1", DisplayName: "extract.csv", Scope: resource.ScopeUser, ScopeID: "o1", UploaderSub: "o1", UploaderEmail: "owner@example.com",
	}}
	ctx := context.Background()

	got, err := st.resolve(ctx, accessreq.TargetResource, "r1")
	require.NoError(t, err)
	assert.Equal(t, "extract.csv", got.Name)
	assert.Equal(t, "owner@example.com", got.OwnerEmail)
	_, err = st.resolve(ctx, accessreq.TargetResource, "none")
	assert.ErrorIs(t, err, errNoItem)

	assert.True(t, st.canOpen(ctx, &access.User{UserID: "o1", Email: "owner@example.com", Roles: []string{"staff"}}, accessreq.TargetResource, "r1"))
	assert.True(t, st.canOpen(ctx, &access.User{UserID: "x", Email: "shared@example.com", Roles: []string{"staff"}}, accessreq.TargetResource, "r1"))
	assert.True(t, st.canOpen(ctx, &access.User{UserID: "a", Email: "a@example.com", Roles: []string{"admin"}}, accessreq.TargetResource, "r1"))
	assert.False(t, st.canOpen(ctx, &access.User{UserID: "s", Email: "s@example.com", Roles: []string{"staff"}}, accessreq.TargetResource, "r1"))
	assert.False(t, st.canOpen(ctx, &access.User{UserID: "s", Email: "s@example.com"}, accessreq.TargetResource, "none"))

	id, err := st.sharer()(ctx, accessreq.TargetResource, "r1", "owner@example.com", "reader@example.com", "viewer")
	require.NoError(t, err)
	require.Len(t, rs.inserted, 1)
	assert.Equal(t, id, rs.inserted[0].ID)
	assert.Equal(t, "r1", rs.inserted[0].ResourceID)
	assert.Equal(t, portaldomain.OriginAccessRequest, rs.inserted[0].Origin)

	none := newStores(&fakeShares{})
	_, err = none.resolve(ctx, accessreq.TargetResource, "r1")
	assert.ErrorIs(t, err, errNoItem, "no resource store names no file")
	assert.False(t, none.canOpen(ctx, &access.User{UserID: "o1"}, accessreq.TargetResource, "r1"))
}
