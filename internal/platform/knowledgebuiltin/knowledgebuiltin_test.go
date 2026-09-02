package knowledgebuiltin

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/platform/instructions"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// The tsvector GIN index refuses an input over 1 MiB; the knowledge-page edit
// surfaces bound bodies at 64 KiB, and the shipped pages must fit the same
// bound they would hold people to.
const maxBuiltinBodyBytes = 64 * 1024

func TestPages_ShippedSetIsWellFormed(t *testing.T) {
	pages, err := Pages()
	require.NoError(t, err)
	require.Len(t, pages, len(pageMetas))

	slugs := map[string]bool{}
	for _, p := range pages {
		assert.Truef(t, strings.HasPrefix(p.Slug, "platform-"),
			"slug %q must carry the platform- prefix so it cannot collide with a deployment's topics", p.Slug)
		assert.Falsef(t, slugs[p.Slug], "duplicate slug %q", p.Slug)
		slugs[p.Slug] = true
		assert.NotEmptyf(t, p.Title, "%s: empty title", p.Slug)
		assert.Falsef(t, strings.HasPrefix(p.Title, "#"), "%s: H1 marker leaked into the title", p.Slug)
		assert.NotEmptyf(t, p.Summary, "%s: empty summary", p.Slug)
		assert.NotEmptyf(t, p.Body, "%s: empty body", p.Slug)
		assert.NotEmptyf(t, p.Tags, "%s: no tags", p.Slug)
		assert.NotContainsf(t, p.Body, dialectPlaceholder, "%s: unsubstituted placeholder", p.Slug)
		assert.NotContainsf(t, p.Body, textTypesPlaceholder, "%s: unsubstituted placeholder", p.Slug)
		assert.NotContainsf(t, p.Body, binaryTypesPlaceholder, "%s: unsubstituted placeholder", p.Slug)
		assert.Falsef(t, strings.HasPrefix(p.Body, "# "), "%s: body still starts with the H1 the title owns", p.Slug)
		assert.LessOrEqualf(t, len(p.Body), maxBuiltinBodyBytes, "%s: body exceeds the edit-surface bound", p.Slug)
		// Every page states its mechanism as a graph as well as in prose: an
		// agent reading the page through fetch gets the fence as a labeled
		// graph rather than a paragraph it has to build one from, and the
		// portal renders it. That the diagrams parse is checked by the
		// renderer's own parser in ui/src/components/renderers/
		// builtinKnowledgeDiagrams.test.ts, which is where mermaid lives.
		assert.Containsf(t, p.Body, "```mermaid", "%s: no diagram", p.Slug)
	}
}

// The authoring page's dialect section is the same constant `manage_script
// help` returns — derived, not restated, so the two cannot drift (#1390).
func TestPages_AuthoringPageCarriesTheDialectContract(t *testing.T) {
	pages, err := Pages()
	require.NoError(t, err)

	var authoring *knowledgepage.BuiltinPage
	for i := range pages {
		if pages[i].Slug == "platform-writing-managed-scripts" {
			authoring = &pages[i]
		}
	}
	require.NotNil(t, authoring, "the authoring page left the shipped set")
	assert.Contains(t, authoring.Body, scriptlayer.DialectContract)
}

func TestSplitTitle(t *testing.T) {
	title, body, err := splitTitle("# A title\n\nThe body.\n")
	require.NoError(t, err)
	assert.Equal(t, "A title", title)
	assert.Equal(t, "The body.\n", body)

	_, _, err = splitTitle("No heading\n")
	require.Error(t, err)
	_, _, err = splitTitle("#    \nbody")
	require.Error(t, err)
}

// fakeReconciler records what Reconcile hands the store. The embedded Store is
// nil: Reconcile must touch nothing but the capability methods.
type fakeReconciler struct {
	knowledgepage.Store
	got        []knowledgepage.BuiltinPage
	err        error
	restored   int
	restoreErr error
}

func (f *fakeReconciler) ReconcileBuiltins(_ context.Context, pages []knowledgepage.BuiltinPage) (knowledgepage.BuiltinReconcileStats, error) {
	f.got = pages
	return knowledgepage.BuiltinReconcileStats{Created: len(pages)}, f.err
}

func (f *fakeReconciler) RestoreHidden(_ context.Context) (int, error) {
	return f.restored, f.restoreErr
}

func TestReconcile_HandsTheShippedSetToTheStore(t *testing.T) {
	store := &fakeReconciler{}
	require.NoError(t, Reconcile(context.Background(), store))
	assert.Len(t, store.got, len(pageMetas))
}

func TestReconcile_PropagatesAStoreFailure(t *testing.T) {
	boom := errors.New("boom")
	err := Reconcile(context.Background(), &fakeReconciler{err: boom})
	require.ErrorIs(t, err, boom)
}

// A store without the capability (any non-postgres Store) is a clean no-op.
func TestReconcile_NoOpWithoutTheCapability(t *testing.T) {
	type bare struct{ knowledgepage.Store }
	require.NoError(t, Reconcile(context.Background(), bare{}))
	require.NoError(t, Reconcile(context.Background(), nil))
}

// Restore un-hides and then reconciles, so a restored page is refreshed to the
// running release rather than resurrected one release stale.
func TestRestore_UnhidesThenReconciles(t *testing.T) {
	store := &fakeReconciler{restored: 2}
	n, err := Restore(context.Background(), store)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Len(t, store.got, len(pageMetas), "the restore must be followed by a reconcile")
}

func TestRestore_PropagatesFailures(t *testing.T) {
	boom := errors.New("boom")
	_, err := Restore(context.Background(), &fakeReconciler{restoreErr: boom})
	require.ErrorIs(t, err, boom)

	// A reconcile failure after a successful un-hide still reports the count.
	n, err := Restore(context.Background(), &fakeReconciler{restored: 1, err: boom})
	require.ErrorIs(t, err, boom)
	assert.Equal(t, 1, n)

	type bare struct{ knowledgepage.Store }
	n, err = Restore(context.Background(), bare{})
	require.NoError(t, err)
	assert.Zero(t, n)
}

// signalingReconciler lets the test wait for Start's background reconcile.
type signalingReconciler struct {
	fakeReconciler
	done chan struct{}
}

func (s *signalingReconciler) ReconcileBuiltins(ctx context.Context, pages []knowledgepage.BuiltinPage) (knowledgepage.BuiltinReconcileStats, error) {
	defer close(s.done)
	return s.fakeReconciler.ReconcileBuiltins(ctx, pages)
}

// Start runs the reconcile in the background and never blocks or panics the
// caller, on success and on failure alike.
func TestStart_RunsTheReconcileInTheBackground(t *testing.T) {
	ok := &signalingReconciler{done: make(chan struct{})}
	Start(context.Background(), ok)
	select {
	case <-ok.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start never ran the reconcile")
	}
	assert.Len(t, ok.got, len(pageMetas))

	failing := &signalingReconciler{done: make(chan struct{})}
	failing.err = errors.New("boom")
	Start(context.Background(), failing)
	select {
	case <-failing.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start never ran the failing reconcile")
	}
}

// Every page `manage_script help` points an author at is a page this package
// ships. The two sets are declared on opposite sides of the import
// (knowledgebuiltin imports scriptlayer for the dialect contract, so the
// reverse import would cycle), and this is the gate that fails when a slug
// help names is removed or renamed here (#1476).
//
// The containment runs one way only. A slug help names and this release does
// not ship resolves to nothing, which is the failure worth a gate; a page
// shipped that help does not name is ordinary, because the shipped set covers
// topics outside script authoring -- content types (#1508) is one -- and
// requiring the two lists to match would push every such page into the script
// author's reading list to satisfy a test.
func TestKnowledgePages_HelpNamesOnlyShippedPages(t *testing.T) {
	shipped := make([]string, 0, len(pageMetas))
	for _, m := range pageMetas {
		shipped = append(shipped, m.slug)
	}
	require.NotEmpty(t, scriptlayer.KnowledgePages, "help names no page at all")
	for _, p := range scriptlayer.KnowledgePages {
		assert.Containsf(t, shipped, p.Slug, "help names %q, which this release does not ship", p.Slug)
		assert.NotEmptyf(t, p.Summary, "%s: help names the page with no summary to choose it by", p.Slug)
		assert.Equalf(t, "mcp:knowledge_page:"+p.Slug, p.Reference,
			"%s: the reference must be the slug in the form fetch takes", p.Slug)
	}
}

// The instruction baseline names pages by slug — the scripts bullet and the
// references bullet both do — which only resolves while those slugs are
// shipped. Every slug it names has to be one of this package's, so a rename
// here cannot leave the baseline pointing at nothing.
func TestBaseline_NamesOnlyShippedSlugs(t *testing.T) {
	shipped := map[string]bool{}
	for _, m := range pageMetas {
		shipped[m.slug] = true
	}

	baseline := instructions.Build([]string{"manage_script", "save_asset", "fetch"})
	named := regexp.MustCompile(`mcp:knowledge_page:([a-z0-9-]+)`).FindAllStringSubmatch(baseline, -1)
	require.NotEmpty(t, named, "the baseline names no page at all")
	for _, m := range named {
		assert.Truef(t, shipped[m[1]], "the baseline names %q, which this release does not ship", m[1])
	}
}

// The content-types page's tables are generated from the tables that decide
// what a door accepts and what extension a stored object carries, so the page
// cannot list a type the code does not handle or omit one it does (#1508).
func TestPages_ContentTypesPageIsGeneratedFromTheCatalog(t *testing.T) {
	pages, err := Pages()
	require.NoError(t, err)

	var page *knowledgepage.BuiltinPage
	for i := range pages {
		if pages[i].Slug == knowledgepage.BuiltinSlugContentTypes {
			page = &pages[i]
		}
	}
	require.NotNil(t, page, "the content-types page left the shipped set")

	for _, ct := range contenttype.StorableTextTypes() {
		assert.Containsf(t, page.Body, "| `"+ct+"` |", "%s is accepted by a door and missing from the page", ct)
	}
	for _, ct := range []string{"image/png", "application/pdf", "video/mp4", "font/woff2"} {
		assert.Containsf(t, page.Body, "| `"+ct+"` |", "%s is stored by the platform and missing from the page", ct)
	}
	assert.NotContains(t, page.Body, "| `"+contenttype.XHTML+"` |",
		"the family every door refuses must not be listed as one to declare")
}

// The two halves of the catalog are rendered into different sections, so a
// type that travels as a string must not appear in the binary table and the
// other way round.
func TestContentTypeTableSplitsTheCatalog(t *testing.T) {
	text, binary := contentTypeTable(true), contentTypeTable(false)

	assert.Contains(t, text, "| `"+contenttype.SVG+"` | `.svg` |")
	assert.NotContains(t, text, "image/png")
	assert.Contains(t, binary, "| `image/png` | `.png` |")
	assert.NotContains(t, binary, contenttype.SVG)
	for _, table := range []string{text, binary} {
		assert.True(t, strings.HasPrefix(table, "| Media type | Extension |\n|---|---|\n"),
			"a table renders its own header, so a page carries no header the code did not write")
		assert.False(t, strings.HasSuffix(table, "\n"),
			"a trailing newline would leave a blank line inside the page's markdown")
	}
}

// A page that points at another page names it by slug, which resolves only
// while that slug is shipped. Nothing compiles a page body, so this is the
// gate that catches a cross-link left behind by a rename.
func TestPages_CrossLinksNameShippedSlugs(t *testing.T) {
	pages, err := Pages()
	require.NoError(t, err)

	shipped := map[string]bool{}
	for _, p := range pages {
		shipped[p.Slug] = true
	}

	named := regexp.MustCompile(`mcp:knowledge_page:([a-z0-9-]+)`)
	links := 0
	for _, p := range pages {
		for _, m := range named.FindAllStringSubmatch(p.Body, -1) {
			links++
			assert.Truef(t, shipped[m[1]], "%s links to %q, which this release does not ship", p.Slug, m[1])
		}
	}
	require.NotZero(t, links, "no page links to another, so this gate is asserting nothing")
}

// TestBaselinePagesAreShipped binds the instruction baseline's page index to the
// pages this binary carries. The baseline names pages by slug because a built-in
// page's row id is generated per deployment at reconcile time (#1476), and a
// slug is a string on both sides: renaming a page here while the baseline keeps
// the old name leaves every agent pointed at a reference that resolves to
// nothing, and each side's own tests would still pass because each asserts its
// own literal. This is the only check that fails when they disagree.
func TestBaselinePagesAreShipped(t *testing.T) {
	pages, err := Pages()
	require.NoError(t, err)

	shipped := make(map[string]bool, len(pages))
	for _, p := range pages {
		shipped[p.Slug] = true
	}

	// Every tool the baseline gates a page on, so the rendered index carries all
	// of them at once.
	baseline := instructions.Build([]string{
		"search", "fetch", "memory_capture",
		"apply_knowledge", "manage_prompt", "trino_query", "manage_script", "save_asset",
	})

	named := knowledgePageRefs(baseline)
	assert.NotEmpty(t, named, "the baseline names no pages; the index has gone missing")
	for _, slug := range named {
		assert.Truef(t, shipped[slug],
			"the instruction baseline points agents at %q, which this binary does not ship", slug)
	}

	// And the other direction: a page shipped but named by nothing is guidance
	// no agent is told exists.
	for _, p := range pages {
		assert.Containsf(t, named, p.Slug,
			"page %q ships but the instruction baseline never names it, so no agent knows it exists", p.Slug)
	}
}

// knowledgePageRefs extracts the built-in page slugs an instruction text names.
func knowledgePageRefs(text string) []string {
	const marker = "mcp:knowledge_page:"
	var slugs []string
	for rest := text; ; {
		i := strings.Index(rest, marker)
		if i < 0 {
			return slugs
		}
		rest = rest[i+len(marker):]
		end := strings.IndexAny(rest, "` \n")
		if end < 0 {
			end = len(rest)
		}
		slugs = append(slugs, rest[:end])
	}
}
