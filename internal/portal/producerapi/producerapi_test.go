package producerapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// The fixtures every test here is written against: one owner, one stranger, one
// report written by a script and by its owner, and one uploaded file.
const (
	ownerID    = "user-owner"
	ownerEmail = "owner@example.com"
	strangerID = "user-stranger"
	strangerML = "stranger@example.com"

	assetID    = "asset-report"
	resourceID = "res-logo"
	scriptID   = "script-1"
	sessionID  = "sess-abc"
)

var writtenAt = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// fakeProducers is the producer record.
type fakeProducers struct {
	byTarget map[string][]producedby.Row
	err      error
}

func (*fakeProducers) Record(context.Context, producedby.Write) error { return nil }

func (f *fakeProducers) ListByTarget(_ context.Context, kind, id string) ([]producedby.Row, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byTarget[kind+"/"+id], nil
}

func (*fakeProducers) ListByProducer(context.Context, string, string, int) ([]producedby.Row, error) {
	return nil, nil
}

type fakeAssets struct {
	portaldomain.AssetStore
	byID map[string]*portaldomain.Asset
}

func (f *fakeAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	asset, ok := f.byID[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return asset, nil
}

type fakeResources struct {
	byID map[string]*resource.Resource
	err  error
}

func (f *fakeResources) Get(_ context.Context, id string) (*resource.Resource, error) {
	if f.err != nil {
		return nil, f.err
	}
	res, ok := f.byID[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return res, nil
}

// fakeShares carries the share facts the access checker consults. A stranger
// has no share, which is what makes the refusal test a refusal.
type fakeShares struct {
	portaldomain.ShareStore
	byAsset map[string][]portaldomain.Share
	listErr error
}

func (f *fakeShares) ListByAsset(_ context.Context, id string) ([]portaldomain.Share, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.byAsset[id], nil
}

func (*fakeShares) GetUserAssetPermissionViaCollection(
	context.Context, string, string, string,
) (portaldomain.SharePermission, error) {
	return "", nil
}

type fakeScriptNames struct {
	names map[string]string
	err   error
}

func (f *fakeScriptNames) Names(context.Context, []string) (map[string]string, error) {
	return f.names, f.err
}

// harness is one assembled surface plus the doubles behind it.
type harness struct {
	producers *fakeProducers
	assets    *fakeAssets
	resources *fakeResources
	scripts   *fakeScriptNames
	shares    *fakeShares
	cfg       Config
}

func scriptRow(kind, id string) producedby.Row {
	return producedby.Row{
		TargetKind: kind, TargetID: id,
		Producer:     producedby.Producer{Kind: producedby.KindScript, ID: scriptID, Label: "daily-sales"},
		Created:      true,
		FirstWriteAt: writtenAt, LastWriteAt: writtenAt.Add(time.Hour), WriteCount: 5, LastVersion: 5,
	}
}

func personRow(kind, id string) producedby.Row {
	return producedby.Row{
		TargetKind: kind, TargetID: id,
		Producer:     producedby.Producer{Kind: producedby.KindPerson, ID: ownerID, Label: ownerEmail},
		FirstWriteAt: writtenAt, LastWriteAt: writtenAt, WriteCount: 1, LastVersion: 6,
	}
}

func newHarness() *harness {
	h := &harness{
		producers: &fakeProducers{byTarget: map[string][]producedby.Row{}},
		assets: &fakeAssets{byID: map[string]*portaldomain.Asset{
			assetID: {ID: assetID, OwnerID: ownerID, OwnerEmail: ownerEmail, Name: "Q4 report"},
		}},
		resources: &fakeResources{byID: map[string]*resource.Resource{
			resourceID: {
				ID: resourceID, Scope: resource.ScopeGlobal, Path: "brand",
				Filename: "logo.png", DisplayName: "Company logo",
			},
		}},
		scripts: &fakeScriptNames{names: map[string]string{scriptID: "daily-sales"}},
		shares:  &fakeShares{byAsset: map[string][]portaldomain.Share{}},
	}
	h.cfg = Config{
		Producers: h.producers,
		Assets:    h.assets,
		Resources: h.resources,
		Scripts:   h.scripts,
		// The parent supplies h.access.IsAdmin(user) here, so the fixture
		// derives the same fact from the same role set the checker below is
		// built with. Hardcoding false left the harness unable to express the
		// caller #1584 is about.
		Claims: func(u *access.User) resource.Claims {
			return resource.BuildClaims(u.UserID, u.Email, "", u.Roles, slices.Contains(u.Roles, "admin"))
		},
	}
	return h
}

func (h *harness) server(user *access.User) http.Handler {
	cfg := h.cfg
	if cfg.Access == nil {
		cfg.Access = access.New(access.Config{
			Assets: cfg.Assets, Shares: h.shares, AdminRoles: []string{"admin"},
		})
	}
	mux := http.NewServeMux()
	Register(mux, cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user != nil {
			r = r.WithContext(access.ContextWithUser(r.Context(), user))
		}
		mux.ServeHTTP(w, r)
	})
}

func (h *harness) get(t *testing.T, user *access.User, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	rec := httptest.NewRecorder()
	h.server(user).ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) producersResponse {
	t.Helper()
	var got producersResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	return got
}

func owner() *access.User { return &access.User{UserID: ownerID, Email: ownerEmail} }

// admin is a platform administrator in no persona: write authority over every
// library, membership of none.
func admin() *access.User {
	return &access.User{UserID: "user-admin", Email: "admin@example.com", Roles: []string{"admin"}}
}

func stranger() *access.User {
	return &access.User{UserID: strangerID, Email: strangerML}
}

// TestAssetProducersListsBothWriters is acceptance criterion 4 read at the
// surface: a report a script refreshes and its owner has edited names both.
func TestAssetProducersListsBothWriters(t *testing.T) {
	h := newHarness()
	h.producers.byTarget[producedby.TargetAsset+"/"+assetID] = []producedby.Row{
		personRow(producedby.TargetAsset, assetID),
		scriptRow(producedby.TargetAsset, assetID),
	}

	rec := h.get(t, owner(), "/api/v1/portal/assets/"+assetID+"/producers")
	require.Equal(t, http.StatusOK, rec.Code)
	got := decode(t, rec)
	require.Len(t, got.Data, 2)
	assert.Equal(t, 2, got.Total)

	assert.Equal(t, producedby.KindPerson, got.Data[0].Kind)
	assert.Equal(t, ownerEmail, got.Data[0].Label)
	assert.False(t, got.Data[0].Created, "the person only edited it")

	assert.Equal(t, producedby.KindScript, got.Data[1].Kind)
	assert.True(t, got.Data[1].Created)
	assert.True(t, got.Data[1].Exists)
	assert.Equal(t, 5, got.Data[1].WriteCount)
	assert.Equal(t, 5, got.Data[1].LastVersion)
	assert.Equal(t, writtenAt, got.Data[1].FirstWriteAt)
}

// TestScriptProducerRenamedShowsTheCurrentName pins that the id is the identity
// and the label is only what to display.
func TestScriptProducerRenamedShowsTheCurrentName(t *testing.T) {
	h := newHarness()
	h.scripts.names = map[string]string{scriptID: "weekly-sales"}
	h.producers.byTarget[producedby.TargetAsset+"/"+assetID] = []producedby.Row{
		scriptRow(producedby.TargetAsset, assetID),
	}
	got := decode(t, h.get(t, owner(), "/api/v1/portal/assets/"+assetID+"/producers"))
	require.Len(t, got.Data, 1)
	assert.Equal(t, "weekly-sales", got.Data[0].Label)
	assert.True(t, got.Data[0].Exists)
}

// TestDeletedScriptProducerStillListed is acceptance criterion 8.
func TestDeletedScriptProducerStillListed(t *testing.T) {
	h := newHarness()
	h.scripts.names = map[string]string{}
	h.producers.byTarget[producedby.TargetAsset+"/"+assetID] = []producedby.Row{
		scriptRow(producedby.TargetAsset, assetID),
	}
	rec := h.get(t, owner(), "/api/v1/portal/assets/"+assetID+"/producers")
	require.Equal(t, http.StatusOK, rec.Code)
	got := decode(t, rec)
	require.Len(t, got.Data, 1)
	assert.False(t, got.Data[0].Exists)
	assert.Equal(t, "daily-sales", got.Data[0].Label, "the recorded name is what remains")
}

func TestScriptLookupFailureReportsProducersAsExisting(t *testing.T) {
	h := newHarness()
	h.scripts.err = errors.New("database down")
	h.producers.byTarget[producedby.TargetAsset+"/"+assetID] = []producedby.Row{
		scriptRow(producedby.TargetAsset, assetID),
	}
	got := decode(t, h.get(t, owner(), "/api/v1/portal/assets/"+assetID+"/producers"))
	require.Len(t, got.Data, 1)
	assert.True(t, got.Data[0].Exists)
}

func TestNoScriptLookupReportsProducersAsExisting(t *testing.T) {
	h := newHarness()
	h.cfg.Scripts = nil
	h.producers.byTarget[producedby.TargetAsset+"/"+assetID] = []producedby.Row{
		scriptRow(producedby.TargetAsset, assetID),
	}
	got := decode(t, h.get(t, owner(), "/api/v1/portal/assets/"+assetID+"/producers"))
	require.Len(t, got.Data, 1)
	assert.True(t, got.Data[0].Exists)
}

func TestSessionProducerNeedsNoScriptLookup(t *testing.T) {
	h := newHarness()
	h.scripts.err = errors.New("must not matter")
	h.producers.byTarget[producedby.TargetAsset+"/"+assetID] = []producedby.Row{{
		TargetKind: producedby.TargetAsset, TargetID: assetID,
		Producer: producedby.Producer{Kind: producedby.KindSession, ID: sessionID},
		Created:  true, FirstWriteAt: writtenAt, LastWriteAt: writtenAt, WriteCount: 1,
	}}
	got := decode(t, h.get(t, owner(), "/api/v1/portal/assets/"+assetID+"/producers"))
	require.Len(t, got.Data, 1)
	assert.Equal(t, sessionID, got.Data[0].ID)
	assert.True(t, got.Data[0].Exists)
}

func TestResourceProducers(t *testing.T) {
	h := newHarness()
	h.producers.byTarget[producedby.TargetResource+"/"+resourceID] = []producedby.Row{
		scriptRow(producedby.TargetResource, resourceID),
	}
	rec := h.get(t, owner(), "/api/v1/portal/resources/"+resourceID+"/producers")
	require.Equal(t, http.StatusOK, rec.Code)
	got := decode(t, rec)
	require.Len(t, got.Data, 1)
	assert.Equal(t, scriptID, got.Data[0].ID)
}

func TestProducersRequireAuthentication(t *testing.T) {
	h := newHarness()
	assert.Equal(t, http.StatusUnauthorized,
		h.get(t, nil, "/api/v1/portal/assets/"+assetID+"/producers").Code)
	assert.Equal(t, http.StatusUnauthorized,
		h.get(t, nil, "/api/v1/portal/resources/"+resourceID+"/producers").Code)
}

// TestAssetProducersShareCheckFailure keeps an unreadable share table from
// being read as "this reader has no grant".
func TestAssetProducersShareCheckFailure(t *testing.T) {
	h := newHarness()
	h.shares.listErr = errors.New("database down")
	rec := h.get(t, stranger(), "/api/v1/portal/assets/"+assetID+"/producers")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAssetProducersRefusesAReaderWithoutAccess(t *testing.T) {
	h := newHarness()
	rec := h.get(t, stranger(), "/api/v1/portal/assets/"+assetID+"/producers")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAssetProducersUnknownAsset(t *testing.T) {
	h := newHarness()
	rec := h.get(t, owner(), "/api/v1/portal/assets/nope/producers")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAssetProducersDeletedAsset(t *testing.T) {
	h := newHarness()
	gone := time.Now().UTC()
	h.assets.byID[assetID].DeletedAt = &gone
	rec := h.get(t, owner(), "/api/v1/portal/assets/"+assetID+"/producers")
	assert.Equal(t, http.StatusGone, rec.Code)
}

func TestResourceProducersUnreadableResourceReadsAsAbsent(t *testing.T) {
	h := newHarness()
	h.resources.byID[resourceID].Scope = resource.ScopeUser
	h.resources.byID[resourceID].ScopeID = "somebody-else"
	rec := h.get(t, owner(), "/api/v1/portal/resources/"+resourceID+"/producers")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestResourceProducersReadFailure(t *testing.T) {
	h := newHarness()
	h.resources.err = errors.New("database down")
	rec := h.get(t, owner(), "/api/v1/portal/resources/"+resourceID+"/producers")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestProducerListingFailure(t *testing.T) {
	h := newHarness()
	h.producers.err = errors.New("database down")
	rec := h.get(t, owner(), "/api/v1/portal/assets/"+assetID+"/producers")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestRegisterSkipsAnIncompleteDeployment pins that a deployment recording no
// producers serves no route at all, rather than one that always refuses.
func TestRegisterSkipsAnIncompleteDeployment(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Config{})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/portal/assets/a/producers", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestResourceRouteAbsentWithoutAResourceLayer keeps the asset route working on
// a deployment that has no managed resources.
func TestResourceRouteAbsentWithoutAResourceLayer(t *testing.T) {
	h := newHarness()
	h.cfg.Resources = nil
	rec := h.get(t, owner(), "/api/v1/portal/resources/"+resourceID+"/producers")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	h.producers.byTarget[producedby.TargetAsset+"/"+assetID] = []producedby.Row{
		scriptRow(producedby.TargetAsset, assetID),
	}
	assert.Equal(t, http.StatusOK, h.get(t, owner(), "/api/v1/portal/assets/"+assetID+"/producers").Code)
}

func TestEmptyProducerList(t *testing.T) {
	h := newHarness()
	rec := h.get(t, owner(), "/api/v1/portal/assets/"+assetID+"/producers")
	require.Equal(t, http.StatusOK, rec.Code)
	got := decode(t, rec)
	assert.Empty(t, got.Data)
	assert.Equal(t, 0, got.Total)
}

// TestResourceProducersAllowsAnAdministratorOutsideTheLibrary is #1584 read at
// this panel. It is drawn on the resource viewer page, and that page's own
// routes resolve a named file through CanAccessResource, so an administrator
// reached a page whose Producers panel answered not-found for the same file in
// the same request cycle.
//
// The resource below is scoped to somebody else's library, which is the same
// fixture TestResourceProducersUnreadableResourceReadsAsAbsent refuses the
// owner on: the difference in the answer is the caller's authority and nothing
// else.
func TestResourceProducersAllowsAnAdministratorOutsideTheLibrary(t *testing.T) {
	h := newHarness()
	h.resources.byID[resourceID].Scope = resource.ScopeUser
	h.resources.byID[resourceID].ScopeID = "somebody-else"
	h.producers.byTarget[producedby.TargetResource+"/"+resourceID] = []producedby.Row{
		scriptRow(producedby.TargetResource, resourceID),
	}

	rec := h.get(t, admin(), "/api/v1/portal/resources/"+resourceID+"/producers")

	require.Equal(t, http.StatusOK, rec.Code)
	got := decode(t, rec)
	require.Len(t, got.Data, 1)
	assert.Equal(t, scriptID, got.Data[0].ID)
}
