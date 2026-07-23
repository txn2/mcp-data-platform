// Package attachserve resolves a prompt's attached resources (#1013) into the
// form each serving surface needs: MCP content items appended to a
// prompts/get result, and a JSON summary carried by manage_prompt use.
//
// It exists as its own package because it is the one place that needs both
// pkg/prompt (the attachment links) and pkg/resource (the material, its
// permissions, and its blobs), and both the MCP prompt layer and the REST
// attachment handler consume it. Putting it in either of those packages would
// point a dependency edge the wrong way.
//
// The resolver never fails a prompt. A prompt whose attachment is unreadable,
// deleted, or unfetchable still serves: the material is reported as
// unavailable, missing, or unreadable, and the prompt text is unaffected. A
// procedure that has lost its template is still a procedure, and telling the
// agent the template is gone is strictly better than refusing to answer.
package attachserve

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Availability describes whether a prompt's attached material reached the
// caller, and if not, why.
type Availability string

const (
	// AvailableEmbedded means the full contents are inline in the result.
	AvailableEmbedded Availability = "embedded"
	// AvailableLinked means the caller received a resource link and must read
	// the resource to get its contents (too large to inline, or binary).
	AvailableLinked Availability = "linked"
	// UnavailableForbidden means the attachment exists but this caller cannot
	// read it. Nothing identifying is disclosed alongside it, not even the
	// resource id: the caller has no repair action, and an id would be an
	// existence probe.
	UnavailableForbidden Availability = "unavailable"
	// UnavailableMissing means the resource was deleted after being attached.
	UnavailableMissing Availability = "missing"
	// UnavailableUnreadable means the resource could not be read. When its
	// metadata resolved and only the blob fetch failed, the link is served in
	// place of the contents; when the metadata read itself failed, nothing is
	// known about it and it is counted among the undelivered materials.
	UnavailableUnreadable Availability = "unreadable"
)

// fieldResourceID is the JSON key naming an attachment's resource in the
// use-response summary.
const fieldResourceID = "resource_id"

// DefaultInlineLimit caps how many bytes of a text attachment are embedded
// directly in a served prompt. Above it the attachment becomes a resource link
// the client can read on demand.
//
// 64 KiB is chosen to match the largest reference material that is still
// plausibly worth spending prompt context on unconditionally; a report template
// or checklist is far below it, a full data extract is far above.
const DefaultInlineLimit = 64 * 1024

// Resolved is one attachment after lookup, permission check, and (when the
// material is inlined) blob read.
type Resolved struct {
	// ResourceID is always set, even when the resource is missing, because it
	// is what an author needs in order to repair a broken link.
	ResourceID string
	// Availability records the outcome. Every other field except ResourceID is
	// empty when it is UnavailableForbidden or UnavailableMissing: an
	// attachment the caller cannot read must not disclose its name, its
	// description, or its size.
	Availability Availability

	URI         string
	DisplayName string
	Description string
	MIMEType    string
	SizeBytes   int64

	// Text holds the contents when Availability is AvailableEmbedded.
	Text string
}

// BlobReader reads a resource's stored bytes. Satisfied by resource.S3Client.
type BlobReader interface {
	GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error)
}

// Deps carries the collaborators the resolver needs. Attachments and Resources
// are required; a nil Blobs (or empty Bucket) degrades every text attachment to
// a link rather than failing, which is the correct behavior for a
// database-only deployment with no blob backend.
type Deps struct {
	Attachments prompt.AttachmentStore
	Resources   resource.Store
	Blobs       BlobReader
	Bucket      string

	// InlineLimit overrides DefaultInlineLimit when positive.
	InlineLimit int
}

// Resolver resolves prompt attachments for a caller.
type Resolver struct {
	deps Deps
}

// New builds a resolver. A nil result is a valid zero-attachment resolver, so
// callers need no nil checks at every serving site.
func New(deps Deps) *Resolver {
	if deps.Attachments == nil || deps.Resources == nil {
		return nil
	}
	return &Resolver{deps: deps}
}

// inlineLimit returns the configured or default inline threshold.
func (r *Resolver) inlineLimit() int {
	if r.deps.InlineLimit > 0 {
		return r.deps.InlineLimit
	}
	return DefaultInlineLimit
}

// Resolve returns a prompt's attachments in authored order, each evaluated for
// this caller. It returns nil for a prompt with no attachments, and nil rather
// than an error when the attachment store read fails: a store outage must not
// take down prompt serving.
func (r *Resolver) Resolve(ctx context.Context, promptID string, claims resource.Claims) []Resolved {
	if r == nil || promptID == "" {
		return nil
	}
	links, err := r.deps.Attachments.ListByPrompt(ctx, promptID)
	if err != nil {
		slog.Warn("prompt attachments: list failed; serving prompt without materials",
			"prompt_id", promptID, "error", err)
		return nil
	}
	if len(links) == 0 {
		return nil
	}
	out := make([]Resolved, 0, len(links))
	for _, link := range links {
		out = append(out, r.resolveOne(ctx, link, claims))
	}
	return out
}

// resolveOne evaluates a single link for the caller.
func (r *Resolver) resolveOne(ctx context.Context, link prompt.Attachment, claims resource.Claims) Resolved {
	res, err := r.deps.Resources.Get(ctx, link.ResourceID)
	switch {
	case resource.IsNotFound(err) || (err == nil && res == nil):
		// A deleted resource is the expected case here, not an anomaly: the
		// attachment row deliberately outlives the resource so the broken link
		// stays visible.
		return Resolved{ResourceID: link.ResourceID, Availability: UnavailableMissing}
	case err != nil:
		// A failed read is not a deleted resource. Reporting it as missing
		// would tell the agent the material is gone when it may be fine, so it
		// is reported as unreadable instead.
		slog.Warn("prompt attachments: resource read failed",
			"resource_id", link.ResourceID, "error", err)
		return Resolved{ResourceID: link.ResourceID, Availability: UnavailableUnreadable}
	}
	if !resource.CanReadResource(claims, res) {
		// Report only that something is attached and out of reach. Returning
		// the name or description here would make an attachment a channel for
		// reading metadata the caller has no access to.
		return Resolved{ResourceID: link.ResourceID, Availability: UnavailableForbidden}
	}

	out := Resolved{
		ResourceID:   res.ID,
		URI:          res.URI,
		DisplayName:  displayName(res),
		Description:  res.Description,
		MIMEType:     res.MIMEType,
		SizeBytes:    res.SizeBytes,
		Availability: AvailableLinked,
	}
	if !r.shouldInline(res) {
		return out
	}
	body, _, err := r.deps.Blobs.GetObject(ctx, r.deps.Bucket, res.S3Key)
	if err != nil {
		slog.Warn("prompt attachments: blob read failed; serving link instead",
			"resource_id", res.ID, "error", err)
		out.Availability = UnavailableUnreadable
		return out
	}
	out.Text = string(body)
	out.Availability = AvailableEmbedded
	return out
}

// shouldInline reports whether a readable resource's contents belong inline.
// Binary material is always a link (it would have to be base64-expanded into
// the prompt), and so is anything above the inline limit.
func (r *Resolver) shouldInline(res *resource.Resource) bool {
	if r.deps.Blobs == nil || r.deps.Bucket == "" || res.S3Key == "" {
		return false
	}
	if !contenttype.IsTextual(res.MIMEType) {
		return false
	}
	return res.SizeBytes > 0 && res.SizeBytes <= int64(r.inlineLimit())
}

// displayName falls back to the stored filename so a resource saved without a
// display name still names itself in the served material.
func displayName(res *resource.Resource) string {
	if res.DisplayName != "" {
		return res.DisplayName
	}
	return res.Filename
}

// Scopes returns the scope of every resource a prompt attaches, for the
// authoring-time rule in prompt.CheckAttachScope. It is deliberately
// caller-independent: the rule asks how widely a resource is visible, not
// whether one particular reader can see it.
//
// A resource that no longer exists is skipped rather than reported: a broken
// link cannot violate a scope rule, and blocking a promotion on it would freeze
// the prompt against every edit until someone detached a link the portal
// already flags. Any other read failure does block, because an unknown scope is
// not a safe scope.
func (r *Resolver) Scopes(ctx context.Context, promptID string) ([]prompt.AttachmentScope, error) {
	if r == nil || promptID == "" {
		return nil, nil
	}
	links, err := r.deps.Attachments.ListByPrompt(ctx, promptID)
	if err != nil {
		return nil, fmt.Errorf("reading prompt attachments: %w", err)
	}
	out := make([]prompt.AttachmentScope, 0, len(links))
	for _, link := range links {
		res, getErr := r.deps.Resources.Get(ctx, link.ResourceID)
		if resource.IsNotFound(getErr) || (getErr == nil && res == nil) {
			continue
		}
		if getErr != nil {
			return nil, fmt.Errorf("reading attached resource %s: %w", link.ResourceID, getErr)
		}
		out = append(out, ScopeOf(res))
	}
	return out, nil
}

// ScopeOf projects a resource onto the fields the scope rule needs.
func ScopeOf(res *resource.Resource) prompt.AttachmentScope {
	return prompt.AttachmentScope{
		ResourceID:  res.ID,
		DisplayName: displayName(res),
		Scope:       string(res.Scope),
		ScopeID:     res.ScopeID,
	}
}

// CheckPromotion reports whether a prompt's current attachments would still
// satisfy the scope rule at the target scope. It is the gate the promotion
// request and approval paths call, and it fails closed: a store error blocks
// the promotion rather than letting it through unchecked.
func (r *Resolver) CheckPromotion(ctx context.Context, promptID, targetScope string, targetPersonas []string) error {
	scopes, err := r.Scopes(ctx, promptID)
	if err != nil {
		return err
	}
	// Unwrapped by design: the rule's error is written for the author and is
	// surfaced verbatim by every caller.
	return prompt.CheckPromotionAttachments(targetScope, targetPersonas, scopes) //nolint:wrapcheck // caller-facing message, deliberately verbatim
}

// Content renders resolved attachments as MCP prompt-message content: embedded
// resources carry the full text, linked resources carry a resource_link the
// client can read, and anything the caller cannot reach is summarized in a
// single trailing note.
//
// The framing text is deliberately directive. An agent that receives a template
// alongside a procedure must fill that template rather than invent its own
// formatting, and the only place to say so is here, next to the material.
func Content(items []Resolved) []mcp.Content {
	if len(items) == 0 {
		return nil
	}
	var out []mcp.Content
	var withheld []Resolved

	for _, it := range items {
		switch it.Availability {
		case AvailableEmbedded:
			out = append(out, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
				URI:      it.URI,
				MIMEType: it.MIMEType,
				Text:     it.Text,
			}})
		case AvailableLinked, UnavailableUnreadable:
			// A resource whose own metadata read failed has no URI to link to;
			// it joins the undelivered count instead of emitting an empty link.
			if it.URI == "" {
				withheld = append(withheld, it)
				continue
			}
			size := it.SizeBytes
			out = append(out, &mcp.ResourceLink{
				URI:         it.URI,
				Name:        it.DisplayName,
				Title:       it.DisplayName,
				Description: it.Description,
				MIMEType:    it.MIMEType,
				Size:        &size,
			})
		case UnavailableForbidden, UnavailableMissing:
			withheld = append(withheld, it)
		}
	}
	if len(out) > 0 {
		out = append([]mcp.Content{&mcp.TextContent{Text: framingText(len(out))}}, out...)
	}
	if note := withheldNote(withheld); note != "" {
		out = append(out, &mcp.TextContent{Text: note})
	}
	return out
}

// framingText introduces the attached material and states its authority. The
// agent reads this sentence, so it agrees in number rather than reading as
// generated text.
func framingText(n int) string {
	subject := fmt.Sprintf("The following %d attached materials accompany", n)
	if n == 1 {
		subject = "The following attached material accompanies"
	}
	return subject + " this prompt and is authoritative. " +
		"When a template is attached, fill it rather than inventing your own format; when a checklist " +
		"is attached, follow it. Resource links must be read before use."
}

// withheldNote summarizes attachments the caller did not receive, counting them
// by reason without naming them. The count alone is what the agent needs: it
// tells the agent its materials are incomplete so it can say so, without
// turning the note into a metadata side channel.
func withheldNote(withheld []Resolved) string {
	if len(withheld) == 0 {
		return ""
	}
	counts := map[Availability]int{}
	for _, it := range withheld {
		counts[it.Availability]++
	}
	var parts []string
	if n := counts[UnavailableForbidden]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d you are not permitted to read", n))
	}
	if n := counts[UnavailableMissing]; n > 0 {
		verb := "exist"
		if n == 1 {
			verb = "exists"
		}
		parts = append(parts, fmt.Sprintf("%d no longer %s", n, verb))
	}
	if n := counts[UnavailableUnreadable]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d could not be read", n))
	}
	subject := fmt.Sprintf("%d attached materials were not delivered", len(withheld))
	if len(withheld) == 1 {
		subject = "1 attached material was not delivered"
	}
	return fmt.Sprintf("Note: %s (%s). "+
		"Proceed, and state that your reference material was incomplete.",
		subject, joinAnd(parts))
}

// joinAnd renders a short list as prose.
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return parts[0] + " and " + joinAnd(parts[1:])
	}
}

// Summary renders resolved attachments for the manage_prompt use provenance
// block, so an agent can state exactly what materials it received. Unavailable
// entries carry their reason and nothing else.
func Summary(items []Resolved) []map[string]any {
	if len(items) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		entry := map[string]any{
			fieldResourceID: it.ResourceID,
			"availability":  string(it.Availability),
		}
		switch {
		case it.Availability == UnavailableForbidden:
			// A caller who may not read the resource learns only that something
			// is attached and out of reach. Even the id is withheld: they have
			// no repair action, and it would be a probe for existence.
			delete(entry, fieldResourceID)
		case it.Availability == UnavailableMissing, it.URI == "":
		default:
			entry["uri"] = it.URI
			entry["display_name"] = it.DisplayName
			entry["description"] = it.Description
			entry["mime_type"] = it.MIMEType
			entry["size_bytes"] = it.SizeBytes
			if it.Availability == AvailableEmbedded {
				entry["content"] = it.Text
			}
		}
		out = append(out, entry)
	}
	return out
}
