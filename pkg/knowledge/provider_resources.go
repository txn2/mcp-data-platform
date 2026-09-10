package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/docread"
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
	reads    resource.ReadRecorder
	tables   TableLookup
	docs     *docread.Reader
}

// NewResourcesProvider builds the resources provider over a resource searcher.
// blobs and bucket locate the file contents fetch returns for a resource; a nil
// reader (no S3 connection configured for resources) leaves fetch returning
// metadata plus the canonical URI for every resource.
//
// docs renders a file into what a reader can use; a nil one falls back to a
// reader with no PDF extractor bound, which serves a PDF as its bytes.
func NewResourcesProvider(searcher ResourceSearcher, blobs ResourceContentReader, bucket string, docs *docread.Reader) *ResourcesProvider {
	if docs == nil {
		docs = docread.New(nil)
	}
	return &ResourcesProvider{searcher: searcher, blobs: blobs, bucket: bucket, docs: docs}
}

// SetReadRecorder binds the recorder that audits resources dereferenced through
// fetch (#1014). Called after construction because audit wiring is resolved
// later than search federation; a provider with no recorder serves the same
// content and records nothing, which is what a deployment with audit disabled
// gets. Search is deliberately not recorded: a ranked hit is not a read.
func (p *ResourcesProvider) SetReadRecorder(rec resource.ReadRecorder) {
	p.reads = rec
}

// SetTableLookup binds the lookup that tells a hit whether the resource behind
// it is readable as a query-engine table (#1327). Called after construction for
// the same reason the read recorder is: registration wiring resolves later than
// search federation. A provider with no lookup serves the hits it always did.
func (p *ResourcesProvider) SetTableLookup(lookup TableLookup) {
	p.tables = lookup
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
	byID := make(map[string]resource.Resource, len(scored))
	for i := range scored {
		r := scored[i].Resource
		byID[r.ID] = r
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
	attachTables(ctx, p.tables, hits, func(h Hit) (TableSubject, bool) {
		r, ok := byID[h.Ref]
		if !ok {
			return TableSubject{}, false
		}
		return TableSubject{
			Kind: TableKindResource, ID: r.ID, Bucket: p.bucket, HeadKey: r.S3Key,
		}, true
	})
	return hits, nil
}

// Fetch dereferences an mcp:resource:<id> reference to the resource's full
// metadata plus its content, in whichever form the file admits
// (internal/docread): text for a textual file, a PDF and a zip-container
// document; the picture itself for an image; and the bytes as they are for a
// family with no reader. Only a file above the shared inline threshold
// (resource.MaxInlineContentBytes, the same threshold the resources/read
// middleware applies) comes back as metadata alone, with the canonical mcp://
// URI to read it by.
//
// Until #1657 every non-textual file came back as metadata alone, and an agent
// handed a PDF's size and MIME type concluded the platform was refusing it the
// file. Metadata is now what a caller gets when the file is too large to carry,
// and nothing else.
//
// One deliberate difference from resources/read: this path checks the RECORDED
// size before fetching, so an oversized object is never pulled into memory just
// to be discarded, whereas the middleware is already holding the bytes when it
// decides. The two agree for every resource whose recorded size matches its
// object, which is all of them — size_bytes is written from the uploaded bytes
// and content is immutable.
//
// It owns only the resource reference form; any other reference is declined
// (owned=false). A resource the caller cannot reach and one that never existed
// are both a clean ErrNotFound, so fetch reveals neither the content nor the
// existence of material the caller has no way to.
//
// The rule is resource.CanAccessResource, which is what the resources REST
// routes resolve a file named by id through and what an asset reference is
// declared under (#1584). Search is narrower on purpose and stays that way: it
// runs over resource.VisibleScopes, which is library membership, because a
// listing hands the caller material they did not name. Fetch is the opposite
// act -- the caller states the reference -- and answering it on the listing
// rule told a platform administrator that a file whose bytes the same session
// could GET and replace did not exist.
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
	if res == nil || !resource.CanAccessResource(callerClaims(caller), res) {
		return nil, true, ErrNotFound
	}

	doc := &Document{
		Reference: ref,
		Source:    SourceResources,
		Title:     res.DisplayName,
		Content:   res,
		Tables: lookupTables(ctx, p.tables, TableSubject{
			Kind: TableKindResource, ID: res.ID, Bucket: p.bucket, HeadKey: res.S3Key,
		}),
	}
	doc.Body, doc.Attachment, doc.Note = p.content(ctx, res)
	p.recordRead(ctx, res, caller)
	return doc, true, nil
}

// recordRead reports a dereferenced resource to the bound recorder. It fires
// for every successful fetch, including one that returned metadata alone: the
// caller pulled this material into their session either way, which is the
// question the read trail answers. No-op without a recorder.
func (p *ResourcesProvider) recordRead(ctx context.Context, res *resource.Resource, caller Caller) {
	if p.reads == nil {
		return
	}
	p.reads.RecordRead(ctx, resource.ReadEvent{
		ResourceID: res.ID,
		URI:        res.URI,
		Surface:    resource.SurfaceFetch,
		UserID:     caller.UserID,
		UserEmail:  caller.Email,
		Persona:    caller.Persona,
	})
}

// content returns the resource's content in the form its family admits -- text
// in the document body, or the bytes themselves as an attachment the fetch
// surface turns into an MCP content block -- plus one line about the rendering
// for the reader of the document.
//
// A file with no blob storage behind it, one above the inline threshold, or a
// read that failed yields neither, and the note says which. A blob read failure
// is logged and degrades to metadata rather than failing the fetch: the caller
// still learns what the resource is and where to read it.
func (p *ResourcesProvider) content(ctx context.Context, res *resource.Resource) (text string, attached *Attachment, note string) {
	if p.blobs == nil || res.S3Key == "" {
		return "", nil, ""
	}
	if res.SizeBytes > resource.MaxInlineContentBytes {
		return "", nil, tooLargeNote(res)
	}
	body, _, err := p.blobs.GetObject(ctx, p.bucket, res.S3Key)
	if err != nil {
		slog.Warn("resource fetch: content read failed; returning metadata only",
			"resource_id", res.ID, "error", err) //nolint:gosec // structured slog of a store error
		return "", nil, "This file's content could not be read just now. Its record is below; " +
			readByURI(res)
	}
	// The recorded size can disagree with the object (a re-upload outside the
	// handler), so bound on what was actually read as well.
	if int64(len(body)) > resource.MaxInlineContentBytes {
		return "", nil, tooLargeNote(res)
	}

	read := p.docs.Read(ctx, res.MIMEType, res.Filename, body, resource.MaxInlineContentBytes)
	if read.Form == docread.FormText {
		return read.Text, nil, textNote(read, res)
	}
	return "", &Attachment{
		URI:      res.URI,
		MIMEType: res.MIMEType,
		Bytes:    body,
		Image:    read.Form == docread.FormImage,
	}, attachedNote(read)
}

// tooLargeNote explains the one case that still answers with metadata alone,
// and names the door that has no size limit.
func tooLargeNote(res *resource.Resource) string {
	return fmt.Sprintf("This file is %d bytes, above the %d-byte limit on content carried in a fetch. %s",
		res.SizeBytes, resource.MaxInlineContentBytes, readByURI(res))
}

// readByURI names the URI and the method that reads a whole file.
func readByURI(res *resource.Resource) string {
	return fmt.Sprintf("Read it with the MCP resources/read method at %s, which has no such limit.", res.URI)
}

// textNote reports a body that is a prefix rather than the whole file, so a
// reader never mistakes a bounded answer for a complete one.
func textNote(read docread.Result, res *resource.Resource) string {
	if !read.Truncated {
		return read.Note
	}
	note := "Only the first part of this file is shown here. " + readByURI(res)
	if read.Note != "" {
		return read.Note + " " + note
	}
	return note
}

// attachedNote says where the file went, since the document's body is empty
// and a reader given no explanation concludes the content was withheld --
// which is the report #1657 was filed as.
func attachedNote(read docread.Result) string {
	where := "The file is attached to this result as an embedded resource; open it with your own tools."
	if read.Form == docread.FormImage {
		where = "The picture is attached to this result as an image; look at it directly."
	}
	if read.Note != "" {
		return read.Note + ". " + where
	}
	return where
}

// callerClaims maps a search caller onto the resource permission claims, so the
// provider derives visibility through resource.VisibleScopes and
// resource.CanAccessResource exactly as the resources REST and MCP surfaces do
// rather than reimplementing the scope rule.
//
// The persona set comes from Caller.Personas (membership derived from roles) and
// ONLY from there. Caller.Persona — the persona the request resolved to — is
// deliberately not used, not even as a fallback: it can be set explicitly on a
// request, so falling back to it would grant material on the strength of the
// persona a request claims to act as rather than one the caller belongs to. An
// empty set therefore means "belongs to no persona", and the caller sees only
// global and their own user-scoped material — the fail-closed answer, and also
// what a caller gets if a deployment never binds the resolver (see
// Toolkit.SetPersonasForRoles).
//
// Roles and IsAdmin travel too (#1584), which is what lets Fetch answer a file
// the caller NAMED on resource.CanAccessResource. They change nothing about
// enumeration: Search runs on resource.VisibleScopes, which is global + the
// caller's own user scope + the personas they belong to, and consults neither
// field. An administrator's search still returns exactly what resources/list
// returns for them.
// An unattended caller's address travels too, so a run finds the material its
// author can see and the file it wrote itself -- which is filed under the person
// it acts for, not under the principal (#1487). It is inert for a human.
func callerClaims(c Caller) resource.Claims {
	claims := resource.BuildClaims(c.UserID, c.Email, "", c.Roles, c.IsAdmin).ActingFor(c.OnBehalfOf)
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
