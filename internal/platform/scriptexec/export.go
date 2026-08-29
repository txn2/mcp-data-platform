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
)

// outputWriter persists one run's outputs, to the portal or to a configured
// bucket destination.
//
// Portal output identity is stable across runs: the pair (script, output name)
// maps to ONE asset, and each run writes a new VERSION of it. A daily report
// therefore keeps its identity, its shares, and its history instead of minting a
// new asset every morning — which is both what a person subscribing to a
// dashboard expects and the difference between a year of scheduled runs
// producing one asset and producing three hundred and sixty-five.
//
// Delivery to a bucket shares everything up to the bytes: the same formatter,
// the same size ceiling, the same exactly-once rule, the same record on the
// run. Only the sink differs, and the sink an output goes to is decided by the
// destination configuration — never by the script's own address.
type outputWriter struct {
	deps   ExportDeps
	runs   script.RunStore
	run    *script.Run
	script *script.Script
	// caller issues the delivery as an ordinary platform tool call over the
	// run's own MCP session, so an object leaving the platform crosses the same
	// authorization and audit middleware every other call crosses. Nil on a
	// deployment with no assembled server, which delivery reports rather than
	// working around.
	caller scriptrun.Caller
	// written names the (output, destination) pairs THIS attempt produced,
	// which is what separates the two ways one can already be present: the run
	// row carries what an earlier attempt wrote (idempotency, skip it), and this
	// set carries what the script has written since the interpreter started (a
	// second call to the same place, which is a bug in the script and must not
	// be swallowed).
	written map[string]bool
	// delivered maps the address of every object this attempt put in a bucket to
	// the output that wrote it, because the object KEY is what identifies a
	// delivered file — two output names can arrive at one key, and the second
	// write would silently replace the first.
	delivered map[string]string
}

// newOutputWriter builds the writer for one claimed run.
func newOutputWriter(deps ExportDeps, runs script.RunStore, run *script.Run, sc *script.Script, caller scriptrun.Caller) *outputWriter {
	return &outputWriter{
		deps: deps, runs: runs, run: run, script: sc, caller: caller,
		written: map[string]bool{}, delivered: map[string]string{},
	}
}

// Export writes one output to the destination the host resolved, and records
// it on the run.
func (w *outputWriter) Export(ctx context.Context, req scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	if err := w.refuseRepeat(req.Name, req.Destination.Name); err != nil {
		return nil, err
	}
	if prior := w.priorAttempt(req.Name, req.Destination.Name); prior != nil {
		return prior, nil
	}

	data, identity, err := scriptrun.FormatOutput(req)
	if err != nil {
		//nolint:wrapcheck // FormatOutput names the output, what went wrong with it,
		// and what to do; a second wrap here would only repeat the output's name.
		return nil, err
	}

	written, out, err := w.write(ctx, req, identity, data)
	if err != nil {
		return nil, err
	}
	w.record(ctx, out)
	return written, nil
}

// write sends one formatted output to its destination.
func (w *outputWriter) write(ctx context.Context, req scriptrun.ExportRequest, identity scriptrun.OutputIdentity, data []byte) (*scriptrun.ExportResult, script.RunOutput, error) {
	if req.Destination.IsPortal() {
		return w.writePortal(ctx, req, identity, data)
	}
	return w.deliver(ctx, req, identity, data)
}

// refuseRepeat refuses a second write of one output name to one destination
// within a single execution.
//
// One name, one output, per destination, per run. A script that exports the
// same name to the same place twice means two different results to store under
// one identity, and the honest answer is to fail rather than to keep the first
// and drop the second. The same name to a DIFFERENT destination is the
// supported case — a dashboard asset and a file for another system, from one
// computed result — so the key is the pair.
func (w *outputWriter) refuseRepeat(name, destination string) error {
	if !w.written[outputKey(name, destination)] {
		return nil
	}
	return fmt.Errorf("output %q was already written to %q by this run; each output name may be written once per destination, so give the second one its own name",
		name, destination)
}

// priorAttempt reports what an EARLIER attempt of this run already wrote for
// one output, or nil.
//
// A run reclaimed after its worker died re-executes from the top, so an output
// a previous attempt persisted must not be written a second time — least of all
// one delivered out of the platform. The run row is the record of that, written
// as each output landed.
func (w *outputWriter) priorAttempt(name, destination string) *scriptrun.ExportResult {
	prior := w.run.Output(name, destination)
	if prior == nil {
		return nil
	}
	slog.Info("scripts: output already written by an earlier attempt of this run",
		logKeyRunID, w.run.ID, "output", name, "destination", destination,
		"asset_id", prior.AssetID, "key", prior.Key)
	// Marked as handled by this attempt too, so a script that writes one name to
	// one place twice is refused the same way whether or not the run it is
	// executing in was reclaimed. Without this the second call would find the
	// same prior record and be answered with it, and the script bug the first
	// attempt would have failed on would pass silently.
	w.written[outputKey(name, destination)] = true
	return &scriptrun.ExportResult{
		AssetID: prior.AssetID, AssetVersion: prior.AssetVersion,
		Bucket: prior.Bucket, Key: prior.Key, Bytes: prior.Bytes,
	}
}

// record notes one written output on the run row and on this attempt.
func (w *outputWriter) record(ctx context.Context, out script.RunOutput) {
	if err := w.runs.RecordOutput(ctx, w.run.Lease(), out); err != nil {
		// The output exists and is correct; only the run's record of it failed.
		// Failing the run here would report a write that did happen as a write
		// that did not, so the run continues and the gap is logged.
		slog.Error("scripts: recording an output on the run failed",
			logKeyRunID, w.run.ID, "output", out.Name, logKeyError, err)
	}
	w.run.Outputs = append(w.run.Outputs, out)
	w.written[outputKey(out.Name, out.Destination)] = true
}

// outputKey identifies one write: an output name at one destination.
func outputKey(name, destination string) string {
	return name + "\x00" + destination
}

// outputIdentityKey is the idempotency key that makes one (script, output name)
// pair one portal asset. It names the script by ID rather than by name, so
// renaming a script keeps its outputs and deleting one does not let a later
// script with the same name inherit them. Both the export write and the
// data-region refresh resolve through it, which is what "the same identity
// rule" means: the asset a refresh finds is the asset the export wrote.
func (w *outputWriter) outputIdentityKey(name string) string {
	return "script:" + w.script.ID + ":" + name
}

// writePortal stores one output as a new version of the script's asset.
func (w *outputWriter) writePortal(ctx context.Context, req scriptrun.ExportRequest, identity scriptrun.OutputIdentity, data []byte) (*scriptrun.ExportResult, script.RunOutput, error) {
	if !w.deps.ready() {
		return nil, script.RunOutput{}, fmt.Errorf("output %q cannot be written: this deployment has no portal asset store or object storage configured", req.Name)
	}
	asset, err := w.assetFor(ctx, req.Name, identity.ContentType)
	if err != nil {
		return nil, script.RunOutput{}, err
	}
	summary := fmt.Sprintf("%s v%d, run %s", w.script.Name, w.run.Version, w.run.ID)
	version, tables, err := w.storeVersion(ctx, asset.ID, identity, data, summary)
	if err != nil {
		return nil, script.RunOutput{}, fmt.Errorf("writing output %q: %w", req.Name, err)
	}
	out := script.RunOutput{
		Name: req.Name, Destination: req.Destination.Name,
		AssetID: asset.ID, AssetVersion: version,
		Format: req.Format, RowCount: len(req.Rows), Document: req.Body != nil,
		Bytes: len(data), Tables: tables,
	}
	return &scriptrun.ExportResult{AssetID: asset.ID, AssetVersion: version, Bytes: len(data), Tables: tables}, out, nil
}

// storeVersion is the one store step a script output version takes, shared by
// the export write and the data-region refresh so how a version is stored —
// the key scheme, the fields, the creating principal — cannot drift between
// them: an immutable per-run object, then the version row that repoints the
// asset at it, then the tables registered over the asset following that
// version (#1536), reported as the sentences the run carries.
func (w *outputWriter) storeVersion(ctx context.Context, assetID string, identity scriptrun.OutputIdentity, data []byte, summary string) (version int, tables []string, err error) {
	key := w.objectKey(assetID, identity.Extension)
	if err := w.deps.S3.PutObject(ctx, w.deps.Bucket, key, data, identity.ContentType); err != nil {
		return 0, nil, fmt.Errorf("uploading the object: %w", err)
	}
	version, err = w.deps.Versions.CreateVersion(ctx, portal.AssetVersion{
		ID:            uuid.New().String(),
		AssetID:       assetID,
		S3Key:         key,
		S3Bucket:      w.deps.Bucket,
		ContentType:   identity.ContentType,
		SizeBytes:     int64(len(data)),
		CreatedBy:     w.script.Principal(),
		ChangeSummary: summary,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("recording the version: %w", err)
	}
	return version, w.followTables(ctx, assetID, version), nil
}

// followTables reports what a version did to the tables registered over the
// asset. A deployment with no registrar has none to report.
func (w *outputWriter) followTables(ctx context.Context, assetID string, version int) []string {
	if w.deps.FollowTables == nil {
		return nil
	}
	return w.deps.FollowTables(ctx, assetID, version)
}

// assetFor finds the asset this output name maps to, creating it the first time
// the script writes that name.
//
// The mapping is carried by the asset's idempotency key (outputIdentityKey),
// which is what the portal store already indexes per owner — so "one asset per
// (script, output)" needs no new table and no lookup this package would have to
// keep unique itself.
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
	key := w.outputIdentityKey(name)
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
