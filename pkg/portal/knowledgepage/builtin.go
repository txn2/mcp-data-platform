package knowledgepage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/lib/pq"
)

// builtinAuthor is the created_by/updated_by every builtin write carries, so a
// reader can tell a release wrote the page rather than a person.
const builtinAuthor = "platform"

// BuiltinSlugContentTypes is the slug of the shipped page listing the media
// types the platform stores and why a write has to declare one (#1508).
//
// It is declared here, rather than beside the page's own body, because two
// packages that cannot import each other both name it: the page is shipped by
// internal/platform/knowledgebuiltin, and the portal toolkit points a caller
// at it from the manage_resource schema and from the refusal a create with no
// content_type gets. A slug is a page's reconcile key across releases, so a
// pointer to a slug this release does not ship resolves to nothing.
const BuiltinSlugContentTypes = "platform-content-types-for-stored-files"

// BuiltinReference renders a built-in page's slug as the reference fetch
// takes, which is the only handle shipped text can name: a built-in page's row
// id is generated per deployment at reconcile time.
func BuiltinReference(slug string) string {
	return mcpScheme + RefTargetKnowledgePage + refKeySep + slug
}

// BuiltinPage is one platform-shipped knowledge page: content embedded in the
// binary and reconciled into the store at startup (#1390), keyed by Slug.
type BuiltinPage struct {
	Slug    string
	Title   string
	Summary string
	Body    string
	Tags    []string
}

// BuiltinReconcileStats reports what one reconcile pass did, for the startup
// log. Skipped counts the shipped pages the pass deliberately left alone: the
// unchanged, the operator-hidden (soft-deleted builtin row), and the
// operator-superseded (a live non-builtin page holds the slug).
type BuiltinReconcileStats struct {
	Created int
	Updated int
	Skipped int
	Pruned  int
}

// BuiltinReconciler is the optional store capability behind the startup
// reconcile of platform-shipped pages. It is not part of Store — only the
// postgres store hosts builtin rows — so callers type-assert it, the same
// shape as Searcher and DuplicateProber.
type BuiltinReconciler interface {
	// ReconcileBuiltins upserts the shipped set and prunes builtin rows whose
	// slug left it. Per shipped page: a live builtin row is updated only when
	// its content differs from the shipped content (which is exact, because
	// Update refuses builtin rows, so nothing else writes one); a live
	// non-builtin row on the slug means the operator superseded the topic and
	// is left alone; a soft-deleted builtin row means the operator hid the
	// page and it is not resurrected; otherwise the page is inserted. A
	// content update flows through the same invalidation an ordinary edit
	// does, so the page is re-embedded and re-versioned. Page-level failures
	// are joined, not short-circuited: one bad page does not block the rest.
	ReconcileBuiltins(ctx context.Context, pages []BuiltinPage) (BuiltinReconcileStats, error)

	// RestoreHidden un-hides every operator-hidden builtin page (a builtin
	// tombstone still holding its slug) whose slug no live page occupies, and
	// returns how many came back. It is the way back from Hide, so hiding is
	// never a one-way door; the caller follows it with ReconcileBuiltins so a
	// restored page is refreshed to the running release (and pruned again if
	// the release no longer ships it).
	RestoreHidden(ctx context.Context) (int, error)
}

var _ BuiltinReconciler = (*postgresStore)(nil)

// ReconcileBuiltins implements BuiltinReconciler.
func (s *postgresStore) ReconcileBuiltins(ctx context.Context, pages []BuiltinPage) (BuiltinReconcileStats, error) {
	var (
		stats BuiltinReconcileStats
		errs  []error
	)
	slugs := make([]string, 0, len(pages))
	for _, p := range pages {
		slugs = append(slugs, p.Slug)
		outcome, err := s.reconcileBuiltinPage(ctx, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("builtin page %q: %w", p.Slug, err))
			continue
		}
		switch outcome {
		case builtinCreated:
			stats.Created++
		case builtinUpdated:
			stats.Updated++
		case builtinSkipped:
			stats.Skipped++
		}
	}
	pruned, err := s.pruneBuiltinPages(ctx, slugs)
	if err != nil {
		errs = append(errs, err)
	}
	stats.Pruned = pruned
	if joined := errors.Join(errs...); joined != nil {
		return stats, fmt.Errorf("reconciling builtin knowledge pages: %w", joined)
	}
	return stats, nil
}

// builtinOutcome is what reconcileBuiltinPage did with one shipped page.
type builtinOutcome int

const (
	builtinSkipped builtinOutcome = iota
	builtinCreated
	builtinUpdated
)

// reconcileBuiltinPage applies one shipped page under one transaction, locking
// the slug's live row so a concurrent edit or delete cannot interleave with
// the decision. The insert races only with another replica's identical
// reconcile, which the slug unique index resolves (ON CONFLICT DO NOTHING).
func (s *postgresStore) reconcileBuiltinPage(ctx context.Context, p BuiltinPage) (builtinOutcome, error) {
	if p.Tags == nil {
		p.Tags = []string{}
	}
	tagsJSON, err := json.Marshal(p.Tags)
	if err != nil {
		return builtinSkipped, fmt.Errorf("marshaling tags: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return builtinSkipped, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	w, err := applyBuiltinDecision(ctx, tx, p, tagsJSON)
	if err != nil {
		return builtinSkipped, err
	}
	if err := tx.Commit(); err != nil {
		return builtinSkipped, fmt.Errorf("committing builtin page reconcile: %w", err)
	}
	// After the commit, matching Insert/Update: an index job claimed before the
	// commit would stamp the pre-write text as current.
	if w.notify {
		s.index.NotifyWrite(ctx, w.pageID)
	}
	// A written body owes the same inline-reference derivation every other
	// body-write surface performs (the portal handler's reconcileInlineRefs,
	// the promotion's writePageRefs), so a builtin page citing an entity in
	// prose joins the reference graph like any operator page. Targets this
	// deployment does not have are dropped, not fatal, matching the promotion
	// path; the reconcile is per-boot, so a failure here retries next start.
	if w.pageID != "" {
		if err := s.replaceBuiltinInlineRefs(ctx, w.pageID, p.Body); err != nil {
			return w.outcome, err
		}
	}
	return w.outcome, nil
}

// replaceBuiltinInlineRefs derives a builtin page's source=inline references
// from its body, filtered to targets that exist on this deployment.
func (s *postgresStore) replaceBuiltinInlineRefs(ctx context.Context, pageID, body string) error {
	refs := ScanBodyRefs(body)
	for i := range refs {
		refs[i].CreatedBy = builtinAuthor
	}
	refs, err := s.FilterExistingRefTargets(ctx, refs)
	if err != nil {
		return err
	}
	return s.ReplaceEntityRefsBySource(ctx, pageID, RefSourceInline, refs)
}

// builtinWrite is what applyBuiltinDecision did: the outcome, the page it
// wrote (empty when it wrote nothing), and whether the indexed text moved and
// the page owes a re-embed.
type builtinWrite struct {
	outcome builtinOutcome
	pageID  string
	notify  bool
}

// applyBuiltinDecision resolves the slug's live row and dispatches: an
// operator page wins the slug, a builtin row is updated when changed, an
// absent row is inserted (unless the operator's tombstone hides it).
func applyBuiltinDecision(ctx context.Context, tx *sql.Tx, p BuiltinPage, tagsJSON []byte) (builtinWrite, error) {
	live, err := lockLiveBySlug(ctx, tx, p.Slug)
	if err != nil {
		return builtinWrite{}, err
	}
	switch {
	case live != nil && !live.builtin:
		// The operator superseded the topic with their own page; theirs wins.
		return builtinWrite{outcome: builtinSkipped}, nil
	case live != nil:
		return updateBuiltinPage(ctx, tx, *live, p, tagsJSON)
	default:
		return insertBuiltinPage(ctx, tx, p, tagsJSON)
	}
}

// liveSlugRow is the live row lockLiveBySlug found for a shipped page's slug.
type liveSlugRow struct {
	id      string
	builtin bool
	content pageContent
	version int
}

// lockLiveBySlug locks the live row holding slug FOR UPDATE and returns it, or
// (nil, nil) when no live row holds the slug.
func lockLiveBySlug(ctx context.Context, tx *sql.Tx, slug string) (*liveSlugRow, error) {
	const q = `SELECT id, builtin, title, summary, body, tags, current_version
		FROM portal_knowledge_pages WHERE slug = $1 AND deleted_at IS NULL FOR UPDATE`
	var row liveSlugRow
	err := tx.QueryRowContext(ctx, q, slug).Scan(
		&row.id, &row.builtin, &row.content.title, &row.content.summary,
		&row.content.body, &row.content.tagsJSON, &row.version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // no live row is a normal outcome, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("locking builtin page slug: %w", err)
	}
	return &row, nil
}

// updateBuiltinPage rewrites a live builtin row whose content differs from the
// shipped content, through the same invalidation an ordinary edit takes
// (applyPageUpdate clears the index marker and chunks; a version row records
// the release change). Unchanged content touches nothing. Tags are compared as
// decoded values, never as JSON text: jsonb re-serializes with its own
// whitespace (`["a", "b"]` for a stored `["a","b"]`), so a byte comparison
// would read every multi-tag page as changed on every boot.
func updateBuiltinPage(ctx context.Context, tx *sql.Tx, live liveSlugRow, p BuiltinPage, tagsJSON []byte) (builtinWrite, error) {
	var liveTags []string
	if err := unmarshalTags(live.content.tagsJSON, &liveTags); err != nil {
		return builtinWrite{}, err
	}
	if live.content.title == p.Title && live.content.summary == p.Summary &&
		live.content.body == p.Body && slices.Equal(liveTags, p.Tags) {
		return builtinWrite{outcome: builtinSkipped}, nil
	}
	content := pageContent{title: p.Title, summary: p.Summary, body: p.Body, tags: p.Tags, tagsJSON: tagsJSON}
	next := live.version + 1
	// The re-embed is owed only when the indexed text (title/body/tags — what
	// IndexText composes) moved, the same rule pageContent.merge applies: a
	// release that reworded only a summary must not drop the fleet's embedding
	// chunks to recompute an identical vector.
	indexedChanged := live.content.title != p.Title || live.content.body != p.Body ||
		!slices.Equal(liveTags, p.Tags)
	if err := applyPageUpdate(ctx, tx, pageUpdateRow{
		id: live.id, content: content, updatedBy: builtinAuthor,
		nextVersion: next, indexedChanged: indexedChanged,
	}); err != nil {
		return builtinWrite{}, err
	}
	if err := insertPageVersion(ctx, tx, pageVersionRow{
		pageID: live.id, version: next, content: content,
		createdBy: builtinAuthor, changeSummary: "Updated by platform release",
	}); err != nil {
		return builtinWrite{}, err
	}
	return builtinWrite{outcome: builtinUpdated, pageID: live.id, notify: indexedChanged}, nil
}

// insertBuiltinPage creates the shipped page, unless a soft-deleted builtin
// row holds the slug — the operator hid the page, and the reconcile respects
// that instead of resurrecting it. Only an operator hide can hold a slug this
// way: a prune tombstone released its slug (pruneBuiltinPages), so a retired
// and later re-shipped page passes this probe and is recreated.
func insertBuiltinPage(ctx context.Context, tx *sql.Tx, p BuiltinPage, tagsJSON []byte) (builtinWrite, error) {
	var hidden bool
	const hiddenQ = `SELECT EXISTS(SELECT 1 FROM portal_knowledge_pages
		WHERE slug = $1 AND builtin AND deleted_at IS NOT NULL)`
	if err := tx.QueryRowContext(ctx, hiddenQ, p.Slug).Scan(&hidden); err != nil {
		return builtinWrite{}, fmt.Errorf("probing hidden builtin page: %w", err)
	}
	if hidden {
		return builtinWrite{outcome: builtinSkipped}, nil
	}

	id := NewID()
	const insertQ = `INSERT INTO portal_knowledge_pages
		(id, slug, title, summary, body, tags, created_by, created_email, updated_by, current_version, builtin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, '', $7, 1, TRUE)
		ON CONFLICT (slug) WHERE slug IS NOT NULL AND deleted_at IS NULL DO NOTHING`
	res, err := tx.ExecContext(ctx, insertQ, id, p.Slug, p.Title, p.Summary, p.Body, tagsJSON, builtinAuthor)
	if err != nil {
		return builtinWrite{}, fmt.Errorf("inserting builtin page: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return builtinWrite{}, fmt.Errorf("builtin page insert rows affected: %w", err)
	}
	if n == 0 {
		// Another replica's reconcile won the insert race; its row is this row.
		return builtinWrite{outcome: builtinSkipped}, nil
	}
	if err := insertPageVersion(ctx, tx, pageVersionRow{
		pageID: id, version: 1,
		content:   pageContent{title: p.Title, summary: p.Summary, body: p.Body, tagsJSON: tagsJSON},
		createdBy: builtinAuthor, changeSummary: "Shipped with platform release",
	}); err != nil {
		return builtinWrite{}, err
	}
	return builtinWrite{outcome: builtinCreated, pageID: id, notify: true}, nil
}

// RestoreHidden implements BuiltinReconciler. A hidden page whose slug a live
// page has since taken (the operator hid the builtin and wrote their own) is
// left hidden: the operator's page keeps the topic, and resurrecting under the
// same slug would break the live-slug unique index anyway.
func (s *postgresStore) RestoreHidden(ctx context.Context) (int, error) {
	const q = `UPDATE portal_knowledge_pages t SET deleted_at = NULL, updated_at = NOW()
		WHERE t.builtin AND t.deleted_at IS NOT NULL AND t.slug IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM portal_knowledge_pages l
			WHERE l.slug = t.slug AND l.deleted_at IS NULL)`
	res, err := s.db.ExecContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("restoring hidden builtin pages: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("builtin page restore rows affected: %w", err)
	}
	return int(n), nil
}

// pruneBuiltinPages retires live builtin rows whose slug left the shipped set,
// so removing a page from a release removes it from every deployment on its
// next start. A retirement soft-deletes the row AND releases its slug: an
// operator hide keeps the slug, and that difference is the whole distinction
// between the two tombstones. A slugless prune tombstone never matches the
// hidden-page probe, so a slug retired by one release (or by a rollback to an
// older binary that does not ship it) is resurrected the next time a release
// ships it, while a page the operator hid stays hidden. Operator pages and
// already-deleted rows are untouched.
func (s *postgresStore) pruneBuiltinPages(ctx context.Context, shipped []string) (int, error) {
	const q = `UPDATE portal_knowledge_pages SET deleted_at = NOW(), updated_at = NOW(), slug = NULL
		WHERE builtin AND deleted_at IS NULL AND (slug IS NULL OR slug <> ALL($1))`
	res, err := s.db.ExecContext(ctx, q, pq.Array(shipped))
	if err != nil {
		return 0, fmt.Errorf("pruning builtin pages: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("builtin page prune rows affected: %w", err)
	}
	return int(n), nil
}
