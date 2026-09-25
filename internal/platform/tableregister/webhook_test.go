package tableregister

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

// fakeHolders maps resources to the webhook source each is a window of.
type fakeHolders struct {
	byResource map[string]string
	err        error
}

func (f fakeHolders) SourcesForResources(_ context.Context, ids []string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]string{}
	for _, id := range ids {
		if s, ok := f.byResource[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func webhookRegistration() Registration {
	return Registration{
		ID: "reg-wh", SourceKind: KindWebhook, SourceID: "esp", Connection: "scratch",
		Catalog: "scratch_resources", Schema: "uploads", Table: "webhook_esp",
		Location: "s3://managed-resources/webhooks/esp/", Format: FormatParquet,
		Columns: []Column{{Name: "event_id", Type: "varchar"}}, RegisteredBy: "admin@example.com",
	}
}

func webhookRegistrar(t *testing.T, holders WindowHolders) (*Registrar, *memStore) {
	t.Helper()
	store := newMemStore()
	require.NoError(t, store.Insert(context.Background(), webhookRegistration()))
	reg := New(Deps{
		Store: store, Trino: &fakeTrino{}, Holders: holders,
		Objects: map[string]ObjectReader{KindResource: &fakeObjects{}},
	})
	require.NotNil(t, reg)
	return reg, store
}

func TestTablesForResolvesAnHourToItsSource(t *testing.T) {
	reg, _ := webhookRegistrar(t, fakeHolders{byResource: map[string]string{"hour-1": "esp"}})
	found, err := reg.TablesFor(context.Background(), KindResource, []string{"hour-1", "plain"})
	require.NoError(t, err)
	require.Len(t, found["hour-1"], 1)
	assert.Equal(t, "webhook_esp", found["hour-1"][0].Table)
	assert.Empty(t, found["plain"])

	lookup := NewLookup(reg)
	tables, err := lookup.TablesFor(context.Background(), []knowledge.TableSubject{
		{Kind: knowledge.TableKindResource, ID: "hour-1", Bucket: "managed-resources", HeadKey: "resources/global/global/hour-1/10-00.parquet"},
	})
	require.NoError(t, err)
	require.Len(t, tables["hour-1"], 1)
	assert.False(t, tables["hour-1"][0].Stale, "a webhook table is not over one file and is never stale")
	assert.Equal(t, "scratch_resources.uploads.webhook_esp", tables["hour-1"][0].Table)
}

func TestTablesForWithoutHolders(t *testing.T) {
	reg, _ := webhookRegistrar(t, nil)
	found, err := reg.TablesFor(context.Background(), KindResource, []string{"hour-1"})
	require.NoError(t, err)
	assert.Empty(t, found)

	failing, _ := webhookRegistrar(t, fakeHolders{err: errors.New("down")})
	_, err = failing.TablesFor(context.Background(), KindResource, []string{"hour-1"})
	assert.Error(t, err)

	asset, _ := webhookRegistrar(t, fakeHolders{byResource: map[string]string{"a": "esp"}})
	found, err = asset.TablesFor(context.Background(), KindAsset, []string{"a"})
	require.NoError(t, err)
	assert.Empty(t, found, "only a managed resource can be a webhook window")
}

// webhookReadFails fails the read of webhook registrations and answers every
// other read.
type webhookReadFails struct{ *memStore }

func (w webhookReadFails) BySource(ctx context.Context, kind, id string) ([]Registration, error) {
	if kind == KindWebhook {
		return nil, errors.New("down")
	}
	return w.memStore.BySource(ctx, kind, id)
}

func (w webhookReadFails) ForSources(ctx context.Context, kind string, ids []string) (map[string][]Registration, error) {
	if kind == KindWebhook {
		return nil, errors.New("down")
	}
	return w.memStore.ForSources(ctx, kind, ids)
}

func TestTablesForStoreFailure(t *testing.T) {
	reg := New(Deps{
		Store: webhookReadFails{newMemStore()}, Trino: &fakeTrino{},
		Holders: fakeHolders{byResource: map[string]string{"hour-1": "esp"}},
		Objects: map[string]ObjectReader{KindResource: &fakeObjects{}},
	})
	_, err := reg.TablesFor(context.Background(), KindResource, []string{"hour-1"})
	assert.Error(t, err)
}

func TestWebhookTableIsNotUnregisteredDirectly(t *testing.T) {
	reg, store := webhookRegistrar(t, nil)
	err := reg.Unregister(context.Background(), Caller{Email: "admin@example.com", IsAdmin: true}, "reg-wh", "rest")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "removed by deleting the source")
	_, err = store.Get(context.Background(), "reg-wh")
	assert.NoError(t, err, "the registration is still there")
}

func TestWebhookSampleReadsToday(t *testing.T) {
	sql := SampleJoinSQL(webhookRegistration())
	assert.Contains(t, sql, "FROM scratch_resources.uploads.webhook_esp")
	assert.Contains(t, sql, "WHERE dt = format_datetime(current_timestamp AT TIME ZONE 'UTC', 'yyyy-MM-dd')")
	assert.NotContains(t, sql, "JOIN", "a webhook table is read by hour, not joined on its first column")
}

func TestTablesOfAddsTheSourceTableToAnHour(t *testing.T) {
	reg, store := webhookRegistrar(t, fakeHolders{byResource: map[string]string{"hour-1": "esp"}})
	require.NoError(t, store.Insert(context.Background(), Registration{
		ID: "own", SourceKind: KindResource, SourceID: "hour-1", Connection: "scratch", Catalog: "c", Schema: "s", Table: "mine",
	}))
	regs, err := reg.TablesOf(context.Background(), KindResource, "hour-1")
	require.NoError(t, err)
	require.Len(t, regs, 2)
	assert.Equal(t, "mine", regs[0].Table, "the file's own registrations come first")
	assert.Equal(t, "webhook_esp", regs[1].Table)

	regs, err = reg.TablesOf(context.Background(), KindResource, "plain")
	require.NoError(t, err)
	assert.Empty(t, regs)
	regs, err = reg.TablesOf(context.Background(), KindAsset, "hour-1")
	require.NoError(t, err)
	assert.Empty(t, regs, "only a managed resource can be a webhook window")

	failing, _ := webhookRegistrar(t, fakeHolders{err: errors.New("down")})
	_, err = failing.TablesOf(context.Background(), KindResource, "hour-1")
	assert.Error(t, err)
	broken := New(Deps{
		Store: webhookReadFails{newMemStore()}, Trino: &fakeTrino{},
		Holders: fakeHolders{byResource: map[string]string{"hour-1": "esp"}},
		Objects: map[string]ObjectReader{KindResource: &fakeObjects{}},
	})
	_, err = broken.TablesOf(context.Background(), KindResource, "hour-1")
	assert.Error(t, err)
}
