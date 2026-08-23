package tableregister

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
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

const (
	createTablePrefix = "CREATE TABLE "
	dropTablePrefix   = "DROP TABLE IF EXISTS "
)

// fakeTrino records every statement it was asked to run, which is what the DDL
// assertions read.
type fakeTrino struct {
	target     trino.ScratchConfig
	hasTarget  bool
	statements []string
	err        error
	// errFor decides the failure per statement, which is how a test makes the
	// DDL of a registration succeed and the drop that rolls it back fail.
	errFor func(sql string) error
	// tables is what the coordinator holds, so a CREATE over a name that is
	// still there fails the way Trino fails it.
	tables map[string]bool
}

func (f *fakeTrino) Exec(ctx context.Context, _, sql string) error {
	// A real client sends nothing on a context that is already done, which is
	// what a test of cleanup after a canceled request depends on.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	f.statements = append(f.statements, sql)
	err := f.err
	if f.errFor != nil {
		err = f.errFor(sql)
	}
	if err != nil {
		return err
	}
	return f.apply(sql)
}

// apply models the one thing about a coordinator that decides whether
// registering the same name a second time works: CREATE TABLE refuses a table
// that is already there, and DROP TABLE IF EXISTS takes it away. A fake that
// accepted every CREATE would let a test claim a retry succeeds when against
// Trino it would not.
func (f *fakeTrino) apply(sql string) error {
	switch {
	case strings.HasPrefix(sql, createTablePrefix):
		name, _, _ := strings.Cut(strings.TrimPrefix(sql, createTablePrefix), " (")
		if f.tables[name] {
			return errors.New("line 1:1: Table '" + name + "' already exists")
		}
		if f.tables == nil {
			f.tables = map[string]bool{}
		}
		f.tables[name] = true
	case strings.HasPrefix(sql, dropTablePrefix):
		delete(f.tables, strings.TrimPrefix(sql, dropTablePrefix))
	}
	return nil
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

// ListDirectory answers for the prefix it was asked about, the way the S3
// adapter's delimiter listing does. It matters once a registration can move to
// a corrected version of the file: that version sits in its own directory, and
// a fake that returned every object it holds whatever the prefix would report
// the previous version as a sibling of it.
func (f *fakeObjects) ListDirectory(
	_ context.Context, _, prefix string,
) (entries []ObjectEntry, truncated bool, err error) {
	if f.listErr != nil {
		return nil, false, f.listErr
	}
	var under []ObjectEntry
	for _, e := range f.entries {
		if strings.HasPrefix(e.Key, prefix) {
			under = append(under, e)
		}
	}
	return under, f.truncated, nil
}

// memStore is an in-memory Store that enforces the same unique name the
// migration's index does, so a test that expects a collision meets one.
type memStore struct {
	mu   sync.Mutex
	rows map[string]Registration
	// listErr, insertErr and deleteErr stand in for a database that is not
	// answering, each on the one call a test needs to fail.
	listErr   error
	insertErr error
	deleteErr error
	// onInsert runs just before Insert answers, which is how a test arranges
	// the request to be canceled by the time the row is written.
	onInsert func()
}

func newMemStore() *memStore { return &memStore{rows: map[string]Registration{}} }

func (m *memStore) Insert(_ context.Context, r Registration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.onInsert != nil {
		m.onInsert()
	}
	if m.insertErr != nil {
		return m.insertErr
	}
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
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.rows[id]; !ok {
		return ErrNotFound
	}
	delete(m.rows, id)
	return nil
}

// fakeReviser stands in for the version trail a source kind already has: it
// records what it was asked to save, mints a per-version directory the way both
// real trails do, and puts the new object in the listing so the registrar sees
// the directory it will point the table at.
type fakeReviser struct {
	objects *fakeObjects
	saved   []savedRevision
	err     error
	// afterSave runs once a version has been written, which is how a test
	// arranges a refusal that arrives after the file has already changed.
	afterSave func()
}

// savedRevision is one thing the reviser was asked to write.
type savedRevision struct {
	sourceKind string
	sourceID   string
	by         string
	summary    string
	content    []byte
}

func (f *fakeReviser) Revise(
	_ context.Context, src Source, caller Caller, content []byte, summary string,
) (Revised, error) {
	if f.err != nil {
		return Revised{}, f.err
	}
	f.saved = append(f.saved, savedRevision{
		sourceKind: src.Kind, sourceID: src.ID, by: caller.Email, summary: summary, content: content,
	})
	version := len(f.saved) + 1
	key := DirectoryOf(src.HeadKey) + "v/rev_" + strconv.Itoa(version) + "/" + fileNameOf(src.HeadKey)
	if f.objects != nil {
		f.objects.entries = append(f.objects.entries, ObjectEntry{Key: key, Size: int64(len(content))})
	}
	if f.afterSave != nil {
		f.afterSave()
	}
	return Revised{Bucket: src.Bucket, Key: key, Version: version}, nil
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

// macBody is a spreadsheet export whose lines end in a bare carriage return.
// It holds three records and every reader on this path finds one in it.
const macBody = "store_id,address\r101,12 Mill Rd\r102,9 Oak St\r"

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
	reviser *fakeReviser
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
	h.reviser = &fakeReviser{objects: h.objects}
	n := 0
	h.reg = New(Deps{
		Store:    h.store,
		Trino:    h.trino,
		Objects:  map[string]ObjectReader{KindAsset: h.objects, KindResource: h.objects},
		Revisers: map[string]Reviser{KindAsset: h.reviser, KindResource: h.reviser},
		Audit:    h.audit,
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
// carries them must not turn every viewed CSV asset into a refusal. The
// filenames are the ones DeriveThumbnailKeyVariant writes.
func TestRegister_HiddenThumbnailsDoNotBlockIt(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		// The adapter's delimiter listing returns every object in the
		// directory, hidden or not; the registrar applies Hive's rule to them.
		h.objects.entries = []ObjectEntry{
			{Key: "artifacts/u1/asset_1/content.csv", Size: int64(len(csvBody))},
			{Key: "artifacts/u1/asset_1/.thumbnail.png", Size: 2048},
			{Key: "artifacts/u1/asset_1/.thumbnail_dark.png", Size: 2048},
			{Key: "artifacts/u1/asset_1/_SUCCESS", Size: 0},
		}
	})

	reg, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.NoError(t, err)
	assert.Equal(t, "s3://portal-assets/artifacts/u1/asset_1/", reg.Location)
}

// TestRegister_NamesOnlyTheSiblingHiveReads pins that a hidden file next to an
// ordinary one neither suppresses the refusal nor appears in it: the caller is
// told to move the file that would come back as rows, and nothing else.
func TestRegister_NamesOnlyTheSiblingHiveReads(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.objects.entries = []ObjectEntry{
			{Key: "artifacts/u1/asset_1/content.csv", Size: int64(len(csvBody))},
			{Key: "artifacts/u1/asset_1/.thumbnail.png", Size: 2048},
			{Key: "artifacts/u1/asset_1/notes.txt", Size: 10},
		}
	})

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notes.txt")
	assert.NotContains(t, err.Error(), "thumbnail")
	assert.Empty(t, h.trino.statements, "nothing runs when the directory is refused")
}

// TestRegister_HiddenSourceNameRefuses is the same rule applied to the object
// the table is built over: Trino skips it, so the table would be created,
// recorded and queried without error and return nothing at all.
func TestRegister_HiddenSourceNameRefuses(t *testing.T) {
	src := testSource()
	src.HeadKey = "resources/user/u1/res_1/_vendor_keys.csv"
	h := newHarness(t, func(h *harness) {
		h.objects.entries = []ObjectEntry{{Key: src.HeadKey, Size: int64(len(csvBody))}}
	})

	_, err := h.reg.Register(context.Background(), testCaller(), src,
		Request{Connection: "scratch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "would return no rows")
	assert.Empty(t, h.trino.statements, "nothing runs when the source name is refused")
	assert.Empty(t, h.store.rows, "no registration is recorded")
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
// nothing, which a key like "d/-.csv" does.
func TestTableNameFor_FallsBackToTheRecordsName(t *testing.T) {
	h := newHarness(t)
	src := testSource()
	src.HeadKey = "artifacts/u1/asset_1/-.csv"
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
	src.HeadKey = "artifacts/u1/asset_1/-.csv"
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

// --- a CSV a line-based reader cannot read (#1441) ---

// A Hive CSV table splits records on "\n" before the quote-aware serde sees
// them, so a line break inside a quoted cell tears one record into fragments
// and shifts every field after the tear into the wrong column. Nothing about
// the created table says so -- the DDL succeeds, the row is written, and the
// query returns rows. These are the assertions that such a file never reaches
// a CREATE TABLE unanswered.

// tornCSV is the shape a spreadsheet export takes: two records carrying a
// multi-line address in one cell, one record already on a single line.
const tornCSV = "store_id,address,rebate\n" +
	"101,\"12 Mill Rd\nSuite 4\",\"$156,142.58 \"\n" +
	"102,\"9 Bay St\nSeattle WA\",4.5\n" +
	"103,880 Pine St,15%\n"

// tornHarness serves a file whose records carry line breaks inside cells.
func tornHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t, func(h *harness) { h.objects.body = []byte(tornCSV) })
}

// TestRegister_RefusesACSVTornByLineBreaks: unasked, the platform does not
// rewrite somebody's file, so the answer is a refusal that names what is wrong
// with it in their terms.
func TestRegister_RefusesACSVTornByLineBreaks(t *testing.T) {
	h := tornHarness(t)

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNeedsRepair, "the refusal is one the surfaces can offer to correct")
	assert.ErrorIs(t, err, ErrRefused, "and it is still a refusal, not a platform failure")
	assert.Contains(t, err.Error(), "2 rows")
	assert.Contains(t, err.Error(), "address")

	assert.Empty(t, h.trino.statements, "no table is created")
	assert.Empty(t, h.store.rows, "and no registration is recorded")
	assert.Empty(t, h.reviser.saved, "the file is not touched either")
}

// TestRegister_RepairSavesAVersionAndRegistersIt is the acceptance assertion:
// the corrected bytes go into the source's own version trail, and the table is
// built over that version's directory rather than the one it replaced.
func TestRegister_RepairSavesAVersionAndRegistersIt(t *testing.T) {
	h := tornHarness(t)

	res, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.NoError(t, err)

	require.Len(t, h.reviser.saved, 1, "exactly one version is written")
	saved := h.reviser.saved[0]
	assert.Equal(t, KindAsset, saved.sourceKind)
	assert.Equal(t, "asset_1", saved.sourceID)
	assert.Equal(t, "alice@example.com", saved.by, "the version is recorded against whoever asked")
	assert.Equal(t, "put 2 rows back onto one line", saved.summary)

	records, err := csv.NewReader(bytes.NewReader(saved.content)).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 4, "the header and one row per source record")
	assert.Equal(t, []string{"101", "12 Mill Rd Suite 4", "$156,142.58 "}, records[1])
	assert.Equal(t, []string{"102", "9 Bay St Seattle WA", "4.5"}, records[2])
	assert.Equal(t, []string{"103", "880 Pine St", "15%"}, records[3])

	// The table reads the corrected version's directory, which holds that one
	// file, and not the directory the uploaded bytes are still in.
	newDir := "s3://portal-assets/artifacts/u1/asset_1/v/rev_2/"
	assert.Equal(t, newDir, res.Location)
	assert.Contains(t, h.trino.statements[len(h.trino.statements)-1], "external_location = '"+newDir+"'")
	assert.Equal(t, "artifacts/u1/asset_1/v/rev_2/content.csv", res.Source.HeadKey,
		"the result reports the version the registration was built over")
	assert.False(t, res.IsStale(res.Source.Bucket, res.Source.HeadKey),
		"a registration made against the version it just wrote is not stale")

	require.NotNil(t, res.Repair)
	assert.Equal(t, 2, res.Repair.RowsRepaired)
	assert.Equal(t, 2, res.Repair.Version)
	assert.Contains(t, res.Repair.Summary(), "version 2")

	stored, err := h.store.Get(context.Background(), res.ID)
	require.NoError(t, err)
	assert.Equal(t, newDir, stored.Location)
}

// TestRegister_RepairConvertsBytesThatAreNotUTF8 is the second defect on the
// same path: the cell reads what it read in the source rather than arriving as
// a replacement mark.
func TestRegister_RepairConvertsBytesThatAreNotUTF8(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		// 0xA9 is the copyright sign in windows-1252 and is not valid UTF-8.
		h.objects.body = []byte("store_id,note\n101,15% \xa9 ACME\n")
	})

	res, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.NoError(t, err)

	require.Len(t, h.reviser.saved, 1)
	assert.Contains(t, string(h.reviser.saved[0].content), "15% © ACME")
	require.NotNil(t, res.Repair)
	assert.Equal(t, "windows-1252", res.Repair.FromEncoding)
	assert.Zero(t, res.Repair.RowsRepaired)
}

// TestRegister_RefusesACSVWhoseLinesEndInACarriageReturn. A file the reader
// splits into one record is the same failure as a torn one and is answered the
// same way: nothing is created, and the refusal says what the lines end in.
func TestRegister_RefusesACSVWhoseLinesEndInACarriageReturn(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.objects.body = []byte(macBody) })

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNeedsRepair, "the refusal is one the surfaces can offer to correct")
	assert.Contains(t, err.Error(), "carriage return")
	assert.NotContains(t, err.Error(), "line break inside a cell",
		"no cell holds a line break, and naming one would name columns the file does not have")

	assert.Empty(t, h.trino.statements, "no table is created")
	assert.Empty(t, h.store.rows, "and no registration is recorded")
}

// TestRegister_RepairGivesACarriageReturnFileTheRowsItHolds is the acceptance
// assertion for a file the query engine would otherwise read whole: the
// registered table declares the file's columns and reads one row per record.
func TestRegister_RepairGivesACarriageReturnFileTheRowsItHolds(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.objects.body = []byte(macBody) })

	res, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.NoError(t, err)

	require.Len(t, h.reviser.saved, 1)
	saved := h.reviser.saved[0]
	assert.Equal(t, "rewrote the carriage return line endings as newlines", saved.summary)

	records, err := csv.NewReader(bytes.NewReader(saved.content)).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 3, "the header and one row per source record, not one row holding the file")
	assert.Equal(t, []string{"101", "12 Mill Rd"}, records[1])
	assert.Equal(t, []string{"102", "9 Oak St"}, records[2])

	assert.Equal(t, []Column{
		{Name: "store_id", Type: "VARCHAR"},
		{Name: "address", Type: "VARCHAR"},
	}, res.Columns, "the table declares the file's own columns")

	require.NotNil(t, res.Repair)
	assert.Equal(t, "carriage return", res.Repair.FromLineEndings)
	assert.Zero(t, res.Repair.RowsRepaired, "no cell held a line break")
}

// TestRegister_RepairRefusesARaggedFile: filling in a short record invents data
// and dropping a field from a long one loses some, so a file the platform
// cannot correct honestly is refused with nothing written.
func TestRegister_RepairRefusesARaggedFile(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.objects.body = []byte("a,b,c\n1,\"x\ny\",3\n4,5\n")
	})

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "record 2 has 2")

	assert.Empty(t, h.reviser.saved, "no version is written")
	assert.Empty(t, h.trino.statements)
	assert.Empty(t, h.store.rows)
}

// TestRegister_LineSafeFileIsRegisteredUntouched, whether or not a correction
// was offered: a file that needed nothing gets no new version and the result
// says nothing extra about it.
func TestRegister_LineSafeFileIsRegisteredUntouched(t *testing.T) {
	for _, repair := range []bool{false, true} {
		h := newHarness(t)

		res, err := h.reg.Register(context.Background(), testCaller(), testSource(),
			Request{Connection: "scratch", Repair: repair})
		require.NoError(t, err)

		assert.Empty(t, h.reviser.saved, "nothing was rewritten")
		assert.Nil(t, res.Repair, "and nothing is reported")
		assert.Equal(t, "s3://portal-assets/artifacts/u1/asset_1/", res.Location)
		assert.Equal(t, "artifacts/u1/asset_1/content.csv", res.Source.HeadKey)
	}
}

// TestRegister_RepairWithoutAVersionTrailRefuses: a deployment with nowhere to
// put a corrected version says so and names the kind, rather than writing a
// derived copy somewhere the version panel cannot show.
func TestRegister_RepairWithoutAVersionTrailRefuses(t *testing.T) {
	h := tornHarness(t)
	h.reg = New(Deps{
		Store: h.store, Trino: h.trino, Audit: h.audit,
		Objects: map[string]ObjectReader{KindAsset: h.objects},
		NewID:   func() (string, error) { return "reg_a", nil },
	})

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "no version history for a stored asset")
	assert.Empty(t, h.trino.statements)
}

// TestRegister_RepairFailureLeavesNothingBehind: the version write is the first
// thing that changes the world, so its failure has to stop the registration
// rather than being registered around.
func TestRegister_RepairFailureLeavesNothingBehind(t *testing.T) {
	h := tornHarness(t)
	h.reviser.err = errors.New("version store unreachable")

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRefused, "a store outage is not something the caller can act on")
	assert.Contains(t, err.Error(), "version store unreachable")
	assert.Empty(t, h.trino.statements)
	assert.Empty(t, h.store.rows)
}

// TestRegister_RepairIsAudited: a correction rewrote somebody's file on their
// behalf, which is a write in its own right and belongs in the trail beside the
// statement that followed it.
func TestRegister_RepairIsAudited(t *testing.T) {
	h := tornHarness(t)

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true, Source: "mcp"})
	require.NoError(t, err)

	require.Len(t, h.audit.events, 1)
	repaired, ok := h.audit.events[0].Parameters["repaired"].(string)
	require.True(t, ok, "the correction is recorded on the registration's own event")
	assert.Contains(t, repaired, "put 2 rows back onto one line")
}

// TestRegister_AuthorityIsSettledBeforeTheFileIsTouched: a caller who may not
// take the name never gets their file rewritten on the way to being told so.
func TestRegister_AuthorityIsSettledBeforeTheFileIsTouched(t *testing.T) {
	h := tornHarness(t)
	require.NoError(t, h.store.Insert(context.Background(), Registration{
		ID: "reg_existing", SourceKind: KindAsset, SourceID: "asset_9",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "analyst_content", RegisteredBy: "bob@example.com",
	}))

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bob@example.com")
	assert.Empty(t, h.reviser.saved, "the refusal came before the correction")
}

// TestRegister_WideEncodingIsRefusedOutrightRatherThanOffered: a spreadsheet's
// "Unicode Text" export is UTF-16, whose every byte is valid windows-1252, so
// a correction would replace the person's data with a character per byte and
// report it as a repair. The refusal names the encoding and offers nothing,
// even when the correction was asked for. Both shapes of that export are
// refused: the one that leads with a byte-order mark, and the one that does
// not and is otherwise valid UTF-8 all the way through.
func TestRegister_WideEncodingIsRefusedOutrightRatherThanOffered(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		// appearsAs is what the refusal has to say about the file, so the
		// person is told what was found in it rather than only that it is
		// wrong.
		appearsAs string
	}{
		{"with a byte-order mark", utf16LE("store_id,note\n101,ok\n"), "UTF-16"},
		// The same export written without the mark. Its content is ASCII, so
		// every one of its NULs is valid UTF-8 and nothing but the NULs
		// themselves says it is not a UTF-8 file (#1447).
		{"with none", utf16LEUnmarked("store_id,note\n101,ok\n"), "NUL byte"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(h *harness) { h.objects.body = tc.body })

			for _, repair := range []bool{false, true} {
				_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
					Request{Connection: "scratch", Repair: repair})
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrRefused)
				assert.NotErrorIs(t, err, ErrNeedsRepair,
					"nothing is offered that the platform cannot honestly do")
				assert.Contains(t, err.Error(), tc.appearsAs)
				assert.Contains(t, err.Error(), "Re-export it as UTF-8 CSV")
			}
			assert.Empty(t, h.reviser.saved, "the file is never rewritten")
			assert.Empty(t, h.trino.statements, "and no table is created over it")
		})
	}
}

// TestRegister_ACorrectionThatARefusalFollowedIsStillReported. The correction
// is written before the last of the checks have run, so a refusal can arrive
// after the person's file has already changed. Both the answer they get and
// the audit trail say so; an answer about the table alone would leave them not
// knowing their file moved.
func TestRegister_ACorrectionThatARefusalFollowedIsStillReported(t *testing.T) {
	h := tornHarness(t)
	// The directory the correction lands in is fine; the listing itself fails,
	// which is the shape of any refusal that comes after the file was written.
	h.reviser.afterSave = func() { h.objects.listErr = errors.New("bucket unreachable") }

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.Error(t, err)
	require.Len(t, h.reviser.saved, 1, "the file was already corrected")

	assert.Contains(t, err.Error(), "put 2 rows back onto one line")
	assert.Contains(t, err.Error(), "The table was not created")
	require.NotNil(t, RepairOf(err), "a surface that rewrites the message keeps the part about the file")
	assert.Equal(t, 2, RepairOf(err).RowsRepaired)

	require.Len(t, h.audit.events, 1, "the correction is a write and is recorded")
	ev := h.audit.events[0]
	assert.False(t, ev.Success)
	assert.Contains(t, ev.Parameters["repaired"], "put 2 rows back onto one line")
	assert.Equal(t, "scratch.uploads.analyst_content", ev.Parameters["table"],
		"the event names the table it was for, not an empty one")
}

// TestRegister_ACorrectionThatAFailedDDLFollowedIsStillReported is the same
// rule one step later: the coordinator refused the statement, and the file has
// still changed.
func TestRegister_ACorrectionThatAFailedDDLFollowedIsStillReported(t *testing.T) {
	h := tornHarness(t)
	h.trino.err = errors.New("trino unreachable")

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "put 2 rows back onto one line")
	assert.Contains(t, err.Error(), "trino unreachable")
	require.NotNil(t, RepairOf(err))
	assert.Equal(t, 2, RepairOf(err).Version)
}

// TestRegister_ACorrectionThatAFailedInsertFollowedIsStillReported is the same
// rule one step later again: the table was made and the row recording it could
// not be written. The file changed on the way there and stays changed.
func TestRegister_ACorrectionThatAFailedInsertFollowedIsStillReported(t *testing.T) {
	h := tornHarness(t)
	h.store.insertErr = errors.New("connection refused to postgres:5432")

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.Error(t, err)
	require.Len(t, h.reviser.saved, 1, "the file did change, which is why it is said")

	assert.Contains(t, err.Error(), "put 2 rows back onto one line")
	require.NotNil(t, RepairOf(err), "a surface that rewrites the message keeps the part about the file")
	assert.Equal(t, 2, RepairOf(err).Version)
}

// TestRegister_ACorrectionThatAFailedDeleteFollowedIsStillReported covers the
// last of the four ways a registration fails: the replaced registration's row
// could not be removed.
func TestRegister_ACorrectionThatAFailedDeleteFollowedIsStillReported(t *testing.T) {
	h := tornHarness(t)
	require.NoError(t, h.store.Insert(context.Background(), Registration{
		ID: "reg_existing", SourceKind: KindAsset, SourceID: "asset_1",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "analyst_content", RegisteredBy: "alice@example.com",
	}))
	h.store.deleteErr = errors.New("connection refused to postgres:5432")

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Repair: true})
	require.Error(t, err)
	require.Len(t, h.reviser.saved, 1)

	assert.Contains(t, err.Error(), "put 2 rows back onto one line")
	require.NotNil(t, RepairOf(err))
	assert.Equal(t, 2, RepairOf(err).Version)
}

// TestRegister_ARecordThatCouldNotBeWrittenTakesItsTableBackOut is why the
// answer can tell the caller to register again: a table nothing records is one
// no surface lists and one BuildDDL will not drop, so the second attempt would
// meet it in Trino and fail. It is taken out with the failure that made it
// unrecorded.
func TestRegister_ARecordThatCouldNotBeWrittenTakesItsTableBackOut(t *testing.T) {
	h := newHarness(t)
	h.store.insertErr = errors.New("connection refused to postgres:5432")

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Source: "portal"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recording the registration")
	assert.Empty(t, h.store.rows)
	assert.Equal(t, `DROP TABLE IF EXISTS "scratch"."uploads"."analyst_content"`,
		h.trino.statements[len(h.trino.statements)-1])
	assert.Empty(t, h.trino.tables, "nothing of the registration is left in the coordinator")

	// The event written when the DDL ran says the table was made; the trail
	// would end there and read as a table that exists.
	require.Len(t, h.audit.events, 2)
	assert.True(t, h.audit.events[0].Success)
	last := h.audit.events[1]
	assert.False(t, last.Success)
	assert.Contains(t, last.Parameters["sql"], "DROP TABLE IF EXISTS")
	assert.Contains(t, last.ErrorMessage, "connection refused")

	// And so the answer it gives is true: registering again works.
	h.store.insertErr = nil
	reg, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Source: "portal"})
	require.NoError(t, err)
	assert.Equal(t, "scratch.uploads.analyst_content", reg.QualifiedName())
}

// TestRegister_AReplacementThatCouldNotBeRecordedTakesItsTableBackOut is the
// same rule on the other store write, and the state it is for is the one
// nothing else would report. The DDL has already re-pointed the table at the
// new file with the new columns; a surviving row would go on advertising the
// old ones, and because this replacement registers the same key it is not even
// marked stale.
func TestRegister_AReplacementThatCouldNotBeRecordedTakesItsTableBackOut(t *testing.T) {
	h := newHarness(t)
	previous := Registration{
		ID: "reg_existing", SourceKind: KindAsset, SourceID: "asset_1",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "analyst_content", RegisteredBy: "alice@example.com",
		Location: LocationURI("portal-assets", "artifacts/u1/asset_1/"),
		Columns:  []Column{{Name: "store_id", Type: "VARCHAR"}},
	}
	require.NoError(t, h.store.Insert(context.Background(), previous))
	h.store.deleteErr = errors.New("connection refused to postgres:5432")

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Source: "portal"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "replacing the previous registration")

	kept, getErr := h.store.Get(context.Background(), "reg_existing")
	require.NoError(t, getErr, "the row the delete could not remove is still there")
	assert.False(t, kept.IsStale(testSource().Bucket, testSource().HeadKey),
		"and nothing marks it, which is why the table it names must not be serving other columns")
	assert.Empty(t, h.trino.tables, "so the table is taken back out")
	assert.Equal(t, `DROP TABLE IF EXISTS "scratch"."uploads"."analyst_content"`,
		h.trino.statements[len(h.trino.statements)-1])

	// A query against the row now fails rather than answering with the wrong
	// columns, and registering again is what repairs it.
	h.store.deleteErr = nil
	reg, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Source: "portal"})
	require.NoError(t, err)
	assert.Equal(t, []Column{
		{Name: "store_id", Type: "VARCHAR"},
		{Name: "vendor_code", Type: "VARCHAR"},
		{Name: "rebate_pct", Type: "VARCHAR"},
	}, reg.Columns)
	_, getErr = h.store.Get(context.Background(), "reg_existing")
	assert.ErrorIs(t, getErr, ErrNotFound)
}

// replacedHarness sets up the registration a replacement takes over, and the
// table in the coordinator that it names.
func replacedHarness(t *testing.T) (*harness, Registration) {
	t.Helper()
	h := newHarness(t)
	previous := Registration{
		ID: "reg_existing", SourceKind: KindAsset, SourceID: "asset_1",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "analyst_content", RegisteredBy: "alice@example.com",
		Location: LocationURI("portal-assets", "artifacts/u1/asset_1/"),
		Columns:  []Column{{Name: "store_id", Type: "VARCHAR"}},
	}
	require.NoError(t, h.store.Insert(context.Background(), previous))
	h.trino.tables = map[string]bool{`"scratch"."uploads"."analyst_content"`: true}
	return h, previous
}

// TestRegister_AReplacementWhoseCreateFailedForgetsTheTableItDropped. A
// replacement drops before it creates, so a coordinator that refuses the
// CREATE leaves the previous table gone and its row the only thing still
// saying it exists -- listed, offered with a sample query, and not marked
// stale, because the location it compares did not move.
func TestRegister_AReplacementWhoseCreateFailedForgetsTheTableItDropped(t *testing.T) {
	h, previous := replacedHarness(t)
	assert.False(t, previous.IsStale(testSource().Bucket, testSource().HeadKey),
		"nothing would mark the row, which is why it cannot be left behind")
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, createTablePrefix) {
			return errors.New("trino coordinator unreachable")
		}
		return nil
	}

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Source: "portal"})
	require.Error(t, err)
	assert.Empty(t, h.trino.tables, "the DROP ran, so the table it named is gone")
	_, getErr := h.store.Get(context.Background(), "reg_existing")
	assert.ErrorIs(t, getErr, ErrNotFound, "and the row that named it goes with it")

	require.Len(t, h.audit.events, 2)
	assert.Contains(t, h.audit.events[1].ErrorMessage, "trino coordinator unreachable")

	// Registering again is what the answer says to do, and it is a fresh
	// registration rather than a replacement of a record that is gone.
	h.trino.errFor = nil
	reg, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Source: "portal"})
	require.NoError(t, err)
	assert.NotEqual(t, "reg_existing", reg.ID)
	assert.Equal(t, "scratch.uploads.analyst_content", reg.QualifiedName())
}

// TestRegister_ARecordThatCannotBeForgottenStillAnswers: removing the row is
// reconciliation, so its failure does not replace the coordinator failure the
// caller can act on, and the trail carries both.
func TestRegister_ARecordThatCannotBeForgottenStillAnswers(t *testing.T) {
	h, _ := replacedHarness(t)
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, createTablePrefix) {
			return errors.New("trino coordinator unreachable")
		}
		return nil
	}
	h.store.deleteErr = errors.New("connection refused to postgres:5432")

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Source: "portal"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trino coordinator unreachable")
	assert.NotContains(t, err.Error(), "postgres:5432")

	require.Len(t, h.audit.events, 2)
	assert.Contains(t, h.audit.events[1].ErrorMessage, "trino coordinator unreachable")
	assert.Contains(t, h.audit.events[1].ErrorMessage, "connection refused to postgres:5432")
}

// TestRegister_AReplacementWhoseDropFailedKeepsTheRecordItDescribes is the
// other half of the same rule: the DROP is the statement that failed, so
// nothing ran that changed anything and the previous registration is still
// accurate. Removing it there is what would leave a table nothing records.
func TestRegister_AReplacementWhoseDropFailedKeepsTheRecordItDescribes(t *testing.T) {
	h, previous := replacedHarness(t)
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, dropTablePrefix) {
			return errors.New("trino coordinator unreachable")
		}
		return nil
	}

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch", Source: "portal"})
	require.Error(t, err)

	kept, getErr := h.store.Get(context.Background(), "reg_existing")
	require.NoError(t, getErr)
	assert.Equal(t, previous.Columns, kept.Columns)
	assert.True(t, h.trino.tables[`"scratch"."uploads"."analyst_content"`],
		"the table it describes is still there")
	require.Len(t, h.audit.events, 1, "nothing was reconciled, so nothing else is recorded")
}

// TestRegister_ATableThatCannotBeTakenBackOutStillAnswers: the drop is
// cleanup, so its failure does not replace the store failure the caller can
// act on.
func TestRegister_ATableThatCannotBeTakenBackOutStillAnswers(t *testing.T) {
	h := newHarness(t)
	h.store.insertErr = errors.New("connection refused to postgres:5432")
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, dropTablePrefix) {
			return errors.New("trino coordinator unreachable")
		}
		return nil
	}

	_, err := h.reg.Register(context.Background(), testCaller(), testSource(),
		Request{Connection: "scratch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused to postgres:5432")
	assert.NotContains(t, err.Error(), "coordinator unreachable")

	// The trail has to say the table is still there, because nothing else
	// records it now.
	require.Len(t, h.audit.events, 2)
	assert.Contains(t, h.audit.events[1].ErrorMessage, "connection refused to postgres:5432")
	assert.Contains(t, h.audit.events[1].ErrorMessage, "coordinator unreachable")
}

// TestRegister_ARecordThatCouldNotBeWrittenIsCleanedUpAfterTheCallerLeaves:
// the store write that failed may have failed because the request was
// canceled, and the table it left behind is exactly what has to be removed
// then.
func TestRegister_ARecordThatCouldNotBeWrittenIsCleanedUpAfterTheCallerLeaves(t *testing.T) {
	h := newHarness(t)
	h.store.insertErr = context.Canceled

	ctx, cancel := context.WithCancel(context.Background())
	h.store.onInsert = cancel

	_, err := h.reg.Register(ctx, testCaller(), testSource(), Request{Connection: "scratch"})
	require.ErrorIs(t, err, context.Canceled)
	require.NotEmpty(t, h.trino.statements, "the table was made before the request went away")
	assert.Empty(t, h.trino.tables, "the drop ran on a context the cancellation does not reach")
}

// TestRepairOf_CarriesNothingWhenNothingWasCorrected, so a surface asking is
// told plainly rather than having to know which errors can carry one.
func TestRepairOf_CarriesNothingWhenNothingWasCorrected(t *testing.T) {
	assert.Nil(t, RepairOf(nil))
	assert.Nil(t, RepairOf(errors.New("trino unreachable")))
	assert.Nil(t, RepairOf(repairedFailure(nil, errors.New("trino unreachable"))))
}

// TestRepairedFailure_KeepsWhatWentWrongMatchable. The correction is added to
// the answer, not put in front of it: every surface that decides a status or a
// retry from the underlying error still reaches it.
func TestRepairedFailure_KeepsWhatWentWrongMatchable(t *testing.T) {
	underlying := refusedf("the file is larger than the 100 MB a registration reads")
	wrapped := repairedFailure(&RepairReport{Version: 2}, underlying)

	assert.ErrorIs(t, wrapped, underlying)
	assert.ErrorIs(t, wrapped, ErrRefused)
	assert.Contains(t, wrapped.Error(), "larger than the 100 MB")
}
