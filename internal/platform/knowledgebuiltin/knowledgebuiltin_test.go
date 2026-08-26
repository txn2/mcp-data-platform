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

// The pages `manage_script help` points an author at are the pages this
// package ships. The two sets are declared on opposite sides of the import
// (knowledgebuiltin imports scriptlayer for the dialect contract, so the
// reverse import would cycle), and this is the gate that fails when a slug is
// added, removed, or renamed on either side (#1476).
func TestKnowledgePages_HelpAndTheShippedSetAgree(t *testing.T) {
	shipped := make([]string, 0, len(pageMetas))
	for _, m := range pageMetas {
		shipped = append(shipped, m.slug)
	}
	named := make([]string, 0, len(scriptlayer.KnowledgePages))
	for _, p := range scriptlayer.KnowledgePages {
		named = append(named, p.Slug)
		assert.NotEmptyf(t, p.Summary, "%s: help names the page with no summary to choose it by", p.Slug)
		assert.Equalf(t, "mcp:knowledge_page:"+p.Slug, p.Reference,
			"%s: the reference must be the slug in the form fetch takes", p.Slug)
	}
	assert.ElementsMatch(t, shipped, named)
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
