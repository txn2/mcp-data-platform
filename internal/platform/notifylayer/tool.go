package notifylayer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ToolName is the MCP tool name, exported for composition roots that bind UI
// apps to it and for the platform's tool inventory.
const ToolName = "notify"

// Actions.
const (
	actionList    = "list"
	actionSend    = "send"
	actionPublish = "publish"
)

// toolDescription is what a model reads to decide whether this tool is the
// one. It states the two things the schema cannot: that a destination has to
// already exist, and that delivery is queued rather than immediate.
const toolDescription = `Send a message, or publish a saved asset, to a notification channel an administrator has configured (Slack, Mattermost, an incoming webhook, or an email list).

Call action=list first: channel names are not guessable, and a channel this session cannot reach is not listed. action=send posts a title and a markdown body with an optional link and up to 5 attached assets or resources. action=publish posts a portal asset -- its title, its content, its preview image, and a link back to it.

For an alert that repeats, pass key: a second send with the same key inside the channel's repeat window is counted and not posted, and the next post says how many were held back. A channel refuses sends past its hourly cap.

Delivery is queued, so success means the message was accepted for delivery, not that it has already appeared. This tool does not create channels.`

// notifyInput is the tool's input schema, inferred by the SDK from these
// fields. The SDK validates a tools/call against it, which is why the input is
// a struct rather than a hand-written schema.
type notifyInput struct {
	// Action is list, send or publish.
	Action string `json:"action" jsonschema:"list, send or publish"`

	// Channel names the destination for send and publish.
	Channel string `json:"channel,omitempty" jsonschema:"the channel name, as action=list reports it"`

	// Title and Body are the document for action=send.
	Title string `json:"title,omitempty" jsonschema:"the message title, required for action=send"`
	Body  string `json:"body,omitempty" jsonschema:"the message body as markdown"`
	// Link is the absolute URL a reader follows from the message.
	Link string `json:"link,omitempty" jsonschema:"an absolute URL the reader can follow"`
	// Data is structured JSON a webhook channel with format json delivers
	// verbatim; every other channel ignores it.
	Data any `json:"data,omitempty" jsonschema:"optional structured JSON for action=send, delivered verbatim to a webhook channel whose format is json (at most 64 KiB) and ignored by every other channel"`

	// Attachments name the assets and resources that travel with the
	// message as files, as a list of strings or of objects.
	Attachments []any `json:"attachments,omitempty" jsonschema:"optional files to attach, at most 5: each an asset id, an asset's portal URL, a resource id or mcp:resource: reference, or an object {asset_id or resource_id, name}"`
	// Key identifies a repeating alert, for send and publish.
	Key string `json:"key,omitempty" jsonschema:"optional key naming this alert; a second send with the same key inside the channel's repeat window is counted and not posted, and the next post states how many were held back"`
	// RepeatAfter lengthens the channel's repeat window for this key.
	RepeatAfter string `json:"repeat_after,omitempty" jsonschema:"optional duration such as 6h that lengthens the channel's repeat window for this key; a value shorter than the channel's own is ignored"`

	// Asset is the portal asset id for action=publish.
	Asset string `json:"asset,omitempty" jsonschema:"the portal asset id to publish, for action=publish"`
	// Message is an optional line published above the asset's content.
	Message string `json:"message,omitempty" jsonschema:"an optional line to publish above the asset"`
}

// channelSummary is one entry of the list action's answer.
type channelSummary struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Mode        string `json:"mode"`
	Description string `json:"description,omitempty"`
	// Carries states what this kind can show, so a caller knows before
	// sending whether its markdown will be read as markup or as text.
	Carries string `json:"carries"`
}

// listResult is the list action's answer.
type listResult struct {
	Channels []channelSummary `json:"channels"`
	Note     string           `json:"note,omitempty"`
}

// sendResult is what send and publish answer with.
type sendResult struct {
	Channel string `json:"channel"`
	Kind    string `json:"kind"`
	// Queued is how many queue rows were written: one for a chat or webhook
	// channel, one per deliverable recipient for an email channel.
	Queued int `json:"queued"`
	// Suppressed reports a keyed send held back by the channel's repeat
	// window: counted, and stated by the next post with the key.
	Suppressed bool `json:"suppressed,omitempty"`
	// Delivered is always false: nothing here waits for the transport. It is
	// stated rather than implied so a caller does not read "queued" as "seen".
	Delivered bool   `json:"delivered"`
	Detail    string `json:"detail"`
	// Link is the URL the message points at, when it carries one.
	Link string `json:"link,omitempty"`
}

// RegisterTool registers notify on the server. A nil handle registers
// nothing, which is how a deployment with no channels advertises no tool.
func (h *Handle) RegisterTool(server *mcp.Server) {
	if h == nil || server == nil {
		return
	}
	toolkit.AddTool(server, &mcp.Tool{
		Name:        ToolName,
		Title:       "Notify a Channel",
		Description: toolDescription,
		InputSchema: inputSchema(),
		// A send leaves a message in somebody's chat client that nothing here
		// can take back, which is a write in every sense that matters. It is
		// not destructive: it removes nothing that was there.
		Annotations: toolkit.WriteAnnotations(false),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input notifyInput) (*mcp.CallToolResult, any, error) {
		return h.handle(ctx, input)
	})
}

// inputSchema is the schema the SDK infers from notifyInput, with the action
// verbs stated as an enum rather than only in prose (#1827): the write
// barrier's verb gate reads the enum to prove each verb is classified, and a
// caller reading the schema learns the set without parsing a sentence. It
// panics on a reflection failure, a programming error covered by tests.
func inputSchema() *jsonschema.Schema {
	s, err := jsonschema.For[notifyInput](nil)
	if err != nil {
		panic("notify: derive the input schema: " + err.Error())
	}
	s.Properties["action"].Enum = []any{actionList, actionSend, actionPublish}
	return s
}

// handle dispatches one call.
func (h *Handle) handle(ctx context.Context, input notifyInput) (*mcp.CallToolResult, any, error) {
	switch strings.TrimSpace(input.Action) {
	case actionList:
		return h.handleList(ctx)
	case actionSend:
		return h.handleSend(ctx, input)
	case actionPublish:
		return h.handlePublish(ctx, input)
	case "":
		return toolkit.ErrorResult("notify: action is required; call action=list to see the channels this session can reach"), nil, nil
	default:
		return toolkit.ErrorResult(fmt.Sprintf("notify: unknown action %q; use list, send or publish", input.Action)), nil, nil
	}
}

// caller is who a send is charged to and authorized as.
type caller struct {
	// email identifies the actor on the queue row and in the audit trail.
	email string
	// userID is the portal identity an entitlement check is made against.
	userID string
	// persona is the name whose connection rules decide which channels are
	// reachable.
	persona string
	// unrestricted lifts the persona boundary for an administrator.
	unrestricted bool
}

// resolveCaller reads the acting identity from the platform context the
// tool-call middleware put there.
//
// A call arriving with no platform context is not treated as an
// administrator: persona stays empty, which the connection scope reads as the
// deny-all persona, so such a caller reaches email channels alone.
func (h *Handle) resolveCaller(ctx context.Context) caller {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return caller{}
	}
	return caller{
		email:        pc.UserEmail,
		userID:       pc.UserID,
		persona:      pc.PersonaName,
		unrestricted: h.cfg.AdminPersona != "" && pc.PersonaName == h.cfg.AdminPersona,
	}
}

// resolveChannel finds the channel a call names and reports why it cannot be
// used, in the words the caller needs.
//
// A channel the caller cannot reach is reported as not existing rather than as
// forbidden. The set of configured channel names is not something an
// unprivileged caller is entitled to enumerate, and two distinguishable
// refusals would enumerate it one guess at a time.
func (h *Handle) resolveChannel(ctx context.Context, name string, c caller) (*notification.Channel, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("channel is required; call action=list to see the channels this session can reach")
	}
	ch, err := h.cfg.Channels.Get(ctx, name)
	if err != nil || !ch.Enabled || !h.reachable(*ch, c.persona, c.unrestricted) {
		return nil, fmt.Errorf("no channel named %q is available to this session", name)
	}
	return ch, nil
}

// enqueue writes the document to the channel and renders the result.
func (h *Handle) enqueue(ctx context.Context, ch notification.Channel, c caller, doc notification.Document, repeatAfter string) (*mcp.CallToolResult, any, error) {
	window, err := parseRepeatAfter(repeatAfter)
	if err != nil {
		return toolkit.ErrorResult("notify: " + err.Error()), nil, nil
	}
	res, err := h.cfg.Enqueuer.NotifyChannel(ctx, notification.ChannelSend{
		Channel: ch, Document: doc, Actor: c.email, RepeatAfter: window,
	})
	if err != nil {
		return toolkit.ErrorResult("notify: " + err.Error()), nil, nil
	}
	result := sendResult{Channel: ch.Name, Kind: ch.Kind, Queued: res.Queued, Suppressed: res.Suppressed, Link: doc.Link}
	switch {
	case res.Suppressed:
		result.Detail = fmt.Sprintf("not posted: a message with key %q went to %q inside the channel's repeat window; "+
			"it was counted, and the next post with this key states how many were held back", doc.Key, ch.Name)
	case res.Queued == 0:
		// Nothing was written and nothing went wrong: every recipient on an
		// email list had opted out, or the actor is over their enqueue rate.
		// Reporting this as a send would be a lie the caller acts on.
		result.Detail = "nothing was queued: either every recipient has opted out of notifications, " +
			"or this session has enqueued too many messages in the last minute"
	default:
		result.Detail = fmt.Sprintf("accepted for delivery to %q; an administrator can see the outcome "+
			"in the platform's notification history", ch.Name)
	}
	return toolkit.JSONResult(result), nil, nil
}

// maxRepeatAfter bounds how long a sender may lengthen a key's window: a week,
// past which a monitor is not repeating so much as asleep.
const maxRepeatAfter = 7 * 24 * time.Hour

// parseRepeatAfter reads the repeat_after parameter, a Go duration string.
func parseRepeatAfter(in string) (time.Duration, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(in)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("repeat_after %q must be a positive duration such as 30m or 6h", in)
	}
	if d > maxRepeatAfter {
		return 0, fmt.Errorf("repeat_after %q is longer than the most a key may be held back, %s", in, maxRepeatAfter)
	}
	return d, nil
}

// handleSend posts a document the caller composed.
func (h *Handle) handleSend(ctx context.Context, input notifyInput) (*mcp.CallToolResult, any, error) {
	c := h.resolveCaller(ctx)
	ch, err := h.resolveChannel(ctx, input.Channel, c)
	if err != nil {
		return toolkit.ErrorResult("notify: " + err.Error()), nil, nil
	}
	doc := notification.Document{
		Title: strings.TrimSpace(input.Title),
		Body:  input.Body,
		Link:  strings.TrimSpace(input.Link),
		Key:   strings.TrimSpace(input.Key),
	}
	if err := h.attach(ctx, &doc, input.Attachments, c); err != nil {
		return toolkit.ErrorResult("notify: " + err.Error()), nil, nil
	}
	if input.Data != nil {
		data, err := wirejson.Marshal(input.Data)
		if err != nil {
			return toolkit.ErrorResult("notify: data must be JSON: " + err.Error()), nil, nil
		}
		doc.Data = data
	}
	stamp(ctx, &doc, notification.DocumentScriptFinding, notification.DocumentNotifySent)
	if err := notification.ValidateDocument(doc); err != nil {
		return toolkit.ErrorResult("notify: " + err.Error()), nil, nil
	}
	return h.enqueue(ctx, *ch, c, doc, input.RepeatAfter)
}

// stamp records what produced a document: a managed script's run, read off
// the context its calls carry, or the person calling. A JSON webhook channel
// delivers both as the envelope's type and source, so a receiver can route a
// script's finding apart from a person's message (#1997).
func stamp(ctx context.Context, doc *notification.Document, scriptType, personType string) {
	pc := middleware.GetPlatformContext(ctx)
	if pc != nil && pc.Source == middleware.SourceScript {
		src := &notification.DocumentSource{Kind: notification.DocumentSourceScript, RunID: pkgsession.AwareSessionID(ctx)}
		if p, ok := producedby.From(ctx); ok && p.Kind == producedby.KindScript {
			src.Script = knowledgepage.ScriptRef(p.ID)
		}
		doc.Type, doc.Source = scriptType, src
		return
	}
	doc.Type, doc.Source = personType, &notification.DocumentSource{Kind: notification.DocumentSourceUser}
}

// handleList answers with the channels this session can reach.
func (h *Handle) handleList(ctx context.Context) (*mcp.CallToolResult, any, error) {
	c := h.resolveCaller(ctx)
	channels, err := h.reachableChannels(ctx, c.persona, c.unrestricted)
	if err != nil {
		return toolkit.ErrorResult("notify: reading the configured channels failed"), nil, nil
	}
	out := listResult{Channels: make([]channelSummary, 0, len(channels))}
	for _, ch := range channels {
		out.Channels = append(out.Channels, channelSummary{
			Name:        ch.Name,
			Kind:        ch.Kind,
			Mode:        ch.Mode,
			Description: ch.Description,
			Carries:     carries(ch.Kind),
		})
	}
	if len(out.Channels) == 0 {
		out.Note = "this session can reach no notification channels; an administrator configures them " +
			"under Admin > Settings > Notification Channels"
	}
	return toolkit.JSONResult(out), nil, nil
}

// carries states what a kind can show.
func carries(kind string) string {
	switch kind {
	case notification.ChannelKindMattermost:
		return "a title, a markdown body, files and a link"
	case notification.ChannelKindWebhook:
		return "a title and a text body; files and the link are appended as links"
	case notification.ChannelKindEmail:
		return "a subject, a rendered markdown body, files and a link button, in the deployment's branded template"
	default:
		return "a title, a body and a link"
	}
}

// AnswerContracts is what the notify tool always answers a send or a publish
// (#1953): the body a managed script's test is held to when it declares the
// answer platform.notify or platform.publish gets.
func AnswerContracts() []toolkit.AnswerContract {
	return []toolkit.AnswerContract{toolkit.ContractFor[sendResult](ToolName, "action", actionSend, actionPublish)}
}
