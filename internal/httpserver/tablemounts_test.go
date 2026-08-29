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
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
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
