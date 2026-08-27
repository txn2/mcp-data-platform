package resource_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachserve"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// This file proves the property the whole feature rests on: refiling a resource
// changes where it lives and what it is called, and changes nothing about what
// already points at it. Both halves are exercised through the real assembled
// surfaces -- the resource PATCH route, the reference-serving route, and the
// prompt attachment resolver -- over one store, rather than by asserting that a
// store method was called.

const (
	testBucket   = "managed-resources"
	testAssetID  = "ast-1"
	testRefToken = "tok-1"
	testPromptID = "prm-1"
	fileBytes    = "the quarterly report template"
)

// --- an in-memory resource store ---

// memResources implements resource.Store over maps, including the alias
// behavior a move depends on: the address a resource vacates keeps resolving,
// and a live address always wins over one.
type memResources struct {
	rows    map[string]*resource.Resource
	aliases map[string]string
}

func newMemResources() *memResources {
	return &memResources{rows: map[string]*resource.Resource{}, aliases: map[string]string{}}
}

var (
	errNoRow = fmt.Errorf("scanning resource: %w", sql.ErrNoRows)
	// errDuplicateURI is the shape CreateResource sniffs for to report a
	// duplicate as a conflict rather than as a failure.
	errDuplicateURI = errors.New("duplicate key value violates unique constraint")
	errNoObject     = errors.New("no such key")
)

func (m *memResources) Insert(_ context.Context, r resource.Resource) error {
	for _, existing := range m.rows {
		if existing.URI == r.URI {
			return errDuplicateURI
		}
	}
	m.rows[r.ID] = &r
	return nil
}

func (m *memResources) Get(_ context.Context, id string) (*resource.Resource, error) {
	r, ok := m.rows[id]
	if !ok {
		return nil, errNoRow
	}
	copied := *r
	return &copied, nil
}

func (m *memResources) GetByIDs(_ context.Context, ids []string) (map[string]*resource.Resource, error) {
	out := map[string]*resource.Resource{}
	for _, id := range ids {
		if r, ok := m.rows[id]; ok {
			copied := *r
			out[id] = &copied
		}
	}
	return out, nil
}

func (m *memResources) GetByURI(ctx context.Context, uri string) (*resource.Resource, error) {
	for id, r := range m.rows {
		if r.URI == uri {
			return m.Get(ctx, id)
		}
	}
	if id, ok := m.aliases[uri]; ok {
		return m.Get(ctx, id)
	}
	return nil, errNoRow
}

func (*memResources) List(_ context.Context, _ resource.Filter) ([]resource.Resource, int, error) {
	return nil, 0, nil
}

func (m *memResources) Update(_ context.Context, id string, u resource.Update) error {
	r, ok := m.rows[id]
	if !ok {
		return errNoRow
	}
	if u.DisplayName != nil {
		r.DisplayName = *u.DisplayName
	}
	if u.Category != nil {
		r.Category = *u.Category
	}
	return nil
}

func (m *memResources) Move(_ context.Context, id string, mv resource.Move) error {
	r, ok := m.rows[id]
	if !ok {
		return errNoRow
	}
	for other, res := range m.rows {
		if other != id && res.URI == mv.URI {
			return resource.ErrURIConflict
		}
	}
	if mv.FromURI != "" && mv.FromURI != mv.URI {
		m.aliases[mv.FromURI] = id
	}
	delete(m.aliases, mv.URI)
	r.Scope, r.ScopeID, r.URI = mv.Scope, mv.ScopeID, mv.URI
	return nil
}

func (m *memResources) Delete(_ context.Context, id string) error {
	delete(m.rows, id)
	return nil
}

// --- blob storage, shared by the resource layer and the serving route ---

type memBlobs struct{ objects map[string][]byte }

func (b *memBlobs) PutObject(_ context.Context, _, key string, data []byte, _ string) error {
	b.objects[key] = data
	return nil
}

func (b *memBlobs) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	data, ok := b.objects[key]
	if !ok {
		return nil, "", errNoObject
	}
	return data, "text/plain", nil
}

func (b *memBlobs) DeleteObject(_ context.Context, _, key string) error {
	delete(b.objects, key)
	return nil
}

// --- the asset's declared references ---

// memRefs holds the reference rows an asset's save wrote. They key on the
// target's id and record the URI exactly as the author wrote it, which is what
// the serve path and the content rewrite each read.
type memRefs struct{ byAsset map[string][]assetrefs.Ref }

func (r *memRefs) Replace(_ context.Context, assetID string, refs []assetrefs.Ref) error {
	r.byAsset[assetID] = refs
	return nil
}

func (r *memRefs) Attach(_ context.Context, ref assetrefs.Ref) (bool, error) {
	r.byAsset[ref.AssetID] = append(r.byAsset[ref.AssetID], ref)
	return true, nil
}

func (*memRefs) Detach(context.Context, string, assetrefs.TargetKind, string) (bool, error) {
	return false, nil
}

func (r *memRefs) ListByAsset(_ context.Context, assetID string) ([]assetrefs.Ref, error) {
	return r.byAsset[assetID], nil
}

func (r *memRefs) ListByTarget(_ context.Context, kind assetrefs.TargetKind, targetID string, _ int) ([]assetrefs.Ref, error) {
	var out []assetrefs.Ref
	for _, refs := range r.byAsset {
		for _, ref := range refs {
			if ref.TargetKind == kind && ref.TargetID == targetID {
				out = append(out, ref)
			}
		}
	}
	return out, nil
}

func (r *memRefs) GetByToken(_ context.Context, assetID, token string) (*assetrefs.Ref, error) {
	for _, ref := range r.byAsset[assetID] {
		if ref.RefToken == token {
			found := ref
			return &found, nil
		}
	}
	// No such reference is (nil, nil), which is the store contract serve.go
	// reads: it treats an error and a nil ref identically, as a 404.
	return nil, nil //nolint:nilnil // interface contract: no such reference is (nil, nil)
}

// --- the prompt's attachments ---

type memAttachments struct {
	byPrompt map[string][]prompt.Attachment
}

func (a *memAttachments) Attach(_ context.Context, at prompt.Attachment) error {
	a.byPrompt[at.PromptID] = append(a.byPrompt[at.PromptID], at)
	return nil
}

func (*memAttachments) Detach(context.Context, string, string) error { return nil }

func (a *memAttachments) ListByPrompt(_ context.Context, id string) ([]prompt.Attachment, error) {
	return a.byPrompt[id], nil
}

func (a *memAttachments) ListByResource(_ context.Context, resourceID string) ([]string, error) {
	var out []string
	for id, links := range a.byPrompt {
		for _, l := range links {
			if l.ResourceID == resourceID {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

func (*memAttachments) Reorder(context.Context, string, []string) error { return nil }

// --- the assembled platform ---

type movePlatform struct {
	// store is the interface rather than the in-memory implementation so the
	// same assembled surfaces can be pointed at a real database. The map-backed
	// store answers the question of whether the routes are wired to each other;
	// only Postgres answers whether the statements they issue run (#1506).
	store    resource.Store
	blobs    *memBlobs
	refs     *memRefs
	patch    http.Handler
	serveRef http.Handler
	prompts  *attachserve.Resolver
	res      *resource.Resource
}

// row reads the fixture resource back through the store, which is how an
// assertion about the moved row reaches either implementation.
func (p *movePlatform) row(t *testing.T) *resource.Resource {
	t.Helper()
	got, err := p.store.Get(t.Context(), p.res.ID)
	if err != nil {
		t.Fatalf("reading the moved resource: %v", err)
	}
	return got
}

// owner is the person whose library the file starts in. They belong to the ops
// persona and hold no admin role anywhere, which is the acceptance case.
func owner() *resource.Claims {
	return &resource.Claims{
		Sub: "sub-1", Email: "me@example.com",
		Personas: []string{"ops"}, Roles: []string{"analyst"},
	}
}

func newMovePlatform(t *testing.T) *movePlatform {
	t.Helper()
	return newMovePlatformOn(t, newMemResources())
}

// newMovePlatformOn assembles the same routes over the store it is given, so
// the map-backed run and the real-Postgres run (move_route_realdb_integration_test.go)
// exercise one set of surfaces rather than two that can drift apart.
func newMovePlatformOn(t *testing.T, store resource.Store) *movePlatform {
	t.Helper()
	blobs := &memBlobs{objects: map[string][]byte{}}
	deps := resource.Deps{
		Store: store, S3Client: blobs, S3Bucket: testBucket, URIScheme: "mcp",
	}

	res, err := resource.CreateResource(t.Context(), deps, owner(), resource.NewResource{
		Scope: resource.ScopeUser, ScopeID: "sub-1", Category: "templates",
		Filename: "report.docx", DisplayName: "Report", Description: "the template",
		Data: []byte(fileBytes), MIMEType: "text/plain",
	})
	if err != nil {
		t.Fatalf("CreateResource: %v", err)
	}

	// The asset declares the resource by the URI its author wrote, which is the
	// state a save leaves behind.
	refs := &memRefs{byAsset: map[string][]assetrefs.Ref{testAssetID: {{
		AssetID: testAssetID, TargetKind: assetrefs.TargetResource,
		TargetID: res.ID, URI: res.URI, RefToken: testRefToken,
	}}}}

	attachments := &memAttachments{byPrompt: map[string][]prompt.Attachment{testPromptID: {{
		PromptID: testPromptID, ResourceID: res.ID, AttachedBy: "me@example.com",
	}}}}

	mux := http.NewServeMux()
	mux.Handle("GET "+assetrefs.PathPrefix+"{id}/{ref}", assetrefs.New(assetrefs.Deps{
		Refs: refs, Resources: store, Blobs: blobs, Bucket: testBucket,
	}))

	return &movePlatform{
		store: store, blobs: blobs, refs: refs, res: res,
		patch: resource.NewHandler(deps,
			func(*http.Request) (*resource.Claims, error) { return owner(), nil }, nil),
		serveRef: mux,
		prompts: attachserve.New(attachserve.Deps{
			Attachments: attachments, Resources: store, Blobs: blobs, Bucket: testBucket,
		}),
	}
}

// renderedReference is what a reader's browser gets for the reference the asset
// declared: the resource's bytes, served under the reference's own token.
func (p *movePlatform) renderedReference(t *testing.T) (status int, body string) {
	t.Helper()
	rec := httptest.NewRecorder()
	p.serveRef.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		assetrefs.PathPrefix+testAssetID+"/"+testRefToken, http.NoBody))
	return rec.Code, rec.Body.String()
}

// servedAttachment is what an agent receiving the prompt gets for its attached
// material.
func (p *movePlatform) servedAttachment(t *testing.T) attachserve.Resolved {
	t.Helper()
	got := p.prompts.Resolve(t.Context(), testPromptID, *owner())
	if len(got) != 1 {
		t.Fatalf("attachments resolved = %d, want 1", len(got))
	}
	return got[0]
}

// move refiles the fixture into the ops persona, which is the acceptance case:
// its owner belongs to that persona and holds no admin role anywhere.
func (p *movePlatform) move(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"scope": "persona", "scope_id": "ops"})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	p.patch.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPatch,
		"/api/v1/resources/"+p.res.ID, bytes.NewReader(body)))
	return rec
}

// TestAnAssetKeepsRenderingAResourceAcrossAMove is the acceptance criterion for
// references, taken end to end: the same reference URL a reader's browser
// already holds serves the same bytes after the file has been published to a
// persona.
func TestAnAssetKeepsRenderingAResourceAcrossAMove(t *testing.T) {
	p := newMovePlatform(t)

	code, before := p.renderedReference(t)
	if code != http.StatusOK || before != fileBytes {
		t.Fatalf("before the move: status %d, body %q", code, before)
	}

	if rec := p.move(t); rec.Code != http.StatusOK {
		t.Fatalf("move: status %d, body %s", rec.Code, rec.Body.String())
	}

	code, after := p.renderedReference(t)
	if code != http.StatusOK || after != fileBytes {
		t.Fatalf("after the move: status %d, body %q", code, after)
	}
	// The reference row is untouched: it still records the URI as the author
	// wrote it, which is the string the content rewrite matches on.
	declared := p.refs.byAsset[testAssetID][0]
	if declared.URI != "mcp://user/sub-1/templates/report.docx" {
		t.Errorf("the declaration was rewritten to %q", declared.URI)
	}
	if moved := p.row(t); moved.URI != "mcp://persona/ops/templates/report.docx" {
		t.Errorf("the resource's own URI is %q", moved.URI)
	}
}

// TestAPromptKeepsServingItsAttachmentAcrossAMove is the same criterion for
// prompt attachments, which key on the resource id.
func TestAPromptKeepsServingItsAttachmentAcrossAMove(t *testing.T) {
	p := newMovePlatform(t)

	before := p.servedAttachment(t)
	if before.Availability != attachserve.AvailableEmbedded || before.Text != fileBytes {
		t.Fatalf("before the move: %s, %q", before.Availability, before.Text)
	}

	if rec := p.move(t); rec.Code != http.StatusOK {
		t.Fatalf("move: status %d, body %s", rec.Code, rec.Body.String())
	}

	after := p.servedAttachment(t)
	if after.Availability != attachserve.AvailableEmbedded || after.Text != fileBytes {
		t.Fatalf("after the move: %s, %q", after.Availability, after.Text)
	}
	// The material is now served under the address it has, not the one it had.
	if after.URI != "mcp://persona/ops/templates/report.docx" {
		t.Errorf("attachment URI = %q", after.URI)
	}
}

// TestTheVacatedAddressStillResolves covers the third kind of citation: text
// that hard-codes the URI, which resolves through GetByURI and has no row
// pointing at the resource to survive on.
func TestTheVacatedAddressStillResolves(t *testing.T) {
	p := newMovePlatform(t)
	old := p.res.URI

	if rec := p.move(t); rec.Code != http.StatusOK {
		t.Fatalf("move: status %d", rec.Code)
	}

	got, err := p.store.GetByURI(t.Context(), old)
	if err != nil {
		t.Fatalf("the vacated address no longer resolves: %v", err)
	}
	if got.ID != p.res.ID {
		t.Errorf("the vacated address resolves to %q", got.ID)
	}
}

// TestALiveAddressWinsOverAVacatedOne is what keeps an alias from shadowing
// whichever resource occupies that address now: the owner moves their file out
// and uploads another under the same name.
func TestALiveAddressWinsOverAVacatedOne(t *testing.T) {
	p := newMovePlatform(t)
	old := p.res.URI

	if rec := p.move(t); rec.Code != http.StatusOK {
		t.Fatalf("move: status %d", rec.Code)
	}

	replacement, err := resource.CreateResource(t.Context(), resource.Deps{
		Store: p.store, S3Client: p.blobs, S3Bucket: testBucket, URIScheme: "mcp",
	}, owner(), resource.NewResource{
		Scope: resource.ScopeUser, ScopeID: "sub-1", Category: "templates",
		Filename: "report.docx", DisplayName: "Report v2", Description: "the new one",
		Data: []byte("newer"), MIMEType: "text/plain",
	})
	if err != nil {
		t.Fatalf("CreateResource: %v", err)
	}

	got, err := p.store.GetByURI(t.Context(), old)
	if err != nil {
		t.Fatalf("GetByURI: %v", err)
	}
	if got.ID != replacement.ID {
		t.Errorf("the address resolved to %q, want the resource that holds it now", got.ID)
	}
}

// TestTheMovedFileLeavesTheMoversLibrary is the visibility half of the
// acceptance: after the move the file is the persona's, and it is no longer in
// the mover's own library.
func TestTheMovedFileLeavesTheMoversLibrary(t *testing.T) {
	p := newMovePlatform(t)

	if rec := p.move(t); rec.Code != http.StatusOK {
		t.Fatalf("move: status %d", rec.Code)
	}
	moved := p.row(t)

	// A member of ops reads it; the same person's personal library no longer
	// contains it, and somebody outside ops cannot read it at all.
	if !resource.CanReadResource(*owner(), moved) {
		t.Error("the persona's own member cannot read the file they moved there")
	}
	for _, sf := range resource.VisibleScopes(*owner()) {
		if sf.Scope == resource.ScopeUser && sf.ScopeID == moved.ScopeID {
			t.Error("the file is still in the mover's personal library")
		}
	}
	outsider := resource.Claims{Sub: "sub-9", Email: "them@example.com", Personas: []string{"finance"}}
	if resource.CanReadResource(outsider, moved) {
		t.Error("somebody outside the persona can read a persona-scoped file")
	}
}
