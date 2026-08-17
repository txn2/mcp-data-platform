package portal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

const shareAssetID = "asset_1"

// --- fakes ------------------------------------------------------------------

// recordingShareStore keeps the shares it is given so a create can be read back
// through list_shares and killed through revoke_share, which is the round trip
// the actions have to hold up under.
type recordingShareStore struct {
	fakeShareStore
	shares    []portal.Share
	insertErr error
	listErr   error
	revokeErr error
	getErr    error
}

func (s *recordingShareStore) Insert(_ context.Context, share portal.Share) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.shares = append(s.shares, share)
	return nil
}

func (s *recordingShareStore) ListByAsset(_ context.Context, assetID string) ([]portal.Share, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	var out []portal.Share
	for _, sh := range s.shares {
		if sh.AssetID == assetID {
			out = append(out, sh)
		}
	}
	return out, nil
}

func (s *recordingShareStore) GetByID(_ context.Context, id string) (*portal.Share, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	for i := range s.shares {
		if s.shares[i].ID == id {
			return &s.shares[i], nil
		}
	}
	return nil, errors.New("share not found")
}

func (s *recordingShareStore) Revoke(_ context.Context, id string) error {
	if s.revokeErr != nil {
		return s.revokeErr
	}
	for i := range s.shares {
		if s.shares[i].ID == id {
			s.shares[i].Revoked = true
			return nil
		}
	}
	return errors.New("share not found")
}

// recordingShareNotifier captures the share notification the toolkit fires.
type recordingShareNotifier struct {
	calls  int
	share  portal.Share
	event  portal.ShareEvent
	thread int
}

func (n *recordingShareNotifier) NotifyShare(_ context.Context, share *portal.Share, ev portal.ShareEvent) {
	n.calls++
	n.share, n.event = *share, ev
}

func (n *recordingShareNotifier) NotifyThreadEvent(context.Context, *portal.Thread, string, string, []string) {
	n.thread++
}

// stubDirectory answers directory lookups from a fixed roster, applying the
// same case-insensitive substring match over email and both name fields that
// the Postgres store's buildUserWhere performs.
type stubDirectory struct {
	people  []user.User
	err     error
	errAt   int // 1-based call number the error starts at (0 = from the first)
	queries []string
}

func (d *stubDirectory) List(_ context.Context, filter user.Filter) ([]user.User, int, error) {
	d.queries = append(d.queries, filter.Query)
	if d.err != nil && len(d.queries) >= max(d.errAt, 1) {
		return nil, 0, d.err
	}
	var out []user.User
	for _, p := range d.people {
		if matchesDirectoryQuery(p, filter.Query) {
			out = append(out, p)
		}
	}
	return out, len(out), nil
}

func matchesDirectoryQuery(p user.User, query string) bool {
	if query == "" {
		return true
	}
	q := strings.ToLower(query)
	return strings.Contains(strings.ToLower(p.Email), q) ||
		strings.Contains(strings.ToLower(p.FirstName), q) ||
		strings.Contains(strings.ToLower(p.LastName), q)
}

// countingDirectory returns one page over a larger total, the shape a directory
// takes when the caller's query matches more people than a page holds.
type countingDirectory struct {
	page  []user.User
	total int
}

func (d *countingDirectory) List(context.Context, user.Filter) ([]user.User, int, error) {
	return d.page, d.total, nil
}

// --- helpers ----------------------------------------------------------------

// shareToolkit builds a toolkit owning one asset, over the given share store
// and directory.
func shareToolkit(t *testing.T, shares portal.ShareStore, dir DirectoryReader) *Toolkit {
	t.Helper()
	assets := newInMemoryAssetStore()
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID: shareAssetID, OwnerID: ownerID, OwnerEmail: ownerEmail, Name: "Q3 Revenue",
	}))
	return New(Config{
		Name:       "test",
		S3Bucket:   "b",
		BaseURL:    "https://platform.example.com",
		AssetStore: assets,
		ShareStore: shares,
		Directory:  dir,
	})
}

// callManage runs one manage_asset action the way the MCP handler does.
func callManage(ctx context.Context, t *testing.T, tk *Toolkit, input manageAssetInput) *mcp.CallToolResult {
	t.Helper()
	res, _, err := tk.handleManageAsset(ctx, nil, input)
	require.NoError(t, err)
	require.NotNil(t, res)
	return res
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

// --- share: recipient form --------------------------------------------------

// The headline case: "share this asset with John" resolves one directory match,
// creates a restricted viewer share addressed to John, mails him, and hands
// back the view URL.
func TestShareByName(t *testing.T) {
	shares := &recordingShareStore{}
	dir := &stubDirectory{people: []user.User{
		{Email: "john.smith@example.com", FirstName: "John", LastName: "Smith"},
		{Email: "maria.garcia@example.com", FirstName: "Maria", LastName: "Garcia"},
	}}
	tk := shareToolkit(t, shares, dir)
	notifier := &recordingShareNotifier{}
	tk.SetFeedbackNotifications(notifier, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "John",
	})
	assert.False(t, res.IsError, resultText(t, res))

	out := decodeResult(t, res)
	assert.Equal(t, "john.smith@example.com", out["shared_with"])
	assert.Equal(t, "viewer", out["permission"])
	assert.Equal(t, "restricted", out["access_mode"])
	assert.Equal(t, true, out["notified"])
	assert.Nil(t, out["expires_at"])

	require.Len(t, shares.shares, 1)
	stored := shares.shares[0]
	assert.Equal(t, shareAssetID, stored.AssetID)
	assert.Equal(t, ownerEmail, stored.CreatedBy)
	assert.Equal(t, "https://platform.example.com/portal/view/"+stored.Token, out["share_url"])

	require.Equal(t, 1, notifier.calls)
	assert.Equal(t, "john.smith@example.com", notifier.share.SharedWithEmail)
	assert.Equal(t, "asset", notifier.event.Kind)
	assert.Equal(t, "Q3 Revenue", notifier.event.ItemTitle)
}

// A full name reaches the right person even though the directory matches one
// column at a time and so never matches "John Smith" outright. Neither the
// other John nor the other Smith is picked up by the retry.
func TestShareByFullName(t *testing.T) {
	shares := &recordingShareStore{}
	dir := &stubDirectory{people: []user.User{
		{Email: "john.smith@example.com", FirstName: "John", LastName: "Smith"},
		{Email: "john.baker@example.com", FirstName: "John", LastName: "Baker"},
		{Email: "alice.smith@example.com", FirstName: "Alice", LastName: "Smith"},
	}}
	tk := shareToolkit(t, shares, dir)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "John Smith",
	})
	assert.False(t, res.IsError, resultText(t, res))
	require.Len(t, shares.shares, 1)
	assert.Equal(t, "john.smith@example.com", shares.shares[0].SharedWithEmail)
}

// An ambiguous name names the candidates and creates nothing: guessing between
// two people would hand the asset to the wrong one.
func TestShareAmbiguousName(t *testing.T) {
	shares := &recordingShareStore{}
	dir := &stubDirectory{people: []user.User{
		{Email: "john.smith@example.com", FirstName: "John", LastName: "Smith"},
		{Email: "john.baker@example.com", FirstName: "John", LastName: "Baker"},
	}}
	tk := shareToolkit(t, shares, dir)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "John",
	})
	assert.True(t, res.IsError)
	text := resultText(t, res)
	assert.Contains(t, text, "john.smith@example.com")
	assert.Contains(t, text, "john.baker@example.com")
	assert.Empty(t, shares.shares)
}

// A directory page cut short cannot report its count as the answer: saying
// "matches 2 people" when the store holds more would be a truncation reported
// as a total.
func TestShareAmbiguousNameReportsTruncation(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, &countingDirectory{
		page: []user.User{
			{Email: "john.smith@example.com", FirstName: "John", LastName: "Smith"},
			{Email: "john.baker@example.com", FirstName: "John", LastName: "Baker"},
		},
		total: 40,
	})

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "John",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "(2 or more)")
	assert.Empty(t, shares.shares)
}

func TestShareUnknownName(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, &stubDirectory{})

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "Nobody",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "Nobody")
	assert.Empty(t, shares.shares)
}

// An address is taken as written: the directory is a convenience for naming
// people, not an allowlist of who may be shared with.
func TestShareByEmailSkipsDirectory(t *testing.T) {
	shares := &recordingShareStore{}
	dir := &stubDirectory{}
	tk := shareToolkit(t, shares, dir)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "New Person <New.Person@Example.com>",
		Permission: string(portal.PermissionEditor),
	})
	assert.False(t, res.IsError, resultText(t, res))
	require.Len(t, shares.shares, 1)
	assert.Equal(t, "new.person@example.com", shares.shares[0].SharedWithEmail)
	assert.Equal(t, portal.PermissionEditor, shares.shares[0].Permission)
	assert.Empty(t, dir.queries)
}

// A recipient that is present but blank is refused: reading it as "no
// recipient" would turn a share meant for one person into a link any signed-in
// user can open.
func TestShareBlankRecipient(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "   ",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "recipient is blank")
	assert.Empty(t, shares.shares)
}

// Without a directory a name cannot be resolved, and the refusal says what to
// give instead rather than silently creating a link share.
func TestShareByNameWithoutDirectory(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "John",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "email address")
	assert.Empty(t, shares.shares)
}

func TestShareDirectoryError(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, &stubDirectory{err: errors.New("boom")})

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "John",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "user directory")
	assert.Empty(t, shares.shares)
}

// The full-name retry queries the directory a second time, and a failure there
// is reported rather than read as "nobody matches".
func TestShareDirectoryErrorOnRetry(t *testing.T) {
	shares := &recordingShareStore{}
	dir := &stubDirectory{err: errors.New("boom"), errAt: 2}
	tk := shareToolkit(t, shares, dir)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "John Smith",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "user directory")
	assert.Len(t, dir.queries, 2)
	assert.Empty(t, shares.shares)
}

// A share created where nothing mails it must not report that anyone was told.
func TestShareWithoutNotifier(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "john@example.com",
	})
	assert.False(t, res.IsError, resultText(t, res))
	out := decodeResult(t, res)
	assert.Equal(t, false, out["notified"])
	assert.NotContains(t, out["message"], "emailed")
}

// --- share: link form -------------------------------------------------------

func TestShareLinkDefaultsToAuthenticatedAndNeverExpires(t *testing.T) {
	shares := &recordingShareStore{}
	notifier := &recordingShareNotifier{}
	tk := shareToolkit(t, shares, nil)
	tk.SetFeedbackNotifications(notifier, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionShare, AssetID: shareAssetID})
	assert.False(t, res.IsError, resultText(t, res))

	out := decodeResult(t, res)
	assert.Equal(t, "authenticated", out["access_mode"])
	assert.Equal(t, "viewer", out["permission"])
	assert.Nil(t, out["expires_at"])
	assert.Empty(t, out["shared_with"])
	assert.Equal(t, false, out["notified"])
	assert.Zero(t, notifier.calls, "a link addresses nobody, so nobody is mailed")
}

func TestSharePublicLinkRequiresExpiry(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, AccessMode: string(portal.AccessModePublic),
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "expires_in is required")
	assert.Empty(t, shares.shares)
}

func TestShareAuthenticatedLinkRefusesExpiry(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, ExpiresIn: "24h",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "expires_in does not apply")
	assert.Empty(t, shares.shares)
}

func TestSharePublicLinkWithExpiry(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID,
		AccessMode: string(portal.AccessModePublic), ExpiresIn: "24h",
	})
	assert.False(t, res.IsError, resultText(t, res))
	require.Len(t, shares.shares, 1)
	require.NotNil(t, shares.shares[0].ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), *shares.shares[0].ExpiresAt, time.Minute)
	assert.Contains(t, decodeResult(t, res)["message"], "expires at")
}

// A link admits an audience the creator never enumerated, so it can never be an
// editor grant however the call asked.
func TestShareLinkForcedToViewer(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Permission: string(portal.PermissionEditor),
	})
	assert.False(t, res.IsError, resultText(t, res))
	require.Len(t, shares.shares, 1)
	assert.Equal(t, portal.PermissionViewer, shares.shares[0].Permission)
}

func TestShareInvalidPermission(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "john@example.com", Permission: "admin",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "viewer or editor")
	assert.Empty(t, shares.shares)
}

func TestShareStoreFailure(t *testing.T) {
	tk := shareToolkit(t, &recordingShareStore{insertErr: errors.New("db down")}, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionShare, AssetID: shareAssetID})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "failed to create share")
}

// A deployment with no public base URL still mints the share; it just has no
// URL to hand back, and must not put a bare token in a URL field.
func TestShareWithoutBaseURL(t *testing.T) {
	shares := &recordingShareStore{}
	assets := newInMemoryAssetStore()
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{ID: shareAssetID, OwnerID: ownerID}))
	tk := New(Config{Name: "test", AssetStore: assets, ShareStore: shares})

	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionShare, AssetID: shareAssetID})
	assert.False(t, res.IsError, resultText(t, res))
	assert.Empty(t, decodeResult(t, res)["share_url"])
	require.Len(t, shares.shares, 1)
	assert.NotEmpty(t, shares.shares[0].Token)
}

// --- authorization ----------------------------------------------------------

func TestShareActionsRefuseNonOwner(t *testing.T) {
	shares := &recordingShareStore{shares: []portal.Share{{
		ID: "share_1", AssetID: shareAssetID, Token: "tok", SharedWithEmail: "john@example.com",
	}}}
	tk := shareToolkit(t, shares, nil)

	for _, input := range []manageAssetInput{
		{Action: actionShare, AssetID: shareAssetID, Recipient: "john@example.com"},
		{Action: actionListShares, AssetID: shareAssetID},
		{Action: actionRevokeShare, ShareID: "share_1"},
	} {
		res := callManage(strangerCtx(), t, tk, input)
		assert.True(t, res.IsError, input.Action)
		assert.Contains(t, resultText(t, res), "only the owner", input.Action)
	}
	assert.Len(t, shares.shares, 1)
	assert.False(t, shares.shares[0].Revoked)
}

// An unauthenticated caller shares the "anonymous" owner sentinel with every
// other unauthenticated session, so it is refused outright rather than matched.
func TestShareActionsRefuseAnonymous(t *testing.T) {
	shares := &recordingShareStore{shares: []portal.Share{{ID: "share_1", AssetID: shareAssetID}}}
	assets := newInMemoryAssetStore()
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID: shareAssetID, OwnerID: anonymousUserName,
	}))
	tk := New(Config{Name: "test", AssetStore: assets, ShareStore: shares, BaseURL: "https://example.com"})

	for _, input := range []manageAssetInput{
		{Action: actionShare, AssetID: shareAssetID, Recipient: "john@example.com"},
		{Action: actionListShares, AssetID: shareAssetID},
		{Action: actionRevokeShare, ShareID: "share_1"},
	} {
		res := callManage(context.Background(), t, tk, input)
		assert.True(t, res.IsError, input.Action)
		assert.Contains(t, resultText(t, res), "authenticated user", input.Action)
	}
	assert.Len(t, shares.shares, 1)
}

// An admin is unrestricted, as everywhere else in the platform.
func TestShareAsAdmin(t *testing.T) {
	admin := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "admin", UserEmail: "admin@example.com", IsAdmin: true,
	})
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, nil)

	res := callManage(admin, t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "john@example.com",
	})
	assert.False(t, res.IsError, resultText(t, res))
	require.Len(t, shares.shares, 1)
	assert.Equal(t, "admin@example.com", shares.shares[0].CreatedBy)
}

func TestShareMissingAssetID(t *testing.T) {
	tk := shareToolkit(t, &recordingShareStore{}, nil)
	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionShare})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "asset_id is required")
}

func TestShareUnknownAsset(t *testing.T) {
	tk := shareToolkit(t, &recordingShareStore{}, nil)
	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionShare, AssetID: "missing"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "asset not found")
}

func TestShareDeletedAsset(t *testing.T) {
	deleted := time.Now()
	assets := newInMemoryAssetStore()
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID: shareAssetID, OwnerID: ownerID, DeletedAt: &deleted,
	}))
	shares := &recordingShareStore{}
	tk := New(Config{Name: "test", AssetStore: assets, ShareStore: shares})

	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionShare, AssetID: shareAssetID})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "deleted")
	assert.Empty(t, shares.shares)
}

// --- list_shares / revoke_share --------------------------------------------

// The round trip the actions exist for: a created share is listed, and after a
// revoke it is gone from the list and dead in the store.
func TestShareListRevokeRoundTrip(t *testing.T) {
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, nil)

	created := decodeResult(t, callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionShare, AssetID: shareAssetID, Recipient: "john@example.com",
	}))
	shareID, ok := created["share_id"].(string)
	require.True(t, ok)

	listed := decodeResult(t, callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionListShares, AssetID: shareAssetID,
	}))
	assert.Equal(t, float64(1), listed[fieldTotal])
	rows, ok := listed["shares"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 1)
	row, ok := rows[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, shareID, row["share_id"])
	assert.Equal(t, "john@example.com", row["shared_with"])
	assert.Contains(t, row["share_url"], "/portal/view/")

	revoked := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionRevokeShare, ShareID: shareID,
	})
	assert.False(t, revoked.IsError, resultText(t, revoked))
	assert.Contains(t, decodeResult(t, revoked)[fieldMessage], "john@example.com")

	after := decodeResult(t, callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionListShares, AssetID: shareAssetID,
	}))
	assert.Equal(t, float64(0), after[fieldTotal])
	require.Len(t, shares.shares, 1)
	assert.True(t, shares.shares[0].Revoked)
}

// list_shares answers who has access, so a share that no longer opens the asset
// is not one of them.
func TestListSharesOmitsDeadShares(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	shares := &recordingShareStore{shares: []portal.Share{
		{ID: "live", AssetID: shareAssetID, Token: "t1", SharedWithEmail: "live@example.com"},
		{ID: "revoked", AssetID: shareAssetID, Token: "t2", Revoked: true},
		{ID: "expired", AssetID: shareAssetID, Token: "t3", ExpiresAt: &past},
		{ID: "future", AssetID: shareAssetID, Token: "t4", ExpiresAt: &future},
		{ID: "other-asset", AssetID: "asset_2", Token: "t5"},
	}}
	tk := shareToolkit(t, shares, nil)

	out := decodeResult(t, callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionListShares, AssetID: shareAssetID,
	}))
	assert.Equal(t, float64(2), out[fieldTotal])
	rows, ok := out["shares"].([]any)
	require.True(t, ok)
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		row, rowOK := r.(map[string]any)
		require.True(t, rowOK)
		id, idOK := row["share_id"].(string)
		require.True(t, idOK)
		ids = append(ids, id)
	}
	assert.ElementsMatch(t, []string{"live", "future"}, ids)
}

func TestListSharesEmpty(t *testing.T) {
	tk := shareToolkit(t, &recordingShareStore{}, nil)
	out := decodeResult(t, callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionListShares, AssetID: shareAssetID,
	}))
	assert.Equal(t, float64(0), out[fieldTotal])
	assert.Empty(t, out["shares"])
}

func TestListSharesStoreFailure(t *testing.T) {
	tk := shareToolkit(t, &recordingShareStore{listErr: errors.New("db down")}, nil)
	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionListShares, AssetID: shareAssetID})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "failed to list shares")
}

func TestRevokeShareMissingID(t *testing.T) {
	tk := shareToolkit(t, &recordingShareStore{}, nil)
	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionRevokeShare})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "share_id is required")
}

func TestRevokeShareUnknownID(t *testing.T) {
	tk := shareToolkit(t, &recordingShareStore{}, nil)
	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionRevokeShare, ShareID: "nope"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "share not found")
}

// A collection or prompt share is somebody else's surface; revoking it here
// would run the asset ownership check against an asset that does not exist.
func TestRevokeShareRejectsNonAssetShare(t *testing.T) {
	shares := &recordingShareStore{shares: []portal.Share{{ID: "c1", CollectionID: "coll_1"}}}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionRevokeShare, ShareID: "c1"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "collection or a prompt")
	assert.False(t, shares.shares[0].Revoked)
}

func TestRevokeShareStoreFailure(t *testing.T) {
	shares := &recordingShareStore{
		shares:    []portal.Share{{ID: "share_1", AssetID: shareAssetID}},
		revokeErr: errors.New("db down"),
	}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionRevokeShare, ShareID: "share_1"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "failed to revoke share")
}

func TestRevokeLinkShareMessage(t *testing.T) {
	shares := &recordingShareStore{shares: []portal.Share{{ID: "share_1", AssetID: shareAssetID}}}
	tk := shareToolkit(t, shares, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: actionRevokeShare, ShareID: "share_1"})
	assert.False(t, res.IsError, resultText(t, res))
	assert.Contains(t, decodeResult(t, res)[fieldMessage], "Revoked the link")
}

// --- through a real MCP session ---------------------------------------------

// The unit tests above call the dispatcher directly, which skips the schema the
// tool actually advertises: an argument the handler reads but the schema does
// not declare is refused by the SDK before the handler runs, and additional
// properties are forbidden. This drives share, list_shares and revoke_share
// over a real client session so the wire arguments are the ones that work.
func TestShareActionsOverMCPSession(t *testing.T) {
	ctx := ownerCtx()
	shares := &recordingShareStore{}
	tk := shareToolkit(t, shares, &stubDirectory{people: []user.User{
		{Email: "john.smith@example.com", FirstName: "John", LastName: "Smith"},
	}})

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	tk.RegisterTools(server)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSess, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer func() { _ = serverSess.Close() }()
	clientSess, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil).
		Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = clientSess.Close() }()

	call := func(args map[string]any) map[string]any {
		t.Helper()
		res, callErr := clientSess.CallTool(ctx, &mcp.CallToolParams{Name: ManageToolName, Arguments: args})
		require.NoError(t, callErr)
		require.False(t, res.IsError, resultText(t, res))
		return decodeResult(t, res)
	}

	created := call(map[string]any{
		"action": actionShare, "asset_id": shareAssetID, "recipient": "John", "permission": "editor",
	})
	assert.Equal(t, "john.smith@example.com", created["shared_with"])
	assert.Equal(t, "editor", created["permission"])
	shareID, ok := created["share_id"].(string)
	require.True(t, ok)

	listed := call(map[string]any{"action": actionListShares, "asset_id": shareAssetID})
	assert.Equal(t, float64(1), listed[fieldTotal])

	call(map[string]any{"action": actionRevokeShare, "share_id": shareID})
	after := call(map[string]any{"action": actionListShares, "asset_id": shareAssetID})
	assert.Equal(t, float64(0), after[fieldTotal])

	link := call(map[string]any{
		"action": actionShare, "asset_id": shareAssetID,
		"access_mode": "public", "expires_in": "24h",
	})
	assert.Equal(t, "public", link["access_mode"])
	assert.NotEmpty(t, link["expires_at"])

	// The schema forbids arguments it does not declare, which is what makes
	// the calls above a real check of it: a field the handler reads but the
	// schema omits never reaches the handler at all.
	refused, err := clientSess.CallTool(ctx, &mcp.CallToolParams{Name: ManageToolName, Arguments: map[string]any{
		"action": actionShare, "asset_id": shareAssetID, "shared_with_email": "john@example.com",
	}})
	require.NoError(t, err)
	require.True(t, refused.IsError)
	assert.Contains(t, resultText(t, refused), "additional properties")
}

// --- helpers over the directory ---------------------------------------------

func TestLongestWord(t *testing.T) {
	assert.Equal(t, "smith", longestWord([]string{"jo", "smith", "b"}))
	assert.Equal(t, "solo", longestWord([]string{"solo"}))
}

func TestDescribePeopleSortsAndFallsBackToEmail(t *testing.T) {
	described := describePeople([]user.User{
		{Email: "zeta@example.com"},
		{Email: "alpha@example.com", FirstName: "Alpha", LastName: "Andersson"},
	})
	assert.Equal(t, []string{"Alpha Andersson <alpha@example.com>", "zeta@example.com"}, described)
}
