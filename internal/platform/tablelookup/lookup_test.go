package tablelookup

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

// fakeTables answers TablesFor from registrations by source id.
type fakeTables struct {
	off   bool
	bySrc map[string][]tableregister.Registration
	err   error
	asked map[string][]string
}

func (f *fakeTables) Available() bool { return !f.off }

func (f *fakeTables) TablesFor(_ context.Context, kind string, ids []string) (map[string][]tableregister.Registration, error) {
	if f.asked == nil {
		f.asked = map[string][]string{}
	}
	f.asked[kind] = append(f.asked[kind], ids...)
	if f.err != nil {
		return nil, f.err
	}
	out := map[string][]tableregister.Registration{}
	for _, id := range ids {
		if regs, ok := f.bySrc[id]; ok {
			out[id] = regs
		}
	}
	return out, nil
}

func csvReg(id, table string) tableregister.Registration {
	return tableregister.Registration{
		ID: id, SourceKind: tableregister.KindAsset, SourceID: "asset_1", Connection: "scratch",
		Catalog: "scratch", Schema: "uploads", Table: table, Format: tableregister.FormatCSV,
		Location: "s3://portal-assets/artifacts/u1/asset_1/", Follow: true,
		Columns: []tableregister.Column{{Name: "store_id", Type: "VARCHAR"}, {Name: "rebate_pct", Type: "VARCHAR"}},
	}
}

var asset = knowledge.TableSubject{
	Kind: knowledge.TableKindAsset, ID: "asset_1", Bucket: "portal-assets", HeadKey: "artifacts/u1/asset_1/content.csv",
}

// TestTablesFor is what puts a table reference on a search hit: the
// connection, the qualified name, the columns, the CAST a join needs, and the
// state manage_table action=list reports.
func TestTablesFor(t *testing.T) {
	failed := csvReg("reg_2", "analyst_feed_norepair")
	failed.FollowError = "the table no longer exists"
	tables := &fakeTables{bySrc: map[string][]tableregister.Registration{"asset_1": {failed, csvReg("reg_1", "analyst_feed")}}}

	got, err := New(tables).TablesFor(context.Background(), []knowledge.TableSubject{asset, asset, {ID: ""}})
	require.NoError(t, err)
	require.Len(t, got["asset_1"], 2, "every registration over the file, in the store's order")
	assert.Equal(t, []string{"asset_1"}, tables.asked[knowledge.TableKindAsset], "a subject named twice is read once")

	first := got["asset_1"][0]
	assert.Equal(t, "reg_2", first.RegistrationID)
	assert.Equal(t, "the table no longer exists", first.FollowError)
	second := got["asset_1"][1]
	assert.Equal(t, "scratch", second.Connection)
	assert.Equal(t, "scratch.uploads.analyst_feed", second.Table)
	assert.Equal(t, []string{"store_id", "rebate_pct"}, second.Columns)
	assert.Equal(t, "VARCHAR", second.ColumnTypes[0].Type)
	assert.Contains(t, second.Sample, `CAST(t."store_id" AS BIGINT)`)
	assert.False(t, second.Stale)
	assert.True(t, second.Follow)
	assert.Equal(t, tableregister.FormatCSV, second.Format)
}

// TestTablesForStaleness: a head moved to another directory reads stale; an
// overwrite at the same key does not; a webhook table never does.
func TestTablesForStaleness(t *testing.T) {
	hook := tableregister.Registration{
		ID: "wh", SourceKind: tableregister.KindWebhook, SourceID: "esp", Catalog: "scratch_resources",
		Schema: "uploads", Table: "webhook_esp", Location: "s3://managed-resources/webhooks/esp/",
	}
	tables := &fakeTables{bySrc: map[string][]tableregister.Registration{
		"asset_1": {csvReg("reg_1", "analyst_feed")}, "hour-1": {hook},
	}}
	moved := asset
	moved.HeadKey = "artifacts/u1/asset_1/v2/content.csv"
	got, err := New(tables).TablesFor(context.Background(), []knowledge.TableSubject{moved})
	require.NoError(t, err)
	assert.True(t, got["asset_1"][0].Stale, "the head moved to a directory the table does not point at")

	got, err = New(tables).TablesFor(context.Background(), []knowledge.TableSubject{asset})
	require.NoError(t, err)
	assert.False(t, got["asset_1"][0].Stale, "an overwrite in place is not staleness")

	got, err = New(tables).TablesFor(context.Background(), []knowledge.TableSubject{{
		Kind: knowledge.TableKindResource, ID: "hour-1", Bucket: "managed-resources",
		HeadKey: "resources/global/global/hour-1/10-00.parquet",
	}})
	require.NoError(t, err)
	require.Len(t, got["hour-1"], 1)
	assert.False(t, got["hour-1"][0].Stale, "a webhook table is not over one file and is never stale")
	assert.Equal(t, "scratch_resources.uploads.webhook_esp", got["hour-1"][0].Table)
}

// TestTablesForFindsNothing: an unregistered subject is absent, and an
// unwired registrar, an empty page and a nil lookup find nothing; a read that
// fails is the lookup's error.
func TestTablesForFindsNothing(t *testing.T) {
	tables := &fakeTables{bySrc: map[string][]tableregister.Registration{"other": {}}}
	got, err := New(tables).TablesFor(context.Background(), []knowledge.TableSubject{asset, {ID: "other"}})
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = New(&fakeTables{off: true}).TablesFor(context.Background(), []knowledge.TableSubject{asset})
	require.NoError(t, err)
	assert.Nil(t, got)

	var none *Lookup
	got, err = none.TablesFor(context.Background(), []knowledge.TableSubject{asset})
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = New(tableregister.New(tableregister.Deps{})).TablesFor(context.Background(), []knowledge.TableSubject{asset})
	require.NoError(t, err)
	assert.Nil(t, got, "a registrar with nothing wired finds nothing")

	_, err = New(&fakeTables{err: errors.New("down")}).TablesFor(context.Background(), []knowledge.TableSubject{asset})
	require.Error(t, err)
}
