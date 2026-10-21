// Package scriptnotify reads a managed script's platform.notify and
// platform.publish arguments into the notify tool call each one issues.
//
// It holds only the reading: what each argument may be, and the payload it
// becomes. Issuing the call -- admission, the write barrier in a draft, the
// funnel every host call goes through, recording what was posted -- stays in
// internal/platform/scriptrun with the rest of the host. The two bindings'
// arguments grew past what that package's size budget had room for, and
// nothing in them needs a run.
package scriptnotify

import (
	"fmt"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// Tool is the platform tool both bindings issue. It is named here rather than
// imported from internal/platform/notifylayer because the host reaches every
// tool by name through one funnel and imports none of them.
const Tool = "notify"

// notify actions, as the tool's schema names them.
const (
	actionSend    = "send"
	actionPublish = "publish"
)

// keyChannel is the payload key naming the channel, in both actions.
const keyChannel = "channel"

// options are the arguments the two bindings share: the suppression key, the
// window it is held for, and the chat channel of a bot channel a post goes to
// (#2241).
type options struct {
	key         string
	repeatAfter string
	target      string
}

// apply writes the shared arguments onto a payload, leaving out the ones the
// script did not give so the tool applies its defaults.
func (o options) apply(payload map[string]any) {
	if o.key != "" {
		payload["key"] = o.key
	}
	if o.repeatAfter != "" {
		payload["repeat_after"] = o.repeatAfter
	}
	if o.target != "" {
		payload["target"] = o.target
	}
}

// Send reads platform.notify(channel, title, body="", link="", data=None,
// key="", repeat_after="", attachments=None, target="") into a notify
// action=send call.
//
// runLink is the run's own page. It is the link when the script names none,
// so every post from a scheduled script leads back to the run that produced
// it: a monitor that posts a number without saying where the number came from
// is the thing people mute.
func Send(b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple, runLink string) (map[string]any, error) {
	var (
		channel, title, body, link string
		opts                       options
		data                       starlark.Value = starlark.None
		attachments                starlark.Value = starlark.None
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"channel", &channel, "title", &title, "body?", &body, "link?", &link, "data?", &data,
		"key?", &opts.key, "repeat_after?", &opts.repeatAfter, "attachments?", &attachments,
		"target?", &opts.target); err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	if channel == "" {
		return nil, fmt.Errorf("in %s: channel is empty; name the channel to post to, as %s(channel=\"ops\", title=\"...\", body=\"...\")", b.Name(), b.Name())
	}
	if title == "" {
		return nil, fmt.Errorf("in %s: title is empty; a message needs a title", b.Name())
	}
	if link == "" {
		link = runLink
	}
	payload := map[string]any{
		"action":   actionSend,
		keyChannel: channel,
		"title":    title,
		"body":     body,
		"link":     link,
	}
	opts.apply(payload)
	if data != starlark.None {
		goData, err := starlarkconv.FromStarlark(data)
		if err != nil {
			return nil, fmt.Errorf("in %s: data must be JSON-shaped (dicts, lists, strings, numbers, booleans, None): %w", b.Name(), err)
		}
		payload["data"] = goData
	}
	if attachments != starlark.None {
		list, err := attachmentList(b, attachments)
		if err != nil {
			return nil, err
		}
		payload["attachments"] = list
	}
	return payload, nil
}

// attachmentList reads the attachments argument: a list whose entries are
// strings (an asset id or a resource reference) or dicts naming asset_id or
// resource_id. The tool checks each entry; this checks only that the shape is
// a list the tool can be handed.
func attachmentList(b *starlark.Builtin, v starlark.Value) ([]any, error) {
	if _, ok := v.(*starlark.List); !ok {
		return nil, fmt.Errorf("in %s: attachments must be a list of asset ids, resource references, or dicts naming asset_id or resource_id, not %s", b.Name(), v.Type())
	}
	goValue, err := starlarkconv.FromStarlark(v)
	if err != nil {
		return nil, fmt.Errorf("in %s: attachments must be strings and dicts: %w", b.Name(), err)
	}
	list, _ := goValue.([]any)
	return list, nil
}

// Publish reads platform.publish(channel, name, message="", key="",
// repeat_after="", access="", expires_in="", target="") into a notify
// action=publish call.
//
// The asset is named the way platform.export named it when it wrote it, so a
// script that builds a report and publishes it says the same name twice rather
// than carrying an id between the two calls. access is who the posted link
// opens for (#2088); left out, the tool's default, signed_in, applies.
func Publish(b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (map[string]any, error) {
	var (
		channel, name, message, access, expiresIn string
		opts                                      options
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"channel", &channel, "name", &name, "message?", &message,
		"key?", &opts.key, "repeat_after?", &opts.repeatAfter,
		"access?", &access, "expires_in?", &expiresIn, "target?", &opts.target); err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	if channel == "" {
		return nil, fmt.Errorf("in %s: channel is empty; name the channel to publish to, as %s(channel=\"ops\", name=\"Weekly report\")", b.Name(), b.Name())
	}
	if name == "" {
		return nil, fmt.Errorf("in %s: name is empty; name the asset to publish, as the name platform.export saved it under", b.Name())
	}
	payload := map[string]any{
		"action":   actionPublish,
		keyChannel: channel,
		"asset":    name,
		"message":  message,
	}
	if access != "" {
		payload["access"] = access
	}
	if expiresIn != "" {
		payload["expires_in"] = expiresIn
	}
	opts.apply(payload)
	return payload, nil
}
