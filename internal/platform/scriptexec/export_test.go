package scriptexec

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeAssets models the portal asset store's contract, including the detail a
// convenient fake would get wrong: GetByIdempotencyKey reports a miss as a
// wrapped sql.ErrNoRows rather than as a nil asset with a nil error.
type fakeAssets struct {
	byKey     map[string]*portal.Asset
	inserted  []portal.Asset
	insertErr error
	lookupErr error
	// lookupOnce fails only the first lookup, which is the shape of a read that
	// missed or failed while the row was in fact there.
	lookupOnce bool
}

func newFakeAssets() *fakeAssets { return &fakeAssets{byKey: map[string]*portal.Asset{}} }

func (f *fakeAssets) Insert(_ context.Context, asset portal.Asset) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, asset)
	f.byKey[asset.OwnerID+"|"+asset.IdempotencyKey] = &asset
	return nil
}

func (f *fakeAssets) GetByIdempotencyKey(_ context.Context, ownerID, key string) (*portal.Asset, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	if f.lookupOnce {
		f.lookupOnce = false
		return nil, errors.New("lookup unavailable")
	}
	if asset, ok := f.byKey[ownerID+"|"+key]; ok {
		return asset, nil
	}
	return nil, fmt.Errorf("querying asset by idempotency key: %w", sql.ErrNoRows)
}

func (*fakeAssets) Get(context.Context, string) (*portal.Asset, error) {
	return nil, sql.ErrNoRows
}

func (*fakeAssets) GetByIDs(context.Context, []string) (assets map[string]*portal.Asset, err error) {
	return map[string]*portal.Asset{}, nil
}

func (*fakeAssets) List(context.Context, portal.AssetFilter) (assets []portal.Asset, total int, err error) {
	return nil, 0, nil
}
func (*fakeAssets) Update(context.Context, string, portal.AssetUpdate) error { return nil }
func (*fakeAssets) AppendProvenanceCapture(context.Context, string, portal.ProvenanceCapture) error {
	return nil
}

func (*fakeAssets) SoftDelete(context.Context, string) error { return nil }

// fakeVersionStore models the portal version store: each CreateVersion returns
// the next number for that asset, which is what makes a run's output a new
// version of one stable asset.
type fakeVersionStore struct {
	created   []portal.AssetVersion
	counts    map[string]int
	createErr error
}

func newFakeVersionStore() *fakeVersionStore {
	return &fakeVersionStore{counts: map[string]int{}}
}

func (f *fakeVersionStore) CreateVersion(_ context.Context, v portal.AssetVersion) (int, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	f.counts[v.AssetID]++
	f.created = append(f.created, v)
	return f.counts[v.AssetID], nil
}

func (*fakeVersionStore) ListByAsset(context.Context, string, int, int) (versions []portal.AssetVersion, total int, err error) {
	return nil, 0, nil
}

func (*fakeVersionStore) GetByVersion(context.Context, string, int) (*portal.AssetVersion, error) {
	return nil, sql.ErrNoRows
}

func (*fakeVersionStore) GetLatest(context.Context, string) (*portal.AssetVersion, error) {
	return nil, sql.ErrNoRows
}

// fakeS3 records the objects the writer uploads.
type fakeS3 struct {
	objects map[string][]byte
	putErr  error
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string][]byte{}} }

func (f *fakeS3) PutObject(_ context.Context, _, key string, data []byte, _ string) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.objects[key] = data
	return nil
}

func (*fakeS3) PutObjectStream(context.Context, string, string, io.Reader, string) (size int64, err error) {
	return 0, nil
}

func (*fakeS3) GetObject(context.Context, string, string) (data []byte, contentType string, err error) {
	return nil, "", nil
}
func (*fakeS3) DeleteObject(context.Context, string, string) error { return nil }
func (*fakeS3) Close() error                                       { return nil }

// deliveryCall is one tool call the writer issued to deliver an output.
type deliveryCall struct {
	tool string
	args map[string]any
}

// fakeCaller stands in for the run's MCP session: it records what the writer
// asked the platform to do, and can fail the call the way a refused write does.
type fakeCaller struct {
	calls  []deliveryCall
	result map[string]any
	err    error
}

func (f *fakeCaller) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	f.calls = append(f.calls, deliveryCall{tool: name, args: args})
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return map[string]any{"bucket": args["bucket"], "key": args["key"]}, nil
}

// writerHarness assembles an output writer over the fakes.
type writerHarness struct {
	writer   *outputWriter
	assets   *fakeAssets
	versions *fakeVersionStore
	s3       *fakeS3
	runs     *fakeRuns
	caller   *fakeCaller
	run      *script.Run
}

func newWriterHarness(t *testing.T) writerHarness {
	t.Helper()
	sc, _, run := executableState()
	run.LockedBy, run.Attempt = "worker-a", 1
	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	// Enqueue resets the status; the writer only ever runs under a claim.
	run.Status, run.LockedBy, run.Attempt = script.RunStatusRunning, "worker-a", 1

	assets, versions, s3 := newFakeAssets(), newFakeVersionStore(), newFakeS3()
	caller := &fakeCaller{}
	deps := ExportDeps{Assets: assets, Versions: versions, S3: s3, Bucket: "assets", Prefix: "portal"}
	return writerHarness{
		writer: newOutputWriter(deps, runs, run, sc, caller),
		assets: assets, versions: versions, s3: s3, runs: runs, caller: caller, run: run,
	}
}

// csvRequest is one output in the shape the engine hands over, addressed to the
// portal.
func csvRequest(name string) scriptrun.ExportRequest { //nolint:unparam // one output name is all these cases need; the parameter names what it is
	return scriptrun.ExportRequest{
		Name: name, Format: "csv", Columns: []string{"region", "total"},
		Rows: []any{
			map[string]any{"region": "west", "total": int64(120)},
			map[string]any{"region": "east", "total": int64(80)},
		},
		Destination: script.PortalDestination(),
	}
}

// TestDraftMeasuresWhatAnApprovedRunWrites pins #1354 across the seam it
// spans: the engine reports a size for an output nobody persisted, and the
// writer reports one for the object it stored. The two used to disagree,
// because the draft measured a JSON encoding of the rows whatever format the
// author declared. They are the same number now because there is one
// serializer, and this asserts it against the bytes that actually reached
// storage rather than against either side's own accounting.
func TestDraftMeasuresWhatAnApprovedRunWrites(t *testing.T) {
	const source = `platform.export(name="daily", format="csv", rows=[` +
		`{"region": "west", "total": 120}, {"region": "east", "total": 80}])`

	draft, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: source, Name: "daily", RunID: "run_draft",
	})
	require.NoError(t, err)
	require.Len(t, draft.Exports, 1)
	require.True(t, draft.Exports[0].Preview, "a run with no Exporter persists nothing")

	h := newWriterHarness(t)
	persisted, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: source, Name: "daily", RunID: "run_1", Exporter: h.writer,
	})
	require.NoError(t, err)
	require.Len(t, persisted.Exports, 1)
	require.False(t, persisted.Exports[0].Preview)

	require.Len(t, h.s3.objects, 1)
	var stored []byte
	for _, data := range h.s3.objects {
		stored = data
	}
	assert.Equal(t, len(stored), draft.Exports[0].Bytes,
		"the draft reports the length of the object a real run writes")
	assert.Equal(t, len(stored), persisted.Exports[0].Bytes)

	// And the number follows the declared format, which is the whole of what
	// was wrong: one JSON estimate stood in for all four formats.
	asJSON, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: strings.Replace(source, `format="csv"`, `format="json"`, 1),
		Name:   "daily", RunID: "run_draft_json",
	})
	require.NoError(t, err)
	require.Len(t, asJSON.Exports, 1)
	assert.NotEqual(t, draft.Exports[0].Bytes, asJSON.Exports[0].Bytes)
}

// TestOutputWriter_WritesAnAssetAndRecordsItOnTheRun is one output end to end:
// the object, the asset, its version, and the run's record of what it wrote.
func TestOutputWriter_WritesAnAssetAndRecordsItOnTheRun(t *testing.T) {
	h := newWriterHarness(t)

	result, err := h.writer.Export(context.Background(), csvRequest("daily"))
	require.NoError(t, err)
	assert.Equal(t, 1, result.AssetVersion)
	assert.Positive(t, result.Bytes)

	require.Len(t, h.assets.inserted, 1)
	asset := h.assets.inserted[0]
	assert.Equal(t, "script:daily", asset.OwnerID, "an output belongs to the script principal")
	assert.Equal(t, "jane@example.com", asset.OwnerEmail, "the owner stays accountable for it")
	assert.Equal(t, "script:script_1:daily", asset.IdempotencyKey)

	require.Len(t, h.versions.created, 1)
	assert.Contains(t, h.versions.created[0].ChangeSummary, h.run.ID)
	assert.Equal(t, "script:daily", h.versions.created[0].CreatedBy)

	require.Len(t, h.s3.objects, 1)
	for key, data := range h.s3.objects {
		assert.Contains(t, key, h.run.ID, "each version is its own immutable object")
		assert.True(t, strings.HasPrefix(string(data), "region,total"),
			"the CSV header follows the column order the script wrote")
	}

	require.Len(t, h.runs.outputs, 1)
	assert.Equal(t, "daily", h.runs.outputs[0].Name)
}

// documentRequest is one composed document in the shape the engine hands over.
func documentRequest(name, format, body string) scriptrun.ExportRequest {
	return scriptrun.ExportRequest{
		Name: name, Format: format, Body: &body,
		Destination: script.PortalDestination(),
	}
}

// TestOutputWriter_WritesADocumentVerbatim pins #1388 across the persistence
// seam: a string body lands byte for byte, under the content type the portal
// renders for that document kind, and a delivered one takes the format's own
// extension when the script names no key.
func TestOutputWriter_WritesADocumentVerbatim(t *testing.T) {
	h := newWriterHarness(t)
	body := "<html><body>dash</body></html>"

	result, err := h.writer.Export(context.Background(), documentRequest("dash", "html", body))
	require.NoError(t, err)
	assert.Equal(t, 1, result.AssetVersion)
	assert.Equal(t, len(body), result.Bytes)

	require.Len(t, h.assets.inserted, 1)
	assert.Equal(t, "text/html", h.assets.inserted[0].ContentType)
	require.Len(t, h.versions.created, 1)
	assert.Equal(t, "text/html", h.versions.created[0].ContentType)
	require.Len(t, h.s3.objects, 1)
	for key, data := range h.s3.objects {
		assert.True(t, strings.HasSuffix(key, ".html"), key)
		assert.Equal(t, body, string(data), "verbatim is the contract")
	}

	// The same document delivered to a granted bucket, with no key named: the
	// default key takes the document format's own extension.
	req := documentRequest("dash", "html", body)
	req.Destination = acmeDrop()
	h.caller.result = map[string]any{}
	_, err = h.writer.Export(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, h.caller.calls, 1)
	assert.Equal(t, "weekly/dash.html", h.caller.calls[0].args["key"])
	assert.Equal(t, "text/html", h.caller.calls[0].args["content_type"])
}

// TestOutputWriter_SameNameIsANewVersionOfOneAsset is the stable-identity
// property: a daily report keeps its asset, its shares, and its history instead
// of minting a new asset every morning.
func TestOutputWriter_SameNameIsANewVersionOfOneAsset(t *testing.T) {
	h := newWriterHarness(t)
	ctx := context.Background()

	first, err := h.writer.Export(ctx, csvRequest("daily"))
	require.NoError(t, err)

	// A second run of the same script: a new run row, the same writer state
	// otherwise.
	secondRun := &script.Run{
		ID: "dpx_2", ScriptID: h.run.ScriptID, VersionID: h.run.VersionID,
		Version: h.run.Version,
	}
	require.NoError(t, h.runs.Enqueue(ctx, secondRun))
	secondRun.LockedBy, secondRun.Attempt = "worker-a", 1
	secondWriter := newOutputWriter(h.writer.deps, h.runs, secondRun, h.writer.script, h.caller)

	second, err := secondWriter.Export(ctx, csvRequest("daily"))
	require.NoError(t, err)

	assert.Equal(t, first.AssetID, second.AssetID, "one output name, one asset")
	assert.Equal(t, 2, second.AssetVersion, "each run is a new version of it")
	assert.Len(t, h.assets.inserted, 1, "the second run must not mint a second asset")
	assert.Len(t, h.s3.objects, 2, "each version keeps its own object")
}

// TestOutputWriter_ReclaimedRunDoesNotWriteTwice is the idempotency the queue
// needs: a run that died after writing an output and was reclaimed re-executes
// from the top, and must not produce a second version of that output.
func TestOutputWriter_ReclaimedRunDoesNotWriteTwice(t *testing.T) {
	h := newWriterHarness(t)
	ctx := context.Background()

	first, err := h.writer.Export(ctx, csvRequest("daily"))
	require.NoError(t, err)

	// The reclaim: another worker takes the same run over and builds a fresh
	// writer over the row, which now carries what the first attempt wrote.
	reclaimed := newOutputWriter(h.writer.deps, h.runs, h.run, h.writer.script, h.caller)
	again, err := reclaimed.Export(ctx, csvRequest("daily"))
	require.NoError(t, err)

	assert.Equal(t, first.AssetID, again.AssetID)
	assert.Equal(t, first.AssetVersion, again.AssetVersion)
	assert.Len(t, h.versions.created, 1, "the output was already written; writing it again would double it")
	assert.Len(t, h.runs.outputs, 1)
}

// TestOutputWriter_RefusesASecondOutputUnderOneName covers the case the reclaim
// guard used to swallow: a script exporting the same name twice in one run has
// two results for one identity, and the second must fail loudly rather than be
// dropped in favor of the first.
func TestOutputWriter_RefusesASecondOutputUnderOneName(t *testing.T) {
	h := newWriterHarness(t)
	ctx := context.Background()

	_, err := h.writer.Export(ctx, csvRequest("daily"))
	require.NoError(t, err)

	_, err = h.writer.Export(ctx, csvRequest("daily"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already written to \"portal\" by this run")
	assert.Len(t, h.versions.created, 1)
}

// TestOutputWriter_Failures covers each step that can fail, since an output
// that did not land must fail the run rather than be reported as written.
func TestOutputWriter_Failures(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(writerHarness)
		request scriptrun.ExportRequest
		wantErr string
	}{
		{
			"unknown format", func(writerHarness) {},
			scriptrun.ExportRequest{Name: "daily", Format: "parquet"},
			"unsupported format",
		},
		{
			"asset insert fails", func(h writerHarness) { h.assets.insertErr = errors.New("boom") },
			csvRequest("daily"), "creating the output asset",
		},
		{
			"upload fails", func(h writerHarness) { h.s3.putErr = errors.New("boom") },
			csvRequest("daily"), "uploading output",
		},
		{
			"version write fails", func(h writerHarness) { h.versions.createErr = errors.New("boom") },
			csvRequest("daily"), "recording output",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWriterHarness(t)
			tt.arrange(h)
			_, err := h.writer.Export(context.Background(), tt.request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestOutputWriter_LosingTheInsertRaceReusesTheWinnersAsset covers two runs of
// one script racing on the first write of an output, and equally a lookup that
// failed for its own reasons: either way the unique idempotency key decides,
// and the loser writes its version of the winner's asset rather than failing.
func TestOutputWriter_LosingTheInsertRaceReusesTheWinnersAsset(t *testing.T) {
	h := newWriterHarness(t)
	h.assets.byKey["script:daily|script:script_1:daily"] = &portal.Asset{ID: "asset_winner"}
	h.assets.insertErr = errors.New("duplicate key")
	// The first read is made to fail too, which is what makes this the
	// unique-key path rather than the found-it-first path.
	h.assets.lookupOnce = true

	result, err := h.writer.Export(context.Background(), csvRequest("daily"))
	require.NoError(t, err)
	assert.Equal(t, "asset_winner", result.AssetID)
}

// TestOutputWriter_RecordFailureDoesNotUnwriteTheAsset pins the honest report:
// the version exists, so the run continues rather than claiming a write that
// did happen did not.
func TestOutputWriter_RecordFailureDoesNotUnwriteTheAsset(t *testing.T) {
	h := newWriterHarness(t)
	h.runs.writeErr = script.ErrLeaseLost

	result, err := h.writer.Export(context.Background(), csvRequest("daily"))
	require.NoError(t, err)
	assert.NotEmpty(t, result.AssetID)
	assert.Len(t, h.versions.created, 1)
}

func TestSanitizeKeySegment(t *testing.T) {
	assert.Equal(t, "dpx_abc-123", sanitizeKeySegment("dpx_abc-123"))
	assert.Equal(t, "a-b-c", sanitizeKeySegment("a/b c"))
	assert.Equal(t, "unnamed", sanitizeKeySegment(""))
}

func TestExportDeps_ReadyRequiresEveryPiece(t *testing.T) {
	full := ExportDeps{Assets: newFakeAssets(), Versions: newFakeVersionStore(), S3: newFakeS3(), Bucket: "b"}
	assert.True(t, full.ready())

	for name, mutate := range map[string]func(*ExportDeps){
		"no assets":   func(d *ExportDeps) { d.Assets = nil },
		"no versions": func(d *ExportDeps) { d.Versions = nil },
		"no s3":       func(d *ExportDeps) { d.S3 = nil },
		"no bucket":   func(d *ExportDeps) { d.Bucket = "" },
	} {
		t.Run(name, func(t *testing.T) {
			deps := full
			mutate(&deps)
			assert.False(t, deps.ready())
		})
	}
}
