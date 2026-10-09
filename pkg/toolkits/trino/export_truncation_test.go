package trino

import (
	"encoding/json"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/exporttrunc"
)

// The deployment cap these tests run under. The engine is a real mcp-trino
// client over sqlmock, so the truncation signal is the client's own: it reads
// one row past the limit and reports whether there was one.
const testCap = 3

// cutFixture is a trino_export toolkit and the stores a test reads back.
type cutFixture struct {
	tk       *Toolkit
	assets   *mockExportAssetStore
	versions *mockExportVersionStore
	s3       *mockExportS3Client
}

// cutToolkit is a trino_export toolkit whose query answers rows rows, under a
// deployment cap of testCap.
func cutToolkit(t *testing.T, rows int) cutFixture {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	answer := sqlmock.NewRows([]string{"id"})
	for i := range rows {
		answer.AddRow(i + 1)
	}
	mock.ExpectQuery("SELECT").WillReturnRows(answer)

	assets, versions, s3 := &mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{}
	tk := newTestExportToolkit(assets, versions, s3)
	tk.manager = singleClient(trinoclient.NewWithDB(db, trinoclient.Config{Timeout: time.Minute}))
	tk.exportDeps.Config.MaxRows = testCap
	return cutFixture{tk: tk, assets: assets, versions: versions, s3: s3}
}

// exportOut decodes a successful call's JSON text, the form a client reads.
func exportOut(t *testing.T, tk *Toolkit, args map[string]any) map[string]any {
	t.Helper()
	result, _ := callExport(t, tk, args)
	require.False(t, result.IsError, "expected success, got %s", resultText(t, result))
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &out))
	return out
}

func exportArgs(sql string, extra map[string]any) map[string]any {
	args := map[string]any{"sql": sql, "format": "csv", "name": "Contacts"}
	maps.Copy(args, extra)
	return args
}

func TestExportBelowAndAtTheCapIsComplete(t *testing.T) {
	for _, rows := range []int{testCap - 1, testCap} {
		t.Run(fmt.Sprintf("%d rows", rows), func(t *testing.T) {
			f := cutToolkit(t, rows)
			tk := f.tk
			assets := f.assets
			versions := f.versions
			out := exportOut(t, tk, exportArgs("SELECT id FROM contacts", nil))
			assert.Equal(t, false, out["truncated"], "a result the cap did not cut is complete, including exactly at it")
			assert.InDelta(t, testCap, out["limit_applied"], 0)
			assert.Equal(t, exporttrunc.SourceDeployment, out["limit_source"])
			assert.Equal(t, exporttrunc.UnitRows, out["limit_unit"])
			assert.NotContains(t, out, "arbitrary_subset")
			assert.Equal(t, fmt.Sprintf("Exported %d rows as csv.", rows), out["message"])
			assert.NotContains(t, assets.inserted.Tags, exporttrunc.Tag)
			assert.Nil(t, versions.created.Metadata, "a complete version records no cut")
		})
	}
}

func TestExportCutAtTheDeploymentCapWritesNothingByDefault(t *testing.T) {
	f := cutToolkit(t, testCap+1)
	tk := f.tk
	assets := f.assets
	versions := f.versions
	s3 := f.s3
	result, out := callExport(t, tk, exportArgs("SELECT id FROM contacts", nil))
	require.True(t, result.IsError)
	assert.Nil(t, out)
	text := resultText(t, result)
	assert.Contains(t, text, "the deployment cap of 3 rows (portal.export.max_rows)", "it names the cap and its key")
	assert.Contains(t, text, "the query returned more rows", "and the evidence there were more")
	assert.Contains(t, text, "Set limit to export a chosen subset", "and the alternatives")
	assert.Contains(t, text, `set on_truncation to \"warn\"`)
	assert.Nil(t, assets.inserted, "no asset")
	assert.Nil(t, versions.created, "no version")
	assert.Empty(t, s3.lastKey, "no upload")
}

func TestExportCutAtTheDeploymentCapUnderWarnWritesAndFlags(t *testing.T) {
	f := cutToolkit(t, testCap+1)
	tk := f.tk
	assets := f.assets
	versions := f.versions
	out := exportOut(t, tk, exportArgs("SELECT id FROM contacts", map[string]any{"on_truncation": "warn"}))
	assert.Equal(t, true, out["truncated"])
	assert.InDelta(t, testCap, out["row_count"], 0)
	assert.Equal(t, exporttrunc.SourceDeployment, out["limit_source"])
	assert.Equal(t, true, out["arbitrary_subset"], "no ORDER BY, so the kept rows are the engine's choice")
	assert.Equal(t, "Exported 3 rows as csv. Truncated at the deployment cap of 3 rows (portal.export.max_rows); "+
		"the query returned more rows. This file is incomplete. The statement has no top-level ORDER BY, so which "+
		"rows were kept is arbitrary and can differ on the next run.", out["message"])
	assert.Contains(t, assets.inserted.Tags, exporttrunc.Tag, "the asset carries the reserved tag")
	assert.Equal(t, map[string]any{
		exporttrunc.MetaTruncated: true, exporttrunc.MetaLimitApplied: testCap,
		exporttrunc.MetaLimitSource: exporttrunc.SourceDeployment, exporttrunc.MetaLimitUnit: exporttrunc.UnitRows,
	}, versions.created.Metadata, "and the version records the cut")
}

func TestExportCutByTheCallersLimitWritesAndFlags(t *testing.T) {
	f := cutToolkit(t, testCap)
	tk := f.tk
	assets := f.assets
	versions := f.versions
	out := exportOut(t, tk, exportArgs("SELECT id FROM contacts ORDER BY id", map[string]any{"limit": 2}))
	assert.Equal(t, true, out["truncated"])
	assert.InDelta(t, 2, out["limit_applied"], 0)
	assert.Equal(t, exporttrunc.SourceRequest, out["limit_source"])
	assert.NotContains(t, out, "arbitrary_subset", "an ORDER BY fixes which rows were kept")
	assert.Contains(t, out["message"], "Truncated at the requested limit of 2 rows (limit)")
	assert.Contains(t, assets.inserted.Tags, exporttrunc.Tag)
	assert.Equal(t, exporttrunc.SourceRequest, versions.created.Metadata[exporttrunc.MetaLimitSource])
}

func TestExportCutByTheCallersLimitUnderFailWritesNothing(t *testing.T) {
	f := cutToolkit(t, testCap)
	tk := f.tk
	assets := f.assets
	result, _ := callExport(t, tk, exportArgs("SELECT id FROM contacts", map[string]any{"limit": 2, "on_truncation": "fail"}))
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "the requested limit of 2 rows (limit)")
	assert.Nil(t, assets.inserted)
}

func TestExportLimitAboveTheCapIsRefused(t *testing.T) {
	f := cutToolkit(t, 0)
	tk := f.tk
	result, _ := callExport(t, tk, exportArgs("SELECT id FROM contacts", map[string]any{"limit": testCap + 1}))
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "limit 4 exceeds deployment maximum of 3 rows")
}

func TestExportCountExpectations(t *testing.T) {
	t.Run("a missed exact count writes nothing", func(t *testing.T) {
		f := cutToolkit(t, 2)
		tk := f.tk
		assets := f.assets
		result, _ := callExport(t, tk, exportArgs("SELECT id FROM contacts", map[string]any{"expect_rows": 3}))
		require.True(t, result.IsError)
		assert.Contains(t, resultText(t, result), "expected exactly 3 rows, got 2; nothing was written.")
		assert.Nil(t, assets.inserted)
	})
	t.Run("a missed minimum under warn writes and flags", func(t *testing.T) {
		f := cutToolkit(t, 2)
		tk := f.tk
		assets := f.assets
		out := exportOut(t, tk, exportArgs("SELECT id FROM contacts",
			map[string]any{"expect_min_rows": 3, "on_truncation": "warn"}))
		assert.Equal(t, "expected at least 3 rows, got 2", out["expect_mismatch"])
		assert.Equal(t, false, out["truncated"])
		assert.NotNil(t, assets.inserted)
	})
	t.Run("an invalid combination is refused before the query", func(t *testing.T) {
		f := cutToolkit(t, 0)
		tk := f.tk
		result, _ := callExport(t, tk, exportArgs("SELECT id FROM contacts",
			map[string]any{"expect_min_rows": 3, "expect_rows": 3}))
		require.True(t, result.IsError)
		assert.Contains(t, resultText(t, result), "set expect_rows or expect_min_rows, not both")
	})
}

func TestExportIdempotencyHitReportsAnEarlierCut(t *testing.T) {
	f := cutToolkit(t, 0)
	tk := f.tk
	assets := f.assets
	assets.idempotencyHit = &ExportAssetRef{ID: "a1", SizeBytes: 9, Metadata: map[string]any{
		exporttrunc.MetaTruncated: true, exporttrunc.MetaLimitApplied: float64(100),
		exporttrunc.MetaLimitSource: exporttrunc.SourceDeployment, exporttrunc.MetaLimitUnit: exporttrunc.UnitRows,
	}}
	out := exportOut(t, tk, exportArgs("SELECT id FROM contacts", map[string]any{"idempotency_key": "k"}))
	assert.Equal(t, true, out["truncated"])
	assert.Equal(t, "Asset already exists (idempotency key matched). That export was truncated at 100 rows; the file is incomplete.", out["message"])

	assets.idempotencyHit = &ExportAssetRef{ID: "a2"}
	out = exportOut(t, tk, exportArgs("SELECT id FROM contacts", map[string]any{"idempotency_key": "k"}))
	assert.NotContains(t, out, "truncated", "a hit on an asset whose metadata records nothing claims nothing")
}

func TestExportToAResourceRecordsTheCutOnTheLanding(t *testing.T) {
	f := cutToolkit(t, testCap+1)
	tk := f.tk
	lander := newFakeLander()
	tk.exportDeps.ResourceLander = lander
	out := exportOut(t, tk, exportArgs("SELECT id FROM contacts", map[string]any{
		"on_truncation": "warn", "resource": map[string]any{"path": "lists", "filename": "contacts.csv"},
	}))
	assert.Equal(t, true, out["truncated"])
	assert.Contains(t, out["message"], "This file is incomplete.")
	require.Len(t, lander.landed, 1)
	assert.Equal(t, true, lander.landed[0].Metadata[exporttrunc.MetaTruncated], "the landing records the cut on the version")
	assert.NotContains(t, lander.landed[0].Tags, exporttrunc.Tag, "a resource's tags would outlive the cut")
}

func TestExportToAResourceRefusesADeploymentCut(t *testing.T) {
	f := cutToolkit(t, testCap+1)
	tk := f.tk
	lander := newFakeLander()
	tk.exportDeps.ResourceLander = lander
	result, _ := callExport(t, tk, exportArgs("SELECT id FROM contacts", map[string]any{
		"resource": map[string]any{"path": "lists", "filename": "contacts.csv"},
	}))
	require.True(t, result.IsError)
	assert.Empty(t, lander.landed, "nothing landed")
}

func TestExportSchemaNamesTheConfiguredCap(t *testing.T) {
	schema := exportInputSchema(applyExportDefaults(ExportConfig{MaxRows: 250_000}))
	props, ok := schema[propProperties].(map[string]any)
	require.True(t, ok)
	limit, ok := props["limit"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, limit[schemaKeyDesc], "Maximum 250,000 rows on this deployment (portal.export.max_rows)")
	onTrunc, ok := props["on_truncation"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{"fail", "warn"}, onTrunc["enum"])
	assert.Contains(t, onTrunc[schemaKeyDesc], "the deployment cap of 250,000 rows (portal.export.max_rows)")
	for _, name := range []string{"expect_rows", "expect_min_rows"} {
		assert.Contains(t, props, name)
	}
}

// TestTheLimitsPlatformInfoReads: the values platform_info reports are the
// ones this toolkit runs under, defaults applied (#2057).
func TestTheLimitsPlatformInfoReads(t *testing.T) {
	tk, err := newSingle("warehouse", Config{Host: "localhost", User: "u", DefaultLimit: 500, MaxLimit: 5000, Timeout: 90 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })
	defaultRows, maxRows, timeout := tk.QueryLimits()
	assert.Equal(t, 500, defaultRows)
	assert.Equal(t, 5000, maxRows)
	assert.Equal(t, 90*time.Second, timeout)

	resolved := ResolveExportConfig(ExportConfig{MaxRows: 250_000})
	assert.Equal(t, 250_000, resolved.MaxRows, "a configured cap stands")
	assert.Equal(t, int64(defaultMaxExportBytes), resolved.MaxBytes, "an unset one takes the default the tool applies")
}
