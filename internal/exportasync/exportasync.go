// Package exportasync runs a long export in the background and lets its
// caller read it again by its idempotency key (#2212).
//
// graphql_export and api_export walk every page inside one tool call. A walk
// longer than the model client's own tool-call timeout failed on the client's
// side, with no asset written and no handle to reconnect to, and a client does
// not let the platform raise that timeout. An export asked to run in the
// background (async, or a page walk from a model client) is answered at once
// with a run id and an idempotency key; the walk runs on the replica that took
// the call. Calling the same export again with the key reads the run: its
// progress while it runs, the export's own answer once it is done, and its
// failure if it failed. A key whose export is still running answers with that
// run, so a retry never starts a second walk against the upstream.
//
// A run is a call task (internal/platform/calltask), the record the MCP Tasks
// extension keeps, marked as a background export; the Tasks extension is for a
// client that declares it, and this is the same work for one that does not.
package exportasync

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The states a run is in.
const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// Progress is how far a walk has come.
type Progress struct {
	PagesFetched int `json:"pages_fetched"`
	ItemsMerged  int `json:"items_merged"`
}

// Run is one background export as its caller reads it.
type Run struct {
	ID     string
	Status string
	// Message is the run's own sentence: what it is doing, or why it failed.
	Message  string
	Progress Progress
	// Result is the export's answer once it completed, in the wire form a
	// tool result takes.
	Result    json.RawMessage
	StartedAt time.Time
	UpdatedAt time.Time
}

// Runner starts background exports and finds them again. The caller is read
// from ctx: a run is found only by the person who started it.
type Runner interface {
	// Start runs work apart from the request under key, or answers with the
	// run already working under it for this caller and tool, with started
	// false.
	Start(ctx context.Context, tool, key string, work func(ctx context.Context) (*mcp.CallToolResult, error)) (run *Run, started bool, err error)
	// Find reads the latest run of tool under key for the caller, or
	// ErrNotFound.
	Find(ctx context.Context, tool, key string) (*Run, error)
}

// ErrNotFound is a key no run of the caller's has.
var ErrNotFound = errors.New("no background export under this idempotency key")

// progressKey carries a run's progress cell on its context.
type progressKey struct{}

// Cell is where a running export writes its progress for the runner to read.
type Cell struct {
	pages atomic.Int64
	items atomic.Int64
}

// Load is the progress written last.
func (c *Cell) Load() Progress {
	return Progress{PagesFetched: int(c.pages.Load()), ItemsMerged: int(c.items.Load())}
}

// WithProgress is ctx carrying cell, which Report writes to.
func WithProgress(ctx context.Context, cell *Cell) context.Context {
	return context.WithValue(ctx, progressKey{}, cell)
}

// Report records how far the export running on ctx has come. A context that
// carries no cell, an export answered inline, records nothing.
func Report(ctx context.Context, pages, items int) {
	cell, ok := ctx.Value(progressKey{}).(*Cell)
	if !ok {
		return
	}
	cell.pages.Store(int64(pages))
	cell.items.Store(int64(items))
}

// Result decodes a completed run's answer back into the tool result it was.
func Result(run *Run) (*mcp.CallToolResult, error) {
	var res mcp.CallToolResult
	if err := json.Unmarshal(run.Result, &res); err != nil {
		return nil, errors.New("the background export's result could not be read back: " + err.Error())
	}
	return &res, nil
}
