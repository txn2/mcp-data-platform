package scriptrec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// Replay answers an execution's host calls from a recording, and never reaches
// an upstream: it is the scriptrun.Caller a test and a regression replay run
// with. Calls with the same key are answered in the order the run made them.
type Replay struct {
	rec  *Recording
	byID map[string][]int
	used map[string]int
	made []Made
	// byTool and lenient answer a call the recording holds no answer for
	// with the next unused answer the same tool gave, when the recording was
	// altered on purpose and its later calls' arguments moved with it.
	byTool  map[string][]int
	usedAt  map[int]bool
	lenient bool
}

// Made is one tool call an execution made against a Replay, with what it was
// answered.
type Made struct {
	Tool  string
	Args  map[string]any
	Out   map[string]any
	Error string
}

// MissingError is a call the recording holds no answer for. Its text names
// the call, which is what the author reads to know what the recording lacks.
type MissingError struct {
	Call string
	// Held is how many answers the recording holds for the call, and so how
	// many times it could be made.
	Held int
}

func (e *MissingError) Error() string {
	if e.Held == 0 {
		return "the recording holds no answer for " + e.Call +
			"; record a run that makes it (run_draft, with allow_writes=true for a call that writes) and replay that one"
	}
	return fmt.Sprintf("the recording holds %d answer(s) for %s, and it was called again", e.Held, e.Call)
}

// NewReplay answers from rec.
func NewReplay(rec *Recording) *Replay {
	r := &Replay{rec: rec, byID: map[string][]int{}, used: map[string]int{}, byTool: map[string][]int{}, usedAt: map[int]bool{}}
	for i, c := range rec.Calls {
		r.byID[c.Key] = append(r.byID[c.Key], i)
		if c.Tool != "" {
			r.byTool[c.Tool] = append(r.byTool[c.Tool], i)
		}
	}
	return r
}

// Lenient returns the replay answering a tool call the recording holds no
// answer for with the next unused answer the same tool gave. A test's replay
// is never lenient; the check that alters a recording's rows is, because the
// calls a script makes after reading altered rows carry altered arguments.
func (r *Replay) Lenient() *Replay {
	r.lenient = true
	return r
}

// Header is what the recorded run started from.
func (r *Replay) Header() Header { return r.rec.Header }

// Made is every tool call the execution made, in order.
func (r *Replay) Made() []Made { return r.made }

// next is the recorded call answering key, or the reason there is none.
func (r *Replay) next(key, describe string) (*Call, error) {
	held := r.byID[key]
	n := r.used[key]
	for n < len(held) && r.usedAt[held[n]] {
		n++
	}
	if n >= len(held) {
		return r.byToolAnswer(key, describe, len(held))
	}
	r.used[key] = n + 1
	r.usedAt[held[n]] = true
	return &r.rec.Calls[held[n]], nil
}

// byToolAnswer is a lenient replay's answer for a call with no exact match.
func (r *Replay) byToolAnswer(key, describe string, held int) (*Call, error) {
	if r.lenient {
		for _, i := range r.byTool[toolOf(key)] {
			if !r.usedAt[i] {
				r.usedAt[i] = true
				return &r.rec.Calls[i], nil
			}
		}
	}
	return nil, &MissingError{Call: describe, Held: held}
}

// toolOf is the tool a ToolKey names, "" for an output's key.
func toolOf(key string) string {
	kind, rest, _ := strings.Cut(key, keySep)
	tool, _, found := strings.Cut(rest, keySep)
	if kind != "tool" || !found {
		return ""
	}
	return tool
}

// CallTool answers one tool call from the recording.
func (r *Replay) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	c, err := r.next(ToolKey(name, args), DescribeCall(name, args))
	if err != nil {
		r.made = append(r.made, Made{Tool: name, Args: args, Error: err.Error()})
		return nil, err
	}
	r.made = append(r.made, Made{Tool: name, Args: args, Out: c.Out, Error: c.Error})
	if c.Error != "" {
		return nil, errors.New(c.Error)
	}
	return cloneMap(c.Out), nil
}

// Exporter is the writer an execution replaying the recording uses: nil when
// the recorded run previewed its outputs, so the replay previews them too, and
// otherwise one answering each write with what the run's writer answered.
func (r *Replay) Exporter() scriptrun.Exporter {
	if r.rec.Preview {
		return nil
	}
	return replayExporter{r}
}

type replayExporter struct{ r *Replay }

// Export answers a write with what the run's writer answered.
func (e replayExporter) Export(_ context.Context, req scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	return e.r.output(ExportKey(req), fmt.Sprintf("platform.export(%q)", req.Name))
}

// PublishData answers a refresh with what the run's writer answered.
func (e replayExporter) PublishData(_ context.Context, req scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	return e.r.output(PublishKey(req), fmt.Sprintf("platform.publish_data(%q)", req.Name))
}

func (r *Replay) output(key, describe string) (*scriptrun.ExportResult, error) {
	c, err := r.next(key, describe)
	if err != nil {
		return nil, err
	}
	if c.Error != "" {
		return nil, errors.New(c.Error)
	}
	if c.Output == nil {
		return &scriptrun.ExportResult{}, nil
	}
	out := *c.Output
	return &out, nil
}

// DescribeCall names a tool call the way the script wrote it: a query by its
// SQL, anything else by its tool and arguments.
func DescribeCall(tool string, args map[string]any) string {
	if sql, ok := args["sql"].(string); ok && tool == scriptrun.ToolQuery {
		return fmt.Sprintf("platform.query(%q)", strings.TrimSpace(sql))
	}
	b, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprintf("platform.call(%q)", tool)
	}
	return fmt.Sprintf("platform.call(%q, %s)", tool, b)
}

// cloneMap copies a recorded answer so an execution that changes the value it
// was handed cannot change what the next replay of the same call answers.
func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return m
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return m
	}
	return out
}
