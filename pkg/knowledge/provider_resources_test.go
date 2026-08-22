package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakeResourceStore models the real Postgres resource store closely enough to
// test the provider: Search applies the caller's visible scopes exactly as the
// SQL predicate does, and Get reports a missing row as a WRAPPED sql.ErrNoRows
// (the real store's contract — returning (nil, nil) instead would make the
// provider's not-found handling look correct while it is broken in production).
type fakeResourceStore struct {
	resources []resource.Resource
	// contents stands in for the content_text column the index consumer fills.
	contents  map[string]string
	getErr    error
	searchErr error
}

func (f *fakeResourceStore) Search(_ context.Context, q resource.SearchQuery) ([]resource.ScoredResource, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	var out []resource.ScoredResource
	for _, r := range f.resources {
		if !visibleIn(q.Scopes, r) {
			continue
		}
		// Match the composed index text, the same corpus the FTS index covers.
		hay := strings.ToLower(resource.IndexText(r, f.contents[r.ID]))
		if strings.Contains(hay, strings.ToLower(q.QueryText)) {
			out = append(out, resource.ScoredResource{Resource: r, Score: 0.5})
		}
	}
	return out, nil
}

func (f *fakeResourceStore) Get(_ context.Context, id string) (*resource.Resource, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.resources {
		if f.resources[i].ID == id {
			r := f.resources[i]
			return &r, nil
		}
	}
	return nil, fmt.Errorf("scanning resource: %w", sql.ErrNoRows)
}

func visibleIn(scopes []resource.ScopeFilter, r resource.Resource) bool {
	for _, sf := range scopes {
		if sf.Scope != r.Scope {
			continue
		}
		if sf.Scope == resource.ScopeGlobal || sf.ScopeID == r.ScopeID {
			return true
		}
	}
	return false
}

// fakeResourceBlobs serves resource bytes by S3 key.
type fakeResourceBlobs struct {
	objects map[string][]byte
	err     error
}

func (f *fakeResourceBlobs) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	if f.err != nil {
		return nil, "", f.err
	}
	body, ok := f.objects[key]
	if !ok {
		return nil, "", errors.New("NoSuchKey")
	}
	return body, "text/csv", nil
}

func seededResources() *fakeResourceStore {
	return &fakeResourceStore{contents: map[string]string{
		"res_g": "column,description\ngross_margin_pct,margin after COGS\n",
	}, resources: []resource.Resource{
		{
			ID: "res_g", Scope: resource.ScopeGlobal, Category: "references", Filename: "dict.csv",
			DisplayName: "Sales Dictionary", Description: "Field reference", MIMEType: "text/csv",
			SizeBytes: 60, S3Key: "k-global", URI: "mcp://global/references/dict.csv",
		},
		{
			ID: "res_p", Scope: resource.ScopePersona, ScopeID: "analyst", Category: "playbooks",
			Filename: "play.md", DisplayName: "Analyst playbook", MIMEType: "text/markdown",
			SizeBytes: 10, S3Key: "k-persona", URI: "mcp://persona/analyst/playbooks/play.md",
		},
		{
			ID: "res_u", Scope: resource.ScopeUser, ScopeID: "sub-a", Category: "notes",
			Filename: "notes.md", DisplayName: "Personal notes", MIMEType: "text/markdown",
			SizeBytes: 10, S3Key: "k-user", URI: "mcp://user/sub-a/notes/notes.md",
		},
		{
			ID: "res_bin", Scope: resource.ScopeGlobal, Category: "references", Filename: "logo.png",
			DisplayName: "Brand logo", MIMEType: "image/png", SizeBytes: 4096,
			S3Key: "k-bin", URI: "mcp://global/references/logo.png",
		},
	}}
}

func resourcesProvider() *ResourcesProvider {
	return NewResourcesProvider(seededResources(), &fakeResourceBlobs{objects: map[string][]byte{
		"k-global": []byte("column,description\ngross_margin_pct,margin after COGS\n"),
		"k-bin":    {0x89, 'P', 'N', 'G'},
	}}, "bucket")
}

func TestResourcesProvider_NameAndScope(t *testing.T) {
	p := resourcesProvider()
	if p.Name() != SourceResources {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopeShared {
		t.Errorf("Scope = %v, want shared", p.Scope())
	}
}

func TestResourcesProvider_SearchScopesToCaller(t *testing.T) {
	p := resourcesProvider()
	ids := func(hits []Hit) []string {
		out := make([]string, 0, len(hits))
		for _, h := range hits {
			out = append(out, h.Ref)
		}
		return out
	}

	// The owner + persona member sees their user-scoped and persona-scoped material.
	hits, err := p.Search(context.Background(), Query{
		Intent: "a", Caller: Caller{UserID: "sub-a", Email: "a@example.com", Persona: "analyst", Personas: []string{"analyst"}},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := ids(hits); !slices.Contains(got, "res_p") || !slices.Contains(got, "res_u") {
		t.Errorf("owner+member hits = %v", got)
	}

	// Another caller in the same persona never sees the user-scoped resource.
	hits, _ = p.Search(context.Background(), Query{
		Intent: "a", Caller: Caller{UserID: "sub-b", Email: "b@example.com", Persona: "analyst", Personas: []string{"analyst"}},
	})
	if got := ids(hits); slices.Contains(got, "res_u") {
		t.Errorf("user-scoped resource leaked to another caller: %v", got)
	}

	// A caller outside the persona sees neither.
	hits, _ = p.Search(context.Background(), Query{
		Intent: "a", Caller: Caller{UserID: "sub-b", Email: "b@example.com", Persona: "engineer", Personas: []string{"engineer"}},
	})
	if got := ids(hits); slices.Contains(got, "res_p") || slices.Contains(got, "res_u") {
		t.Errorf("scoped resources leaked to a non-member: %v", got)
	}

	// An anonymous caller still sees global material and nothing else.
	hits, _ = p.Search(context.Background(), Query{Intent: "a"})
	for _, h := range hits {
		if h.Ref != "res_g" && h.Ref != "res_bin" {
			t.Errorf("anonymous caller saw a scoped resource: %v", h.Ref)
		}
	}
}

// The reason this source exists: a term that appears only inside the file finds
// the resource, and the hit carries a fetchable reference plus a client
// attachable link.
func TestResourcesProvider_SearchByContentCarriesReferenceAndLink(t *testing.T) {
	hits, err := resourcesProvider().Search(context.Background(), Query{Intent: "gross_margin_pct"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v", hits)
	}
	h := hits[0]
	if h.Source != SourceResources || h.Reference != "mcp:resource:res_g" {
		t.Errorf("hit provenance/reference wrong: %+v", h)
	}
	if !strings.Contains(h.Text, "Sales Dictionary") || !strings.Contains(h.Text, "dict.csv") {
		t.Errorf("hit text = %q", h.Text)
	}
	if h.Link == nil || h.Link.URI != "mcp://global/references/dict.csv" || h.Link.MIMEType != "text/csv" {
		t.Errorf("hit link = %+v", h.Link)
	}
}

// The persona-scope rule is MEMBERSHIP, and Caller.Persona is not membership: it
// falls back to the configured default persona for a caller whose roles match
// none. A caller who belongs to no persona must therefore see no persona
// material, even when their resolved persona names one.
func TestResourcesProvider_PersonaScopeUsesMembershipNotResolvedPersona(t *testing.T) {
	p := resourcesProvider()

	// Resolved as "analyst" (the default persona) but a member of nothing.
	hits, err := p.Search(context.Background(), Query{
		Intent: "a", Caller: Caller{UserID: "sub-x", Email: "x@example.com", Persona: "analyst", Personas: []string{}},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, h := range hits {
		if h.Ref == "res_p" {
			t.Fatalf("a non-member inherited the default persona's material: %+v", hits)
		}
	}
	if _, _, err := p.Fetch(context.Background(), "mcp:resource:res_p",
		Caller{UserID: "sub-x", Persona: "analyst", Personas: []string{}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fetch err = %v, want ErrNotFound for a non-member", err)
	}

	// A caller who belongs to SEVERAL personas sees all of their material, not
	// only the one the request resolved to.
	multi := Caller{UserID: "sub-y", Email: "y@example.com", Persona: "engineer", Personas: []string{"engineer", "analyst"}}
	hits, err = p.Search(context.Background(), Query{Intent: "a", Caller: multi})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, h := range hits {
		if h.Ref == "res_p" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a member of two personas lost one persona's material: %+v", hits)
	}
	if _, _, err := p.Fetch(context.Background(), "mcp:resource:res_p", multi); err != nil {
		t.Fatalf("fetch by a member of the persona failed: %v", err)
	}
}

func TestResourcesProvider_SearchWithoutIntentIsNoop(t *testing.T) {
	hits, err := resourcesProvider().Search(context.Background(), Query{EntityURNs: []string{"urn:li:dataset:(x)"}})
	if err != nil || hits != nil {
		t.Fatalf("entity-only query should yield nothing: %v, %v", hits, err)
	}
}

func TestResourcesProvider_SearchErrorIsWrapped(t *testing.T) {
	p := NewResourcesProvider(&fakeResourceStore{searchErr: errors.New("db down")}, nil, "")
	if _, err := p.Search(context.Background(), Query{Intent: "x"}); err == nil ||
		!strings.Contains(err.Error(), "resource search") {
		t.Fatalf("err = %v", err)
	}
}

func TestResourcesProvider_FetchTextInlinesContent(t *testing.T) {
	doc, owned, err := resourcesProvider().Fetch(context.Background(), "mcp:resource:res_g", Caller{})
	if err != nil || !owned {
		t.Fatalf("owned=%v err=%v", owned, err)
	}
	if doc.Title != "Sales Dictionary" || doc.Source != SourceResources {
		t.Errorf("doc = %+v", doc)
	}
	if !strings.Contains(doc.Body, "gross_margin_pct") {
		t.Errorf("text resource should come back with its content inline: %q", doc.Body)
	}
	res, ok := doc.Content.(*resource.Resource)
	if !ok || res.URI == "" {
		t.Errorf("document must carry the resource record with its canonical URI: %+v", doc.Content)
	}
}

func TestResourcesProvider_FetchBinaryReturnsMetadataOnly(t *testing.T) {
	doc, owned, err := resourcesProvider().Fetch(context.Background(), "mcp:resource:res_bin", Caller{})
	if err != nil || !owned {
		t.Fatalf("owned=%v err=%v", owned, err)
	}
	if doc.Body != "" {
		t.Errorf("binary resource must not be inlined: %q", doc.Body)
	}
	res, ok := doc.Content.(*resource.Resource)
	if !ok || res.URI != "mcp://global/references/logo.png" || res.SizeBytes == 0 {
		t.Errorf("binary fetch must carry the URI and size: %+v", doc.Content)
	}
}

// The recorded size is checked BEFORE the blob read, so an oversized object is
// never pulled into memory to be discarded. The fixture makes the recorded size
// disagree with the object precisely to prove the pre-read guard is what fires.
func TestResourcesProvider_FetchOversizedTextIsNotInlined(t *testing.T) {
	store := seededResources()
	store.resources[0].SizeBytes = resource.MaxInlineContentBytes + 1
	p := NewResourcesProvider(store, &fakeResourceBlobs{objects: map[string][]byte{"k-global": []byte("x")}}, "b")

	doc, _, err := p.Fetch(context.Background(), "mcp:resource:res_g", Caller{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doc.Body != "" {
		t.Errorf("a resource over the inline threshold must not be inlined: %q", doc.Body)
	}
}

func TestResourcesProvider_FetchScopeEnforced(t *testing.T) {
	p := resourcesProvider()

	// The owner reads their own user-scoped resource.
	if _, _, err := p.Fetch(context.Background(), "mcp:resource:res_u", Caller{UserID: "sub-a"}); err != nil {
		t.Fatalf("owner could not fetch their own resource: %v", err)
	}
	// Another caller gets a clean not-found: neither the content nor its existence.
	_, owned, err := p.Fetch(context.Background(), "mcp:resource:res_u", Caller{UserID: "sub-b", Personas: []string{"analyst"}})
	if !owned || !errors.Is(err, ErrNotFound) {
		t.Fatalf("owned=%v err=%v, want ErrNotFound", owned, err)
	}
	// A persona resource is out of reach for a non-member.
	if _, _, err := p.Fetch(context.Background(), "mcp:resource:res_p", Caller{UserID: "sub-b", Personas: []string{"engineer"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResourcesProvider_FetchDeclinesOtherReferenceForms(t *testing.T) {
	p := resourcesProvider()
	for _, ref := range []string{"mcp:asset:a1", "urn:li:dataset:(x,y,z)", "nonsense"} {
		doc, owned, err := p.Fetch(context.Background(), ref, Caller{})
		if owned || doc != nil || err != nil {
			t.Errorf("ref %q: expected a clean decline, got owned=%v err=%v", ref, owned, err)
		}
	}
}

func TestResourcesProvider_FetchMissingIsNotFound(t *testing.T) {
	_, owned, err := resourcesProvider().Fetch(context.Background(), "mcp:resource:gone", Caller{})
	if !owned || !errors.Is(err, ErrNotFound) {
		t.Fatalf("owned=%v err=%v, want ErrNotFound", owned, err)
	}
}

// A store failure is a real error, not a not-found: fetch must not report
// "deleted" when the database is down.
func TestResourcesProvider_FetchStoreErrorSurfaces(t *testing.T) {
	p := NewResourcesProvider(&fakeResourceStore{getErr: errors.New("db down")}, nil, "")
	_, owned, err := p.Fetch(context.Background(), "mcp:resource:res_g", Caller{})
	if !owned || err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("owned=%v err=%v, want a real error", owned, err)
	}
}

// A blob read failure degrades to metadata-only rather than failing the fetch.
func TestResourcesProvider_FetchBlobFailureDegrades(t *testing.T) {
	p := NewResourcesProvider(seededResources(), &fakeResourceBlobs{err: errors.New("connection reset")}, "b")
	doc, _, err := p.Fetch(context.Background(), "mcp:resource:res_g", Caller{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doc.Body != "" || doc.Title != "Sales Dictionary" {
		t.Errorf("expected metadata-only degradation, got %+v", doc)
	}
}

// With no blob reader configured, fetch still resolves metadata.
func TestResourcesProvider_FetchWithoutBlobReader(t *testing.T) {
	p := NewResourcesProvider(seededResources(), nil, "")
	doc, _, err := p.Fetch(context.Background(), "mcp:resource:res_g", Caller{})
	if err != nil || doc.Body != "" || doc.Title != "Sales Dictionary" {
		t.Fatalf("doc=%+v err=%v", doc, err)
	}
}

// recordingReads captures the read events fetch reports.
type recordingReads struct {
	events []resource.ReadEvent
}

func (r *recordingReads) RecordRead(_ context.Context, ev resource.ReadEvent) {
	r.events = append(r.events, ev)
}

func TestResourcesProvider_FetchRecordsARead(t *testing.T) {
	reads := &recordingReads{}
	p := resourcesProvider()
	p.SetReadRecorder(reads)

	caller := Caller{UserID: "u-1", Email: "analyst@example.com", Persona: "analyst"}
	if _, _, err := p.Fetch(context.Background(), "mcp:resource:res_g", caller); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if len(reads.events) != 1 {
		t.Fatalf("recorded reads = %d, want 1", len(reads.events))
	}
	ev := reads.events[0]
	if ev.ResourceID != "res_g" || ev.Surface != resource.SurfaceFetch {
		t.Errorf("event = %+v, want a fetch of res_g", ev)
	}
	if ev.URI != "mcp://global/references/dict.csv" {
		t.Errorf("uri = %q, want the resource's canonical URI", ev.URI)
	}
	if ev.UserID != "u-1" || ev.UserEmail != "analyst@example.com" || ev.Persona != "analyst" {
		t.Errorf("caller = %+v, want the fetching caller's identity", ev)
	}
}

func TestResourcesProvider_FetchRecordsMetadataOnlyReads(t *testing.T) {
	reads := &recordingReads{}
	p := resourcesProvider()
	p.SetReadRecorder(reads)

	// A binary resource comes back as metadata plus its URI. The caller still
	// pulled the material into their session, so it still counts as usage.
	if _, _, err := p.Fetch(context.Background(), "mcp:resource:res_bin", Caller{UserID: "u-1"}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(reads.events) != 1 {
		t.Fatalf("recorded reads = %d, want 1 for a metadata-only fetch", len(reads.events))
	}
}

func TestResourcesProvider_RefusedFetchRecordsNothing(t *testing.T) {
	reads := &recordingReads{}
	p := resourcesProvider()
	p.SetReadRecorder(reads)

	// A caller outside the resource's scope gets not-found; nothing was served.
	if _, _, err := p.Fetch(context.Background(), "mcp:resource:res_p", Caller{UserID: "outsider"}); err == nil {
		t.Fatal("fetch of a persona resource by a non-member succeeded")
	}
	if len(reads.events) != 0 {
		t.Errorf("recorded reads = %d, want 0: a refused fetch is not a read", len(reads.events))
	}
}

func TestResourcesProvider_FetchWithoutARecorder(t *testing.T) {
	if _, _, err := resourcesProvider().Fetch(context.Background(), "mcp:resource:res_g", Caller{}); err != nil {
		t.Fatalf("fetch with audit disabled: %v", err)
	}
}

// TestResourcesProvider_CarriesTheTableReference is the cross-component
// assertion for the resource half: the lookup bound by the composition root
// reaches a search hit and a fetched document through the real provider, with
// the subject built from the resource's configured bucket and its head key, so
// a revision that moved the head can be reported as stale (#1327).
func TestResourcesProvider_CarriesTheTableReference(t *testing.T) {
	p := resourcesProvider()
	lookup := &stubLookup{tables: map[string]*HitTable{
		"res_g": {Connection: "scratch", Table: "scratch.uploads.analyst_dict", Stale: true},
	}}
	p.SetTableLookup(lookup)

	hits, err := p.Search(context.Background(), Query{Intent: "gross_margin_pct"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Table == nil {
		t.Fatalf("hit carries no table reference: %+v", hits)
	}
	if hits[0].Table.Table != "scratch.uploads.analyst_dict" || !hits[0].Table.Stale {
		t.Errorf("table = %+v", hits[0].Table)
	}

	if len(lookup.seen) != 1 {
		t.Fatalf("subjects = %+v", lookup.seen)
	}
	if lookup.seen[0].Kind != TableKindResource {
		t.Errorf("kind = %q; want %q", lookup.seen[0].Kind, TableKindResource)
	}
	// The bucket is the one the provider was configured with, and the key is
	// the resource's own head; a registration is judged stale against both.
	if lookup.seen[0].Bucket != "bucket" || lookup.seen[0].HeadKey != "k-global" {
		t.Errorf("subject = %+v; want the configured bucket and the head key", lookup.seen[0])
	}

	doc, owned, err := p.Fetch(context.Background(), "mcp:resource:res_g", Caller{})
	if err != nil || !owned {
		t.Fatalf("Fetch: owned=%v err=%v", owned, err)
	}
	if doc.Table == nil || doc.Table.Table != "scratch.uploads.analyst_dict" {
		t.Errorf("document carries no table reference: %+v", doc.Table)
	}
}

// TestResourcesProvider_WithoutALookupServesTheHitsItAlwaysDid.
func TestResourcesProvider_WithoutALookup(t *testing.T) {
	hits, err := resourcesProvider().Search(context.Background(), Query{Intent: "gross_margin_pct"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Table != nil {
		t.Errorf("a deployment with no registration mechanism carries no reference: %+v", hits)
	}
}
