package scriptrec

import (
	"context"
	"errors"
	"time"
)

// The kinds of run a recording is of.
const (
	KindRun   = "run"
	KindDraft = "draft"
)

// ErrNotFound is a recording no store holds.
var ErrNotFound = errors.New("no such recording")

// Meta describes a stored recording.
type Meta struct {
	// RunID is the run's id, or the draft's, and is what a test names.
	RunID string `json:"run_id"`
	// ScriptID is the script recorded, empty for a draft of a script not yet
	// saved until a script naming the recording in a test is saved.
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name"`
	Kind       string `json:"kind"`
	// RecordedBy is who the recorded run ran for: the draft's author, or the
	// owner of the script a run ran.
	RecordedBy string `json:"recorded_by"`
	// Version is the version a run executed, zero for a draft.
	Version      int    `json:"version,omitempty"`
	SourceSHA256 string `json:"source_sha256"`
	Succeeded    bool   `json:"succeeded"`
	// Reason says why there is no recording to replay, when there is none.
	Reason string `json:"reason,omitempty"`
	Bytes  int    `json:"bytes"`
	// Kept is true while a test in the script's latest version names the
	// recording, which keeps it past the run-retention sweep.
	Kept      bool      `json:"kept"`
	CreatedAt time.Time `json:"created_at"`
}

// Replayable reports whether the recording can be replayed.
func (m Meta) Replayable() bool { return m.Reason == "" }

// Stored is a recording as a store holds it.
type Stored struct {
	Meta
	Data []byte
}

// Store keeps recordings.
type Store interface {
	// Save stores a recording, replacing one recorded under the same run id
	// (a run taken over from a worker that died records again).
	Save(ctx context.Context, rec Stored) error
	// Get returns one recording, or ErrNotFound.
	Get(ctx context.Context, runID string) (*Stored, error)
	// Recent returns the newest replayable recordings of the script's
	// successful runs, newest first.
	Recent(ctx context.Context, scriptID string, limit int) ([]Stored, error)
	// Keep marks the recordings runIDs names as kept, and every other
	// recording of the script as not, and attaches to the script the ones
	// among them author recorded as drafts of it before it was saved.
	Keep(ctx context.Context, scriptID, author string, runIDs []string) error
	// Purge deletes the recordings older than retention that are not kept.
	Purge(ctx context.Context, retention time.Duration) (int64, error)
}
