//go:build integration

package producedby

// Real-Postgres tests for the producer relation (#1569). The upsert is written
// as an ON CONFLICT expression over the row's own current values -- created is
// OR-ed, the label falls back to what is stored, the version takes the greater
// -- so what a second write does to a row is a property of PostgreSQL's
// semantics rather than something a mocked driver can show.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func write(target, id, kind, producer, label string, created bool, version int) Write {
	return Write{
		TargetKind: target, TargetID: id,
		Producer: Producer{Kind: kind, ID: producer, Label: label},
		Created:  created, Version: version,
	}
}

// TestRecordCountsOneSaveOnce_RealDB pins the two-note shape of an asset save:
// the create counts, and version 1 records the version without counting again.
func TestRecordCountsOneSaveOnce_RealDB(t *testing.T) {
	store := NewPostgres(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-once", KindSession, "sess-1", "", true, 0)))
	first := write(TargetAsset, "asset-once", KindSession, "sess-1", "", false, 1)
	first.Uncounted = true
	require.NoError(t, store.Record(ctx, first))

	rows, err := store.ListByTarget(ctx, TargetAsset, "asset-once")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].WriteCount, "one save is one write, not two")
	assert.Equal(t, 1, rows[0].LastVersion, "the save still recorded the version it wrote")
	assert.True(t, rows[0].Created)

	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-once", KindSession, "sess-1", "", false, 2)))
	rows, err = store.ListByTarget(ctx, TargetAsset, "asset-once")
	require.NoError(t, err)
	assert.Equal(t, 2, rows[0].WriteCount)
	assert.Equal(t, 2, rows[0].LastVersion)
}

// TestRecordFoldsRepeatWrites_RealDB is acceptance criterion 1: a second run of
// the same script updates the row it already has rather than adding another.
func TestRecordFoldsRepeatWrites_RealDB(t *testing.T) {
	store := NewPostgres(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-1", KindScript, "script-1", "daily-sales", true, 1)))
	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-1", KindScript, "script-1", "daily-sales", false, 2)))
	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-1", KindScript, "script-1", "daily-sales", false, 3)))

	rows, err := store.ListByTarget(ctx, TargetAsset, "asset-1")
	require.NoError(t, err)
	require.Len(t, rows, 1, "three writes by one producer are one row")
	assert.Equal(t, 3, rows[0].WriteCount)
	assert.Equal(t, 3, rows[0].LastVersion)
	assert.True(t, rows[0].Created, "a later modification must not demote the creator")
	assert.False(t, rows[0].LastWriteAt.Before(rows[0].FirstWriteAt))
}

// TestRecordKeepsTheKindsApart pins why the kind is in the key: an asset id and
// a resource id are separate id spaces and the same string can name both.
func TestRecordKeepsTheKindsApart_RealDB(t *testing.T) {
	store := NewPostgres(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Record(ctx, write(TargetAsset, "same-id", KindScript, "script-1", "s", true, 1)))
	require.NoError(t, store.Record(ctx, write(TargetResource, "same-id", KindScript, "script-1", "s", true, 1)))

	assets, err := store.ListByTarget(ctx, TargetAsset, "same-id")
	require.NoError(t, err)
	assert.Len(t, assets, 1)
	resources, err := store.ListByTarget(ctx, TargetResource, "same-id")
	require.NoError(t, err)
	assert.Len(t, resources, 1)
}

// TestRecordListsEveryProducerOfOneFile_RealDB is acceptance criterion 4: a
// person editing a file a script also refreshes leaves both producers listed.
func TestRecordListsEveryProducerOfOneFile_RealDB(t *testing.T) {
	store := NewPostgres(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-2", KindScript, "script-1", "daily-sales", true, 1)))
	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-2", KindPerson, "sub-1", "a@example.com", false, 2)))

	rows, err := store.ListByTarget(ctx, TargetAsset, "asset-2")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, KindPerson, rows[0].Producer.Kind, "most recent writer first")
	assert.Equal(t, KindScript, rows[1].Producer.Kind)
	assert.True(t, rows[1].Created)
}

// TestRecordKeepsALabelAWriteDidNotCarry_RealDB: a session carries no label,
// and a write that does not know a script's name must not erase it.
func TestRecordKeepsALabelAWriteDidNotCarry_RealDB(t *testing.T) {
	store := NewPostgres(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-3", KindScript, "script-1", "daily-sales", true, 1)))
	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-3", KindScript, "script-1", "", false, 2)))

	rows, err := store.ListByTarget(ctx, TargetAsset, "asset-3")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "daily-sales", rows[0].Producer.Label)

	// A rename does replace it: the id is the identity, the label is display.
	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-3", KindScript, "script-1", "weekly-sales", false, 3)))
	rows, err = store.ListByTarget(ctx, TargetAsset, "asset-3")
	require.NoError(t, err)
	assert.Equal(t, "weekly-sales", rows[0].Producer.Label)
}

// TestListByProducer_RealDB is acceptance criterion 7's query: everything one
// script has written, across every file, newest first and capped.
func TestListByProducer_RealDB(t *testing.T) {
	store := NewPostgres(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-4", KindScript, "script-9", "s", true, 1)))
	require.NoError(t, store.Record(ctx, write(TargetResource, "res-4", KindScript, "script-9", "s", false, 2)))
	require.NoError(t, store.Record(ctx, write(TargetAsset, "asset-5", KindScript, "other", "o", true, 1)))

	rows, err := store.ListByProducer(ctx, KindScript, "script-9", 0)
	require.NoError(t, err)
	require.Len(t, rows, 2, "only this script's writes")

	capped, err := store.ListByProducer(ctx, KindScript, "script-9", 1)
	require.NoError(t, err)
	assert.Len(t, capped, 1)
}

// TestRecordRefusesAnUnknownKind_RealDB pins the CHECK constraints: the table
// holds the kinds the platform defines and nothing else, so a writer that
// invented one fails loudly rather than filling the record with rows no surface
// can render.
func TestRecordRefusesAnUnknownKind_RealDB(t *testing.T) {
	store := NewPostgres(testdb.New(t))
	ctx := context.Background()

	assert.Error(t, store.Record(ctx, write("collection", "c-1", KindScript, "script-1", "s", true, 1)))
	assert.Error(t, store.Record(ctx, write(TargetAsset, "asset-6", "robot", "r-1", "r", true, 1)))
}
