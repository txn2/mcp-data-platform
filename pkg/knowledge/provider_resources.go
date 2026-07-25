package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// SourceResources is the provenance label for managed-resource hits.
const SourceResources = "resources"

// ResourceSearcher is what the resources provider needs from the managed
// resource store: relevance search over the caller's visible resources (the text
// path) and a by-id read (fetch). The concrete postgres resource store satisfies
// it; declared here so the provider depends on the capability and the platform
// asserts one authority for "a searchable, fetchable resource store".
type ResourceSearcher interface {
	Search(ctx context.Context, q resource.SearchQuery) ([]resource.ScoredResource, error)
	Get(ctx context.Context, id string) (*resource.Resource, error)
}

// ResourceContentReader fetches a resource's bytes from blob storage so fetch
// can return a text resource inline. It is the read half of resource.S3Client,
// the same contract the resources/read middleware uses.
type ResourceContentReader interface {
	GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error)
}

// ResourcesProvider exposes human-uploaded reference material (managed
// resources) to the router (#1012). Visibility is mixed the way prompts are:
// global resources are visible to everyone, persona-scoped resources only to a
// caller carrying that persona, and user-scoped resources only to their owner.
// The provider derives the caller's visible scopes exactly as the MCP
// resources/list middleware does (resource.VisibleScopes over claims built from
// the caller identity) and passes them into the SQL, so a resource the caller
// could not list is never ranked. It is therefore shared (always queried,
// returning at least the global resources) yet fails closed on the
// non-global scopes when the caller carries no identity.
type ResourcesProvider struct {
	searcher ResourceSearcher
	blobs    ResourceContentReader
	bucket   string
}

// NewResourcesProvider builds the resources provider over a resource searcher.
// blobs and bucket locate the file contents fetch returns inline for text
// resources; a nil reader (no S3 connection configured for resources) leaves
// fetch returning metadata plus the canonical URI for every resource.
func NewResourcesProvider(searcher ResourceSearcher, blobs ResourceContentReader, bucket string) *ResourcesProvider {
	return &ResourcesProvider{searcher: searcher, blobs: blobs, bucket: bucket}
}

// Name returns the provenance label.
func (*ResourcesProvider) Name() string { return SourceResources }

// Scope marks resources shared (always queried); the visible-scope set derived
// from the caller identity self-filters persona and user-scoped material.
func (*ResourcesProvider) Scope() Scope { return ScopeShared }

// Search returns the resources visible to the caller, ranked by relevance to the
// intent. It responds to the text path only; a query with no intent yields
// nothing.
func (p *ResourcesProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}

	scored, err := p.searcher.Search(ctx, resource.SearchQuery{
		Embedding: q.Embedding,
		QueryText: q.Intent,
		Scopes:    resource.VisibleScopes(callerClaims(q.Caller)),
		Limit:     q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("resource search: %w", err)
	}

	hits := make([]Hit, 0, len(scored))
	for i := range scored {
		r := scored[i].Resource
		hits = append(hits, Hit{
			Text:      resourceHitText(r),
			Source:    SourceResources,
			Ref:       r.ID,
			Score:     scored[i].Score,
			Reference: knowledgepage.ResourceRef(r.ID),
			Link: &HitLink{
				URI:         r.URI,
				Name:        r.DisplayName,
				Description: r.Description,
				MIMEType:    r.MIMEType,
			},
		})
	}
	return hits, nil
}

// Fetch dereferences an mcp:resource:<id> reference to the resource's full
// metadata, plus its content inline when the resource is text at or under the
// shared inline threshold (resource.MaxInlineContentBytes, the same threshold
// the resources/read middleware applies). A binary or oversized resource returns
// metadata alone: the record carries the canonical mcp:// URI, MIME type, and
// size, which is what the agent needs to decide how to read it.
//
// One deliberate difference from resources/read: this path checks the RECORDED
// size before fetching, so an oversized object is never pulled into memory just
// to be discarded, whereas the middleware is already holding the bytes when it
// decides. The two agree for every resource whose recorded size matches its
// object, which is all of them — size_bytes is written from the uploaded bytes
// and content is immutable.
//
// It owns only the resource reference form; any other reference is declined
// (owned=false). The read re-applies the same visibility rule Search enforces
// (resource.CanReadResource), so a resource outside the caller's scopes and one
// that never existed are both a clean ErrNotFound: fetch reveals neither the
// content nor the existence of material the caller could not have searched.
func (p *ResourcesProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetResource {
		// Not a resource reference: decline so the Router tries the next provider.
		return nil, false, nil //nolint:nilerr // a non-resource reference is a decline, not a failure
	}

	res, err := p.searcher.Get(ctx, parsed.ResourceID)
	if err != nil {
		// The store reports a missing row as a wrapped sql.ErrNoRows; a stale or
		// deleted citation must be a clean not-found, not a hard failure.
		if resource.IsNotFound(err) {
			return nil, true, ErrNotFound
		}
		return nil, true, fmt.Errorf("getting resource %s: %w", parsed.ResourceID, err)
	}
	if res == nil || !resource.CanReadResource(callerClaims(caller), res) {
		return nil, true, ErrNotFound
	}

	return &Document{
		Reference: ref,
		Source:    SourceResources,
		Title:     res.DisplayName,
		Body:      p.inlineContent(ctx, res),
		Content:   res,
	}, true, nil
}

// inlineContent returns the resource's content as text when it is a text-family
// resource at or under the inline threshold, and "" otherwise. A blob read
// failure is logged and degrades to metadata-only rather than failing the fetch:
// the caller still learns what the resource is and where to read it.
func (p *ResourcesProvider) inlineContent(ctx context.Context, res *resource.Resource) string {
	if p.blobs == nil || res.S3Key == "" || !contenttype.IsTextual(res.MIMEType) {
		return ""
	}
	if res.SizeBytes > resource.MaxInlineContentBytes {
		return ""
	}
	body, _, err := p.blobs.GetObject(ctx, p.bucket, res.S3Key)
	if err != nil {
		slog.Warn("resource fetch: content read failed; returning metadata only",
			"resource_id", res.ID, "error", err) //nolint:gosec // structured slog of a store error
		return ""
	}
	// The recorded size can disagree with the object (a re-upload outside the
	// handler), so bound on what was actually read as well.
	if int64(len(body)) > resource.MaxInlineContentBytes {
		return ""
	}
	return string(body)
}

// callerClaims maps a search caller onto the resource permission claims, so the
// provider derives visibility through resource.VisibleScopes / CanReadResource
// exactly as the resources REST and MCP surfaces do rather than reimplementing
// the scope rule.
//
// The persona set comes from Caller.Personas (membership derived from roles) and
// ONLY from there. Caller.Persona — the persona the request resolved to — is
// deliberately not used, not even as a fallback: resolution substitutes the
// configured default_persona when a caller's roles match none, so falling back
// to it would hand every unmatched caller the default persona's material, which
// resources/list and resources/read refuse them. An empty set therefore means
// "belongs to no persona", and the caller sees only global and their own
// user-scoped material — the fail-closed answer, and also what a caller gets if
// a deployment never binds the resolver (see Toolkit.SetPersonasForRoles).
//
// Roles are not carried on a search caller, so the claims grant no persona-admin
// or platform-admin authority: visibility here is the caller's own global +
// persona + user scopes, matching what resources/list returns.
func callerClaims(c Caller) resource.Claims {
	claims := resource.BuildClaims(c.UserID, c.Email, "", nil, false)
	claims.Personas = c.Personas
	return claims
}

// resourceHitText renders a resource as a knowledge snippet: its display name,
// its description when present, and its filename, so a hit conveys what the
// material is (and what kind of file it is) without a follow-up fetch.
func resourceHitText(r resource.Resource) string {
	parts := make([]string, 0, 3)
	parts = append(parts, r.DisplayName)
	if r.Description != "" {
		parts = append(parts, r.Description)
	}
	if r.Filename != "" {
		parts = append(parts, r.Filename)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
