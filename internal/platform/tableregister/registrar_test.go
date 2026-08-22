package tableregister

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// --- fakes ---

// fakeTrino records every statement it was asked to run, which is what the DDL
// assertions read.
type fakeTrino struct {
	target     trino.ScratchConfig
	hasTarget  bool
	statements []string
	err        error
}

func (f *fakeTrino) Exec(_ context.Context, _, sql string) error {
	f.statements = append(f.statements, sql)
	return f.err
}

func (f *fakeTrino) ScratchTarget(string) (trino.ScratchConfig, bool) {
	return f.target, f.hasTarget
}

// fakeObjects serves one directory of objects.
type fakeObjects struct {
	body      []byte
	bodyCT    string
	getErr    error
	entries   []ObjectEntry
	truncated bool
	listErr   error
}

func (f *fakeObjects) GetObject(_ context.Context, _, _ string) (body []byte, contentType string, err error) {
	if f.getErr != nil {
		return nil, "", f.getErr
	}
	return f.body, f.bodyCT, nil
}

func (f *fakeObjects) ListDirectory(
	_ context.Context, _, _ string,
) (entries []ObjectEntry, truncated bool, err error) {
	if f.listErr != nil {
		return nil, false, f.listErr
	}
	return f.entries, f.truncated, nil
}

// memStore is an in-memory Store that enforces the same unique name the
// migration's index does, so a test that expects a collision meets one.
type memStore struct {
	mu      sync.Mutex
	rows    map[string]Registration
	listErr error
}

func newMemStore() *memStore { return &memStore{rows: map[string]Registration{}} }

func (m *memStore) Insert(_ context.Context, r Registration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.rows {
		if existing.Connection == r.Connection && existing.Catalog == r.Catalog &&
			existing.Schema == r.Schema && existing.Table == r.Table {
			return ErrNameTaken
		}
	}
	m.rows[r.ID] = r
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &r, nil
}

func (m *memStore) ByName(_ context.Context, connection, catalog, schema, table string) (*Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.Connection == connection && r.Catalog == catalog && r.Schema == schema && r.Table == table {
			found := r
			return &found, nil
		}
	}
	return nil, nil //nolint:nilnil // a free name is an answer, not a failure
}

func (m *memStore) BySource(_ context.Context, kind, sourceID string) ([]Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	var out []Registration
	for _, r := range m.rows {
		if r.SourceKind == kind && r.SourceID == sourceID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memStore) ForSources(_ context.Context, kind string, ids []string) (map[string][]Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := map[string][]Registration{}
	for _, r := range m.rows {
		if r.SourceKind == kind && want[r.SourceID] {
			out[r.SourceID] = append(out[r.SourceID], r)
		}
	}
	return out, nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[id]; !ok {
		return ErrNotFound
	}
	delete(m.rows, id)
	return nil
}

// denyScope refuses one named connection and allows everything else.
type denyScope struct{ denied string }

func (d denyScope) AllowConnection(_, connection string) bool { return connection != d.denied }

// captureAudit collects the events the registrar wrote.
type captureAudit struct{ events []audit.Event }

func (c *captureAudit) Log(_ context.Context, ev audit.Event) error {
	c.events = append(c.events, ev)
	return nil
}

// --- fixtures ---

const csvBody = "store_id,vendor_code,rebate_pct\n101,ACME-NW,4.5\n"

func testSource() Source {
	return Source{
		Kind:        KindAsset,
		ID:          "asset_1",
		Name:        "Vendor keys",
		Bucket:      "portal-assets",
		HeadKey:     "artifacts/u1/asset_1/content.csv",
		ContentType: "text/csv",
		OwnerID:     "u1",
	}
}

func testCaller() Caller {
	return Caller{UserID: "u1", Email: "alice@example.com", Persona: "analyst"}
}

type harness struct {
	reg     *Registrar
	trino   *fakeTrino
	objects *fakeObjects
	store   *memStore
	audit   *captureAudit
}

func newHarness(t *testing.T, opts ...func(*harness)) *harness {
	t.Helper()
	h := &harness{
		trino: &fakeTrino{
			target:    trino.ScratchConfig{Catalog: "scratch", Schema: "uploads"},
			hasTarget: true,
		},
		objects: &fakeObjects{
			body:    []byte(csvBody),
			bodyCT:  "text/csv",
			entries: []ObjectEntry{{Key: "artifacts/u1/asset_1/content.csv", Size: int64(len(csvBody))}},
		},
		store: newMemStore(),
		audit: &captureAudit{},
	}
	for _, opt := range opts {
		opt(h)
	}
	n := 0
	h.reg = New(Deps{
		Store:   h.store,
		Trino:   h.trino,
		Objects: map[string]ObjectReader{KindAsset: h.objects, KindResource: h.objects},
		Audit:   h.audit,
		NewID: func() (string, error) {
			n++
			return "reg_" + strconv.Itoa(n), nil
		},
	})
	return h
}

// --- registration ---

// TestRegister_BuildsTheExactDDL is the acceptance assertion: the statements
// the registrar issues, the VARCHAR columns taken from the header, and the
// external location pointing at the head key's directory.
func TestRegister_BuildsTheExactDDL(t *testing.T) {
	h := newHarness(t)

	reg, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Source: "portal"})
	require.NoError(t, err)

	assert.Equal(t, []string{
		`CREATE SCHEMA IF NOT EXISTS "scratch"."uploads"`,
		`CREATE TABLE "scratch"."uploads"."analyst_content" ` +
			`("store_id" VARCHAR, "vendor_code" VARCHAR, "rebate_pct" VARCHAR) ` +
			`WITH (external_location = 's3://portal-assets/artifacts/u1/asset_1/', ` +
			`format = 'CSV', skip_header_line_count = 1)`,
	}, h.trino.statements)

	assert.Equal(t, "scratch.uploads.analyst_content", reg.QualifiedName())
	assert.Equal(t, "s3://portal-assets/artifacts/u1/asset_1/", reg.Location)
	assert.Equal(t, []Column{
		{Name: "store_id", Type: "VARCHAR"},
		{Name: "vendor_code", Type: "VARCHAR"},
		{Name: "rebate_pct", Type: "VARCHAR"},
	}, reg.Columns)

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Equal(t, reg.QualifiedName(), stored.QualifiedName())
}

// TestRegister_AuditsTheStatement pins that a registration lands in the audit
// trail with the SQL it ran, the connection, and where it came from.
func TestRegister_AuditsTheStatement(t *testing.T) {
	h := newHarness(t)

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", TableName: "vendor_keys", Source: "mcp"})
	require.NoError(t, err)

	require.Len(t, h.audit.events, 1)
	ev := h.audit.events[0]
	assert.Equal(t, "trino", ev.ToolkitKind)
	assert.Equal(t, "scratch", ev.Connection)
	assert.Equal(t, "mcp", ev.Source)
	assert.Equal(t, "analyst", ev.Persona)
	assert.Equal(t, "alice@example.com", ev.UserEmail)
	assert.True(t, ev.Success)
	assert.Contains(t, ev.Parameters["sql"], "CREATE TABLE")
	assert.Equal(t, "scratch.uploads.analyst_vendor_keys", ev.Parameters["table"])
}

// TestRegister_NamesTheSiblingThatBlocksIt is the behavior Step 0 made
// necessary: Hive reads a stray object as CSV rows instead of failing, so the
// refusal is the only protection, and it has to say what is in the way.
func TestRegister_NamesTheSiblingThatBlocksIt(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.objects.entries = append(h.objects.entries,
			ObjectEntry{Key: "artifacts/u1/asset_1/notes.txt", Size: 10})
	})

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notes.txt")
	assert.Empty(t, h.trino.statements, "nothing runs when the directory is refused")
}

// TestRegister_HiddenThumbnailsDoNotBlockIt is the other half: thumbnails are
// written under hidden names precisely so Hive skips them, and a listing that
// carries them must not turn every viewed CSV asset into a refusal.
func TestRegister_HiddenThumbnailsDoNotBlockIt(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		// The adapter's delimiter listing returns what Hive sees; a hidden
		// object is still an object in the directory.
		h.objects.entries = []ObjectEntry{
			{Key: "artifacts/u1/asset_1/content.csv", Size: int64(len(csvBody))},
		}
	})

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)
}

// TestRegister_TruncatedListingRefuses pins that a page boundary is never read
// as "nothing else is there".
func TestRegister_TruncatedListingRefuses(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.objects.truncated = true })

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more objects than can be checked")
}

func TestRegister_ConnectionNotGrantedRefuses(t *testing.T) {
	h := newHarness(t)
	h.reg = New(Deps{
		Store: h.store, Trino: h.trino, Audit: h.audit,
		Objects: map[string]ObjectReader{KindAsset: h.objects},
		Scope:   denyScope{denied: "scratch"},
		NewID:   func() (string, error) { return "reg_a", nil },
	})

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	assert.ErrorIs(t, err, ErrConnectionDenied)
	assert.Empty(t, h.trino.statements)
}

func TestRegister_NoScratchTargetRefuses(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.trino.hasTarget = false })

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "warehouse"})
	assert.ErrorIs(t, err, ErrNoScratchTarget)
}

// TestRegister_ReadOnlyConnectionRefusesThroughExec pins that the read-only
// refusal reaches the caller rather than being swallowed, and that nothing is
// recorded for a table that was never created.
func TestRegister_ReadOnlyConnectionRefusesThroughExec(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.trino.err = errors.New(`write operations not allowed: connection "warehouse" is read-only`)
	})

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "warehouse"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")

	regs, err := h.store.BySource(context.Background(), KindAsset, "asset_1")
	require.NoError(t, err)
	assert.Empty(t, regs, "a failed DDL records nothing")

	require.Len(t, h.audit.events, 1)
	assert.False(t, h.audit.events[0].Success, "the attempt is still audited")
}

func TestRegister_NonCSVRefuses(t *testing.T) {
	h := newHarness(t)
	src := testSource()
	src.ContentType = "text/html"
	src.HeadKey = "artifacts/u1/asset_1/content.html"
	h.objects.entries = []ObjectEntry{{Key: src.HeadKey}}

	_, err := h.reg.Register(context.Background(), testCaller(), src, Request{Connection: "scratch"})
	assert.ErrorIs(t, err, ErrNotCSV)
}

// TestRegister_NameHeldBySomeoneElseRefuses pins the shared-schema rule: the
// last writer does not silently win.
func TestRegister_NameHeldBySomeoneElseRefuses(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.store.Insert(context.Background(), Registration{
		ID: "reg_existing", SourceKind: KindAsset, SourceID: "asset_9",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "analyst_content", RegisteredBy: "bob@example.com",
	}))

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bob@example.com")
	assert.Empty(t, h.trino.statements)
}

// TestRegister_OwnReRegistrationReplaces covers the re-register that follows a
// new revision: the same person takes their own name back, and the DDL drops
// before it creates.
func TestRegister_OwnReRegistrationReplaces(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.store.Insert(context.Background(), Registration{
		ID: "reg_existing", SourceKind: KindAsset, SourceID: "asset_1",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "analyst_content", RegisteredBy: "alice@example.com",
	}))

	reg, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)
	assert.Contains(t, h.trino.statements[1], `DROP TABLE IF EXISTS "scratch"."uploads"."analyst_content"`)

	_, err = h.store.Get(context.Background(), "reg_existing")
	assert.ErrorIs(t, err, ErrNotFound, "the replaced record goes with the replaced table")
	_, err = h.store.Get(context.Background(), reg.ID)
	assert.NoError(t, err)
}

// TestRegister_AdminReplacesAnotherPersonsRegistration: administrators are
// unrestricted by design.
func TestRegister_AdminReplacesAnotherPersonsRegistration(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.store.Insert(context.Background(), Registration{
		ID: "reg_existing", SourceKind: KindAsset, SourceID: "asset_9",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "admin_content", RegisteredBy: "bob@example.com",
	}))

	admin := Caller{UserID: "u2", Email: "root@example.com", Persona: "admin", IsAdmin: true}
	_, err := h.reg.Register(context.Background(), admin, testSource(), Request{Connection: "scratch"})
	require.NoError(t, err)
}

// --- unregistration ---

func TestUnregister_DropsAndForgets(t *testing.T) {
	h := newHarness(t)
	reg, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)
	h.trino.statements = nil

	require.NoError(t, h.reg.Unregister(context.Background(), testCaller(), reg.ID, "portal"))
	assert.Equal(t, []string{`DROP TABLE IF EXISTS "scratch"."uploads"."analyst_content"`}, h.trino.statements)

	_, err = h.store.Get(context.Background(), reg.ID)
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestUnregister_FailedDropStillForgets pins the choice: a record of a table
// nothing can remove is worse than an orphaned table in a scratch schema.
func TestUnregister_FailedDropStillForgets(t *testing.T) {
	h := newHarness(t)
	reg, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)
	h.trino.err = errors.New("trino unreachable")

	require.NoError(t, h.reg.Unregister(context.Background(), testCaller(), reg.ID, "portal"))
	_, err = h.store.Get(context.Background(), reg.ID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestUnregister_SomeoneElsesRefuses(t *testing.T) {
	h := newHarness(t)
	reg, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)

	other := Caller{UserID: "u2", Email: "bob@example.com", Persona: "analyst"}
	err = h.reg.Unregister(context.Background(), other, reg.ID, "portal")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alice@example.com")
}

// TestUnregisterAllForSource is what a resource or asset delete calls.
func TestUnregisterAllForSource(t *testing.T) {
	h := newHarness(t)
	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)

	h.reg.UnregisterAllForSource(context.Background(), KindAsset, "asset_1")

	regs, err := h.store.BySource(context.Background(), KindAsset, "asset_1")
	require.NoError(t, err)
	assert.Empty(t, regs)
	assert.Contains(t, strings.Join(h.trino.statements, "\n"), "DROP TABLE IF EXISTS")
}

// --- availability ---

func TestRegistrar_UnavailableWithoutWiring(t *testing.T) {
	r := New(Deps{})
	assert.False(t, r.Available())

	_, err := r.Register(context.Background(), testCaller(), testSource(), Request{Connection: "scratch"})
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.ErrorIs(t, r.Unregister(context.Background(), testCaller(), "reg_a", "portal"), ErrUnavailable)

	// The source-delete hook is a no-op rather than a panic, so a deployment
	// with no registration wired still deletes assets.
	r.UnregisterAllForSource(context.Background(), KindAsset, "asset_1")
}

// --- discovery lookup ---

// TestLookup_TablesFor is what puts a table reference on a search hit: the
// connection, the qualified name, and the CAST a join needs.
func TestLookup_TablesFor(t *testing.T) {
	h := newHarness(t)
	reg, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)

	lookup := NewLookup(h.reg)
	tables, err := lookup.TablesFor(context.Background(), []knowledge.TableSubject{{
		Kind: knowledge.TableKindAsset, ID: "asset_1",
		Bucket: "portal-assets", HeadKey: "artifacts/u1/asset_1/content.csv",
	}})
	require.NoError(t, err)

	got := tables["asset_1"]
	require.NotNil(t, got)
	assert.Equal(t, "scratch", got.Connection)
	assert.Equal(t, reg.QualifiedName(), got.Table)
	assert.Contains(t, got.Sample, "CAST(t.\"store_id\" AS BIGINT)",
		"every column is VARCHAR, so the sample shows the cast a join needs")
	assert.False(t, got.Stale)
}

// TestLookup_MovedHeadKeyReadsStale is the revision case: a new version writes
// under a new directory and moves the head, and the table keeps serving the
// revision it was registered against.
func TestLookup_MovedHeadKeyReadsStale(t *testing.T) {
	h := newHarness(t)
	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)

	tables, err := NewLookup(h.reg).TablesFor(context.Background(), []knowledge.TableSubject{{
		Kind: knowledge.TableKindAsset, ID: "asset_1",
		Bucket: "portal-assets", HeadKey: "artifacts/u1/asset_1/v2/content.csv",
	}})
	require.NoError(t, err)
	require.NotNil(t, tables["asset_1"])
	assert.True(t, tables["asset_1"].Stale, "the head moved to a directory the table does not point at")
}

// TestLookup_OverwriteInPlaceIsNotStale: replacing the object at the same key
// changes what the table returns with no re-registration, which is what makes
// a repeating drop a re-upload rather than a chore.
func TestLookup_OverwriteInPlaceIsNotStale(t *testing.T) {
	h := newHarness(t)
	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)

	tables, err := NewLookup(h.reg).TablesFor(context.Background(), []knowledge.TableSubject{{
		Kind: knowledge.TableKindAsset, ID: "asset_1",
		Bucket: "portal-assets", HeadKey: "artifacts/u1/asset_1/content.csv",
	}})
	require.NoError(t, err)
	assert.False(t, tables["asset_1"].Stale)
}

func TestLookup_UnregisteredSubjectIsAbsent(t *testing.T) {
	h := newHarness(t)
	tables, err := NewLookup(h.reg).TablesFor(context.Background(), []knowledge.TableSubject{{
		Kind: knowledge.TableKindAsset, ID: "asset_unknown", Bucket: "b", HeadKey: "d/content.csv",
	}})
	require.NoError(t, err)
	assert.Nil(t, tables["asset_unknown"])
}

func TestLookup_NoRegistrarFindsNothing(t *testing.T) {
	tables, err := NewLookup(New(Deps{})).TablesFor(context.Background(),
		[]knowledge.TableSubject{{Kind: knowledge.TableKindAsset, ID: "asset_1"}})
	require.NoError(t, err)
	assert.Empty(t, tables)
}

// TestRegister_ReadsTheStoreItsKindLivesIn pins that the two kinds are read
// through their own object stores. A deployment names portal.s3_connection and
// resources.managed.s3_connection separately, so a shared reader would look for
// a resource's file wherever the portal's assets happen to live.
func TestRegister_ReadsTheStoreItsKindLivesIn(t *testing.T) {
	assets := &fakeObjects{
		body:    []byte("wrong_store\n1\n"),
		bodyCT:  "text/csv",
		entries: []ObjectEntry{{Key: "resources/global/global/res_1/keys.csv"}},
	}
	resources := &fakeObjects{
		body:    []byte(csvBody),
		bodyCT:  "text/csv",
		entries: []ObjectEntry{{Key: "resources/global/global/res_1/keys.csv"}},
	}
	trinoFake := &fakeTrino{
		target:    trino.ScratchConfig{Catalog: "scratch", Schema: "uploads"},
		hasTarget: true,
	}
	reg := New(Deps{
		Store:   newMemStore(),
		Trino:   trinoFake,
		Objects: map[string]ObjectReader{KindAsset: assets, KindResource: resources},
		NewID:   func() (string, error) { return "reg_a", nil },
	})

	src := Source{
		Kind: KindResource, ID: "res_1", Name: "Vendor keys",
		Bucket: "managed-resources", HeadKey: "resources/global/global/res_1/keys.csv",
		ContentType: "text/csv",
	}
	got, err := reg.Register(context.Background(), testCaller(), src, Request{Connection: "scratch"})
	require.NoError(t, err)
	assert.Equal(t, []Column{
		{Name: "store_id", Type: "VARCHAR"},
		{Name: "vendor_code", Type: "VARCHAR"},
		{Name: "rebate_pct", Type: "VARCHAR"},
	}, got.Columns, "the header came from the resource store, not the asset store")
}

// TestRegister_KindWithNoStoreIsUnavailable: a platform with a portal but no
// managed resources configured cannot register a resource, and says so rather
// than reading one kind's file out of the other's bucket.
func TestRegister_KindWithNoStoreIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.reg = New(Deps{
		Store: h.store, Trino: h.trino, Audit: h.audit,
		Objects: map[string]ObjectReader{KindAsset: h.objects},
		NewID:   func() (string, error) { return "reg_a", nil },
	})

	src := testSource()
	src.Kind = KindResource
	_, err := h.reg.Register(context.Background(), testCaller(), src, Request{Connection: "scratch"})
	assert.ErrorIs(t, err, ErrUnavailable)
}

// TestRegister_RecordsTheSourcesOwnKind pins what a delete sweep and a search
// lookup key on: a registration over an asset is filed as an asset, and one
// over a resource as a resource. Filing either under the other's kind leaves a
// deleted file's table in place and a live file's table unfindable.
func TestRegister_RecordsTheSourcesOwnKind(t *testing.T) {
	h := newHarness(t)

	asset, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)
	assert.Equal(t, KindAsset, asset.SourceKind)

	res := testSource()
	res.Kind = KindResource
	res.ID = "res_1"
	res.HeadKey = "resources/global/global/res_1/keys.csv"
	h.objects.entries = []ObjectEntry{{Key: res.HeadKey}}
	registered, err := h.reg.Register(context.Background(), testCaller(), res,
		Request{Connection: "scratch", TableName: "res_keys"})
	require.NoError(t, err)
	assert.Equal(t, KindResource, registered.SourceKind)

	// Each is found only under its own kind.
	byAsset, err := h.store.BySource(context.Background(), KindAsset, "asset_1")
	require.NoError(t, err)
	assert.Len(t, byAsset, 1)
	byResource, err := h.store.BySource(context.Background(), KindResource, "asset_1")
	require.NoError(t, err)
	assert.Empty(t, byResource)
}

// TestRegister_AnonymousCallRefuses: a registration records who made it and
// decides replacement on that, so one nobody owns would be one anybody could
// take over.
func TestRegister_AnonymousCallRefuses(t *testing.T) {
	h := newHarness(t)

	_, err := h.reg.Register(context.Background(), Caller{Persona: "analyst"}, testSource(),
		Request{Connection: "scratch"})
	assert.ErrorIs(t, err, ErrNoIdentity)
	assert.Empty(t, h.trino.statements)

	assert.ErrorIs(t, h.reg.Unregister(context.Background(), Caller{}, "reg_a", "portal"), ErrNotFound,
		"an unknown id is answered before identity, so nothing is revealed about what exists")
}

// TestRefusals_AreDistinguishableFromFailures pins the separation a surface
// renders on: a refusal the caller can act on carries ErrRefused, and a store
// or engine failure does not, so a database outage is never reported as a
// conflict the caller could resolve.
func TestRefusals_AreDistinguishableFromFailures(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.objects.entries = append(h.objects.entries,
			ObjectEntry{Key: "artifacts/u1/asset_1/notes.txt"})
	})
	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	assert.ErrorIs(t, err, ErrRefused, "a sibling in the way is the caller's to resolve")

	// A failure to reach the object store is not.
	broken := newHarness(t, func(h *harness) { h.objects.listErr = errors.New("s3 unreachable") })
	_, err = broken.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRefused)
}

// --- remaining branches ---

// TestUnregisterAllForSource_IsBestEffort. The delete that triggered it has
// its own reasons to succeed; failing it because a scratch table could not be
// dropped would make an unrelated Trino outage look like a broken delete.
func TestUnregisterAllForSource_IsBestEffort(t *testing.T) {
	h := newHarness(t)
	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)

	h.trino.err = errors.New("trino unreachable")
	assert.NotPanics(t, func() {
		h.reg.UnregisterAllForSource(context.Background(), KindAsset, "asset_1")
	})
	regs, err := h.store.BySource(context.Background(), KindAsset, "asset_1")
	require.NoError(t, err)
	assert.Empty(t, regs, "the record goes even when the drop failed")
}

// TestUnregisterAllForSource_ListFailureIsSwallowed for the same reason.
func TestUnregisterAllForSource_ListFailureIsSwallowed(t *testing.T) {
	h := newHarness(t)
	h.store.listErr = errors.New("connection refused")

	assert.NotPanics(t, func() {
		h.reg.UnregisterAllForSource(context.Background(), KindAsset, "asset_1")
	})
	assert.Empty(t, h.trino.statements)
}

func TestNewID_Failures(t *testing.T) {
	h := newHarness(t)

	// No generator configured at all.
	noGen := New(Deps{Store: h.store, Trino: h.trino, Objects: map[string]ObjectReader{KindAsset: h.objects}})
	_, err := noGen.Register(context.Background(), testCaller(), testSource(), Request{Connection: "scratch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no id generator")

	// A generator that fails.
	failing := New(Deps{
		Store: h.store, Trino: h.trino,
		Objects: map[string]ObjectReader{KindAsset: h.objects},
		NewID:   func() (string, error) { return "", errors.New("no entropy") },
	})
	_, err = failing.Register(context.Background(), testCaller(), testSource(), Request{Connection: "scratch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generating a registration id")
}

// TestReadPaths_UnwiredAnswerNothing so a list view on a deployment with no
// registration mechanism renders empty rather than failing.
func TestReadPaths_UnwiredAnswerNothing(t *testing.T) {
	r := New(Deps{})

	regs, err := r.BySource(context.Background(), KindAsset, "asset_1")
	require.NoError(t, err)
	assert.Empty(t, regs)

	found, err := r.ForSources(context.Background(), KindAsset, []string{"asset_1"})
	require.NoError(t, err)
	assert.Empty(t, found)

	// No ids is not a query either.
	h := newHarness(t)
	found, err = h.reg.ForSources(context.Background(), KindAsset, nil)
	require.NoError(t, err)
	assert.Empty(t, found)
}

func TestColumnsFor_ReadFailures(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.objects.getErr = errors.New("s3 unreachable") })
	_, err := h.reg.Register(context.Background(), testCaller(), testSource(), Request{Connection: "scratch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading the file")

	// A file larger than the registration reads. Learning the first line costs
	// a full GetObject, so the bound is on what came back.
	big := newHarness(t, func(h *harness) { h.objects.body = make([]byte, 64) })
	big.reg = New(Deps{
		Store: big.store, Trino: big.trino,
		Objects:  map[string]ObjectReader{KindAsset: big.objects},
		MaxBytes: 8,
		NewID:    func() (string, error) { return "reg_a", nil },
	})
	_, err = big.reg.Register(context.Background(), testCaller(), testSource(), Request{Connection: "scratch"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "larger than")
}

// TestTableNameFor_FallsBackToTheRecordsName when the filename slugifies to
// nothing, which a key like "d/_.csv" does.
func TestTableNameFor_FallsBackToTheRecordsName(t *testing.T) {
	h := newHarness(t)
	src := testSource()
	src.HeadKey = "artifacts/u1/asset_1/_.csv"
	src.Name = "Vendor keys"
	h.objects.entries = []ObjectEntry{{Key: src.HeadKey}}

	reg, err := h.reg.Register(context.Background(), testCaller(), src, Request{Connection: "scratch"})
	require.NoError(t, err)
	assert.Equal(t, "analyst_vendor_keys", reg.Table)
}

// TestTableNameFor_NothingDerivable is the case where neither the filename nor
// the record's name yields an identifier.
func TestTableNameFor_NothingDerivable(t *testing.T) {
	h := newHarness(t)
	src := testSource()
	src.HeadKey = "artifacts/u1/asset_1/_.csv"
	src.Name = "..."
	h.objects.entries = []ObjectEntry{{Key: src.HeadKey}}

	_, err := h.reg.Register(context.Background(), testCaller(), src, Request{Connection: "scratch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "give one explicitly")
}

// TestLocationFor_KeyWithNoDirectory: an object at a bucket's root has no
// directory to point a table at, and pointing one at the bucket would read
// every object in it.
func TestLocationFor_KeyWithNoDirectory(t *testing.T) {
	h := newHarness(t)
	src := testSource()
	src.HeadKey = "content.csv"

	_, err := h.reg.Register(context.Background(), testCaller(), src, Request{Connection: "scratch"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "directory of its own")
}
