package knowledgepage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
)

// NewID returns a unique id for a knowledge page ("kp_<uuid>").
func NewID() string { return "kp_" + uuid.New().String() }

// NewVersionID returns a unique id for a page version ("kpv_<uuid>").
func NewVersionID() string { return "kpv_" + uuid.New().String() }

// Page is a canonical unit of business/domain knowledge: a markdown
// page in the platform's internal knowledge store (the sibling of DataHub). It
// is org-shared, not owner-scoped: every caller can read it, and personas with
// apply_knowledge access edit it. The markdown body is stored inline (not in S3)
// so page CONTENT is directly embeddable and full-text searchable.
type Page struct {
	ID             string     `json:"id" example:"kp_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	Slug           string     `json:"slug,omitempty" example:"fiscal-calendar"`
	Title          string     `json:"title" example:"Fiscal Calendar"`
	Summary        string     `json:"summary,omitempty" example:"How the company defines fiscal quarters."`
	Body           string     `json:"body" example:"# Fiscal Calendar\n\nQ1 begins..."`
	Tags           []string   `json:"tags"`
	CreatedBy      string     `json:"created_by,omitempty" example:"alice@example.com"`
	CreatedEmail   string     `json:"created_email,omitempty" example:"alice@example.com"`
	UpdatedBy      string     `json:"updated_by,omitempty" example:"bob@example.com"`
	CurrentVersion int        `json:"current_version" example:"3"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

// Version records a single saved version of a page's content. The
// body is stored inline (pages are text-first and bounded), unlike asset
// versions which reference S3 keys.
type Version struct {
	ID            string    `json:"id" example:"kpv_01HK7R9A"`
	PageID        string    `json:"page_id" example:"kp_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	Version       int       `json:"version" example:"2"`
	Title         string    `json:"title" example:"Fiscal Calendar"`
	Summary       string    `json:"summary,omitempty"`
	Body          string    `json:"body"`
	Tags          []string  `json:"tags"`
	CreatedBy     string    `json:"created_by,omitempty" example:"bob@example.com"`
	ChangeSummary string    `json:"change_summary,omitempty" example:"Clarified Q1 start"`
	CreatedAt     time.Time `json:"created_at"`
}

// Filter narrows a knowledge page listing. Tag filters to pages
// carrying the tag; Query is a substring match on title for the browse UI. Only
// non-deleted pages are ever returned.
type Filter struct {
	Tag    string
	Query  string
	Limit  int
	Offset int
}

// Update carries the editable fields of a page. A nil pointer means
// "leave unchanged"; a non-nil pointer (including empty string) sets the field.
// Whenever Title, Body, or Tags change, the store clears the embedding columns so
// the indexjobs reconciler re-embeds the new content off the request path.
type Update struct {
	Slug          *string
	Title         *string
	Summary       *string
	Body          *string
	Tags          *[]string
	UpdatedBy     string
	ChangeSummary string
}

// Store persists and queries canonical knowledge pages.
type Store interface {
	Insert(ctx context.Context, page Page) error
	Get(ctx context.Context, id string) (*Page, error)
	GetBySlug(ctx context.Context, slug string) (*Page, error)
	List(ctx context.Context, filter Filter) ([]Page, int, error)
	Update(ctx context.Context, id string, updates Update) error
	SoftDelete(ctx context.Context, id string) error
	ListVersions(ctx context.Context, pageID string, limit, offset int) ([]Version, int, error)
	GetVersion(ctx context.Context, pageID string, version int) (*Version, error)
	// Entity references (#664): the entities a page provides knowledge about.
	ListEntityRefs(ctx context.Context, pageID string) ([]EntityRef, error)
	// ValidateRefTargets checks each FK-backed reference target exists, so a
	// citation to a missing entity is rejected before the page is written (#690).
	ValidateRefTargets(ctx context.Context, refs []EntityRef) error
	// FilterExistingRefTargets returns the subset of refs whose target exists,
	// dropping stale references carried from a source insight (#690).
	FilterExistingRefTargets(ctx context.Context, refs []EntityRef) ([]EntityRef, error)
	AddEntityRefs(ctx context.Context, pageID string, refs []EntityRef) error
	ReplaceEntityRefs(ctx context.Context, pageID string, refs []EntityRef) error
	ReplaceEntityRefsBySource(ctx context.Context, pageID, source string, refs []EntityRef) error
	// ListPagesReferencing is the reverse lookup: the pages that reference a target.
	ListPagesReferencing(ctx context.Context, ref EntityRef) ([]PageRef, error)
}

// ErrNotFound is returned when a page id/slug does not resolve to a
// live page.
var ErrNotFound = errors.New("knowledge page not found")

// IndexText composes the text a page is embedded and lexically
// indexed on: its title, body, and tags. The indexjobs knowledge-pages consumer
// and the request-path search MUST agree on this composition so a stored
// embedding lives in the same space as the query; portal_knowledge_page_fts
// (migration 000070) composes the same corpus from the same columns. Empty
// fields are skipped so a sparse page does not pad the text.
func IndexText(title, body string, tags []string) string {
	parts := make([]string, 0, 3)
	if title != "" {
		parts = append(parts, title)
	}
	if body != "" {
		parts = append(parts, body)
	}
	if len(tags) > 0 {
		parts = append(parts, strings.Join(tags, " "))
	}
	return strings.Join(parts, "\n")
}

// pageColumns is the projection every page read uses, in
// scanPage order so the scan cannot drift from the query.
const pageColumns = `id, COALESCE(slug, ''), title, summary, body, tags, ` +
	`created_by, created_email, updated_by, current_version, created_at, updated_at, deleted_at`

type postgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL knowledge page store.
func NewPostgresStore(db *sql.DB) Store {
	return &postgresStore{db: db}
}

// Compile-time checks.
var (
	_ Store    = (*postgresStore)(nil)
	_ Searcher = (*postgresStore)(nil)
)

// Insert creates a page at version 1 and snapshots that initial version, in one
// transaction. The embedding columns are left NULL so the reconciler embeds the
// page on its next sweep.
func (s *postgresStore) Insert(ctx context.Context, page Page) error { //nolint:revive // interface impl
	if page.Tags == nil {
		page.Tags = []string{}
	}
	tags, err := json.Marshal(page.Tags)
	if err != nil {
		return fmt.Errorf("marshaling tags: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	const insertPage = `
		INSERT INTO portal_knowledge_pages
		(id, slug, title, summary, body, tags, created_by, created_email, updated_by, current_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1)`
	if _, err := tx.ExecContext(ctx, insertPage,
		page.ID, nullableSlug(page.Slug), page.Title, page.Summary, page.Body, tags,
		page.CreatedBy, page.CreatedEmail, page.CreatedBy,
	); err != nil {
		return fmt.Errorf("inserting knowledge page: %w", err)
	}

	if err := insertPageVersion(ctx, tx, pageVersionRow{
		pageID:        page.ID,
		version:       1,
		content:       pageContent{title: page.Title, summary: page.Summary, body: page.Body, tagsJSON: tags},
		createdBy:     page.CreatedBy,
		changeSummary: "Initial version",
	}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing knowledge page insert: %w", err)
	}
	return nil
}

// Get returns a page by id (including a soft-deleted one, so callers can inspect
// it); use the DeletedAt field to tell. Returns ErrNotFound when no
// row exists.
func (s *postgresStore) Get(ctx context.Context, id string) (*Page, error) { //nolint:revive // interface impl
	query, args, err := psq.Select(pageColumns).
		From("portal_knowledge_pages").Where(sq.Eq{"id": id}).ToSql()
	if err != nil {
		return nil, fmt.Errorf("building get query: %w", err)
	}
	page, err := scanPage(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying knowledge page: %w", err)
	}
	return page, nil
}

// GetBySlug returns the live page for a slug, or ErrNotFound. Used
// by apply_knowledge promotion to find-or-create by topic.
func (s *postgresStore) GetBySlug(ctx context.Context, slug string) (*Page, error) { //nolint:revive // interface impl
	query, args, err := psq.Select(pageColumns).
		From("portal_knowledge_pages").
		Where(sq.Eq{"slug": slug}).Where("deleted_at IS NULL").ToSql()
	if err != nil {
		return nil, fmt.Errorf("building get-by-slug query: %w", err)
	}
	page, err := scanPage(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying knowledge page by slug: %w", err)
	}
	return page, nil
}

// List returns non-deleted pages matching the filter, plus the total count
// (for pagination). Ordered by most-recently-updated, with the unique id as a
// tiebreaker so the order is a deterministic TOTAL order: pages that share an
// updated_at timestamp (e.g. a bulk import) keep a stable relative position across
// separate paginated queries, so an offset/limit sweep neither skips nor
// double-returns a page.
func (s *postgresStore) List(ctx context.Context, filter Filter) ([]Page, int, error) { //nolint:revive // interface impl
	total, err := s.countPages(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	qb := applyFilter(psq.Select(pageColumns).From("portal_knowledge_pages"), filter).
		Where("deleted_at IS NULL").
		OrderBy("updated_at DESC", "id ASC").
		Limit(uint64(clampSearchLimit(filter.Limit))) // #nosec G115 -- clampSearchLimit bounds to [1, maxSearchLimit]
	if filter.Offset > 0 {
		qb = qb.Offset(uint64(filter.Offset)) // #nosec G115 -- offset guarded > 0
	}
	query, args, err := qb.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building list query: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, query, args...) // #nosec G701 -- builder-generated query
	if err != nil {
		return nil, 0, fmt.Errorf("listing knowledge pages: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var pages []Page
	for rows.Next() {
		page, err := scanPage(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scanning knowledge page row: %w", err)
		}
		pages = append(pages, *page)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating knowledge page rows: %w", err)
	}
	return pages, total, nil
}

func (s *postgresStore) countPages(ctx context.Context, filter Filter) (int, error) {
	qb := applyFilter(psq.Select("COUNT(*)").From("portal_knowledge_pages"), filter).
		Where("deleted_at IS NULL")
	query, args, err := qb.ToSql()
	if err != nil {
		return 0, fmt.Errorf("building count query: %w", err)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("counting knowledge pages: %w", err)
	}
	return total, nil
}

// Update applies the changed fields, snapshots the new content as the next
// version, and (when the indexed text changes) clears the embedding so the
// reconciler re-embeds. All in one transaction. Returns ErrNotFound
// if the page is missing or already deleted.
func (s *postgresStore) Update(ctx context.Context, id string, updates Update) error { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	cur, currentVersion, err := lockPageContent(ctx, tx, id)
	if err != nil {
		return err
	}
	indexedChanged, err := cur.merge(updates)
	if err != nil {
		return err
	}
	nextVersion := currentVersion + 1

	if err := applyPageUpdate(ctx, tx, pageUpdateRow{
		id: id, content: cur, slug: updates.Slug, updatedBy: updates.UpdatedBy,
		nextVersion: nextVersion, indexedChanged: indexedChanged,
	}); err != nil {
		return err
	}
	if err := insertPageVersion(ctx, tx, pageVersionRow{
		pageID: id, version: nextVersion, content: cur,
		createdBy: updates.UpdatedBy, changeSummary: updates.ChangeSummary,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing knowledge page update: %w", err)
	}
	return nil
}

// lockPageContent locks the live page row FOR UPDATE and returns its current
// content plus version. Returns ErrNotFound when the page is missing
// or already deleted.
func lockPageContent(ctx context.Context, tx *sql.Tx, id string) (pageContent, int, error) {
	const lockQuery = `SELECT title, summary, body, tags, current_version
		FROM portal_knowledge_pages WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`
	var (
		c              pageContent
		currentVersion int
	)
	err := tx.QueryRowContext(ctx, lockQuery, id).Scan(&c.title, &c.summary, &c.body, &c.tagsJSON, &currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return c, 0, ErrNotFound
	}
	if err != nil {
		return c, 0, fmt.Errorf("locking knowledge page: %w", err)
	}
	if err := unmarshalTags(c.tagsJSON, &c.tags); err != nil {
		return c, 0, err
	}
	return c, currentVersion, nil
}

// pageUpdateRow carries the merged fields applyPageUpdate writes back.
type pageUpdateRow struct {
	id             string
	content        pageContent
	slug           *string
	updatedBy      string
	nextVersion    int
	indexedChanged bool
}

// applyPageUpdate writes the merged content back to the page row, clearing the
// embedding when the indexed text changed so the reconciler re-embeds.
func applyPageUpdate(ctx context.Context, tx *sql.Tx, u pageUpdateRow) error {
	setQB := psq.Update("portal_knowledge_pages").
		Set("title", u.content.title).Set("summary", u.content.summary).Set("body", u.content.body).Set("tags", u.content.tagsJSON).
		Set("updated_by", u.updatedBy).Set("current_version", u.nextVersion).Set("updated_at", sq.Expr("NOW()"))
	if u.slug != nil {
		setQB = setQB.Set("slug", nullableSlug(*u.slug))
	}
	if u.indexedChanged {
		setQB = setQB.Set("embedding", nil).Set("embedding_model", "").Set("embedding_text_hash", nil)
	}
	query, args, err := setQB.Where(sq.Eq{"id": u.id}).ToSql()
	if err != nil {
		return fmt.Errorf("building update query: %w", err)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil { // #nosec G701 -- builder-generated query
		return fmt.Errorf("updating knowledge page: %w", err)
	}
	return nil
}

// pageContent is the mutable, per-version content of a knowledge page.
type pageContent struct {
	title, summary, body string
	tags                 []string
	tagsJSON             []byte
}

// merge applies the update onto the current content, re-marshals tags, and
// reports whether the indexed text (title/body/tags) changed (the signal to
// re-embed).
func (c *pageContent) merge(u Update) (bool, error) {
	indexedChanged := false
	if u.Title != nil && *u.Title != c.title {
		c.title, indexedChanged = *u.Title, true
	}
	if u.Body != nil && *u.Body != c.body {
		c.body, indexedChanged = *u.Body, true
	}
	if u.Tags != nil {
		c.tags, indexedChanged = *u.Tags, true
	}
	if u.Summary != nil {
		c.summary = *u.Summary
	}
	if c.tags == nil {
		c.tags = []string{}
	}
	tagsJSON, err := json.Marshal(c.tags)
	if err != nil {
		return false, fmt.Errorf("marshaling tags: %w", err)
	}
	c.tagsJSON = tagsJSON
	return indexedChanged, nil
}

// pageVersionRow carries the fields of one version snapshot.
type pageVersionRow struct {
	pageID        string
	version       int
	content       pageContent
	createdBy     string
	changeSummary string
}

// SoftDelete marks a page deleted. It stays in the version history but leaves
// search (deleted_at IS NULL) immediately. Returns ErrNotFound when
// no live row matches.
func (s *postgresStore) SoftDelete(ctx context.Context, id string) error { //nolint:revive // interface impl
	const q = `UPDATE portal_knowledge_pages SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`
	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("deleting knowledge page: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("knowledge page delete rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListVersions returns a page's version history (newest first) and the total count.
func (s *postgresStore) ListVersions(ctx context.Context, pageID string, limit, offset int) ([]Version, int, error) { //nolint:revive // interface impl
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM portal_knowledge_page_versions WHERE page_id = $1`, pageID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting knowledge page versions: %w", err)
	}

	qb := psq.Select("id, page_id, version, title, summary, body, tags, created_by, change_summary, created_at").
		From("portal_knowledge_page_versions").
		Where(sq.Eq{"page_id": pageID}).
		OrderBy("version DESC").
		Limit(uint64(clampSearchLimit(limit))) // #nosec G115 -- clampSearchLimit bounds to [1, maxSearchLimit]
	if offset > 0 {
		qb = qb.Offset(uint64(offset)) // #nosec G115 -- offset guarded > 0
	}
	query, args, err := qb.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building versions query: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, query, args...) // #nosec G701 -- builder-generated query
	if err != nil {
		return nil, 0, fmt.Errorf("listing knowledge page versions: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var versions []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, 0, err
		}
		versions = append(versions, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating knowledge page versions: %w", err)
	}
	return versions, total, nil
}

// GetVersion returns a single historical version of a page.
func (s *postgresStore) GetVersion(ctx context.Context, pageID string, version int) (*Version, error) { //nolint:revive // interface impl
	query, args, err := psq.Select("id, page_id, version, title, summary, body, tags, created_by, change_summary, created_at").
		From("portal_knowledge_page_versions").
		Where(sq.Eq{"page_id": pageID, "version": version}).ToSql()
	if err != nil {
		return nil, fmt.Errorf("building get-version query: %w", err)
	}
	v, err := scanVersion(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying knowledge page version: %w", err)
	}
	return v, nil
}

// insertPageVersion snapshots one version row inside an open transaction.
func insertPageVersion(ctx context.Context, tx *sql.Tx, v pageVersionRow) error {
	const q = `INSERT INTO portal_knowledge_page_versions
		(id, page_id, version, title, summary, body, tags, created_by, change_summary)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	if _, err := tx.ExecContext(ctx, q,
		NewVersionID(), v.pageID, v.version, v.content.title, v.content.summary,
		v.content.body, v.content.tagsJSON, v.createdBy, v.changeSummary,
	); err != nil {
		return fmt.Errorf("inserting knowledge page version: %w", err)
	}
	return nil
}

// applyFilter adds the tag/query predicates shared by List and count.
func applyFilter(qb sq.SelectBuilder, filter Filter) sq.SelectBuilder {
	if filter.Tag != "" {
		tagJSON, _ := json.Marshal([]string{filter.Tag})
		qb = qb.Where(sq.Expr("tags @> ?::jsonb", tagJSON))
	}
	if filter.Query != "" {
		qb = qb.Where(sq.Expr("title ILIKE ?", "%"+filter.Query+"%"))
	}
	return qb
}

// scanDest returns the scan targets for a page row in
// pageColumns order, so the base read and the ranked-search read share
// one projection and cannot drift.
func scanDest(page *Page, tagsJSON *[]byte, deletedAt *sql.NullTime) []any {
	return []any{
		&page.ID, &page.Slug, &page.Title, &page.Summary, &page.Body, tagsJSON,
		&page.CreatedBy, &page.CreatedEmail, &page.UpdatedBy, &page.CurrentVersion,
		&page.CreatedAt, &page.UpdatedAt, deletedAt,
	}
}

// finishScannedPage applies the post-scan fixups (deleted_at, tags) shared by
// the base read and the ranked-search read.
func finishScannedPage(page *Page, tagsJSON []byte, deletedAt sql.NullTime) error {
	if deletedAt.Valid {
		page.DeletedAt = &deletedAt.Time
	}
	return unmarshalTags(tagsJSON, &page.Tags)
}

// scanPage scans one page row in pageColumns order.
func scanPage(row interface{ Scan(...any) error }) (*Page, error) {
	var (
		page      Page
		tagsJSON  []byte
		deletedAt sql.NullTime
	)
	if err := row.Scan(scanDest(&page, &tagsJSON, &deletedAt)...); err != nil {
		return nil, fmt.Errorf("scanning knowledge page: %w", err)
	}
	if err := finishScannedPage(&page, tagsJSON, deletedAt); err != nil {
		return nil, err
	}
	return &page, nil
}

func scanVersion(row interface{ Scan(...any) error }) (*Version, error) {
	var (
		v        Version
		tagsJSON []byte
	)
	if err := row.Scan(
		&v.ID, &v.PageID, &v.Version, &v.Title, &v.Summary, &v.Body, &tagsJSON, &v.CreatedBy, &v.ChangeSummary, &v.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("scanning knowledge page version: %w", err)
	}
	if err := unmarshalTags(tagsJSON, &v.Tags); err != nil {
		return nil, err
	}
	return &v, nil
}

// unmarshalTags decodes a JSONB tags array into a non-nil slice so the JSON
// surface and templates never see a null tags field.
func unmarshalTags(data []byte, out *[]string) error {
	if len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("unmarshaling tags: %w", err)
		}
	}
	if *out == nil {
		*out = []string{}
	}
	return nil
}

// nullableSlug maps an empty slug to SQL NULL so the partial unique index does
// not collide multiple slug-less pages.
func nullableSlug(slug string) any {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil
	}
	return slug
}
