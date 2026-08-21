package script

import (
	"context"
	"crypto/sha256"
	"time"
)

// The account of a draft execution (#1364).
//
// A dry run persists nothing it produced: platform.export previews, no asset
// is versioned, and no object is delivered. What is recorded here is the
// ACCOUNT of one — that a person ran this exact source, when, how it ended,
// what it printed, and the shape of the outputs it would have written.
//
// It exists for whoever reads the script later. Keying the account to the
// SOURCE rather than to a version is what makes it work in the order authors
// actually write in: an edit is dry-run before it is saved, so there is no
// version to attach it to yet, and the digest links it to whichever version
// later carries that exact source — and to no other.

// DryRunOutput is one output a draft run would have written. It carries the
// shape and nothing else: a preview has no asset id and no object key, because
// it wrote neither.
type DryRunOutput struct {
	Name        string `json:"name" example:"daily_sales"`
	Destination string `json:"destination,omitempty" example:"portal"`
	Format      string `json:"format" example:"csv"`
	RowCount    int    `json:"row_count" example:"1200"`
	// Document marks an output written verbatim from a string body, whose
	// RowCount is therefore not a fact about it.
	Document bool `json:"document,omitempty" example:"false"`
	// Refresh marks a platform.publish_data call: the run would have replaced
	// the data region of an existing asset, and Bytes is the payload it would
	// have spliced in.
	Refresh bool `json:"refresh,omitempty" example:"false"`
	// Bytes is the serialized length in the declared format. A preview
	// serializes to measure rather than estimating, so it is the size a real
	// run of the same rows would write.
	Bytes int `json:"bytes" example:"48213"`
}

// DryRun is one recorded draft execution.
type DryRun struct {
	// ID is the run id the draft executed under, which is also its session id,
	// so the audit rows the run produced are reachable from this account.
	ID       string `json:"id" example:"run_a1b2c3d4"`
	ScriptID string `json:"script_id"`
	// SourceSHA256 is the digest of the source that executed. It is what links
	// this account to a version, and it is a raw digest rather than its hex
	// spelling because that is what the column stores.
	SourceSHA256 []byte `json:"-"`
	RequestedBy  string `json:"requested_by,omitempty" example:"jane@example.com"`
	// Status is RunStatusSucceeded or RunStatusFailed. A draft run has no
	// pending state: it is executed inline, and the caller waits for it.
	Status string `json:"status" example:"succeeded"`
	// Error is why a failed draft failed, including the interpreter traceback.
	Error string `json:"error,omitempty"`
	// Log is what the run printed, bounded when it was captured.
	Log          string         `json:"log,omitempty"`
	LogTruncated bool           `json:"log_truncated,omitempty"`
	Metrics      RunMetrics     `json:"metrics"`
	Outputs      []DryRunOutput `json:"outputs,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

// Succeeded reports whether the recorded draft run finished without failing.
func (d *DryRun) Succeeded() bool { return d != nil && d.Status == RunStatusSucceeded }

// SourceDigest is the digest a dry-run account is keyed by. One definition,
// used by the writer that records a run and by the reader that matches a
// version against it, so the two cannot disagree about what "the same source"
// means.
func SourceDigest(source string) []byte {
	sum := sha256.Sum256([]byte(source))
	return sum[:]
}

// DryRunStore records and resolves accounts of draft executions.
type DryRunStore interface {
	// RecordDryRun stores one account, trimming the author's older accounts of
	// the same script so the table is bounded by the authoring loop's working
	// set rather than by how many times somebody pressed the button.
	RecordDryRun(ctx context.Context, d *DryRun) error

	// LatestDryRun returns the newest account of one script's exact source, or
	// nil when nobody has run it. Nil is the ordinary answer — most versions
	// were never dry-run — so it is not an error.
	LatestDryRun(ctx context.Context, scriptID string, sourceSHA256 []byte) (*DryRun, error)
}
