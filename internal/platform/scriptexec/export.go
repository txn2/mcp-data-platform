package scriptexec

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/script"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// maxOutputBytes caps one serialized output. It matches the ceiling the portal
// export path applies, so a script cannot write an asset a human could not have
// exported by hand.
const maxOutputBytes = 100 << 20

// outputWriter persists one run's outputs as portal assets.
//
// Output identity is stable across runs: the pair (script, output name) maps to
// ONE asset, and each run writes a new VERSION of it. A daily report therefore
// keeps its identity, its shares, and its history instead of minting a new
// asset every morning — which is both what a person subscribing to a dashboard
// expects and the difference between a year of scheduled runs producing one
// asset and producing three hundred and sixty-five.
type outputWriter struct {
	deps   ExportDeps
	runs   script.RunStore
	run    *script.Run
	script *script.Script
	// written names the outputs THIS attempt produced, which is what separates
	// the two ways an output name can already be present: the run row carries
	// what an earlier attempt wrote (idempotency, skip it), and this set carries
	// what the script has written since the interpreter started (a second call
	// under the same name, which is a bug in the script and must not be
	// swallowed).
	written map[string]bool
}

// newOutputWriter builds the writer for one claimed run.
func newOutputWriter(deps ExportDeps, runs script.RunStore, run *script.Run, sc *script.Script) *outputWriter {
	return &outputWriter{deps: deps, runs: runs, run: run, script: sc, written: map[string]bool{}}
}

// Export writes one output and records it on the run.
func (w *outputWriter) Export(ctx context.Context, req scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	// One name, one output, per run. A script that exports the same name twice
	// means two different results to store under one identity, and the honest
	// answer is to fail rather than to keep the first and drop the second.
	if w.written[req.Name] {
		return nil, fmt.Errorf("output %q was already written by this run; each output name may be written once, so give the second one its own name",
			req.Name)
	}
	// A run reclaimed after its worker died re-executes from the top, so an
	// output an EARLIER attempt persisted must not be written a second time. The
	// run row is the record of that, written as each output landed.
	if prior := w.run.Output(req.Name); prior != nil {
		slog.Info("scripts: output already written by an earlier attempt of this run",
			logKeyRunID, w.run.ID, "output", req.Name, "asset_id", prior.AssetID)
		return &scriptrun.ExportResult{
			AssetID: prior.AssetID, AssetVersion: prior.AssetVersion, Bytes: prior.Bytes,
		}, nil
	}

	formatter, err := trinokit.NewFormatter(req.Format)
	if err != nil {
		return nil, fmt.Errorf("output %q: %w", req.Name, err)
	}
	data, err := formatter.Format(req.Columns, tabular(req.Columns, req.Rows))
	if err != nil {
		return nil, fmt.Errorf("formatting output %q: %w", req.Name, err)
	}
	if len(data) > maxOutputBytes {
		return nil, fmt.Errorf("output %q is %d bytes, over the %d-byte limit; aggregate in SQL or write fewer columns",
			req.Name, len(data), maxOutputBytes)
	}

	asset, err := w.assetFor(ctx, req.Name, formatter.ContentType())
	if err != nil {
		return nil, err
	}
	key := w.objectKey(asset.ID, formatter.FileExtension())
	if err := w.deps.S3.PutObject(ctx, w.deps.Bucket, key, data, formatter.ContentType()); err != nil {
		return nil, fmt.Errorf("uploading output %q: %w", req.Name, err)
	}
	version, err := w.deps.Versions.CreateVersion(ctx, portal.AssetVersion{
		ID:          uuid.New().String(),
		AssetID:     asset.ID,
		S3Key:       key,
		S3Bucket:    w.deps.Bucket,
		ContentType: formatter.ContentType(),
		SizeBytes:   int64(len(data)),
		CreatedBy:   w.script.Principal(),
		ChangeSummary: fmt.Sprintf("%s v%d, run %s",
			w.script.Name, w.run.Version, w.run.ID),
	})
	if err != nil {
		return nil, fmt.Errorf("recording output %q: %w", req.Name, err)
	}

	out := script.RunOutput{
		Name: req.Name, AssetID: asset.ID, AssetVersion: version,
		Format: req.Format, RowCount: len(req.Rows), Bytes: len(data),
	}
	if err := w.runs.RecordOutput(ctx, w.run.Lease(), out); err != nil {
		// The asset version exists and is correct; only the run's record of it
		// failed. Failing the run here would report a write that did happen as a
		// write that did not, so the run continues and the gap is logged.
		slog.Error("scripts: recording an output on the run failed",
			logKeyRunID, w.run.ID, "output", req.Name, logKeyError, err)
	}
	w.run.Outputs = append(w.run.Outputs, out)
	w.written[req.Name] = true
	return &scriptrun.ExportResult{AssetID: asset.ID, AssetVersion: version, Bytes: len(data)}, nil
}

// assetFor finds the asset this output name maps to, creating it the first time
// the script writes that name.
//
// The mapping is carried by the asset's idempotency key, which is what the
// portal store already indexes per owner — so "one asset per (script, output)"
// needs no new table and no lookup this package would have to keep unique
// itself. The key names the script by ID rather than by name, so renaming a
// script keeps its outputs and deleting one does not let a later script with
// the same name inherit them.
//
// A failed lookup is treated as a miss rather than as an error, because no
// portal store distinguishes the two: the PostgreSQL one reports a miss as a
// wrapped sql.ErrNoRows and the no-database one as its own sentinel, and
// neither says "the lookup itself failed". What guarantees one asset per output
// is the unique idempotency key underneath, not this read — so a lookup that
// went wrong falls through to the insert, and the insert either succeeds or
// tells us who won.
func (w *outputWriter) assetFor(ctx context.Context, name, contentType string) (*portal.Asset, error) {
	owner := w.script.Principal()
	key := "script:" + w.script.ID + ":" + name
	if existing, err := w.deps.Assets.GetByIdempotencyKey(ctx, owner, key); err == nil && existing != nil {
		return existing, nil
	}
	asset := portal.Asset{
		ID:          uuid.New().String(),
		OwnerID:     owner,
		OwnerEmail:  w.script.OwnerEmail,
		Name:        name,
		Description: fmt.Sprintf("Output of the managed script %s. Each run writes a new version.", w.script.Name),
		ContentType: contentType,
		S3Bucket:    w.deps.Bucket,
		Tags:        []string{"script", w.script.Name},
		// CurrentVersion stays zero: CreateVersion increments it to 1 as it
		// records the first version's content.
		SessionID:      w.run.ID,
		IdempotencyKey: key,
		Provenance: portal.Provenance{
			UserID:    owner,
			SessionID: w.run.ID,
		},
	}
	if err := w.deps.Assets.Insert(ctx, asset); err != nil {
		// Two runs of the same script racing on the first write of one output
		// both try to insert; the unique idempotency key means one loses. The
		// loser reads the winner's asset and writes its own version of it, which
		// is exactly the intended shape.
		if found, lookupErr := w.deps.Assets.GetByIdempotencyKey(ctx, owner, key); lookupErr == nil && found != nil {
			return found, nil
		}
		return nil, fmt.Errorf("creating the output asset for %q: %w", name, err)
	}
	return &asset, nil
}

// objectKey composes the S3 key for one output version. It includes the run id,
// so each version is its own immutable object and a reclaimed run rewriting the
// same output lands on the same key with the same bytes rather than clobbering
// the version before it.
func (w *outputWriter) objectKey(assetID, extension string) string {
	return path.Join(w.deps.Prefix, "scripts", sanitizeKeySegment(w.script.ID),
		sanitizeKeySegment(assetID), sanitizeKeySegment(w.run.ID)+extension)
}

// sanitizeKeySegment keeps an identifier usable as one S3 key segment across
// S3-compatible backends, which differ in what they accept.
func sanitizeKeySegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "unnamed"
	}
	return b.String()
}

// tabular projects row dicts onto the column order the script wrote. A row
// missing a column contributes an empty cell rather than shifting the row,
// which is what keeps a ragged result readable instead of misaligned.
func tabular(columns []string, rows []any) [][]any {
	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		dict, ok := row.(map[string]any)
		if !ok {
			// A non-dict row has no columns to project. Rendering it as an empty
			// row keeps the row count honest; the alternative, dropping it, would
			// make the output disagree with the row count the script was told.
			out = append(out, make([]any, len(columns)))
			continue
		}
		cells := make([]any, len(columns))
		for i, column := range columns {
			cells[i] = dict[column]
		}
		out = append(out, cells)
	}
	return out
}
