package assetverify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

type fakeAssets struct {
	portaldomain.AssetStore
	rows map[string]*portaldomain.Asset
}

func (f *fakeAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	a, ok := f.rows[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *a
	return &cp, nil
}

type fakeShares struct {
	portaldomain.ShareStore
	byAsset map[string][]portaldomain.Share
}

func (f *fakeShares) ListByAsset(_ context.Context, id string) ([]portaldomain.Share, error) {
	return f.byAsset[id], nil
}

func (*fakeShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

// fakeVerify is a Store that applies verifications to the fake assets.
type fakeVerify struct {
	assets    *fakeAssets
	by        map[string]string
	failWrite error
	failBy    error
}

func (f *fakeVerify) Verify(_ context.Context, id string, v Verification) error {
	if f.failWrite != nil {
		return f.failWrite
	}
	v, err := v.Normalize(time.Now())
	if err != nil {
		return err
	}
	f.by[id] = v.Actor
	f.assets.rows[id].Verification = &portaldomain.AssetVerification{
		VerifiedBy: v.Actor, VerifiedVersion: v.Version, Note: v.Note, ExpiresAt: v.ExpiresAt,
	}
	return nil
}

func (f *fakeVerify) Unverify(_ context.Context, id, _ string) (bool, error) {
	if f.failWrite != nil {
		return false, f.failWrite
	}
	_, had := f.by[id]
	delete(f.by, id)
	f.assets.rows[id].Verification = nil
	return had, nil
}

func (f *fakeVerify) VerifiedBy(_ context.Context, id string) (string, error) {
	return f.by[id], f.failBy
}

func (*fakeVerify) ClaimDue(context.Context, time.Time, time.Time, int) ([]Due, error) {
	return nil, nil
}

const (
	ownerEmail    = "owner@example.com"
	verifierEmail = "verifier@example.com"
	adminEmail    = "admin@example.com"
	readerEmail   = "reader@example.com"
)

func on() *bool { v := true; return &v }

// checker resolves "verifiers" to a persona carrying verify_assets and
// "admin" to the administrator.
func checker(assets *fakeAssets, shares *fakeShares) *access.Checker {
	return access.New(access.Config{
		Assets: assets, Shares: shares,
		AdminRule: func(roles []string) bool { return slices.Contains(roles, "admin") },
		PortalPermissions: func(roles []string) persona.PortalAccess {
			p := &persona.Persona{}
			if slices.Contains(roles, "verifiers") {
				p.Portal = &persona.PortalAccess{VerifyAssets: on()}
			}
			return p.PortalPermissions(nil)
		},
	})
}

func fixture() (*fakeAssets, *fakeVerify, *http.ServeMux) {
	assets := &fakeAssets{rows: map[string]*portaldomain.Asset{
		"a1": {ID: "a1", OwnerID: "u-owner", OwnerEmail: ownerEmail, Name: "Q3", CurrentVersion: 4},
	}}
	everyone := make([]portaldomain.Share, 0, 3)
	for _, e := range []string{verifierEmail, adminEmail, readerEmail} {
		everyone = append(everyone, portaldomain.Share{AssetID: "a1", SharedWithEmail: e, Permission: portaldomain.PermissionViewer})
	}
	shares := &fakeShares{byAsset: map[string][]portaldomain.Share{"a1": everyone}}
	store := &fakeVerify{assets: assets, by: map[string]string{}}
	mux := http.NewServeMux()
	Mount(store, assets)(mux, checker(assets, shares))
	return assets, store, mux
}

func call(mux *http.ServeMux, method, email string, roles []string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, "/api/v1/portal/assets/a1/verification", strings.NewReader(body))
	if email != "" {
		req = req.WithContext(access.ContextWithUser(req.Context(), &access.User{UserID: "u-" + email, Email: email, Roles: roles}))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPermitted(t *testing.T) {
	assets, _, _ := fixture()
	c := checker(assets, &fakeShares{})
	assert.True(t, Permitted(c, &access.User{Roles: []string{"admin"}}), "an administrator")
	assert.True(t, Permitted(c, &access.User{Roles: []string{"verifiers"}}), "a persona carrying verify_assets")
	assert.False(t, Permitted(c, &access.User{Roles: []string{"analyst"}}), "a persona without it")
	assert.False(t, Permitted(nil, &access.User{Roles: []string{"admin"}}))
	assert.False(t, Permitted(c, nil))
}

func TestVerifyNeedsThePermission(t *testing.T) {
	assets, store, mux := fixture()
	rec := call(mux, http.MethodPut, ownerEmail, nil, `{"note":"mine"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code, "an owner without the permission cannot verify their own asset")
	assert.Contains(t, rec.Body.String(), "Can verify assets")
	assert.Nil(t, assets.rows["a1"].Verification)
	assert.Empty(t, store.by)
}

func TestVerifyRefusesWhoCannotOpenIt(t *testing.T) {
	_, _, mux := fixture()
	rec := call(mux, http.MethodPut, "stranger@example.com", []string{"verifiers"}, `{}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "verifying needs the asset to be openable")
	rec = call(mux, http.MethodPut, "", nil, `{}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVerifyRecordsTheCurrentVersion(t *testing.T) {
	_, store, mux := fixture()
	expires := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	rec := call(mux, http.MethodPut, verifierEmail, []string{"verifiers"}, `{"note":"ledger checked","expires_at":"`+expires+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got portaldomain.Asset
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.NotNil(t, got.Verification)
	assert.Equal(t, verifierEmail, got.Verification.VerifiedBy)
	assert.Equal(t, 4, got.Verification.VerifiedVersion, "the version that was current when verified")
	assert.Equal(t, "ledger checked", got.Verification.Note)
	assert.Equal(t, verifierEmail, store.by["a1"])
}

func TestVerifyRefusesAPastExpiryAndABadBody(t *testing.T) {
	_, store, mux := fixture()
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rec := call(mux, http.MethodPut, adminEmail, []string{"admin"}, `{"expires_at":"`+past+`"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "in the future")
	rec = call(mux, http.MethodPut, adminEmail, []string{"admin"}, `{"expires_at":12}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, store.by)

	store.failWrite = errors.New("db down")
	rec = call(mux, http.MethodPut, adminEmail, []string{"admin"}, `{}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "db down")
}

func TestUnverifyByTheVerifierHoldersAndAdmins(t *testing.T) {
	assets, store, mux := fixture()
	// Verified by a person who has since lost the permission.
	store.by["a1"] = readerEmail
	assets.rows["a1"].Verification = &portaldomain.AssetVerification{VerifiedBy: readerEmail}

	rec := call(mux, http.MethodDelete, ownerEmail, nil, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "the owner without the permission cannot remove another's badge")
	require.NotNil(t, assets.rows["a1"].Verification)

	rec = call(mux, http.MethodDelete, readerEmail, nil, "")
	require.Equal(t, http.StatusOK, rec.Code, "the person who verified may remove it")
	assert.Nil(t, assets.rows["a1"].Verification)

	for _, who := range []struct {
		email string
		roles []string
	}{{verifierEmail, []string{"verifiers"}}, {adminEmail, []string{"admin"}}} {
		store.by["a1"] = readerEmail
		rec = call(mux, http.MethodDelete, who.email, who.roles, "")
		assert.Equal(t, http.StatusOK, rec.Code, who.email)
	}
}

func TestUnverifyFailures(t *testing.T) {
	_, store, mux := fixture()
	store.failBy = errors.New("boom")
	rec := call(mux, http.MethodDelete, readerEmail, nil, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "an unreadable verifier grants no removal")

	store.failBy = nil
	store.failWrite = errors.New("db down")
	rec = call(mux, http.MethodDelete, adminEmail, []string{"admin"}, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestMayRemoveMatchesTheVerifierByIDToo(t *testing.T) {
	assets, store, _ := fixture()
	c := checker(assets, &fakeShares{})
	store.by["a1"] = "u-42"
	assert.True(t, MayRemove(context.Background(), store, c, &access.User{UserID: "u-42"}, "a1"))
	assert.False(t, MayRemove(context.Background(), store, c, &access.User{UserID: "u-43"}, "a1"))
	assert.False(t, MayRemove(context.Background(), store, c, &access.User{UserID: "u-42"}, "a2"), "no verification")
}

func TestWriteAssetReadFailureIs500(t *testing.T) {
	assets, store, _ := fixture()
	api := &API{store: store}
	api.gate.Assets = assets
	rec := httptest.NewRecorder()
	api.writeAsset(context.Background(), rec, "missing")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
