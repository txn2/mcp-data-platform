package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/internal/platform/tablereconcile"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	trinotoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

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

func (f *fakeBlobs) GetObjectRange(
	_ context.Context, _, _ string, offset, length int64,
) (body []byte, size int64, err error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	end := min(offset+length, int64(len(f.body)))
	return f.body[offset:end], int64(len(f.body)), nil
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

// TestTableSourceHooks_UnwiredIsNil so a delete on a deployment that cannot
// register calls nothing rather than a hook that panics.
func TestTableSourceHooks_UnwiredIsNil(t *testing.T) {
	hooks := tableSourceHooks(nil)
	assert.Nil(t, hooks.AssetDeleted)
	assert.Nil(t, hooks.ResourceDeleted)
	assert.Nil(t, hooks.AssetRevised)
	assert.Nil(t, hooks.ResourceRevised)
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

// pickerTrino answers which connections carry a scratch target and which of
// them accept the statement that creates a table there.
type pickerTrino struct {
	targets map[string]trinotoolkit.ScratchConfig
	// readOnly names the connections that refuse write SQL. Absent means the
	// connection accepts writes, which is the case every other test here wants.
	readOnly map[string]bool
}

func (pickerTrino) Exec(context.Context, string, string) error { return nil }

func (p pickerTrino) ScratchTarget(name string) (trinotoolkit.ScratchConfig, bool) {
	t, ok := p.targets[name]
	return t, ok
}

func (p pickerTrino) AcceptsWrites(name string) bool { return !p.readOnly[name] }

func (pickerTrino) TableExists(context.Context, string, string, string, string) (bool, error) {
	return true, nil
}

func (pickerTrino) TableLocation(context.Context, string, string, string, string) (location string, found bool, err error) {
	return "", true, nil
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

// A scratch target is a destination, not a permission. A read-only connection
// can name one and still refuse the CREATE TABLE, which is what produced a
// picker offering a connection and a 500 "the registration could not be
// completed" the moment it was chosen.
func TestScratchConnectionChoices_SkipsReadOnly(t *testing.T) {
	exec := pickerTrino{
		targets: map[string]trinotoolkit.ScratchConfig{
			"scratch":   {Catalog: "scratch", Schema: "uploads"},
			"warehouse": {Catalog: "scratch", Schema: "uploads"},
		},
		readOnly: map[string]bool{"warehouse": true},
	}
	reachable := []connreach.Connection{
		{Name: "warehouse", Kind: "trino", Description: "Read-only"},
		{Name: "scratch", Kind: "trino", Description: "Working schema"},
	}

	got := scratchConnectionChoices(reachable, exec)
	require.Len(t, got, 1)
	assert.Equal(t, "scratch", got[0].Name)
}

// Every connection read-only is the same answer as no connection at all: a
// form saying nothing here can hold a table, rather than a picker whose every
// choice fails.
func TestScratchConnectionChoices_AllReadOnlyIsEmpty(t *testing.T) {
	exec := pickerTrino{
		targets:  map[string]trinotoolkit.ScratchConfig{"scratch": {Catalog: "scratch", Schema: "uploads"}},
		readOnly: map[string]bool{"scratch": true},
	}

	assert.Empty(t, scratchConnectionChoices(
		[]connreach.Connection{{Name: "scratch", Kind: "trino"}}, exec))
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

// TestConnectionVisibility_AppliesThePersonaBoundary. The listing shows a
// caller the registrations they could query, which is the same predicate a
// tool call meets.
func TestConnectionVisibility_AppliesThePersonaBoundary(t *testing.T) {
	tr, pr := enumeratorFixture(t)
	visible := connectionVisibility(connreach.New(connreach.Deps{Toolkits: tr, Personas: pr}))

	names, all := visible(context.Background(), tableregister.Caller{Persona: "analyst"})
	assert.False(t, all)
	assert.Equal(t, []string{"warehouse"}, names)
}

// TestConnectionVisibility_KeepsToConnectionsATableCanBeOn: a registration
// lives on a query engine, so an object-store connection the persona also
// reaches is not one of them.
func TestConnectionVisibility_KeepsToConnectionsATableCanBeOn(t *testing.T) {
	tr, pr := enumeratorFixture(t)
	require.NoError(t, pr.Register(&persona.Persona{
		Name: "everything", Roles: []string{"dp_everything"},
		Connections: persona.ConnectionRules{Allow: []string{"*"}},
	}))
	visible := connectionVisibility(connreach.New(connreach.Deps{Toolkits: tr, Personas: pr}))

	names, all := visible(context.Background(), tableregister.Caller{Persona: "everything"})

	assert.False(t, all)
	assert.Equal(t, []string{"warehouse"}, names, "the object-store connection cannot hold a table")
}

// TestConnectionVisibility_AnAdministratorIsUnrestricted, which is what the
// operator opens this page for.
func TestConnectionVisibility_AnAdministratorIsUnrestricted(t *testing.T) {
	tr, pr := enumeratorFixture(t)
	visible := connectionVisibility(connreach.New(connreach.Deps{Toolkits: tr, Personas: pr}))

	names, all := visible(context.Background(), tableregister.Caller{Persona: "analyst", IsAdmin: true})

	assert.True(t, all)
	assert.Empty(t, names, "an unrestricted listing carries no connection list to intersect with")
}

// TestConnectionVisibility_WithNothingToEnumerateShowsNothing is the
// fail-closed reading: a deployment that cannot say which connections a
// persona reaches must not answer "all of them".
func TestConnectionVisibility_WithNothingToEnumerateShowsNothing(t *testing.T) {
	visible := connectionVisibility(connreach.New(connreach.Deps{}))

	names, all := visible(context.Background(), tableregister.Caller{Persona: "analyst"})

	assert.False(t, all)
	assert.Empty(t, names)
}

// registrationMaxBytes is the CSV or JSON-lines file a registration reads
// whole: its own setting, else a deployment's own upload ceiling so one that
// raised it keeps registering what it stores (#1634), else 100 MB, which stays
// put although the upload default rose to 1 GiB (#2069).
func TestRegistrationMaxBytes(t *testing.T) {
	tests := []struct {
		name         string
		upload, regs int64
		want         int64
	}{
		{name: "its own setting wins", upload: 262144000, regs: 50 << 20, want: 50 << 20},
		{name: "a raised upload ceiling is what a registration reads by", upload: 262144000, want: 262144000},
		{name: "a deployment that configures nothing reads 100 MB, not the 1 GiB upload default", want: tableregister.DefaultMaxBytes},
		{name: "negative values select the default", upload: -1, regs: -1, want: tableregister.DefaultMaxBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &platform.Config{}
			cfg.Resources.Managed.MaxUploadBytes = tt.upload
			cfg.Resources.Managed.TableRegistrationMaxBytes = tt.regs
			assert.Equal(t, tt.want, registrationMaxBytes(cfg))
		})
	}
	assert.Less(t, int64(tableregister.DefaultMaxBytes), int64(resource.MaxUploadBytes),
		"the default registration read is smaller than the default upload")
	assert.EqualValues(t, tableregister.DefaultMaxBytes, registrationMaxBytes(nil),
		"a nil config cannot make a registration read by zero")
}

// TestTableAdminConfig: the listing is always offered; the reconcile only
// when the query engine can list a schema, and only with a store to read.
func TestTableAdminConfig(t *testing.T) {
	reg := tableregister.New(tableregister.Deps{})
	cfg := tableAdminConfig(reg, nil, tablereconcile.Deps{})
	assert.NotNil(t, cfg.Registrations)
	assert.Nil(t, cfg.Reconciler, "no engine, no reconcile")

	cfg = tableAdminConfig(reg, pickerTrino{}, tablereconcile.Deps{})
	assert.Nil(t, cfg.Reconciler, "an engine that cannot list a schema, no reconcile")

	cfg = tableAdminConfig(reg, listingTrino{}, tablereconcile.Deps{})
	assert.Nil(t, cfg.Reconciler, "no store, no reconcile")

	cfg = tableAdminConfig(reg, listingTrino{}, tablereconcile.Deps{Store: regstoreStub{}})
	assert.NotNil(t, cfg.Reconciler)
	assert.NotPanics(t, func() { mountTableAdmin(http.NewServeMux(), nil) })
}

// listingTrino is a picker engine that can also list a schema.
type listingTrino struct{ pickerTrino }

func (listingTrino) ListTables(context.Context, string, string, string) ([]trinotoolkit.SchemaTable, bool, error) {
	return nil, false, nil
}

// regstoreStub is a registration store that holds nothing.
type regstoreStub struct{ tableregister.Store }

// TestAdminTableCaller: a repair is audited under the administrator the admin
// API authenticated, and as an administrator when none is on the request.
func TestAdminTableCaller(t *testing.T) {
	var got tableregister.Caller
	h := admin.RequireAdmin(&admin.APIKeyAuthenticator{Keys: map[string]admin.User{
		"k": {UserID: "u1", Email: "ops@example.com", Roles: []string{"admin"}},
	}})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = adminTableCaller(r) }))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-API-Key", "k")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, tableregister.Caller{UserID: "u1", Email: "ops@example.com", Roles: []string{"admin"}, IsAdmin: true}, got)

	bare := adminTableCaller(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody))
	assert.Equal(t, tableregister.Caller{IsAdmin: true}, bare)
}
