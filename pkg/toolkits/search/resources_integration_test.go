package search

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// These tests assemble the real read path for managed resources end to end: the
// search / fetch tool handlers -> knowledge.Router -> the real ResourcesProvider
// -> a resource store. The store is a fake, but it applies the caller's visible
// scopes exactly as the SQL predicate does and reports a missing row as the
// wrapped sql.ErrNoRows the Postgres store returns, so the test proves the tool
// resolves the caller from the platform context, the provider derives visibility
// from that identity, and both halves agree on what the caller may see (#1012).

const resourceBucket = "resources"

// weeklyTemplateBody is the approved layout an agent asked to produce the
// weekly report is meant to follow rather than invent. Its section order is the
// evidence: the agent could not have guessed it.
const weeklyTemplateBody = "# Weekly Report\n\n## Headline\n## Metrics\n## Risks and blockers\n## Next week\n"

// scopedResourceStore is a resource searcher whose Search honors the caller's
// visible scopes and matches over the same composed index text the FTS index
// covers (metadata + extracted file content), so a content-only term behaves as
// it does in Postgres.
type scopedResourceStore struct {
	resources []resource.Resource
	contents  map[string]string
}

func (s *scopedResourceStore) Search(_ context.Context, q resource.SearchQuery) ([]resource.ScoredResource, error) {
	var out []resource.ScoredResource
	for _, r := range s.resources {
		if !scopeVisible(q.Scopes, r) {
			continue
		}
		hay := strings.ToLower(resource.IndexText(r, s.contents[r.ID]))
		if strings.Contains(hay, strings.ToLower(q.QueryText)) {
			out = append(out, resource.ScoredResource{Resource: r, Score: 0.5})
		}
	}
	return out, nil
}

// Get reads by id regardless of scope (the provider applies the read check),
// reporting a missing row the way the Postgres store does: a wrapped
// sql.ErrNoRows, not (nil, nil).
func (s *scopedResourceStore) Get(_ context.Context, id string) (*resource.Resource, error) {
	for i := range s.resources {
		if s.resources[i].ID == id {
			r := s.resources[i]
			return &r, nil
		}
	}
	return nil, fmt.Errorf("scanning resource: %w", sql.ErrNoRows)
}

// remove deletes a resource from the corpus, modeling the row DELETE that also
// removes its inline index entry.
func (s *scopedResourceStore) remove(id string) {
	kept := s.resources[:0]
	for _, r := range s.resources {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	s.resources = kept
	delete(s.contents, id)
}

func scopeVisible(scopes []resource.ScopeFilter, r resource.Resource) bool {
	for _, sf := range scopes {
		if sf.Scope == r.Scope && (sf.Scope == resource.ScopeGlobal || sf.ScopeID == r.ScopeID) {
			return true
		}
	}
	return false
}

func seedResourceStore() *scopedResourceStore {
	return &scopedResourceStore{
		contents: map[string]string{
			"res_dict":   "column,description\ngross_margin_pct,margin after COGS\n",
			"res_weekly": weeklyTemplateBody,
		},
		resources: []resource.Resource{
			{
				ID: "res_weekly", Scope: resource.ScopeGlobal, Path: "templates",
				Filename: "weekly-report-template.md", DisplayName: "Weekly Report Template",
				Description: "Approved structure for the weekly report", MIMEType: "text/markdown",
				SizeBytes: int64(len(weeklyTemplateBody)), S3Key: "k-weekly",
				URI: "mcp://global/templates/weekly-report-template.md",
			},
			{
				ID: "res_dict", Scope: resource.ScopeGlobal, Path: "references",
				Filename: "sales-dictionary.csv", DisplayName: "Sales Dictionary",
				Description: "Field reference for the sales extract", MIMEType: "text/csv",
				SizeBytes: 60, S3Key: "k-dict", URI: "mcp://global/references/sales-dictionary.csv",
			},
			{
				ID: "res_alice", Scope: resource.ScopeUser, ScopeID: userAID, Path: "notes",
				Filename: "alice-notes.md", DisplayName: "Alice private margin notes",
				MIMEType: "text/markdown", SizeBytes: 20, S3Key: "k-alice",
				URI: "mcp://user/" + userAID + "/notes/alice-notes.md",
			},
			{
				ID: "res_playbook", Scope: resource.ScopePersona, ScopeID: "analyst", Path: "playbooks",
				Filename: "margin-playbook.md", DisplayName: "Analyst margin playbook",
				MIMEType: "text/markdown", SizeBytes: 20, S3Key: "k-play",
				URI: "mcp://persona/analyst/playbooks/margin-playbook.md",
			},
			{
				ID: "res_logo", Scope: resource.ScopeGlobal, Path: "references",
				Filename: "margin-chart.png", DisplayName: "Margin chart image",
				MIMEType: "image/png", SizeBytes: 4096, S3Key: "k-logo",
				URI: "mcp://global/references/margin-chart.png",
			},
		},
	}
}

func seedResourceBlobs() knowledge.ResourceContentReader {
	return &staticBlobs{objects: map[string][]byte{
		"k-dict":   []byte("column,description\ngross_margin_pct,margin after COGS\n"),
		"k-alice":  []byte("alice private notes body"),
		"k-play":   []byte("analyst playbook body"),
		"k-logo":   {0x89, 'P', 'N', 'G'},
		"k-weekly": []byte(weeklyTemplateBody),
	}}
}

type staticBlobs struct{ objects map[string][]byte }

func (b *staticBlobs) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	body, ok := b.objects[key]
	if !ok {
		return nil, "", fmt.Errorf("no such key: %s", key)
	}
	return body, "", nil
}

// ctxForPersona builds a caller whose ROLES place them in a persona. The
// toolkit resolves membership from roles (see personaOfRole below), which is the
// production path: PersonaName alone is the persona the request resolved to and
// falls back to the default persona, so a test that set only PersonaName would
// not exercise membership at all.
func ctxForPersona(userID, email, persona string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: userID, UserEmail: email, PersonaName: persona, Roles: []string{"role-" + persona},
	})
}

// personaOfRole is the test's stand-in for the platform's role-to-persona
// membership resolver: role "role-analyst" means membership in "analyst". A
// caller with no matching role belongs to no persona, exactly as the real
// resolver reports.
func personaOfRole(roles []string) []string {
	var out []string
	for _, r := range roles {
		if name, ok := strings.CutPrefix(r, "role-"); ok {
			out = append(out, name)
		}
	}
	return out
}

// callSearchRaw is callSearch that also returns the raw tool result, so a test
// can assert on the content blocks (resource links) alongside the JSON body.
func callSearchRaw(ctx context.Context, t *testing.T, tk *Toolkit, intent string) (*mcp.CallToolResult, searchOutput) {
	t.Helper()
	res, _, err := tk.handleSearch(ctx, &mcp.CallToolRequest{}, searchInput{Intent: intent})
	if err != nil {
		t.Fatalf("handleSearch returned transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool reported error: %v", res.Content)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	var out searchOutput
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("decoding output: %v", err)
	}
	return res, out
}

func resourceLinks(res *mcp.CallToolResult) []*mcp.ResourceLink {
	var links []*mcp.ResourceLink
	for _, c := range res.Content {
		if l, ok := c.(*mcp.ResourceLink); ok {
			links = append(links, l)
		}
	}
	return links
}

// AC: uploading a resource and then searching for its topic surfaces it with an
// mcp:resource:<id> reference; fetch on that reference returns the content. The
// search term appears ONLY inside the file, never in the name or description.
func TestResources_ContentSearchToFetchRoundTrip(t *testing.T) {
	tk := assembledToolkit()
	ctx := ctxFor(userAID, userAEmail)

	res, out := callSearchRaw(ctx, t, tk, "gross_margin_pct")
	ref := referenceFor(t, out, knowledge.SourceResources)
	if ref != "mcp:resource:res_dict" {
		t.Fatalf("reference = %q, want mcp:resource:res_dict", ref)
	}

	// AC: the hit carries a resource_link content block with the canonical URI.
	links := resourceLinks(res)
	if len(links) != 1 || links[0].URI != "mcp://global/references/sales-dictionary.csv" {
		t.Fatalf("resource links = %+v", links)
	}
	if links[0].MIMEType != "text/csv" || links[0].Name != "Sales Dictionary" {
		t.Errorf("resource link labels wrong: %+v", links[0])
	}

	got := callFetch(ctx, t, tk, ref)
	if !got.Found || got.Document == nil {
		t.Fatalf("fetch found=false for a live resource reference: %+v", got)
	}
	if !strings.Contains(got.Document.Body, "gross_margin_pct") {
		t.Errorf("fetch did not return the file contents: %q", got.Document.Body)
	}
	if got.Document.Source != knowledge.SourceResources || got.Document.Reference != ref {
		t.Errorf("document provenance wrong: %+v", got.Document)
	}
}

// The weekly-report scenario from #1015: platform_info steers an agent to
// search for an applicable template before formatting a deliverable, and this
// proves the path that instruction points at actually delivers one. Asking the
// discovery front door for "weekly report template" returns the approved
// template, and fetching its reference returns the layout in full, so following
// the instruction is sufficient to produce the report in the approved
// structure without the user pasting anything.
//
// What it does not prove is that a model chooses to follow the instruction;
// that is a behavioral question and belongs to the feature benchmarks (#982).
// This test covers the half that is deterministic: the material is reachable by
// the words a person would use for it.
func TestResources_WeeklyReportTemplateIsReachableByName(t *testing.T) {
	tk := assembledToolkit()
	ctx := ctxFor(userAID, userAEmail)

	res, out := callSearchRaw(ctx, t, tk, "weekly report template")
	ref := referenceFor(t, out, knowledge.SourceResources)
	if ref != "mcp:resource:res_weekly" {
		t.Fatalf("reference = %q, want mcp:resource:res_weekly", ref)
	}

	// A client with native resource support can attach the template directly
	// instead of round-tripping the bytes through the model.
	links := resourceLinks(res)
	if len(links) != 1 || links[0].URI != "mcp://global/templates/weekly-report-template.md" {
		t.Fatalf("resource links = %+v", links)
	}

	got := callFetch(ctx, t, tk, ref)
	if !got.Found || got.Document == nil {
		t.Fatalf("fetch found=false for the approved template: %+v", got)
	}
	// The whole layout, not a summary of it: an agent cannot reproduce a
	// structure it only got the name of.
	if got.Document.Body != weeklyTemplateBody {
		t.Errorf("fetch did not return the template verbatim:\n got: %q\nwant: %q",
			got.Document.Body, weeklyTemplateBody)
	}
}

// AC: a binary resource comes back as metadata plus the canonical URI and size,
// so the agent knows what it is and how to get it.
func TestResources_BinaryFetchReturnsURIAndSize(t *testing.T) {
	tk := assembledToolkit()
	got := callFetch(ctxFor(userAID, userAEmail), t, tk, "mcp:resource:res_logo")
	if !got.Found {
		t.Fatalf("fetch found=false: %+v", got)
	}
	if got.Document.Body != "" {
		t.Errorf("binary content must not be inlined: %q", got.Document.Body)
	}
	// The document content is the resource record; it round-trips through JSON as
	// a map, which is what a client actually receives.
	content, ok := got.Document.Content.(map[string]any)
	if !ok {
		t.Fatalf("content = %T, want the resource record", got.Document.Content)
	}
	if content["uri"] != "mcp://global/references/margin-chart.png" {
		t.Errorf("uri = %v", content["uri"])
	}
	if content["size_bytes"] != float64(4096) {
		t.Errorf("size_bytes = %v", content["size_bytes"])
	}
	if content["mime_type"] != "image/png" {
		t.Errorf("mime_type = %v", content["mime_type"])
	}
}

// AC: a user-scoped resource never appears in another caller's search results,
// and fetch by its reference is a clean not-found rather than content.
func TestResources_UserScopeIsolation(t *testing.T) {
	tk := assembledToolkit()

	_, aliceOut := callSearchRaw(ctxFor(userAID, userAEmail), t, tk, "margin")
	if !hasRef(aliceOut, "mcp:resource:res_alice") {
		t.Fatalf("owner did not see their own resource: %+v", hitsOf(aliceOut))
	}

	_, bobOut := callSearchRaw(ctxFor(userBID, userBEmail), t, tk, "margin")
	if hasRef(bobOut, "mcp:resource:res_alice") {
		t.Fatalf("user-scoped resource leaked into another caller's search: %+v", hitsOf(bobOut))
	}
	if got := callFetch(ctxFor(userBID, userBEmail), t, tk, "mcp:resource:res_alice"); got.Found {
		t.Errorf("user B fetched user A's resource: %+v", got.Document)
	}
	if got := callFetch(context.Background(), t, tk, "mcp:resource:res_alice"); got.Found {
		t.Errorf("anonymous caller fetched a user-scoped resource: %+v", got.Document)
	}
}

// AC: a persona-scoped resource appears only for members of that persona, and
// membership comes from roles — a caller whose persona was merely RESOLVED to
// analyst (the default-persona fallback, no matching role) is not a member and
// must see nothing.
func TestResources_PersonaScope(t *testing.T) {
	tk := assembledToolkit()

	resolvedButNotAMember := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: userBID, UserEmail: userBEmail, PersonaName: "analyst", Roles: []string{"unmatched"},
	})
	_, fallbackOut := callSearchRaw(resolvedButNotAMember, t, tk, "margin")
	if hasRef(fallbackOut, "mcp:resource:res_playbook") {
		t.Fatalf("a caller who belongs to no persona inherited the default persona's material: %+v", hitsOf(fallbackOut))
	}
	if got := callFetch(resolvedButNotAMember, t, tk, "mcp:resource:res_playbook"); got.Found {
		t.Errorf("non-member fetched persona material via the default-persona fallback: %+v", got.Document)
	}

	member := ctxForPersona(userBID, userBEmail, "analyst")
	_, memberOut := callSearchRaw(member, t, tk, "margin")
	if !hasRef(memberOut, "mcp:resource:res_playbook") {
		t.Fatalf("persona member did not see the persona resource: %+v", hitsOf(memberOut))
	}
	if got := callFetch(member, t, tk, "mcp:resource:res_playbook"); !got.Found {
		t.Errorf("persona member could not fetch the persona resource: %+v", got)
	}

	outsider := ctxForPersona(userBID, userBEmail, "engineer")
	_, outsiderOut := callSearchRaw(outsider, t, tk, "margin")
	if hasRef(outsiderOut, "mcp:resource:res_playbook") {
		t.Fatalf("persona resource leaked to a non-member: %+v", hitsOf(outsiderOut))
	}
	if got := callFetch(outsider, t, tk, "mcp:resource:res_playbook"); got.Found {
		t.Errorf("non-member fetched a persona resource: %+v", got.Document)
	}
}

// AC: deleting a resource removes it from the index; a subsequent search returns
// no ghost hit and fetch on the old reference is a structured not-found. The
// index lives on the resource row, so the delete takes it with it.
func TestResources_DeleteLeavesNoGhost(t *testing.T) {
	store := seedResourceStore()
	tk := assembledToolkitWithResources(store)
	ctx := ctxFor(userAID, userAEmail)

	_, before := callSearchRaw(ctx, t, tk, "gross_margin_pct")
	if !hasRef(before, "mcp:resource:res_dict") {
		t.Fatalf("resource was not searchable to begin with: %+v", hitsOf(before))
	}

	store.remove("res_dict")

	res, after := callSearchRaw(ctx, t, tk, "gross_margin_pct")
	if hasRef(after, "mcp:resource:res_dict") {
		t.Errorf("deleted resource still returned as a search hit: %+v", hitsOf(after))
	}
	if links := resourceLinks(res); len(links) != 0 {
		t.Errorf("deleted resource still emitted a resource link: %+v", links)
	}
	if got := callFetch(ctx, t, tk, "mcp:resource:res_dict"); got.Found {
		t.Errorf("deleted resource still fetchable: %+v", got.Document)
	}
}

// An agent only learns a reference form exists from what the tools advertise, so
// the registered tools themselves — description and input schema — must name it.
// Reading the package vars would prove nothing about what the server exposes.
func TestRegisteredToolsAdvertiseResources(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	assembledToolkit().RegisterTools(srv)

	tools := listToolsOver(t, srv)

	fetch, ok := tools[fetchToolName]
	if !ok {
		t.Fatalf("fetch tool not registered; got %v", tools)
	}
	if got := schemaJSON(t, fetch.InputSchema); !strings.Contains(got, "mcp:resource:<id>") {
		t.Errorf("the fetch input schema does not enumerate the mcp:resource:<id> form: %s", got)
	}
	if !strings.Contains(fetch.Description, "resource") {
		t.Errorf("the fetch description does not mention resources: %q", fetch.Description)
	}

	search, ok := tools[toolName]
	if !ok {
		t.Fatalf("search tool not registered; got %v", tools)
	}
	// The source name as it must be typed into `sources`, not a loose word match.
	if !strings.Contains(schemaJSON(t, search.InputSchema), "assets, resources, prompts") {
		t.Error("the search input schema does not list resources among the known sources")
	}
	if !strings.Contains(search.Description, "uploaded reference material") {
		t.Errorf("the search description does not name uploaded reference material: %q", search.Description)
	}
}

// listToolsOver connects a real in-memory client to the server and returns what
// tools/list advertises, keyed by name — the same view a client gets.
func listToolsOver(t *testing.T, srv *mcp.Server) map[string]*mcp.Tool {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()

	errCh := make(chan error, 1)
	go func() {
		ss, err := srv.Connect(ctx, serverTransport, nil)
		if err != nil {
			errCh <- err
			return
		}
		errCh <- ss.Wait()
	}()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	list, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	out := make(map[string]*mcp.Tool, len(list.Tools))
	for _, tool := range list.Tools {
		out[tool.Name] = tool
	}
	return out
}

// schemaJSON renders an advertised input schema back to JSON for substring
// assertions on the descriptions it carries. HTML escaping is off so a literal
// like mcp:resource:<id> reads as itself rather than as \u003cid\u003e.
func schemaJSON(t *testing.T, schema any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(schema); err != nil {
		t.Fatalf("marshaling advertised schema: %v", err)
	}
	return buf.String()
}

func hasRef(out searchOutput, ref string) bool {
	for _, h := range hitsOf(out) {
		if h.Reference == ref {
			return true
		}
	}
	return false
}

// AC (#1584): an administrator's authority reaches the fetch of a file they
// NAME, and reaches nothing about what a search returns.
//
// This is the wiring half of the change: the provider's rule admits an
// administrator only if callerFromContext actually carries IsAdmin off the
// platform context, and a unit test on the provider alone would pass with that
// field dropped on the floor. The request below is built the way the middleware
// builds one, and the two halves are asserted together because the pair is the
// point -- fetch widens, discovery does not.
func TestResources_AdministratorFetchesNamedMaterialButSearchesTheirOwn(t *testing.T) {
	tk := assembledToolkit()

	// An administrator in no persona: PersonaName is admin, and no role places
	// them in the analyst persona the playbook belongs to.
	adminCtx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: userBID, UserEmail: userBEmail, PersonaName: "admin",
		Roles: []string{"admin"}, IsAdmin: true,
	})

	if got := callFetch(adminCtx, t, tk, "mcp:resource:res_playbook"); !got.Found {
		t.Errorf("an administrator could not fetch a persona-library file they named: %+v", got)
	}
	if got := callFetch(adminCtx, t, tk, "mcp:resource:res_alice"); !got.Found {
		t.Errorf("an administrator could not fetch another person's file they named: %+v", got)
	}

	_, adminOut := callSearchRaw(adminCtx, t, tk, "margin")
	if hasRef(adminOut, "mcp:resource:res_playbook") {
		t.Errorf("an administrator's search returned a persona library they are not a member of: %+v", hitsOf(adminOut))
	}
	if hasRef(adminOut, "mcp:resource:res_alice") {
		t.Errorf("an administrator's search returned another person's library: %+v", hitsOf(adminOut))
	}
}
