package whtable

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// fakeExec records statements and fails the first that contains failOn.
type fakeExec struct {
	stmts    []string
	failOn   string
	failWith error
	scratch  map[string]trino.ScratchConfig
	readOnly bool
}

func (f *fakeExec) Exec(_ context.Context, _, sql string) error {
	f.stmts = append(f.stmts, sql)
	if f.failOn != "" && strings.Contains(sql, f.failOn) {
		return f.failWith
	}
	return nil
}

func (f *fakeExec) ScratchTarget(c string) (trino.ScratchConfig, bool) {
	sc, ok := f.scratch[c]
	return sc, ok
}

func (f *fakeExec) AcceptsWrites(string) bool { return !f.readOnly }

var (
	ctx = context.Background()
	src = whsource.Source{Name: "crm-contacts"}
	tg  = Target{Connection: "scratch", Catalog: "scratch_resources", Schema: "uploads"}
)

func TestTargetFor(t *testing.T) {
	f := &fakeExec{scratch: map[string]trino.ScratchConfig{
		"scratch": {Catalog: "scratch_resources", Schema: "uploads"},
		"half":    {Catalog: "c"},
	}}
	tables := New(f, "managed-resources")
	got, err := tables.TargetFor("scratch")
	require.NoError(t, err)
	assert.Equal(t, tg, got)

	_, err = tables.TargetFor("none")
	assert.ErrorIs(t, err, ErrNoScratchTarget)
	_, err = tables.TargetFor("half")
	assert.ErrorIs(t, err, ErrNoScratchTarget)
	f.readOnly = true
	_, err = tables.TargetFor("scratch")
	assert.ErrorIs(t, err, ErrReadOnly)
}

func TestCreate(t *testing.T) {
	f := &fakeExec{}
	require.NoError(t, New(f, "managed-resources").Create(ctx, tg, src))
	require.Len(t, f.stmts, 4)
	assert.Equal(t, `CREATE SCHEMA IF NOT EXISTS "scratch_resources"."uploads"`, f.stmts[0])
	assert.Contains(t, f.stmts[1], `CREATE TABLE IF NOT EXISTS "scratch_resources"."uploads"."webhook_crm_contacts_raw"`)
	assert.Contains(t, f.stmts[1], `"received_at" timestamp(6), "landed_at" timestamp(6), "event_id" varchar`)
	assert.Contains(t, f.stmts[1], `"payload" varchar, "dt" varchar, "hour" varchar, "minute" varchar`)
	assert.Contains(t, f.stmts[1], `external_location = 's3://managed-resources/webhooks/crm-contacts/raw/', format = 'JSON', partitioned_by = ARRAY['dt', 'hour', 'minute']`)
	assert.Contains(t, f.stmts[2], `"webhook_crm_contacts_compacted"`)
	assert.Contains(t, f.stmts[2], `format = 'PARQUET'`)
	assert.Contains(t, f.stmts[3], `CREATE OR REPLACE VIEW "scratch_resources"."uploads"."webhook_crm_contacts" SECURITY INVOKER AS`)
	assert.Contains(t, f.stmts[3], `FROM "scratch_resources"."uploads"."webhook_crm_contacts_compacted$partitions" p WHERE p."dt" = r."dt" AND p."hour" = r."hour" AND p."minute" = r."minute"`)

	f = &fakeExec{failOn: "VIEW", failWith: errors.New("no")}
	assert.Error(t, New(f, "b").Create(ctx, tg, src))
}

func TestDrop(t *testing.T) {
	f := &fakeExec{failOn: "DROP VIEW", failWith: errors.New("no")}
	err := New(f, "b").Drop(ctx, tg, src)
	require.Error(t, err)
	assert.Len(t, f.stmts, 3, "a failed drop does not stop the others")
	f = &fakeExec{}
	require.NoError(t, New(f, "b").Drop(ctx, tg, src))
}

func TestPartitions(t *testing.T) {
	start := time.Date(2026, 9, 24, 7, 15, 0, 0, time.UTC)
	f := &fakeExec{}
	tables := New(f, "managed-resources")
	require.NoError(t, tables.RegisterWindow(ctx, tg, src, start, "s3://managed-resources/resources/global/global/r1/"))
	require.Len(t, f.stmts, 2, "registering replaces what the window pointed at")
	assert.Equal(t, `CALL "scratch_resources".system.unregister_partition('uploads', 'webhook_crm_contacts_compacted', ARRAY['dt', 'hour', 'minute'], ARRAY['2026-09-24', '07', '15'])`, f.stmts[0])
	assert.Equal(t, `CALL "scratch_resources".system.register_partition('uploads', 'webhook_crm_contacts_compacted', ARRAY['dt', 'hour', 'minute'], ARRAY['2026-09-24', '07', '15'], 's3://managed-resources/resources/global/global/r1/')`, f.stmts[1])

	require.NoError(t, tables.SyncRaw(ctx, tg, src))
	assert.Equal(t, `CALL "scratch_resources".system.sync_partition_metadata('uploads', 'webhook_crm_contacts_raw', 'FULL')`, f.stmts[2])

	missing := &fakeExec{failOn: "unregister_partition", failWith: errors.New(`query failed: USER_ERROR: Partition 'dt=2026-09-24/hour=07/minute=15' does not exist`)}
	assert.NoError(t, New(missing, "b").UnregisterWindow(ctx, tg, src, start), "a window with no partition is already unregistered")

	disabled := &fakeExec{failOn: "register_partition(", failWith: errors.New("register_partition procedure is disabled")}
	err := New(disabled, "b").RegisterWindow(ctx, tg, src, start, "s3://x/")
	assert.ErrorIs(t, err, ErrRegisterDisabled)

	other := &fakeExec{failOn: "unregister_partition", failWith: errors.New("boom")}
	assert.Error(t, New(other, "b").RegisterWindow(ctx, tg, src, start, "s3://x/"))
	sync := &fakeExec{failOn: "sync_partition_metadata", failWith: errors.New("boom")}
	assert.Error(t, New(sync, "b").SyncRaw(ctx, tg, src))
}

func TestRegisterRawWindow(t *testing.T) {
	start := time.Date(2026, 9, 24, 7, 15, 0, 0, time.UTC)
	f := &fakeExec{}
	require.NoError(t, New(f, "managed-resources").RegisterRawWindow(ctx, tg, src, start))
	assert.Equal(t, `CALL "scratch_resources".system.register_partition('uploads', 'webhook_crm_contacts_raw', ARRAY['dt', 'hour', 'minute'], ARRAY['2026-09-24', '07', '15'], 's3://managed-resources/webhooks/crm-contacts/raw/dt=2026-09-24/hour=07/minute=15/')`, f.stmts[0])

	exists := &fakeExec{failOn: "register_partition", failWith: errors.New("query failed: Partition [dt=2026-09-24/hour=07/minute=15] is already registered with location s3://managed-resources/webhooks/crm-contacts/raw/dt=2026-09-24/hour=07/minute=15/")}
	assert.NoError(t, New(exists, "b").RegisterRawWindow(ctx, tg, src, start))
	other := &fakeExec{failOn: "register_partition", failWith: errors.New("boom")}
	assert.Error(t, New(other, "b").RegisterRawWindow(ctx, tg, src, start))
}

func TestProbe(t *testing.T) {
	f := &fakeExec{}
	require.NoError(t, New(f, "managed-resources").Probe(ctx, tg, src))
	require.Len(t, f.stmts, 4)
	assert.Equal(t, `SELECT count(*) FROM "scratch_resources"."uploads"."webhook_crm_contacts"`, f.stmts[0])
	assert.Contains(t, f.stmts[2], `ARRAY['1970-01-01', '00', '00'], 's3://managed-resources/webhooks/crm-contacts/compacted/probe/'`)
	assert.Contains(t, f.stmts[3], "unregister_partition")

	unreadable := &fakeExec{failOn: "SELECT count", failWith: errors.New("bucket does not exist")}
	err := New(unreadable, "managed-resources").Probe(ctx, tg, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the managed-resources bucket managed-resources")

	disabled := &fakeExec{failOn: "register_partition(", failWith: errors.New("register_partition procedure is disabled")}
	assert.ErrorIs(t, New(disabled, "b").Probe(ctx, tg, src), ErrRegisterDisabled)
}

func TestQuoting(t *testing.T) {
	assert.Equal(t, `"a""b"`, quoteIdent(`a"b`))
	assert.Equal(t, `'it''s'`, quoteLiteral("it's"))
	assert.Equal(t, "s3://b/x/", New(nil, "b").S3Location("/x/"))
}
