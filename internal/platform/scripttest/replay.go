package scripttest

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// Outcome is what one execution of a source against a recording produced, in
// the platform's terms rather than a test's: the regression replay a save
// runs compares two of them (#1942).
type Outcome struct {
	Exports   []scriptrun.ExportRequest
	Publishes []scriptrun.PublishRequest
	// State is what the execution staged with platform.save_state, nil when
	// it staged nothing.
	State map[string]any
	// Calls is every tool call the execution made, in order.
	Calls []scriptrec.Made
	// Result is what platform.result handed back, empty when nothing was.
	Result json.RawMessage
	// Failure is the error the execution stopped with, empty when it
	// finished. Missing names the call it stopped at when that call is one
	// the recording holds no answer for.
	Failure string
	Missing string
}

// Replay runs the request's source against rec, calling main() as a run does,
// and reports what it produced. Nothing reaches an upstream and nothing is
// written.
func Replay(ctx context.Context, req Request, rec *scriptrec.Recording) Outcome {
	out, _, err := execute(ctx, req, execution{recording: rec.RunID, rec: rec})
	o := Outcome{
		Exports: out.exports, Publishes: out.publishes,
		Calls: out.replay.Made(), Result: out.live.Result(),
	}
	if out.state != nil {
		o.State = out.state.Value
	}
	if err != nil {
		o.Failure, _ = describeFailure(err, req.Name)
		var missing *scriptrec.MissingError
		if errors.As(err, &missing) {
			o.Missing = missing.Call
		}
	}
	return o
}
