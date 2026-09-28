package scripttest

import (
	"encoding/json"
	"fmt"
	"slices"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlive"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// toolNotify is the tool platform.notify and platform.publish issue.
const toolNotify = "notify"

// produced is what one test's execution has produced so far: the outputs it
// wrote or previewed, the state it staged, and, through the replay and the
// live report, the calls it made and the value it returned.
type produced struct {
	exports   []scriptrun.ExportRequest
	publishes []scriptrun.PublishRequest
	state     *script.StateWrite
	replay    *scriptrec.Replay
	live      *scriptlive.Live
	// asserts is how many assertions the test made, and failures how many of
	// them were assert.fails catching the failure it expected.
	asserts  int
	failures int
}

// observe is the scriptrun.TestHooks.Observe of one test.
func (p *produced) observe(v any) {
	switch v := v.(type) {
	case scriptrun.ExportRequest:
		p.export(v)
	case scriptrun.PublishRequest:
		p.publishes = append(p.publishes, v)
	case *script.StateWrite:
		p.state = v
	}
}

// export keeps one output. The pages of an appended output are one output,
// their rows in the order they were written; the whole written when the run
// ends is those pages again, and is not kept twice.
func (p *produced) export(req scriptrun.ExportRequest) {
	if req.Spooled != nil {
		return
	}
	if req.Append {
		for i := range p.exports {
			if p.exports[i].Append && p.exports[i].Name == req.Name && p.exports[i].Destination.Name == req.Destination.Name {
				p.exports[i].Rows = append(slices.Clone(p.exports[i].Rows), req.Rows...)
				return
			}
		}
	}
	p.exports = append(p.exports, req)
}

// nothing reports whether the execution produced no output at all: nothing
// written, refreshed, staged, returned, posted, or sent through a tool other
// than the query tool.
func (p *produced) nothing() bool {
	posted := false
	for _, m := range p.replay.Made() {
		posted = posted || m.Tool != scriptrun.ToolQuery
	}
	return len(p.exports) == 0 && len(p.publishes) == 0 && p.state == nil && len(p.live.Result()) == 0 && !posted
}

// value is testing.outputs(): what the test's execution produced, as a struct
// a test asserts on. Nothing it holds was written anywhere.
func (p *produced) value() (starlark.Value, error) {
	exports := make([]starlark.Value, 0, len(p.exports))
	for _, req := range p.exports {
		v, err := exportValue(req)
		if err != nil {
			return nil, err
		}
		exports = append(exports, v)
	}
	publishes := make([]starlark.Value, 0, len(p.publishes))
	for _, req := range p.publishes {
		data, err := starlarkconv.ToStarlark(req.Data)
		if err != nil {
			return nil, fmt.Errorf("converting published data %q: %w", req.Name, err)
		}
		publishes = append(publishes, starlarkstruct.FromStringDict(starlarkstruct.Default,
			starlark.StringDict{"name": starlark.String(req.Name), "data": data}))
	}
	calls, notifies, err := p.calls()
	if err != nil {
		return nil, err
	}
	state, err := p.stateValue()
	if err != nil {
		return nil, err
	}
	result, err := p.resultValue()
	if err != nil {
		return nil, err
	}
	logText, _ := p.live.Log()
	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"exports":   starlark.NewList(exports),
		"publishes": starlark.NewList(publishes),
		"notifies":  starlark.NewList(notifies),
		"calls":     starlark.NewList(calls),
		"state":     state,
		"result":    result,
		"log":       starlark.String(logText),
	}), nil
}

// calls is every tool call the execution made, and the notify calls among
// them as their arguments.
func (p *produced) calls() (calls, notifies []starlark.Value, err error) {
	made := p.replay.Made()
	calls = make([]starlark.Value, 0, len(made))
	notifies = []starlark.Value{}
	for _, m := range made {
		args, err := starlarkconv.ToStarlark(m.Args)
		if err != nil {
			return nil, nil, fmt.Errorf("converting the arguments of %s: %w", m.Tool, err)
		}
		calls = append(calls, starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"tool": starlark.String(m.Tool), "args": args, "error": starlark.String(m.Error),
		}))
		if m.Tool == toolNotify {
			notifies = append(notifies, args)
		}
	}
	return calls, notifies, nil
}

func (p *produced) stateValue() (starlark.Value, error) {
	if p.state == nil {
		return starlark.None, nil
	}
	v, err := starlarkconv.ToStarlark(p.state.Value)
	if err != nil {
		return nil, fmt.Errorf("converting the staged state: %w", err)
	}
	return v, nil
}

func (p *produced) resultValue() (starlark.Value, error) {
	raw := p.live.Result()
	if len(raw) == 0 {
		return starlark.None, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("reading platform.result: %w", err)
	}
	out, err := starlarkconv.ToStarlark(v)
	if err != nil {
		return nil, fmt.Errorf("converting platform.result: %w", err)
	}
	return out, nil
}

// exportValue is one output as a test reads it.
func exportValue(req scriptrun.ExportRequest) (starlark.Value, error) {
	rows, err := starlarkconv.ToStarlark(req.Rows)
	if err != nil {
		return nil, fmt.Errorf("converting the rows of output %q: %w", req.Name, err)
	}
	if req.Rows == nil {
		rows = starlark.NewList(nil)
	}
	columns := make([]starlark.Value, 0, len(req.Columns))
	for _, c := range req.Columns {
		columns = append(columns, starlark.String(c))
	}
	var body starlark.Value = starlark.None
	if req.Body != nil {
		body = starlark.String(*req.Body)
	}
	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"name":        starlark.String(req.Name),
		"format":      starlark.String(req.Format),
		"destination": starlark.String(req.Destination.Name),
		"key":         starlark.String(req.Key),
		"columns":     starlark.NewList(columns),
		"rows":        rows,
		"row_count":   starlark.MakeInt(req.RowCount()),
		"body":        body,
	}), nil
}
