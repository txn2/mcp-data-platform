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

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

//go:embed pages/*.md
var pagesFS embed.FS

// dialectPlaceholder marks where a page body receives scriptlayer's dialect
// contract — the same constant `manage_script help` returns — so the page and
// the help text cannot drift apart.
const dialectPlaceholder = "{{DIALECT_CONTRACT}}"

// pageMeta is the shipped metadata of one page. The title is the body file's
// leading H1 (one source for it); slug, summary, and tags live here so they
// are compile-checked against the file list.
type pageMeta struct {
	file    string
	slug    string
	summary string
	tags    []string
}

// pageMetas is the shipped set. A slug is the page's reconcile key across
// releases: renaming one abandons the operator state (hide) held under the old
// slug and prunes the old page, so treat slugs as stable identifiers.
var pageMetas = []pageMeta{
	{
		file:    "writing-managed-scripts.md",
		slug:    "platform-writing-managed-scripts",
		summary: "How to write a managed script: the Starlark dialect's deliberate absences, DECIMAL columns arriving as strings, the validate/dry-run loop, and what a save makes runnable.",
		tags:    []string{"scripts", "starlark", "authoring"},
	},
	{
		file:    "script-outputs-and-export-identity.md",
		slug:    "platform-script-outputs-and-export-identity",
		summary: "Where a script's output lands and what identity it keeps: one (script, output name) pair is one versioned asset, a stable name is a refresh, a dated name is an archive, and the zero-data guard.",
		tags:    []string{"scripts", "exports", "assets"},
	},
	{
		file:    "semi-dynamic-dashboards.md",
		slug:    "platform-semi-dynamic-dashboards",
		summary: "One dashboard asset whose numbers a scheduled script refreshes: the template stays in the asset, the data in the script, and platform.publish_data splices the id=\"data\" region.",
		tags:    []string{"scripts", "dashboards", "assets"},
	},
	{
		file:    "provenance-and-the-capture-loop.md",
		slug:    "platform-provenance-and-the-capture-loop",
		summary: "Naming sources with call references so an asset's provenance is exact, what a capture holds, and the loop that turns session knowledge into reviewed catalog knowledge.",
		tags:    []string{"provenance", "knowledge", "memory"},
	},
}

// Pages returns the shipped set, bodies loaded from the embedded files with
// the dialect contract substituted. It fails only on a malformed build (a
// missing file or H1), which the tests catch before a release does.
func Pages() ([]knowledgepage.BuiltinPage, error) {
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
		pages = append(pages, knowledgepage.BuiltinPage{
			Slug: m.slug, Title: title, Summary: m.summary, Body: body, Tags: m.tags,
		})
	}
	return pages, nil
}

// splitTitle takes the file's leading H1 as the page title and returns the
// rest as the body, so the title is written once: the portal renders the
// stored title as the page header, and an H1 left in the body would render it
// twice.
func splitTitle(raw string) (title, body string, err error) {
	first, rest, _ := strings.Cut(raw, "\n")
	title = strings.TrimSpace(strings.TrimPrefix(first, "# "))
	if !strings.HasPrefix(first, "# ") || title == "" {
		return "", "", fmt.Errorf("page must start with an H1 title line, got %q", first)
	}
	return title, strings.TrimLeft(rest, "\n"), nil
}

// Start runs Reconcile in the background, logging a failure instead of
// returning it: the reconcile is a startup job that must never block or fail a
// boot — a deployment that cannot reconcile still serves, one release staler.
// It is called by the composition root (internal/server), not by pkg/platform,
// whose size budget is at its cap.
func Start(ctx context.Context, store knowledgepage.Store) {
	go func() {
		if err := Reconcile(ctx, store); err != nil {
			slog.Warn("built-in knowledge pages not reconciled", "error", err)
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
func Reconcile(ctx context.Context, store knowledgepage.Store) error {
	reconciler, ok := store.(knowledgepage.BuiltinReconciler)
	if !ok {
		return nil
	}
	pages, err := Pages()
	if err != nil {
		return err
	}
	stats, err := reconciler.ReconcileBuiltins(ctx, pages)
	if err != nil {
		return fmt.Errorf("knowledgebuiltin: reconciling built-in knowledge pages: %w", err)
	}
	slog.Info("built-in knowledge pages reconciled",
		"shipped", len(pages), "created", stats.Created, "updated", stats.Updated,
		"skipped", stats.Skipped, "pruned", stats.Pruned)
	return nil
}

// Restore is the way back from hiding: it un-hides the operator-hidden
// built-in pages and immediately reconciles, so a restored page carries the
// running release's content (and a restored page this release no longer ships
// is pruned right back). It returns how many pages came back. A store without
// the capability restores nothing.
func Restore(ctx context.Context, store knowledgepage.Store) (int, error) {
	reconciler, ok := store.(knowledgepage.BuiltinReconciler)
	if !ok {
		return 0, nil
	}
	restored, err := reconciler.RestoreHidden(ctx)
	if err != nil {
		return 0, fmt.Errorf("knowledgebuiltin: restoring hidden built-in pages: %w", err)
	}
	if err := Reconcile(ctx, store); err != nil {
		return restored, err
	}
	return restored, nil
}
