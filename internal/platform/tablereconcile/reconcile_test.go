package tablereconcile

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// fakeEngine is a scratch schema: its tables by name with the location each
// reads, its views, and the statements it was sent.
type fakeEngine struct {
	noTarget    bool
	tables      map[string]string
	views       []string
	truncated   bool
	listErr     error
	locationErr error
	existsErr   error
	execErr     error
	sent        []string
}

func (f *fakeEngine) ScratchTarget(string) (trino.ScratchConfig, bool) {
	return trino.ScratchConfig{Catalog: "scratch", Schema: "uploads"}, !f.noTarget
}

func (f *fakeEngine) ListTables(context.Context, string, string, string) ([]trino.SchemaTable, bool, error) {
	if f.listErr != nil {
		return nil, false, f.listErr
	}
	out := make([]trino.SchemaTable, 0, len(f.tables))
	for name := range f.tables {
		out = append(out, trino.SchemaTable{Name: name})
	}
	for _, v := range f.views {
		out = append(out, trino.SchemaTable{Name: v, View: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, f.truncated, nil
}

func (f *fakeEngine) TableLocation(_ context.Context, _, _, _, table string) (location string, found bool, err error) {
	if f.locationErr != nil {
		return "", false, f.locationErr
	}
	loc, ok := f.tables[table]
	return loc, ok, nil
}

func (f *fakeEngine) TableExists(_ context.Context, _, _, _, table string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	_, ok := f.tables[table]
	return ok || slices.Contains(f.views, table), nil
}

func (f *fakeEngine) Exec(_ context.Context, _, sql string) error {
	f.sent = append(f.sent, sql)
	if f.execErr != nil {
		return f.execErr
	}
	for name := range f.tables {
		if strings.HasSuffix(sql, `."`+name+`"`) && strings.HasPrefix(sql, "DROP TABLE") {
			delete(f.tables, name)
		}
	}
	return nil
}

// fakeStore holds registrations by id.
type fakeStore struct {
	rows      map[string]tableregister.Registration
	listErr   error
	byNameErr error
	deleteErr error
}

func (f *fakeStore) List(_ context.Context, flt tableregister.Filter) ([]tableregister.Registration, int, error) {
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	var out []tableregister.Registration
	for _, r := range f.rows {
		if slices.Contains(flt.Connections, r.Connection) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	total := len(out)
	if flt.Offset >= total {
		return nil, total, nil
	}
	return out[flt.Offset:min(flt.Offset+flt.EffectiveLimit(), total)], total, nil
}

func (f *fakeStore) ByName(_ context.Context, _, _, _, table string) (*tableregister.Registration, error) {
	if f.byNameErr != nil {
		return nil, f.byNameErr
	}
	for _, r := range f.rows {
		if r.Table == table {
			return &r, nil
		}
	}
	return nil, nil //nolint:nilnil // no row holds the name
}

func (f *fakeStore) Get(_ context.Context, id string) (*tableregister.Registration, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, tableregister.ErrNotFound
	}
	return &r, nil
}

func (f *fakeStore) Delete(_ context.Context, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.rows, id)
	return nil
}

// fakeAudit records the repairs audited.
type fakeAudit struct{ events []string }

func (f *fakeAudit) AuditStatement(_ context.Context, s tableregister.Statement) {
	f.events = append(f.events, s.Source+":"+s.Registration.Table)
}

// The asset the platform holds: testSource's file, under its own directory.
var held = tableregister.Source{
	Kind: tableregister.KindAsset, ID: "asset_1", Bucket: "portal-assets",
	HeadKey: "artifacts/u1/asset_1/content.csv",
}

func reg(id, table, kind, location string) tableregister.Registration {
	return tableregister.Registration{
		ID: id, SourceKind: kind, SourceID: id, Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: table, Location: location, RegisteredBy: "alice@example.com",
	}
}

type rig struct {
	rec    *Reconciler
	engine *fakeEngine
	store  *fakeStore
	audit  *fakeAudit
}

func newRig() *rig {
	r := &rig{
		engine: &fakeEngine{tables: map[string]string{}},
		store:  &fakeStore{rows: map[string]tableregister.Registration{}},
		audit:  &fakeAudit{},
	}
	r.rec = New(Deps{
		Engine: r.engine, Store: r.store, Audit: r.audit, ResourceBucket: "resources",
		Locate: func(_ context.Context, kind, id string) (tableregister.Source, bool) {
			if kind == held.Kind && id == held.ID {
				return held, true
			}
			return tableregister.Source{}, false
		},
	})
	return r
}

// add registers a table and puts it in the schema.
func (r *rig) add(row tableregister.Registration, present bool) {
	r.store.rows[row.ID] = row
	if present {
		r.engine.tables[row.Table] = row.Location
	}
}

var ctx = context.Background()

// TestReconcile is #2136's three groups: a table no registration holds, with
// the file its location is the directory of when there is one; a
// registration whose table is gone; and a registration whose table reads
// somewhere else. A webhook source's raw table and its view are neither.
func TestReconcile(t *testing.T) {
	r := newRig()
	r.add(reg("kept", "analyst_kept", tableregister.KindAsset, "s3://portal-assets/artifacts/u1/asset_1/"), true)
	r.add(reg("dropped", "analyst_dropped", tableregister.KindAsset, "s3://portal-assets/artifacts/u1/asset_1/"), false)
	r.add(reg("moved", "analyst_moved", tableregister.KindAsset, "s3://portal-assets/artifacts/u1/asset_1/"), false)
	r.engine.tables["analyst_moved"] = "s3://portal-assets/elsewhere/"
	r.add(reg("wh", "webhook_esp", tableregister.KindWebhook, "s3://resources/webhooks/esp/"), false)
	r.engine.views = []string{"webhook_esp"}
	r.add(reg("whgone", "webhook_gone", tableregister.KindWebhook, "s3://resources/webhooks/gone/"), false)
	r.engine.tables["left_over"] = "s3://portal-assets/artifacts/u1/asset_1/"
	r.engine.tables["deleted_upload"] = "s3://resources/resources/user/u1/res_gone/v/x/"
	r.engine.tables["by_hand"] = "s3://somebody/else/"
	r.engine.tables["webhook_esp_raw"] = "s3://resources/webhooks/esp/raw/"

	got, err := r.rec.Reconcile(ctx, "scratch")
	require.NoError(t, err)
	orphans := map[string]Orphan{}
	for _, o := range got.Orphans {
		orphans[o.Table] = o
	}
	require.Len(t, orphans, 3, "the webhook source's raw table is not an orphan: %v", got.Orphans)
	assert.Equal(t, &SourceRef{Kind: tableregister.KindAsset, ID: "asset_1", Exists: true}, orphans["left_over"].Source)
	assert.True(t, orphans["left_over"].Provable)
	assert.Equal(t, &SourceRef{Kind: tableregister.KindResource, ID: "res_gone"}, orphans["deleted_upload"].Source,
		"a managed resource's directory is the platform's whether or not the resource is still there")
	assert.False(t, orphans["by_hand"].Provable)
	assert.Nil(t, orphans["by_hand"].Source)

	missing := map[string]bool{}
	for _, m := range got.Missing {
		missing[m.Registration.Table] = m.Provable
	}
	assert.Equal(t, map[string]bool{"analyst_dropped": true, "webhook_gone": false}, missing)
	require.Len(t, got.Drifted, 1)
	assert.Equal(t, "analyst_moved", got.Drifted[0].Registration.Table)
	assert.Equal(t, "s3://portal-assets/elsewhere/", got.Drifted[0].Location)
}

// TestReconcileLeavesManagedTablesAlone: a table with no external location is
// a managed table, which a registration never is (a registration is an
// external table over a stored file), so it is not an orphan. A managed
// script's working tables are the case (#2141).
func TestReconcileLeavesManagedTablesAlone(t *testing.T) {
	r := newRig()
	r.engine.tables["ht_art_culture_batches"] = ""
	r.engine.tables["by_hand"] = "s3://somebody/else/"

	got, err := r.rec.Reconcile(ctx, "scratch")
	require.NoError(t, err)
	require.Len(t, got.Orphans, 1, "a managed table is not an orphan: %v", got.Orphans)
	assert.Equal(t, "by_hand", got.Orphans[0].Table)

	out, err := r.rec.Repair(ctx, tableregister.Caller{}, "scratch",
		[]RepairItem{{Action: ActionDrop, Table: "ht_art_culture_batches"}})
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.False(t, out[0].Done, "a managed table is never dropped by a repair")
	assert.Contains(t, r.engine.tables, "ht_art_culture_batches")
}

// TestReconcileCutListing: a listing cut short reports no registration as
// missing, since its table may be past the cut, and empty groups are lists.
func TestReconcileCutListing(t *testing.T) {
	r := newRig()
	r.add(reg("x", "analyst_x", tableregister.KindAsset, "s3://b/x/"), false)
	r.engine.truncated = true
	got, err := r.rec.Reconcile(ctx, "scratch")
	require.NoError(t, err)
	assert.True(t, got.Truncated)
	assert.Empty(t, got.Missing)
	assert.NotNil(t, got.Orphans)
	assert.NotNil(t, got.Drifted)
}

// TestReconcileReadsEveryPage: registrations past one store page are read.
func TestReconcileReadsEveryPage(t *testing.T) {
	r := newRig()
	for i := range tableregister.MaxListLimit + 5 {
		id := "r" + strings.Repeat("x", i)
		r.add(reg(id, id, tableregister.KindAsset, "s3://b/"+id+"/"), true)
	}
	got, err := r.rec.Reconcile(ctx, "scratch")
	require.NoError(t, err)
	assert.Empty(t, got.Orphans, "every table past the first page is found registered")
}

// TestReconcileFailures: no scratch target, a listing, a registration read
// and a definition read that fail are each an error.
func TestReconcileFailures(t *testing.T) {
	r := newRig()
	r.engine.noTarget = true
	_, err := r.rec.Reconcile(ctx, "scratch")
	require.ErrorIs(t, err, tableregister.ErrNoScratchTarget)

	r = newRig()
	r.engine.listErr = errors.New("down")
	_, err = r.rec.Reconcile(ctx, "scratch")
	require.Error(t, err)

	r = newRig()
	r.store.listErr = errors.New("down")
	_, err = r.rec.Reconcile(ctx, "scratch")
	require.Error(t, err)

	r = newRig()
	r.engine.tables["t"] = "s3://x/y/"
	r.engine.locationErr = errors.New("down")
	_, err = r.rec.Reconcile(ctx, "scratch")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading the location of t")
}

// TestRepair: a provable orphan is dropped and a registration whose table is
// gone is forgotten, both audited; every repair that cannot be proved at the
// moment it is made is reported and not made.
func TestRepair(t *testing.T) {
	r := newRig()
	admin := tableregister.Caller{Email: "admin@example.com", IsAdmin: true}
	r.add(reg("gone", "analyst_gone", tableregister.KindAsset, "s3://portal-assets/artifacts/u1/asset_1/"), false)
	r.add(reg("here", "analyst_here", tableregister.KindAsset, "s3://portal-assets/artifacts/u1/asset_1/"), true)
	r.add(reg("wh", "webhook_esp", tableregister.KindWebhook, ""), false)
	r.engine.tables["left_over"] = "s3://portal-assets/artifacts/u1/asset_1/"
	r.engine.tables["by_hand"] = "s3://somebody/else/"

	out, err := r.rec.Repair(ctx, admin, "scratch", []RepairItem{
		{Action: ActionDrop, Table: "left_over"},
		{Action: ActionDrop, Table: "by_hand"},
		{Action: ActionDrop, Table: "analyst_here"},
		{Action: ActionDrop, Table: "never_was"},
		{Action: ActionForget, RegistrationID: "gone"},
		{Action: ActionForget, RegistrationID: "here"},
		{Action: ActionForget, RegistrationID: "wh"},
		{Action: ActionForget, RegistrationID: "nope"},
		{Action: "rename"},
	})
	require.NoError(t, err)
	require.Len(t, out, 9)
	assert.True(t, out[0].Done, out[0].Reason)
	assert.NotContains(t, r.engine.tables, "left_over")
	assert.Equal(t, []string{`DROP TABLE IF EXISTS "scratch"."uploads"."left_over"`}, r.engine.sent)
	assert.Contains(t, out[1].Reason, "not the platform's")
	assert.Contains(t, r.engine.tables, "by_hand")
	assert.Contains(t, out[2].Reason, "a registration holds this table")
	assert.Contains(t, out[3].Reason, "already gone")
	assert.True(t, out[4].Done, out[4].Reason)
	assert.NotContains(t, r.store.rows, "gone")
	assert.Contains(t, out[5].Reason, "the table exists")
	assert.Contains(t, out[6].Reason, "webhook source")
	assert.Contains(t, out[7].Reason, "no registration nope")
	assert.Contains(t, out[8].Reason, "unknown action")
	assert.Equal(t, []string{"admin:left_over", "admin:analyst_gone"}, r.audit.events)

	r.engine.noTarget = true
	_, err = r.rec.Repair(ctx, admin, "scratch", nil)
	require.ErrorIs(t, err, tableregister.ErrNoScratchTarget)
}

// TestRepairFailures: a store or engine that fails a repair's proof or its
// action is reported on that repair.
func TestRepairFailures(t *testing.T) {
	admin := tableregister.Caller{Email: "admin@example.com", IsAdmin: true}
	drop := []RepairItem{{Action: ActionDrop, Table: "left_over"}}
	forget := []RepairItem{{Action: ActionForget, RegistrationID: "gone"}}
	for name, tc := range map[string]struct {
		set   func(r *rig)
		items []RepairItem
		want  string
	}{
		"drop fails":     {func(r *rig) { r.engine.execErr = errors.New("metastore down") }, drop, "the drop failed"},
		"location fails": {func(r *rig) { r.engine.locationErr = errors.New("down") }, drop, "could not be read"},
		"by name fails":  {func(r *rig) { r.store.byNameErr = errors.New("down") }, drop, "registrations could not be read"},
		"exists fails":   {func(r *rig) { r.engine.existsErr = errors.New("down") }, forget, "could not be looked up"},
		"delete fails":   {func(r *rig) { r.store.deleteErr = errors.New("down") }, forget, "could not be removed"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			r.engine.tables["left_over"] = "s3://portal-assets/artifacts/u1/asset_1/"
			r.add(reg("gone", "analyst_gone", tableregister.KindAsset, "s3://b/"), false)
			tc.set(r)
			out, err := r.rec.Repair(ctx, admin, "scratch", tc.items)
			require.NoError(t, err)
			assert.False(t, out[0].Done)
			assert.Contains(t, out[0].Reason, tc.want)
		})
	}
}

func TestNewNeedsItsPorts(t *testing.T) {
	assert.Nil(t, New(Deps{}))
	assert.Nil(t, resourceByLayout("resources/user/u1"))
	assert.Nil(t, newRig().rec.sourceAt(ctx, "hdfs://x/y"))
	noLocate := New(Deps{Engine: &fakeEngine{}, Store: &fakeStore{}, Audit: &fakeAudit{}})
	assert.Nil(t, noLocate.sourceAt(ctx, "s3://resources/resources/user/u1/r1/"), "no resources bucket named")
}
