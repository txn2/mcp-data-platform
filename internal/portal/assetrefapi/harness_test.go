package assetrefapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// The fixtures every test in this package is written against: one owner, one
// reader the asset is shared with, one report, and two files it could name.
const (
	ownerID    = "user-owner"
	ownerEmail = "owner@example.com"
	readerID   = "user-reader"
	readerMail = "reader@example.com"

	assetID   = "asset-report"
	otherID   = "asset-other"
	logoID    = "res-logo"
	logoURI   = "mcp://global/brand/logo.png"
	chartID   = "res-chart"
	chartURI  = "mcp://persona/finance/chart.png"
	logoToken = "tok-logo"

	assetBucket = "portal-assets"
	assetKey    = "assets/report/content.html"
)

// errStore is a store failure, distinct from a row that is simply absent.
var errStore = errors.New("database unavailable")

// fakeRefs is an in-memory reference store.
//
// GetByToken models the Postgres store's contract exactly -- no such reference
// is (nil, nil) -- so a handler branch that passes here cannot diverge in
// production.
type fakeRefs struct {
	byAsset  map[string][]portaldomain.AssetResourceRef
	listErr  error
	byResErr error
	// listErrAfter makes ListByAsset start failing once it has answered this
	// many times, which is how a test reaches the read-back a mutation does
	// after its write has already landed.
	listErrAfter int
	listCall     int
	replaceErr   error
	replaceCall  int
	attachErr    error
	attachCall   int
	detachErr    error
	detachCall   int
}

func newFakeRefs() *fakeRefs {
	return &fakeRefs{byAsset: map[string][]portaldomain.AssetResourceRef{}}
}

func (f *fakeRefs) Replace(_ context.Context, id string, refs []portaldomain.AssetResourceRef) error {
	f.replaceCall++
	if f.replaceErr != nil {
		return f.replaceErr
	}
	f.byAsset[id] = refs
	return nil
}

func (f *fakeRefs) ListByAsset(_ context.Context, id string) ([]portaldomain.AssetResourceRef, error) {
	f.listCall++
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listErrAfter > 0 && f.listCall > f.listErrAfter {
		return nil, errStore
	}
	return f.byAsset[id], nil
}

// Attach models the store's ON CONFLICT DO NOTHING: an asset that already names
// the resource is (false, nil), never an error and never a second row.
func (f *fakeRefs) Attach(_ context.Context, ref portaldomain.AssetResourceRef) (bool, error) {
	f.attachCall++
	if f.attachErr != nil {
		return false, f.attachErr
	}
	for _, existing := range f.byAsset[ref.AssetID] {
		if existing.ResourceID == ref.ResourceID {
			return false, nil
		}
	}
	ref.Position = len(f.byAsset[ref.AssetID])
	f.byAsset[ref.AssetID] = append(f.byAsset[ref.AssetID], ref)
	return true, nil
}

func (f *fakeRefs) Detach(_ context.Context, assetID, resourceID string) (bool, error) {
	f.detachCall++
	if f.detachErr != nil {
		return false, f.detachErr
	}
	kept := make([]portaldomain.AssetResourceRef, 0, len(f.byAsset[assetID]))
	found := false
	for _, ref := range f.byAsset[assetID] {
		if ref.ResourceID == resourceID {
			found = true
			continue
		}
		kept = append(kept, ref)
	}
	f.byAsset[assetID] = kept
	return found, nil
}

// ListByResource honors the limit the way the SQL does, so a test can prove the
// handler asked for one more than the bound and reported the cut.
func (f *fakeRefs) ListByResource(
	_ context.Context, resourceID string, limit int,
) ([]portaldomain.AssetResourceRef, error) {
	if f.byResErr != nil {
		return nil, f.byResErr
	}
	var out []portaldomain.AssetResourceRef
	for _, id := range slices.Sorted(maps.Keys(f.byAsset)) {
		for _, ref := range f.byAsset[id] {
			if ref.ResourceID == resourceID && len(out) < limit {
				out = append(out, ref)
			}
		}
	}
	return out, nil
}

func (f *fakeRefs) GetByToken(_ context.Context, id, token string) (*portaldomain.AssetResourceRef, error) {
	for _, ref := range f.byAsset[id] {
		if ref.RefToken == token {
			return &ref, nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract: no such reference is (nil, nil)
}

// fakeResources is the managed-resource layer: a global logo anyone may read
// and a finance-scoped chart only that persona may.
type fakeResources struct {
	byID   map[string]*resource.Resource
	getErr error
}

func newFakeResources() *fakeResources {
	return &fakeResources{byID: map[string]*resource.Resource{
		logoID: {
			ID: logoID, Scope: resource.ScopeGlobal, Category: "brand",
			Filename: "logo.png", DisplayName: "Company logo",
			MIMEType: "image/png", SizeBytes: 4096, URI: logoURI,
		},
		chartID: {
			ID: chartID, Scope: resource.ScopePersona, ScopeID: "finance",
			Filename: "chart.png", DisplayName: "Revenue chart",
			MIMEType: "image/png", SizeBytes: 8192, URI: chartURI,
		},
	}}
}

func (f *fakeResources) Get(_ context.Context, id string) (*resource.Resource, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	res, ok := f.byID[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return res, nil
}

func (f *fakeResources) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, res := range f.byID {
		if res.URI == uri {
			return res, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (f *fakeResources) GetByIDs(_ context.Context, ids []string) (map[string]*resource.Resource, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := make(map[string]*resource.Resource, len(ids))
	for _, id := range ids {
		if res, ok := f.byID[id]; ok {
			out[id] = res
		}
	}
	return out, nil
}

// fakeAssets holds the assets by id. A missing row is an error, which is the
// PostgreSQL store's not-found shape and the one the access checks are written
// against.
type fakeAssets struct {
	byID   map[string]*portaldomain.Asset
	getErr error
}

func newFakeAssets() *fakeAssets {
	return &fakeAssets{byID: map[string]*portaldomain.Asset{
		assetID: {
			ID: assetID, OwnerID: ownerID, OwnerEmail: ownerEmail,
			Name: "Q4 report", ContentType: "text/html",
			S3Bucket: assetBucket, S3Key: assetKey, SizeBytes: 120,
			UpdatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		otherID: {
			ID: otherID, OwnerID: readerID, OwnerEmail: readerMail,
			Name: "Someone else's memo", ContentType: "text/markdown",
			S3Bucket: assetBucket, S3Key: "assets/other/content.md", SizeBytes: 20,
		},
	}}
}

func (*fakeAssets) Insert(context.Context, portaldomain.Asset) error { return nil }

func (f *fakeAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	asset, ok := f.byID[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return asset, nil
}

func (f *fakeAssets) GetByIDs(_ context.Context, ids []string) (map[string]*portaldomain.Asset, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := make(map[string]*portaldomain.Asset, len(ids))
	for _, id := range ids {
		if asset, ok := f.byID[id]; ok {
			out[id] = asset
		}
	}
	return out, nil
}

func (*fakeAssets) GetByIdempotencyKey(context.Context, string, string) (*portaldomain.Asset, error) {
	return nil, sql.ErrNoRows
}

func (*fakeAssets) List(context.Context, portaldomain.AssetFilter) ([]portaldomain.Asset, int, error) {
	return nil, 0, nil
}
func (*fakeAssets) Update(context.Context, string, portaldomain.AssetUpdate) error { return nil }
func (*fakeAssets) AppendProvenanceCapture(context.Context, string, portaldomain.ProvenanceCapture) error {
	return nil
}
func (*fakeAssets) SoftDelete(context.Context, string) error { return nil }

// fakeShares carries the two share facts this surface reads: who an asset is
// shared with, and whether it carries a public link.
type fakeShares struct {
	portaldomain.ShareStore
	byAsset   map[string][]portaldomain.Share
	summaries map[string]portaldomain.ShareSummary
	listErr   error
	sumErr    error
}

func newFakeShares() *fakeShares {
	return &fakeShares{
		byAsset:   map[string][]portaldomain.Share{},
		summaries: map[string]portaldomain.ShareSummary{},
	}
}

func (f *fakeShares) ListByAsset(_ context.Context, id string) ([]portaldomain.Share, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.byAsset[id], nil
}

func (*fakeShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

func (f *fakeShares) ListActiveShareSummaries(_ context.Context, ids []string) (map[string]portaldomain.ShareSummary, error) {
	if f.sumErr != nil {
		return nil, f.sumErr
	}
	out := make(map[string]portaldomain.ShareSummary, len(ids))
	for _, id := range ids {
		if s, ok := f.summaries[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

// shareWith grants a permission on an asset to an address, as an active share.
func (f *fakeShares) shareWith(asset, email string, perm portaldomain.SharePermission) {
	f.byAsset[asset] = append(f.byAsset[asset], portaldomain.Share{
		ID: "share-" + email, AssetID: asset, SharedWithEmail: email, Permission: perm,
	})
}

// fakeBlobs serves the asset's stored content.
type fakeBlobs struct {
	byKey  map[string][]byte
	getErr error
}

func (f *fakeBlobs) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	if f.getErr != nil {
		return nil, "", f.getErr
	}
	body, ok := f.byKey[key]
	if !ok {
		return nil, "", errStore
	}
	return body, "text/html", nil
}

// reportBody is the stored report: it writes the logo's URI once, indented
// inside markup, which is what the occurrence scan has to find.
const reportBody = "<h1>Q4</h1>\n" +
	`    <img src="` + logoURI + `" alt="logo">` + "\n" +
	"<p>Revenue rose.</p>\n"

// harness is one assembled surface plus the doubles behind it, so a test can
// arrange a fixture and then assert on what the handler wrote.
type harness struct {
	refs      *fakeRefs
	resources *fakeResources
	assets    *fakeAssets
	shares    *fakeShares
	blobs     *fakeBlobs
	cfg       Config
}

// newHarness builds the default arrangement: the owner's report, no references,
// a readable logo, a finance-only chart, and the report's stored body.
func newHarness() *harness {
	h := &harness{
		refs:      newFakeRefs(),
		resources: newFakeResources(),
		assets:    newFakeAssets(),
		shares:    newFakeShares(),
		blobs:     &fakeBlobs{byKey: map[string][]byte{assetKey: []byte(reportBody)}},
	}
	h.cfg = Config{
		Refs:      h.refs,
		Resources: h.resources,
		Assets:    h.assets,
		Shares:    h.shares,
		Blobs:     h.blobs,
		Claims:    claimsFor,
	}
	return h
}

// claimsFor builds a caller's resource claims the way the parent does, so the
// read rule this surface applies is the one every resource surface applies. The
// finance persona is carried on a role, which is how a persona reaches a
// resource claim in a deployment.
func claimsFor(user *access.User) resource.Claims {
	persona := ""
	for _, role := range user.Roles {
		if name, ok := strings.CutPrefix(role, "persona:"); ok {
			persona = name
		}
	}
	isAdmin := false
	for _, role := range user.Roles {
		if role == "admin" {
			isAdmin = true
		}
	}
	return resource.BuildClaims(user.UserID, user.Email, persona, user.Roles, isAdmin)
}

// owner and reader are the two callers every test is written from.
func owner() *access.User {
	return &access.User{UserID: ownerID, Email: ownerEmail}
}

func reader() *access.User {
	return &access.User{UserID: readerID, Email: readerMail}
}

// server assembles the seam exactly as the parent does -- one Config, one
// checker over the same stores -- and returns a mux serving its routes behind
// an authenticator that injects user. Tests drive real requests through it, so
// route registration and the access checks are exercised together.
func (h *harness) server(user *access.User) http.Handler {
	cfg := h.cfg
	if cfg.Access == nil {
		cfg.Access = access.New(access.Config{
			Assets:     cfg.Assets,
			Shares:     cfg.Shares,
			AdminRoles: []string{"admin"},
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

// do drives one request through the assembled surface.
func (h *harness) do(t *testing.T, user *access.User, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, payload)
	rec := httptest.NewRecorder()
	h.server(user).ServeHTTP(rec, req)
	return rec
}

// declare seeds a reference the way a save would have.
func (h *harness) declare(asset string, refs ...portaldomain.AssetResourceRef) {
	for i := range refs {
		refs[i].AssetID = asset
		refs[i].Position = i
	}
	h.refs.byAsset[asset] = refs
}

// logoRef is the reference the report declares in most fixtures.
func logoRef() portaldomain.AssetResourceRef {
	return portaldomain.AssetResourceRef{
		ResourceID: logoID, URI: logoURI, RefToken: logoToken, DeclaredBy: ownerEmail,
	}
}

// decode reads a JSON response body into v, failing the test on a bad shape.
func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// serveBare drives one GET through a mux directly, for the case where no routes
// were registered at all and there is no harness to drive.
func serveBare(t *testing.T, mux http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec
}
