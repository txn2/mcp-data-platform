// Package knowledgebuiltin ships the platform's own knowledge pages: the
// rationale behind the advanced features that is not readable from tool
// schemas (the managed-script dialect and its gotchas, export identity, the
// semi-dynamic dashboard pattern, provenance and the capture loop), embedded
// in the binary and reconciled into the knowledge-page store at startup
// (#1390), so a release that changes them updates every deployment on its next
// start.
//
// Once reconciled they are ordinary rows: the indexjobs consumer embeds them
// in chunks like any other page, search ranks them in the knowledge_pages
// source, fetch dereferences them, and the portal renders them. The builtin
// marker makes them read-only where people edit; an operator hides one by
// deleting it, and the reconcile respects that instead of resurrecting it (see
// knowledgepage.BuiltinReconciler for the exact per-slug rules).
//
// It is an internal/platform seam, following utilconn: composition the
// platform facade wires with one call, never a Platform field or method (the
// god-object budget is frozen, #854).
package knowledgebuiltin

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/maps"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptexamples"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

//go:embed pages/*.md
var pagesFS embed.FS

// dialectPlaceholder marks where a page body receives scriptlayer's dialect
// contract — the same constant `manage_script help` returns — so the page and
// the help text cannot drift apart. The content-types page takes its tables
// the same way; see contenttypes.go.
const dialectPlaceholder = "{{DIALECT_CONTRACT}}"

// referencePlaceholder marks where the reference script page receives the
// built-in example it shows, so the page shows the script the gates hold to
// the rules rather than a copy of it.
const referencePlaceholder = "{{REFERENCE_SCRIPT}}"

// referenceScript is the reference example's source, without its final
// newline, for the fenced block the placeholder sits in.
func referenceScript() string {
	ex, _ := scriptexamples.Lookup(scriptexamples.ReferenceName)
	return strings.TrimRight(ex.Source, newline)
}

// indexPlaceholder marks where the index page receives its list of every
// other shipped page, generated from pageMetas so it cannot leave one out.
const indexPlaceholder = "{{PAGE_INDEX}}"

// newline separates a page body's lines.
const newline = "\n"

// indexSlug is the slug of the page that lists every other built-in page.
const indexSlug = "platform-index"

// The slugs of the shipped pages, which pageMetas names them by and which
// the pages' related lists cite.
const (
	slugWriting       = "platform-writing-managed-scripts"
	slugDialect       = "platform-managed-script-dialect"
	slugReference     = "platform-reference-script"
	slugOutputs       = "platform-script-outputs-and-export-identity"
	slugDashboards    = "platform-semi-dynamic-dashboards"
	slugAssetRefs     = "platform-asset-references-and-the-refresh-loop"
	slugProvenance    = "platform-provenance-and-the-capture-loop"
	slugFitScreen     = "platform-documents-that-fit-any-screen"
	slugPresentations = "platform-presentations"
	slugMaps          = "platform-maps"
	slugVisuals       = "platform-visual-libraries"
	slugContentTypes  = knowledgepage.BuiltinSlugContentTypes
)

// pageMeta is the shipped metadata of one page. The title is the body file's
// leading H1 (one source for it); slug, summary, and tags live here so they
// are compile-checked against the file list. related names the pages a reader
// of this one goes to next; each is cited at the foot of the page, beside the
// index, so the built-in pages form a graph rather than eleven islands
// (#2200).
type pageMeta struct {
	file    string
	slug    string
	summary string
	tags    []string
	related []string
}

// pageMetas is the shipped set. A slug is the page's reconcile key across
// releases: renaming one abandons the operator state (hide) held under the old
// slug and prunes the old page, so treat slugs as stable identifiers.
var pageMetas = []pageMeta{
	{
		file:    "index.md",
		slug:    indexSlug,
		summary: "Every page the platform ships about itself, each with a one-line summary: scripts, outputs, dashboards, references, provenance, layout, presentations, maps and content types.",
		tags:    []string{"platform", "reference", "index"},
	},
	{
		file:    "writing-managed-scripts.md",
		slug:    slugWriting,
		summary: "How to build an automation, which the platform builds as a managed script: the Starlark dialect's deliberate absences, the tools a script can call and the persona that decides them, DECIMAL columns arriving as strings, the validate/dry-run loop, and what a save makes runnable.",
		tags:    []string{"scripts", "automation", "starlark", "authoring"},
		related: []string{slugDialect, slugReference, slugOutputs, slugDashboards},
	},
	{
		file:    "managed-script-dialect.md",
		slug:    slugDialect,
		summary: "The managed-script dialect contract, the text manage_script help returns: every module, member and argument a script has, and every rule a save holds it to.",
		tags:    []string{"scripts", "automation", "starlark", "reference"},
		related: []string{slugWriting, slugReference},
	},
	{
		file:    "reference-script.md",
		slug:    slugReference,
		summary: "A whole automation written as a saved one is: helpers with docstrings, the work in main(), a pinned date window, a watermark in state, bound SQL, a failure that says why, an export and a result, and the tests it is saved with, with how each test is recorded and written.",
		tags:    []string{"scripts", "automation", "tests", "authoring", "example"},
		related: []string{slugWriting, slugDialect, slugOutputs},
	},
	{
		file:    "script-outputs-and-export-identity.md",
		slug:    slugOutputs,
		summary: "Where a script's output lands and what identity it keeps: one (script, output name) pair is one versioned asset, a stable name is a refresh, a dated name is an archive, and the zero-data guard.",
		tags:    []string{"scripts", "exports", "assets"},
		related: []string{slugWriting, slugDashboards, slugAssetRefs, slugProvenance},
	},
	{
		file:    "semi-dynamic-dashboards.md",
		slug:    slugDashboards,
		summary: "Choosing between composing a whole document every run and publishing one document whose data region a scheduled script refreshes: what each costs, and how platform.publish_data splices the id=\"data\" region.",
		tags:    []string{"scripts", "dashboards", "assets"},
		related: []string{slugOutputs, slugAssetRefs, slugFitScreen, slugVisuals},
	},
	{
		file:    "asset-references-and-the-refresh-loop.md",
		slug:    slugAssetRefs,
		summary: "Naming a file from an asset's content instead of carrying it: the two reference forms, the logo and load-the-data patterns an HTML or JSX document uses, refreshing the file without re-saving the document, why a referencing version is not an as-of snapshot, and who can load the file once it is declared.",
		tags:    []string{"assets", "resources", "references", "dashboards"},
		related: []string{slugDashboards, slugContentTypes, slugFitScreen, slugPresentations, slugMaps},
	},
	{
		file:    "provenance-and-the-capture-loop.md",
		slug:    slugProvenance,
		summary: "Naming sources with call references so an asset's provenance is exact, what a capture holds, and the loop that turns session knowledge into reviewed catalog knowledge.",
		tags:    []string{"provenance", "knowledge", "memory"},
		related: []string{slugOutputs, slugAssetRefs, slugWriting},
	},
	{
		file:    "documents-that-fit-any-screen.md",
		slug:    slugFitScreen,
		summary: "Writing a stored document that reflows into the reader's screen rather than scrolling sideways: which content type is framed by what, the viewport declaration an HTML asset owes, rows that wrap, the three things allowed their own scroll box, and the five defects to look for before saving.",
		tags:    []string{"assets", "authoring", "layout"},
		related: []string{slugPresentations, slugMaps, slugAssetRefs, slugContentTypes},
	},
	{
		file:    "presentations.md",
		slug:    slugPresentations,
		summary: "Building a presentation as an HTML asset on the reveal.js runtime the platform serves: the paths a deck names and why they are same-origin, the document skeleton, one slide per section with vertical detail, Markdown slides, brand by reference, what the Present control and the thumbnail do with it, and what the portal frame does not allow.",
		tags:    []string{"assets", "authoring", "presentations", "slides"},
		related: []string{slugFitScreen, slugVisuals, slugAssetRefs, slugContentTypes},
	},
	{
		file:    "maps.md",
		slug:    slugMaps,
		summary: "Building a map as an HTML asset on the map runtime and basemap this deployment serves: which regions are ready and where each archive is, the document skeleton with light and dark styles, drawing points, a route and areas from GeoJSON, fitting the view, the attribution that stays on, what to do where no region covers the data, and state and county maps that need no basemap.",
		tags:    []string{"assets", "authoring", "maps", "geospatial"},
		related: []string{slugFitScreen, slugVisuals, slugContentTypes, slugAssetRefs},
	},
	{
		file:    "visual-libraries.md",
		slug:    slugVisuals,
		summary: "Drawing a chart, flow, mind map, roadmap, network, graph, sketch or animation with a library loaded from a public CDN instead of hand-drawn SVG: Vega-Lite, Mermaid, Markmap, d3-sankey, Frappe Gantt, Cytoscape.js, Graphviz, Rough.js and dotLottie at pinned versions, a skeleton for each that follows light and dark and fits the frame, their use on a slide and in a JSX asset.",
		tags:    []string{"assets", "authoring", "charts", "diagrams", "animation"},
		related: []string{slugPresentations, slugDashboards, slugFitScreen, slugAssetRefs},
	},
	{
		file:    "content-types-for-stored-files.md",
		slug:    slugContentTypes,
		summary: "The media type every stored file carries and why a write declares it: the families detection cannot name from bytes, the types to declare for text and for binary content, and what a replacement keeps.",
		tags:    []string{"resources", "assets", "content-types"},
		related: []string{slugAssetRefs, slugFitScreen},
	},
}

// PagesWith returns the shipped set, bodies loaded from the embedded files with
// the dialect contract substituted and the maps page naming the basemap
// regions state holds, or saying they could not be read when state is nil.
// It fails only on a malformed build (a missing file or
// H1), which the tests catch before a release does.
func PagesWith(state *maps.State) ([]knowledgepage.BuiltinPage, error) {
	pages := make([]knowledgepage.BuiltinPage, 0, len(pageMetas))
	for _, m := range pageMetas {
		raw, err := pagesFS.ReadFile("pages/" + m.file)
		if err != nil {
			return nil, fmt.Errorf("knowledgebuiltin: reading %s: %w", m.file, err)
		}
		title, body, err := splitTitle(string(raw))
		if err != nil {
			return nil, fmt.Errorf("knowledgebuiltin: %s: %w", m.file, err)
		}
		body = strings.ReplaceAll(body, dialectPlaceholder, scriptlayer.DialectContract)
		body = strings.ReplaceAll(body, referencePlaceholder, referenceScript())
		body = strings.ReplaceAll(body, textTypesPlaceholder, contentTypeTable(true))
		body = strings.ReplaceAll(body, binaryTypesPlaceholder, contentTypeTable(false))
		body = strings.ReplaceAll(body, mapRegionsPlaceholder, mapRegionsSection(state))
		body = strings.ReplaceAll(body, indexPlaceholder, pageIndex())
		body += relatedSection(m)
		pages = append(pages, knowledgepage.BuiltinPage{
			Slug: m.slug, Title: title, Summary: m.summary, Body: body, Tags: m.tags,
		})
	}
	return pages, nil
}

// pageIndex is the index page's list: every other shipped page cited by its
// reference, with its summary.
func pageIndex() string {
	lines := make([]string, 0, len(pageMetas))
	for _, m := range pageMetas {
		if m.slug == indexSlug {
			continue
		}
		lines = append(lines, "- "+knowledgepage.BuiltinReference(m.slug)+": "+m.summary)
	}
	return strings.Join(lines, newline)
}

// relatedSection is the foot of a page: the pages its meta names as related,
// then the index, each cited outside a code span so the body scan stores it
// as a reference and the portal renders it as a link. The index itself has
// none; its body already cites every page.
func relatedSection(m pageMeta) string {
	if m.slug == indexSlug {
		return ""
	}
	lines := make([]string, 0, len(m.related))
	for _, slug := range m.related {
		lines = append(lines, "- "+knowledgepage.BuiltinReference(slug))
	}
	lines = append(lines, "- "+knowledgepage.BuiltinReference(indexSlug)+", every page the platform ships")
	return "\n\n## Related pages\n\n" + strings.Join(lines, newline) + newline
}

// splitTitle takes the file's leading H1 as the page title and returns the
// rest as the body, so the title is written once: the portal renders the
// stored title as the page header, and an H1 left in the body would render it
// twice.
func splitTitle(raw string) (title, body string, err error) {
	first, rest, _ := strings.Cut(raw, newline)
	title = strings.TrimSpace(strings.TrimPrefix(first, "# "))
	if !strings.HasPrefix(first, "# ") || title == "" {
		return "", "", fmt.Errorf("page must start with an H1 title line, got %q", first)
	}
	return title, strings.TrimLeft(rest, newline), nil
}

// Start runs Reconcile in the background, logging a failure instead of
// returning it: the reconcile is a startup job that must never block or fail a
// boot — a deployment that cannot reconcile still serves, one release staler.
// It is called by the composition root (internal/server), not by pkg/platform,
// whose size budget is at its cap.
func Start(ctx context.Context, store knowledgepage.Store, ms MapState) {
	go func() {
		if err := Reconcile(ctx, store, ms); err != nil {
			slog.WarnContext(ctx, "built-in knowledge pages not reconciled", "error", err)
		}
	}()
}

// Reconcile upserts the shipped pages into the store and prunes builtin rows
// whose slug left the shipped set. A store without the capability — a nil
// store included — is a clean no-op.
//
// The reconcile converges on whatever binary started last: it has no notion of
// release ordering, exactly like the prompt static ingest and the embedded
// API-spec seeds. During a staggered upgrade, replicas restarting on different
// binaries can therefore rewrite a changed page back and forth until the fleet
// converges — transient version-history entries and re-embeds, bounded by the
// upgrade window, and accepted rather than paid for with a stored release
// counter no other reconciled content carries.
func Reconcile(ctx context.Context, store knowledgepage.Store, ms MapState) error {
	reconciler, ok := store.(knowledgepage.BuiltinReconciler)
	if !ok {
		return nil
	}
	pages, err := PagesWith(mapStateOf(ctx, ms))
	if err != nil {
		return err
	}
	stats, err := reconciler.ReconcileBuiltins(ctx, pages)
	if err != nil {
		return fmt.Errorf("knowledgebuiltin: reconciling built-in knowledge pages: %w", err)
	}
	slog.InfoContext(ctx, "built-in knowledge pages reconciled",
		"shipped", len(pages), "created", stats.Created, "updated", stats.Updated,
		"skipped", stats.Skipped, "pruned", stats.Pruned)
	return nil
}

// Restore is the way back from hiding: it un-hides the operator-hidden
// built-in pages and immediately reconciles, so a restored page carries the
// running release's content (and a restored page this release no longer ships
// is pruned right back). It returns how many pages came back. A store without
// the capability restores nothing.
func Restore(ctx context.Context, store knowledgepage.Store, ms MapState) (int, error) {
	reconciler, ok := store.(knowledgepage.BuiltinReconciler)
	if !ok {
		return 0, nil
	}
	restored, err := reconciler.RestoreHidden(ctx)
	if err != nil {
		return 0, fmt.Errorf("knowledgebuiltin: restoring hidden built-in pages: %w", err)
	}
	if err := Reconcile(ctx, store, ms); err != nil {
		return restored, err
	}
	return restored, nil
}
