package portal

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetstatus"
	"github.com/txn2/mcp-data-platform/internal/portal/assetverify"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// markStatus records the status changes the toolkit writes.
type markStatus struct {
	set     map[string]assetstatus.Change
	cleared []string
	err     error
}

func (m *markStatus) Set(_ context.Context, id string, c assetstatus.Change) error {
	if m.err != nil {
		return m.err
	}
	c, err := c.Normalize()
	if err != nil {
		return fmt.Errorf("fake status store: %w", err)
	}
	m.set[id] = c
	return nil
}

func (m *markStatus) Clear(_ context.Context, id, _ string) (bool, error) {
	m.cleared = append(m.cleared, id)
	return true, m.err
}

func (*markStatus) History(context.Context, string, int) ([]assetstatus.Event, error) {
	return nil, nil
}

// markVerify records the verifications the toolkit writes.
type markVerify struct {
	verified   map[string]assetverify.Verification
	by         map[string]string
	unverified []string
	err        error
}

func (m *markVerify) Verify(_ context.Context, id string, v assetverify.Verification) error {
	if m.err != nil {
		return m.err
	}
	m.verified[id] = v
	m.by[id] = v.Actor
	return nil
}

func (m *markVerify) Unverify(_ context.Context, id, _ string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	m.unverified = append(m.unverified, id)
	return true, nil
}

func (m *markVerify) VerifiedBy(_ context.Context, id string) (string, error) {
	return m.by[id], nil
}

func (*markVerify) ClaimDue(context.Context, time.Time, time.Time, int) ([]assetverify.Due, error) {
	return nil, nil
}

const (
	markOwner    = "owner@example.com"
	markVerifier = "carol@example.com"
	markReader   = "reader@example.com"
)

type markFixture struct {
	tk     *Toolkit
	assets *inMemoryAssetStore
	status *markStatus
	verify *markVerify
}

// newMarkFixture builds a toolkit over one asset the owner owns and the
// share store opens to anyone else, with the persona "verifiers" carrying
// verify_assets.
func newMarkFixture(t *testing.T) *markFixture {
	t.Helper()
	assets := newInMemoryAssetStore()
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID: "a1", OwnerID: "u-owner", OwnerEmail: markOwner, Name: "Q3", CurrentVersion: 5,
	}))
	tk := New(Config{
		Name: "test", AssetStore: assets, VersionStore: newLinkedVersionStore(assets),
		S3Client: &mockS3Client{}, S3Bucket: "bucket", S3Prefix: "assets/",
		ShareStore: &fakeShareStore{assetShare: &portal.Share{AssetID: "a1", Permission: portal.PermissionViewer}},
	})
	f := &markFixture{
		tk: tk, assets: assets,
		status: &markStatus{set: map[string]assetstatus.Change{}},
		verify: &markVerify{verified: map[string]assetverify.Verification{}, by: map[string]string{}},
	}
	tk.SetMarks(Marks{Status: f.status, Verify: f.verify, VerifyAllowed: func(p string) bool { return p == "verifiers" }})
	return f
}

func markCtx(email, persona string, admin bool) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u-" + email, UserEmail: email, PersonaName: persona, IsAdmin: admin,
	})
}

func TestCheckStatus(t *testing.T) {
	for _, tc := range []struct {
		status string
		clear  bool
		ok     bool
	}{
		{"", false, true},
		{"draft", false, true},
		{" Published ", false, true},
		{"deprecated", true, true},
		{"none", true, true},
		{"none", false, false},
		{"archived", false, false},
		{"archived", true, false},
	} {
		got := checkStatus(tc.status, tc.clear)
		assert.Equal(t, tc.ok, got == "", "%q clear=%v: %q", tc.status, tc.clear, got)
		if !tc.ok && tc.clear {
			assert.Contains(t, got, "or none to clear it")
		}
	}
}

func TestApplyStatus(t *testing.T) {
	f := newMarkFixture(t)
	ctx := markCtx(markOwner, "analyst", false)
	assert.Nil(t, f.tk.applyStatus(ctx, "a1", statusArgs{}), "no status writes nothing")
	assert.Empty(t, f.status.set)

	require.Nil(t, f.tk.applyStatus(ctx, "a1", statusArgs{Status: "Deprecated", StatusNote: "see Q4"}))
	assert.Equal(t, assetstatus.Change{Status: "deprecated", Note: "see Q4", Actor: markOwner}, f.status.set["a1"])

	require.Nil(t, f.tk.applyStatus(ctx, "a1", statusArgs{Status: "none"}))
	assert.Equal(t, []string{"a1"}, f.status.cleared)

	res := f.tk.applyStatus(ctx, "a1", statusArgs{Status: "archived"})
	require.NotNil(t, res)
	assert.True(t, res.IsError)

	f.status.err = errors.New("db down")
	res = f.tk.applyStatus(ctx, "a1", statusArgs{Status: "draft"})
	require.NotNil(t, res)
	assert.Contains(t, resultText(t, res), "failed to set the status")

	f.tk.SetMarks(Marks{})
	res = f.tk.applyStatus(ctx, "a1", statusArgs{Status: "draft"})
	require.NotNil(t, res)
	assert.Contains(t, resultText(t, res), errMarksUnavailable)
}

func TestCallerActorFallsBackToTheID(t *testing.T) {
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: "u-9"})
	assert.Equal(t, "u-9", callerActor(ctx), "a caller with no address is recorded under their id")
	assert.Equal(t, anonymousUserName, callerActor(context.Background()))
}

// A verification recorded under "anonymous" is not anyone's to remove: two
// callers with no identity would otherwise match each other.
func TestUnverifyRefusesAnAnonymousMatch(t *testing.T) {
	f := newMarkFixture(t)
	f.verify.by["a1"] = anonymousUserName
	f.tk.shareStore = &fakeShareStore{assetShare: &portal.Share{AssetID: "a1", Permission: portal.PermissionViewer}}
	isErr, text := manage(middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{}), t, f.tk,
		manageAssetInput{Action: actionUnverify, AssetID: "a1"})
	assert.True(t, isErr, text)
	assert.Contains(t, text, "only the person who verified")
	assert.Empty(t, f.verify.unverified)
}

func TestCanVerify(t *testing.T) {
	f := newMarkFixture(t)
	assert.True(t, f.tk.canVerify(markCtx("x@example.com", "analyst", true)), "an administrator")
	assert.True(t, f.tk.canVerify(markCtx("x@example.com", "verifiers", false)), "a persona carrying verify_assets")
	assert.False(t, f.tk.canVerify(markCtx("x@example.com", "analyst", false)))
	assert.False(t, f.tk.canVerify(context.Background()), "no caller")
	f.tk.SetMarks(Marks{Verify: f.verify})
	assert.False(t, f.tk.canVerify(markCtx("x@example.com", "verifiers", false)), "no rule wired allows none but admins")
}

func manage(ctx context.Context, t *testing.T, tk *Toolkit, in manageAssetInput) (isError bool, text string) {
	t.Helper()
	res, _, err := tk.handleManageAsset(ctx, nil, in)
	require.NoError(t, err)
	return res.IsError, resultText(t, res)
}

func TestVerifyAction(t *testing.T) {
	f := newMarkFixture(t)
	verifier := markCtx(markVerifier, "verifiers", false)

	isErr, text := manage(verifier, t, f.tk, manageAssetInput{Action: actionVerify})
	assert.True(t, isErr)
	assert.Contains(t, text, "asset_id is required for the verify action")

	isErr, text = manage(markCtx(markOwner, "analyst", false), t, f.tk, manageAssetInput{Action: actionVerify, AssetID: "a1"})
	assert.True(t, isErr, "the owner without the permission cannot verify their own asset")
	assert.Contains(t, text, "Can verify assets")
	assert.Empty(t, f.verify.verified)

	isErr, _ = manage(verifier, t, f.tk, manageAssetInput{Action: actionVerify, AssetID: "missing"})
	assert.True(t, isErr)

	isErr, text = manage(verifier, t, f.tk, manageAssetInput{Action: actionVerify, AssetID: "a1", ExpiresAt: "soon"})
	assert.True(t, isErr)
	assert.Contains(t, text, "expires_at must be a date")

	isErr, text = manage(verifier, t, f.tk, manageAssetInput{
		Action: actionVerify, AssetID: "a1", Note: "ledger checked", ExpiresAt: "2026-12-31",
	})
	require.False(t, isErr, text)
	got := f.verify.verified["a1"]
	assert.Equal(t, markVerifier, got.Actor)
	assert.Equal(t, 5, got.Version, "the version current when verified")
	assert.Equal(t, "ledger checked", got.Note)
	require.NotNil(t, got.ExpiresAt)
	assert.Equal(t, time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), *got.ExpiresAt)
	assert.Contains(t, text, `"id": "a1"`, "answers with the asset")

	f.verify.err = errors.New("db down")
	isErr, text = manage(verifier, t, f.tk, manageAssetInput{Action: actionVerify, AssetID: "a1"})
	assert.True(t, isErr)
	assert.Contains(t, text, "failed to verify the asset")
}

func TestVerifyActionNotReadable(t *testing.T) {
	f := newMarkFixture(t)
	f.tk.shareStore = &fakeShareStore{}
	isErr, text := manage(markCtx("stranger@example.com", "verifiers", false), t, f.tk, manageAssetInput{Action: actionVerify, AssetID: "a1"})
	assert.True(t, isErr)
	assert.Contains(t, text, "you do not have access to this asset")
	assert.Empty(t, f.verify.verified)
}

func TestVerifyActionsUnavailable(t *testing.T) {
	f := newMarkFixture(t)
	f.tk.SetMarks(Marks{})
	for _, action := range []string{actionVerify, actionUnverify} {
		isErr, text := manage(markCtx(markOwner, "", true), t, f.tk, manageAssetInput{Action: action, AssetID: "a1"})
		assert.True(t, isErr)
		assert.Contains(t, text, errMarksUnavailable)
	}
}

func TestUnverifyAction(t *testing.T) {
	f := newMarkFixture(t)
	f.verify.by["a1"] = markReader

	isErr, text := manage(markCtx(markReader, "", false), t, f.tk, manageAssetInput{Action: actionUnverify})
	assert.True(t, isErr)
	assert.Contains(t, text, "asset_id is required for the unverify action")

	isErr, text = manage(markCtx(markOwner, "analyst", false), t, f.tk, manageAssetInput{Action: actionUnverify, AssetID: "a1"})
	assert.True(t, isErr, "another person without the permission cannot remove it")
	assert.Contains(t, text, "only the person who verified")
	assert.Empty(t, f.verify.unverified)

	isErr, text = manage(markCtx(markReader, "", false), t, f.tk, manageAssetInput{Action: actionUnverify, AssetID: "a1"})
	require.False(t, isErr, "the person who verified may remove it: %s", text)
	assert.Equal(t, []string{"a1"}, f.verify.unverified)

	isErr, _ = manage(markCtx(markVerifier, "verifiers", false), t, f.tk, manageAssetInput{Action: actionUnverify, AssetID: "a1"})
	assert.False(t, isErr, "a holder of the permission may remove it")

	isErr, _ = manage(markCtx(markVerifier, "verifiers", false), t, f.tk, manageAssetInput{Action: actionUnverify, AssetID: "missing"})
	assert.True(t, isErr)

	f.verify.err = errors.New("db down")
	isErr, text = manage(markCtx(markVerifier, "verifiers", false), t, f.tk, manageAssetInput{Action: actionUnverify, AssetID: "a1"})
	assert.True(t, isErr)
	assert.Contains(t, text, "failed to remove the verification")
}

func TestMarkedAssetReadFailure(t *testing.T) {
	f := newMarkFixture(t)
	res, _, err := f.tk.markedAsset(context.Background(), "missing")
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestParseExpiry(t *testing.T) {
	got, err := parseExpiry("  ")
	require.NoError(t, err)
	assert.Nil(t, got, "empty never expires")

	got, err = parseExpiry("2026-12-01T10:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC), *got)

	got, err = parseExpiry("2026-12-31")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), *got, "a date expires at the end of that day")

	_, err = parseExpiry("next tuesday")
	require.Error(t, err)
}

func TestSaveAssetWithAStatusSetsItAfterCreating(t *testing.T) {
	f := newMarkFixture(t)
	ctx := markCtx(markOwner, "analyst", false)
	res, _, err := f.tk.handleSaveAsset(ctx, nil, saveAssetInput{
		Name: "Draft report", Content: "# Draft", ContentType: "text/markdown",
		Status: "draft",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(t, res))
	id, _ := decodeResult(t, res)["asset_id"].(string)
	require.NotEmpty(t, id)
	assert.Equal(t, "draft", f.status.set[id].Status)
	assert.Equal(t, markOwner, f.status.set[id].Actor)
}

func TestSaveAssetWithoutAStatusLeavesItNone(t *testing.T) {
	f := newMarkFixture(t)
	res, _, err := f.tk.handleSaveAsset(markCtx(markOwner, "", false), nil, saveAssetInput{
		Name: "Report", Content: "# R", ContentType: "text/markdown",
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	assert.Empty(t, f.status.set)
}

func TestSaveAssetRefusesAnUnknownStatusBeforeWriting(t *testing.T) {
	f := newMarkFixture(t)
	before := len(f.assets.assets)
	res, _, err := f.tk.handleSaveAsset(markCtx(markOwner, "", false), nil, saveAssetInput{
		Name: "Report", Content: "# R", ContentType: "text/markdown", Status: "none",
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "draft, published or deprecated")
	assert.Len(t, f.assets.assets, before, "no asset was created")
	assert.Empty(t, f.status.set)
}

func TestUpdateWithAStatusAlone(t *testing.T) {
	f := newMarkFixture(t)
	ctx := markCtx(markOwner, "", false)
	ctx = middleware.WithPlatformContext(ctx, &middleware.PlatformContext{UserID: "u-owner", UserEmail: markOwner})

	isErr, text := manage(ctx, t, f.tk, manageAssetInput{Action: actionUpdate, AssetID: "a1", Status: "published"})
	require.False(t, isErr, "a status alone is a field to update: %s", text)
	assert.Equal(t, "published", f.status.set["a1"].Status)

	isErr, _ = manage(ctx, t, f.tk, manageAssetInput{Action: actionUpdate, AssetID: "a1", Status: "none"})
	require.False(t, isErr)
	assert.Equal(t, []string{"a1"}, f.status.cleared)

	isErr, text = manage(ctx, t, f.tk, manageAssetInput{Action: actionUpdate, AssetID: "a1", Status: "archived"})
	assert.True(t, isErr)
	assert.Contains(t, text, "or none to clear it")

	isErr, text = manage(ctx, t, f.tk, manageAssetInput{Action: actionUpdate, AssetID: "a1"})
	assert.True(t, isErr)
	assert.Contains(t, text, "references, or status")
}

func TestManageAssetSchemaOffersTheVerifyActions(t *testing.T) {
	for _, want := range []string{`"verify"`, `"unverify"`, `"status_note"`, `"expires_at"`} {
		assert.Contains(t, string(manageAssetSchemaBase), want)
	}
	assert.Contains(t, string(saveAssetSchema), `"status"`)
	assert.Equal(t, portaldomain.StatusNone, "none")
}
