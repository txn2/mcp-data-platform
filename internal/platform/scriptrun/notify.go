package scriptrun

import (
	"fmt"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptnotify"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// notify posts a message to a channel an administrator configured.
//
// It is a named helper over one notify action rather than something a script
// does through platform.call, for the reason query and export are: the call it
// composes is worth a name, and a monitor that posts what it found is the
// thing most scripts that post do. The authorization is unchanged either way
// -- the call goes through the same funnel, under the run's persona, and a
// channel the run cannot reach is refused by the tool in the tool's own words.
func (h *hostState) notify(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	payload, err := scriptnotify.Send(b, args, kwargs, h.runLink())
	if err != nil {
		return nil, err //nolint:wrapcheck // the reader names the binding
	}
	return h.issueNotify(b, payload)
}

// publish posts a portal asset this script wrote to a channel.
func (h *hostState) publish(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	payload, err := scriptnotify.Publish(b, args, kwargs)
	if err != nil {
		return nil, err //nolint:wrapcheck // the reader names the binding
	}
	return h.issueNotify(b, payload)
}

// issueNotify runs one notify call through the same admission and funnel every
// other host binding's call goes through, and reports what the tool answered.
//
// A draft run without allow_writes is refused here, by the write barrier,
// exactly as it refuses a saved asset: a message posted to a colleague's chat
// is not something a dry run may do.
func (h *hostState) issueNotify(b *starlark.Builtin, payload map[string]any) (starlark.Value, error) {
	if h.opts.Caller == nil {
		return nil, fmt.Errorf("host binding %s is not available in this context", b.Name())
	}
	decision, err := h.admitCall(b, scriptnotify.Tool, payload)
	if err != nil {
		return nil, err
	}
	out, err := h.callTool(scriptnotify.Tool, payload)
	if err != nil {
		return nil, argErr(b, err)
	}
	if decision.Writes && h.opts.Writes == WritesReported {
		h.writes = append(h.writes, WriteRecord{Tool: scriptnotify.Tool, Call: decision.Call})
	}
	h.noteChannel(payload, out)
	return starlarkconv.ToStarlark(out) //nolint:wrapcheck // the converter names the value it could not convert
}

// runLink is the page of the run making the call, or nothing when this
// deployment does not know its own public address.
func (h *hostState) runLink() string {
	return h.opts.RunURL
}
