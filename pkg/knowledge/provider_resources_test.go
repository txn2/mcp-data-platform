package knowledge

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/docread"
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
			ID: "res_g", Scope: resource.ScopeGlobal, Path: "references", Filename: "dict.csv",
			DisplayName: "Sales Dictionary", Description: "Field reference", MIMEType: "text/csv",
			SizeBytes: 60, S3Key: "k-global", URI: "mcp://global/references/dict.csv",
		},
		{
			ID: "res_p", Scope: resource.ScopePersona, ScopeID: "analyst", Path: "playbooks",
			Filename: "play.md", DisplayName: "Analyst playbook", MIMEType: "text/markdown",
			SizeBytes: 10, S3Key: "k-persona", URI: "mcp://persona/analyst/playbooks/play.md",
		},
		{
			ID: "res_u", Scope: resource.ScopeUser, ScopeID: "sub-a", Path: "notes",
			Filename: "notes.md", DisplayName: "Personal notes", MIMEType: "text/markdown",
			SizeBytes: 10, S3Key: "k-user", URI: "mcp://user/sub-a/notes/notes.md",
		},
		{
			ID: "res_bin", Scope: resource.ScopeGlobal, Path: "references", Filename: "logo.png",
			DisplayName: "Brand logo", MIMEType: "image/png", SizeBytes: 4096,
			S3Key: "k-bin", URI: "mcp://global/references/logo.png",
		},
	}}
}

func resourcesProvider() *ResourcesProvider {
	return NewResourcesProvider(seededResources(), &fakeResourceBlobs{objects: map[string][]byte{
		"k-global": []byte("column,description\ngross_margin_pct,margin after COGS\n"),
		"k-bin":    {0x89, 'P', 'N', 'G'},
	}}, "bucket", nil)
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
	p := NewResourcesProvider(&fakeResourceStore{searchErr: errors.New("db down")}, nil, "", nil)
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

// An image comes back as the picture itself, attached, not as a body of text
// and not as metadata alone. Before #1657 this returned the metadata row, and
// an agent handed a size and a MIME type reports that it cannot open the file.
func TestResourcesProvider_FetchAnImageAttachesThePicture(t *testing.T) {
	doc, owned, err := resourcesProvider().Fetch(context.Background(), "mcp:resource:res_bin", Caller{})
	if err != nil || !owned {
		t.Fatalf("owned=%v err=%v", owned, err)
	}
	if doc.Body != "" {
		t.Errorf("an image must not be inlined as text: %q", doc.Body)
	}
	if doc.Attachment == nil {
		t.Fatal("an image fetch carried no attachment")
	}
	if !doc.Attachment.Image {
		t.Error("the attachment was not marked as a picture")
	}
	if len(doc.Attachment.Bytes) == 0 || doc.Attachment.MIMEType != "image/png" {
		t.Errorf("the attachment did not carry the file: %+v", doc.Attachment)
	}
	if doc.Attachment.URI != "mcp://global/references/logo.png" {
		t.Errorf("the attachment did not carry the canonical URI: %q", doc.Attachment.URI)
	}
	// An empty body with no explanation is what produced the report that the
	// platform was withholding the file.
	if !strings.Contains(doc.Note, "attached") {
		t.Errorf("the document did not say where the file went: %q", doc.Note)
	}
	res, ok := doc.Content.(*resource.Resource)
	if !ok || res.URI != "mcp://global/references/logo.png" || res.SizeBytes == 0 {
		t.Errorf("the record must still travel with its URI and size: %+v", doc.Content)
	}
}

// A family the server has no reader for still reaches the caller: the bytes
// are attached for the caller's own tools, not withheld.
func TestResourcesProvider_FetchAnUnreadableFamilyAttachesItsBytes(t *testing.T) {
	store := seededResources()
	store.resources[3].MIMEType = "application/vnd.acme.thing"
	p := NewResourcesProvider(store, &fakeResourceBlobs{objects: map[string][]byte{
		"k-bin": {0x01, 0x02, 0x03, 0x04},
	}}, "bucket", nil)

	doc, _, err := p.Fetch(context.Background(), "mcp:resource:res_bin", Caller{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if doc.Attachment == nil || doc.Attachment.Image {
		t.Fatalf("an unreadable family did not attach its bytes: %+v", doc.Attachment)
	}
	if len(doc.Attachment.Bytes) != 4 {
		t.Errorf("the attachment did not carry the file: %d bytes", len(doc.Attachment.Bytes))
	}
}

// A presentation is a zip of XML parts, and its parts are what a caller
// reproducing it as a template needs. Flattening it to prose would throw away
// the very structure they came for.
func TestResourcesProvider_FetchAPresentationReturnsItsParts(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, part := range [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0"?><Types/>`},
		{"ppt/slides/slide1.xml", `<?xml version="1.0"?><sld><a:t>Quarterly Review</a:t></sld>`},
	} {
		w, err := zw.Create(part[0])
		if err != nil {
			t.Fatalf("creating %s: %v", part[0], err)
		}
		if _, err = w.Write([]byte(part[1])); err != nil {
			t.Fatalf("writing %s: %v", part[0], err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing the archive: %v", err)
	}

	store := seededResources()
	store.resources[3].MIMEType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	store.resources[3].Filename = "deck.pptx"
	p := NewResourcesProvider(store, &fakeResourceBlobs{
		objects: map[string][]byte{"k-bin": buf.Bytes()},
	}, "bucket", nil)

	doc, _, err := p.Fetch(context.Background(), "mcp:resource:res_bin", Caller{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if doc.Attachment != nil {
		t.Errorf("a presentation was attached as bytes rather than read: %+v", doc.Attachment)
	}
	if !strings.Contains(doc.Body, "ppt/slides/slide1.xml") || !strings.Contains(doc.Body, "Quarterly Review") {
		t.Errorf("the deck's parts did not reach the body: %q", doc.Body)
	}
}

// A PDF comes back as its text. The extractor is a stub here; the real one is
// exercised in internal/pdftext, which is where the WebAssembly module belongs.
func TestResourcesProvider_FetchAPDFReturnsItsText(t *testing.T) {
	store := seededResources()
	store.resources[3].MIMEType = "application/pdf"
	store.resources[3].Filename = "report.pdf"
	p := NewResourcesProvider(store, &fakeResourceBlobs{
		objects: map[string][]byte{"k-bin": []byte("%PDF-1.5 whatever")},
	}, "bucket", docread.New(stubPDFText("Quarterly revenue grew 12 percent")))

	doc, _, err := p.Fetch(context.Background(), "mcp:resource:res_bin", Caller{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if doc.Attachment != nil {
		t.Errorf("a readable PDF was attached as bytes: %+v", doc.Attachment)
	}
	if !strings.Contains(doc.Body, "grew 12 percent") {
		t.Errorf("the PDF's text did not reach the body: %q", doc.Body)
	}
}

// stubPDFText stands in for the WebAssembly extractor, including its contract
// of stopping once it has produced what it was asked for: a stub that ignored
// the budget would let the caller's truncation arithmetic pass untested.
type stubPDFText string

func (s stubPDFText) ExtractText(_ context.Context, _ []byte, limit int) (string, error) {
	if len(s) > limit {
		return string(s[:limit]), nil
	}
	return string(s), nil
}

// A file too large to carry is the one case that still answers with metadata,
// and it must name the door that has no limit rather than look like a refusal.
func TestResourcesProvider_FetchOversizedFileNamesTheWayToReadIt(t *testing.T) {
	store := seededResources()
	store.resources[0].SizeBytes = resource.MaxInlineContentBytes + 1
	p := NewResourcesProvider(store, &fakeResourceBlobs{objects: map[string][]byte{"k-global": []byte("x")}}, "b", nil)

	doc, _, err := p.Fetch(context.Background(), "mcp:resource:res_g", Caller{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(doc.Note, "resources/read") || !strings.Contains(doc.Note, store.resources[0].URI) {
		t.Errorf("the note did not name the way to read the file: %q", doc.Note)
	}
}

// A body that is a prefix must say so and name the door that has no limit, or
// a reader acts on a bounded answer as though it were the whole file. A PDF is
// where this is reachable: its extracted text can outrun the budget even when
// the file itself fits inside it.
func TestResourcesProvider_FetchTruncatedTextSaysSo(t *testing.T) {
	store := seededResources()
	store.resources[3].MIMEType = "application/pdf"
	store.resources[3].Filename = "report.pdf"
	p := NewResourcesProvider(store, &fakeResourceBlobs{
		objects: map[string][]byte{"k-bin": []byte("%PDF-1.5 whatever")},
	}, "bucket", docread.New(stubPDFText(strings.Repeat("p", resource.MaxInlineContentBytes+1024))))

	doc, _, err := p.Fetch(context.Background(), "mcp:resource:res_bin", Caller{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(doc.Body) != resource.MaxInlineContentBytes {
		t.Fatalf("body = %d bytes, want the budget", len(doc.Body))
	}
	if !strings.Contains(doc.Note, "Only the first part") || !strings.Contains(doc.Note, "resources/read") {
		t.Errorf("a truncated body did not say so and name the way to the rest: %q", doc.Note)
	}
}

// The attachment is carried out of band of the JSON. A Bytes field inside the
// serialized document would ship the file a second time, base64-expanded, in
// the very payload the attachment exists to stay out of.
func TestResourcesProvider_FetchAttachmentIsNotSerialized(t *testing.T) {
	doc, _, err := resourcesProvider().Fetch(context.Background(), "mcp:resource:res_bin", Caller{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshaling the document: %v", err)
	}
	if strings.Contains(string(encoded), "ttachment") {
		t.Errorf("the attachment reached the JSON payload: %s", encoded)
	}
}

// The recorded size is checked BEFORE the blob read, so an oversized object is
// never pulled into memory to be discarded. The fixture makes the recorded size
// disagree with the object precisely to prove the pre-read guard is what fires.
func TestResourcesProvider_FetchOversizedTextIsNotInlined(t *testing.T) {
	store := seededResources()
	store.resources[0].SizeBytes = resource.MaxInlineContentBytes + 1
	p := NewResourcesProvider(store, &fakeResourceBlobs{objects: map[string][]byte{"k-global": []byte("x")}}, "b", nil)

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
	p := NewResourcesProvider(&fakeResourceStore{getErr: errors.New("db down")}, nil, "", nil)
	_, owned, err := p.Fetch(context.Background(), "mcp:resource:res_g", Caller{})
	if !owned || err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("owned=%v err=%v, want a real error", owned, err)
	}
}

// A blob read failure degrades to metadata-only rather than failing the fetch.
func TestResourcesProvider_FetchBlobFailureDegrades(t *testing.T) {
	p := NewResourcesProvider(seededResources(), &fakeResourceBlobs{err: errors.New("connection reset")}, "b", nil)
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
	p := NewResourcesProvider(seededResources(), nil, "", nil)
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

func TestResourcesProvider_FetchRecordsANonTextRead(t *testing.T) {
	reads := &recordingReads{}
	p := resourcesProvider()
	p.SetReadRecorder(reads)

	// An image comes back as the picture rather than as text. The caller
	// pulled the material into their session either way, so it counts as usage.
	if _, _, err := p.Fetch(context.Background(), "mcp:resource:res_bin", Caller{UserID: "u-1"}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(reads.events) != 1 {
		t.Fatalf("recorded reads = %d, want 1 for a non-text fetch", len(reads.events))
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
	lookup := &stubLookup{tables: map[string][]HitTable{
		"res_g": {{Connection: "scratch", Table: "scratch.uploads.analyst_dict", Stale: true}},
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
	if len(doc.Tables) != 1 || doc.Tables[0].Table != "scratch.uploads.analyst_dict" {
		t.Errorf("document carries no table reference: %+v", doc.Tables)
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

// TestCallerClaimsCarryAnUnattendedCallersPerson is the discovery half of the
// rule the write path applies: a managed-script run finds the material its
// author can see, including the file the same run wrote, which is filed under
// the person it acts for rather than under the principal (#1487).
func TestCallerClaimsCarryAnUnattendedCallersPerson(t *testing.T) {
	run := Caller{UserID: "script:weekly-refresh", Email: "owner@example.com", OnBehalfOf: "author@example.com"}

	claims := callerClaims(run)

	assert.Equal(t, "author@example.com", claims.OnBehalfOf)
	assert.True(t, resource.CanReadResource(claims, &resource.Resource{
		Scope: resource.ScopeUser, ScopeID: "author@example.com",
	}), "a run must find the file it wrote itself")

	human := callerClaims(Caller{UserID: "sub-1", Email: "person@example.com"})
	assert.Empty(t, human.OnBehalfOf, "a person acts as themselves")
}

// TestResourcesProvider_FetchAdmitsAnAdministrator is #1584 at this provider: a
// platform administrator states a reference to a file in a persona library they
// do not belong to, and gets it.
//
// The same file was answered as not-found while every other read the same
// session could make -- the REST detail and content routes, a content replace,
// and an asset reference declaration -- resolved through
// resource.CanAccessResource and served them. A fetch that a caller names is
// not enumeration, so it is answered on the predicate the rest of the resource
// surface uses.
func TestResourcesProvider_FetchAdmitsAnAdministrator(t *testing.T) {
	p := resourcesProvider()
	admin := Caller{UserID: "sub-admin", Email: "admin@example.com", IsAdmin: true}

	doc, owned, err := p.Fetch(context.Background(), "mcp:resource:res_p", admin)
	if err != nil || !owned {
		t.Fatalf("an administrator was refused a persona-library file: owned=%v err=%v", owned, err)
	}
	if doc.Title != "Analyst playbook" {
		t.Fatalf("fetch returned the wrong document: %+v", doc)
	}

	// The persona-admin arm answers the same way for the one library it covers,
	// and no other.
	personaAdmin := Caller{UserID: "sub-pa", Email: "pa@example.com", Roles: []string{"persona-admin:analyst"}}
	if _, _, err := p.Fetch(context.Background(), "mcp:resource:res_p", personaAdmin); err != nil {
		t.Fatalf("the analyst library's administrator was refused its file: %v", err)
	}
	if _, _, err := p.Fetch(context.Background(), "mcp:resource:res_u", personaAdmin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("authority over one library reached another person's: err=%v", err)
	}
}

// TestResourcesProvider_SearchIsUnchangedForAnAdministrator is the other half of
// the change above, and the reason it is safe: Search runs on
// resource.VisibleScopes, which is membership and reads neither Roles nor
// IsAdmin. Carrying authority onto the caller widens what a stated reference
// resolves to and nothing about what a search returns, so an administrator's
// discovery does not silently become every persona's library.
func TestResourcesProvider_SearchIsUnchangedForAnAdministrator(t *testing.T) {
	p := resourcesProvider()

	member, err := p.Search(context.Background(), Query{
		Intent: "Analyst playbook",
		Caller: Caller{UserID: "sub-m", Email: "m@example.com", Personas: []string{"analyst"}},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(member) != 1 {
		t.Fatalf("a member of the persona does not see its playbook: %v", member)
	}

	admin, err := p.Search(context.Background(), Query{
		Intent: "Analyst playbook",
		Caller: Caller{UserID: "sub-admin", Email: "admin@example.com", IsAdmin: true},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(admin) != 0 {
		t.Fatalf("an administrator's search returned a library they are not a member of: %v", admin)
	}
}
