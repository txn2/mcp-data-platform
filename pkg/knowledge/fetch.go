package knowledge

import (
	"context"
	"errors"
	"strings"
)

// ErrNotFound is the fetch contract's sentinel for a reference that resolves to
// no content: no provider owns it, it never existed, it was deleted, or it names
// content the caller may not read. The Router returns it (wrapped) from Fetch, and
// the fetch surface maps it to a structured not-found rather than an error, so a
// stale citation is a normal answer ("that reference is gone"), not a failure
// (#694). A Fetcher signals the same condition for a reference it owns by
// returning ErrNotFound; the Router passes it through unchanged.
var ErrNotFound = errors.New("reference not found")

// Document is the full, dereferenced content of a reference that search emitted on
// a Hit. It is the consumer of the Reference field that every searchable source
// already produces: search returns navigational pointers with truncated snippets,
// and fetch turns one pointer back into the whole record.
//
// A source fills the shape that fits it: text-bodied sources (knowledge pages,
// context documents, prompts) put their full text in Body, while structured
// sources (a catalog dataset's context, a managed asset's metadata, a connection's
// descriptor) put their native payload in Content. Reference echoes the resolved
// reference and Source is the provider provenance, so a caller can cite the result
// the same way search let it cite the pointer.
type Document struct {
	// Reference is the canonical citation that was dereferenced (echoed back so a
	// caller can re-cite or cache by it).
	Reference string `json:"reference"`
	// Source is the provider provenance label (the same Source a search Hit carries).
	Source string `json:"source"`
	// Title is a short human label for the content (page title, document title,
	// dataset name, asset name, prompt display name, connection name).
	Title string `json:"title,omitempty"`
	// Body is the full textual content for text-bodied sources (knowledge page
	// markdown, context-document body, prompt content). Empty for purely structured
	// sources (a dataset, an asset, a connection), which carry their payload in
	// Content instead.
	Body string `json:"body,omitempty"`
	// Content is the source-native full payload for sources whose record is
	// structured (a dataset's TableContext, an asset's metadata, a connection
	// descriptor, the full prompt record). A source may populate both Body and
	// Content when it has a primary text body and structured metadata around it (a
	// prompt fills Body with its text and Content with the whole record); a purely
	// text-bodied source (knowledge page, context document) leaves Content nil.
	Content any `json:"content,omitempty"`
	// EntityURNs are the catalog entities this content is about, when the source
	// links any (a context document's related assets, a dataset's own URN).
	EntityURNs []string `json:"entity_urns,omitempty"`
}

// Fetcher is the optional capability of a Provider to dereference a reference (the
// canonical citation string search emits on a Hit) to its full content. A
// provider that can search a store can usually also read one record from it by id;
// implementing Fetcher exposes that as the other half of search. A provider that
// does not implement Fetcher is search-only, and the Router skips it when fetching.
//
// The Router asks each scope-permitted Fetcher in turn whether it owns a reference
// and returns the first owner's content, so ownership across providers is
// partitioned by reference form (mcp:knowledge_page:, urn:li:dataset:, ...) and
// never ambiguous.
type Fetcher interface {
	// Fetch dereferences ref to its full content for caller.
	//
	// owned reports whether ref is a form this provider recognizes. When false the
	// Router moves on to the next provider and doc/err are ignored, so a provider
	// must cheaply decline references that are not its own rather than erroring.
	//
	// When owned is true: a nil err returns doc; ErrNotFound means the reference is
	// well-formed and this provider's, but resolves to nothing the caller may read
	// (deleted, never existed, or out of the caller's scope) and is rendered as a
	// structured not-found; any other err is a real fetch failure (store/transport).
	//
	// A per-user provider must scope the read to caller exactly as its Search does:
	// a reference the caller could not have searched must return ErrNotFound, not
	// content, so fetch never widens what a persona can see.
	Fetch(ctx context.Context, ref string, caller Caller) (doc *Document, owned bool, err error)
}

// Fetch dereferences a reference to its full content. It asks each scope-permitted
// Fetcher provider, in registration order, whether it owns the reference and
// returns the first owner's content.
//
// Scope mirrors Search exactly: a per-user provider is consulted only when the
// caller carries an identity (an anonymous caller is never offered per-user
// content), and each provider re-applies its own ownership filter, so fetch can
// never read content the same caller could not have searched. A reference no
// provider owns, an empty reference, or one an owner cannot resolve all return
// ErrNotFound, which the caller renders as a structured not-found.
//
// Unlike Search this does not fan out concurrently: ownership is partitioned by
// reference form, so exactly one provider does real work and the rest decline in a
// cheap prefix check; a serial walk is both correct and the lower-overhead choice.
func (r *Router) Fetch(ctx context.Context, ref string, caller Caller) (*Document, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrNotFound
	}
	for _, p := range r.providers {
		f, ok := p.(Fetcher)
		if !ok {
			continue
		}
		// The per-user scope gate is the same one selectProviders applies to search:
		// an anonymous caller is never offered a per-user provider, so it can neither
		// search nor fetch that content.
		if p.Scope() == ScopePerUser && caller.Anonymous() {
			continue
		}
		doc, owned, err := f.Fetch(ctx, ref, caller)
		if !owned {
			continue
		}
		if err != nil {
			// Pass the owner's error through unchanged: it is either ErrNotFound (which
			// the caller matches with errors.Is and renders as a structured not-found) or
			// a provider error the provider already contextualized; wrapping here would
			// add a redundant layer and obscure the sentinel.
			return nil, err //nolint:wrapcheck // deliberate passthrough; preserves the ErrNotFound sentinel
		}
		return doc, nil
	}
	return nil, ErrNotFound
}
