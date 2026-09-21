package scriptrun

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"

	"go.starlark.net/starlark"
)

// toolNotify is the platform tool both bindings issue. It is named here rather
// than imported from internal/platform/notifylayer because this package
// reaches every tool by name through one funnel and imports none of them.
const toolNotify = "notify"

// notify actions, as the tool's schema names them.
const (
	notifyActionSend    = "send"
	notifyActionPublish = "publish"
)

// notify posts a message to a channel an administrator configured.
//
// It is a named helper over one notify action rather than something a script
// does through platform.call, for the reason query and export are: the call it
// composes is worth a name, and a monitor that posts what it found is the
// thing most scripts that post do. The authorization is unchanged either way
// -- the call goes through the same funnel, under the run's persona, and a
// channel the run cannot reach is refused by the tool in the tool's own words.
//
// The link defaults to the run's own page, so every post from a scheduled
// script leads back to the run that produced it. A monitor that posts a number
// without saying where the number came from is the thing people mute.
func (h *hostState) notify(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var channel, title, body, link string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"channel", &channel, "title", &title, "body?", &body, "link?", &link); err != nil {
		return nil, argErr(b, err)
	}
	if channel == "" {
		return nil, fmt.Errorf("in %s: channel is empty; name the channel to post to, as %s(channel=\"ops\", title=\"...\", body=\"...\")", b.Name(), b.Name())
	}
	if title == "" {
		return nil, fmt.Errorf("in %s: title is empty; a message needs a title", b.Name())
	}
	if link == "" {
		link = h.runLink()
	}
	return h.issueNotify(b, map[string]any{
		"action":  notifyActionSend,
		"channel": channel,
		"title":   title,
		"body":    body,
		"link":    link,
	})
}

// publish posts a portal asset this script wrote to a channel.
//
// The asset is named the way platform.export named it when it wrote it, so a
// script that builds a report and publishes it says the same name twice rather
// than carrying an id between the two calls.
func (h *hostState) publish(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var channel, name, message string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"channel", &channel, "name", &name, "message?", &message); err != nil {
		return nil, argErr(b, err)
	}
	if channel == "" {
		return nil, fmt.Errorf("in %s: channel is empty; name the channel to publish to, as %s(channel=\"ops\", name=\"Weekly report\")", b.Name(), b.Name())
	}
	if name == "" {
		return nil, fmt.Errorf("in %s: name is empty; name the asset to publish, as the name platform.export saved it under", b.Name())
	}
	return h.issueNotify(b, map[string]any{
		"action":  notifyActionPublish,
		"channel": channel,
		"asset":   name,
		"message": message,
	})
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
	decision, err := h.admitCall(b, toolNotify, payload)
	if err != nil {
		return nil, err
	}
	out, err := h.callTool(toolNotify, payload)
	if err != nil {
		return nil, argErr(b, err)
	}
	if decision.Writes && h.opts.Writes == WritesReported {
		h.writes = append(h.writes, WriteRecord{Tool: toolNotify, Call: decision.Call})
	}
	h.noteChannel(payload, out)
	return starlarkconv.ToStarlark(out) //nolint:wrapcheck // the converter names the value it could not convert
}

// runLink is the page of the run making the call, or nothing when this
// deployment does not know its own public address.
func (h *hostState) runLink() string {
	if h.opts.RunURL == "" {
		return ""
	}
	return h.opts.RunURL
}
