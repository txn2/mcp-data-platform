package httpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
	trinotoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// TestAssetVisibleTo is the decision the table routes make before anything
// else: an asset belongs to one person, and registering publishes its contents
// into a shared schema, so this is owner authority the way sharing is.
func TestAssetVisibleTo(t *testing.T) {
	asset := portal.Asset{ID: "a1", OwnerID: "u1", OwnerEmail: "alice@example.com"}
	admins := []string{"admin"}

	assert.True(t, assetVisibleTo(asset,
		&portal.User{UserID: "u1", Email: "alice@example.com"}, admins), "the owner")

	// An administrator is unrestricted by design, everywhere.
	assert.True(t, assetVisibleTo(asset,
		&portal.User{UserID: "u9", Email: "root@example.com", Roles: []string{"admin"}}, admins),
		"an administrator reaches every asset")

	assert.False(t, assetVisibleTo(asset,
		&portal.User{UserID: "u2", Email: "bob@example.com"}, admins), "another person")

	assert.False(t, assetVisibleTo(asset, nil, admins), "no caller")
}

// TestAssetVisibleTo_MatchesOnlyOnANonEmptyIdentity. An unauthenticated
// request and an asset that recorded no owner are not the same person, and a
// match on two empty strings would hand every unattributed asset to anybody.
func TestAssetVisibleTo_MatchesOnlyOnANonEmptyIdentity(t *testing.T) {
	unowned := portal.Asset{ID: "a1"}
	assert.False(t, assetVisibleTo(unowned, &portal.User{}, nil))
	assert.False(t, assetVisibleTo(unowned, &portal.User{UserID: "u1"}, nil))

	// The address match holds only when both sides carry one.
	byEmail := portal.Asset{ID: "a1", OwnerEmail: "alice@example.com"}
	assert.False(t, assetVisibleTo(byEmail, &portal.User{UserID: "u2"}, nil))
	assert.True(t, assetVisibleTo(byEmail, &portal.User{Email: "ALICE@example.com"}, nil),
		"an address is matched case-insensitively, as it is everywhere else")
}

func TestHasAnyRoleIn(t *testing.T) {
	assert.True(t, hasAnyRoleIn([]string{"analyst", "admin"}, []string{"admin"}))
	assert.False(t, hasAnyRoleIn([]string{"analyst"}, []string{"admin"}))
	assert.False(t, hasAnyRoleIn(nil, []string{"admin"}))
	assert.False(t, hasAnyRoleIn([]string{"admin"}, nil),
		"a deployment with no admin persona makes nobody an administrator")
}

// TestNewRegistrationID mints an opaque id with the prefix every other id on
// the platform carries.
func TestNewRegistrationID(t *testing.T) {
	first, err := newRegistrationID()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(first, "reg_"))
	assert.Len(t, first, len("reg_")+newIDLength*2)

	second, err := newRegistrationID()
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
}

// --- objectReaderAdapter ---

// fakeBlobs stands in for the shared S3 adapter.
type fakeBlobs struct {
	body      []byte
	entries   []s3adapter.ObjectEntry
	truncated bool
	err       error
}

func (f *fakeBlobs) GetObject(context.Context, string, string) (body []byte, contentType string, err error) {
	if f.err != nil {
		return nil, "", f.err
	}
	return f.body, "text/csv", nil
}

func (f *fakeBlobs) ListDirectory(
	context.Context, string, string,
) (entries []s3adapter.ObjectEntry, truncated bool, err error) {
	return f.entries, f.truncated, f.err
}

// TestObjectReaderAdapter is the whole of the impedance between the shared S3
// adapter's listing shape and the registrar's, including the truncation flag,
// which must never be read as "nothing else is there".
func TestObjectReaderAdapter(t *testing.T) {
	blobs := &fakeBlobs{
		body: []byte("a,b\n"),
		entries: []s3adapter.ObjectEntry{
			{Key: "d/content.csv", Size: 128},
			{Key: "d/notes.txt", Size: 12},
		},
	}
	adapter := objectReaderAdapter{client: blobs}

	body, ct, err := adapter.GetObject(context.Background(), "b", "d/content.csv")
	require.NoError(t, err)
	assert.Equal(t, "a,b\n", string(body))
	assert.Equal(t, "text/csv", ct)

	got, truncated, err := adapter.ListDirectory(context.Background(), "b", "d/")
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, []tableregister.ObjectEntry{
		{Key: "d/content.csv", Size: 128},
		{Key: "d/notes.txt", Size: 12},
	}, got)

	blobs.truncated = true
	_, truncated, err = adapter.ListDirectory(context.Background(), "b", "d/")
	require.NoError(t, err)
	assert.True(t, truncated, "a page boundary is reported, never read as the end")
}

func TestObjectReaderAdapter_Errors(t *testing.T) {
	boom := errors.New("s3 unreachable")
	adapter := objectReaderAdapter{client: &fakeBlobs{err: boom}}

	_, _, err := adapter.GetObject(context.Background(), "b", "k")
	assert.ErrorIs(t, err, boom)

	_, _, err = adapter.ListDirectory(context.Background(), "b", "d/")
	assert.ErrorIs(t, err, boom)
}

// TestTableCleanupHooks_UnwiredIsNil so a delete on a deployment that cannot
// register calls nothing rather than a hook that panics.
func TestTableCleanupHooks_UnwiredIsNil(t *testing.T) {
	hooks := tableCleanupHooks(nil)
	assert.Nil(t, hooks.AssetDeleted)
	assert.Nil(t, hooks.ResourceDeleted)
}

// TestBuildTableRegistrar_NoPlatformIsUnavailable: every gate the composition
// root applies before offering the action at all.
func TestBuildTableRegistrar_NoPlatformIsUnavailable(t *testing.T) {
	assert.Nil(t, buildTableRegistrar(nil))
	assert.False(t, buildTableRegistrar(nil).Available())
}

// TestWireTableLookup_NoRouterIsANoop covers the stdio shape, where there is
// no search federation to hand a lookup to.
func TestWireTableLookup_NoRouterIsANoop(t *testing.T) {
	assert.NotPanics(t, func() { wireTableLookup(nil, nil) })
}

// TestMountTableAPI_UnwiredMountsNothing: the routes are absent rather than
// present and always refusing, which is what lets the portal hide the action.
func TestMountTableAPI_UnwiredMountsNothing(t *testing.T) {
	assert.NotPanics(t, func() { mountTableAPI(nil, nil, nil, nil) })
	assert.NotPanics(t, func() { wireTableToolRegistrar(nil, nil) })
}

// TestErrorsIsUsableAcrossThePackages pins that a refusal keeps its identity
// through the wiring layer, which is what the HTTP status mapping reads.
func TestRefusalIdentitySurvivesWrapping(t *testing.T) {
	wrapped := errors.Join(tableregister.ErrRefused, errors.New("context"))
	assert.ErrorIs(t, wrapped, tableregister.ErrRefused)
}

// --- the connection picker ---

// pickerTrino answers which connections carry a scratch target.
type pickerTrino struct {
	targets map[string]trinotoolkit.ScratchConfig
}

func (pickerTrino) Exec(context.Context, string, string) error { return nil }

func (p pickerTrino) ScratchTarget(name string) (trinotoolkit.ScratchConfig, bool) {
	t, ok := p.targets[name]
	return t, ok
}

// TestScratchConnectionChoices is the picker's whole rule: a choice it offers
// must be one the registrar accepts. A connection the caller reaches but that
// cannot hold a table is not a choice, and neither is one of another kind.
func TestScratchConnectionChoices(t *testing.T) {
	exec := pickerTrino{targets: map[string]trinotoolkit.ScratchConfig{
		"scratch": {Catalog: "scratch", Schema: "uploads"},
		// A half-configured target is not usable and must not be offered.
		"half": {Catalog: "scratch"},
	}}
	reachable := []connreach.Connection{
		{Name: "warehouse", Kind: "trino", Description: "Curated tables"},
		{Name: "scratch", Kind: "trino", Description: "Working schema"},
		{Name: "half", Kind: "trino"},
		{Name: "acme-s3", Kind: "s3"},
		// A connection of another kind that happens to share a name with a
		// configured target is still not a Trino connection.
		{Name: "scratch", Kind: "s3"},
	}

	got := scratchConnectionChoices(reachable, exec)
	require.Len(t, got, 1)
	assert.Equal(t, "scratch", got[0].Name)
	assert.Equal(t, "Working schema", got[0].Description)
	assert.Equal(t, "scratch", got[0].Catalog)
	assert.Equal(t, "uploads", got[0].Schema)
}

// TestScratchConnectionChoices_NoneReachableIsEmpty, which a form renders as
// "no connection here can hold a table" rather than as a broken picker.
func TestScratchConnectionChoices_NoneReachable(t *testing.T) {
	exec := pickerTrino{targets: map[string]trinotoolkit.ScratchConfig{
		"scratch": {Catalog: "scratch", Schema: "uploads"},
	}}

	assert.Empty(t, scratchConnectionChoices(nil, exec))
	assert.Empty(t, scratchConnectionChoices(
		[]connreach.Connection{{Name: "warehouse", Kind: "trino"}}, exec),
		"a connection with no scratch target is not a choice")
}
