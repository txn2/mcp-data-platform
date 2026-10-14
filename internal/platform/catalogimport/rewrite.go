package catalogimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

// RewriteReport is what the rewrite of stored references did.
type RewriteReport struct {
	// Rewritten counts the rows changed, by table and column.
	Rewritten map[string]int `json:"rewritten"`
	// Unmapped counts the references left as they were, because the import
	// has no catalog entity for them; each is listed in the report's skips
	// with where it was found.
	Unmapped int `json:"unmapped"`
	// IndexRowsDropped counts the rows of the dataset search index keyed by a
	// DataHub URN. The index reads the platform catalog; it re-reads the
	// datasets those rows named under their catalog references.
	IndexRowsDropped int `json:"index_rows_dropped"`
}

// Resolver turns a DataHub URN into the catalog reference it became, or ""
// when there is none.
type Resolver func(urn string) string

// urnPattern finds a DataHub URN in text: a type and a key that is either a
// parenthesized tuple, which may itself hold one URN, or a run of characters
// that cannot end a sentence's punctuation.
var urnPattern = regexp.MustCompile(`urn:li:[A-Za-z]+:(?:\((?:[^()]|\([^()]*\))*\)|[^\s"'<>()\[\]{},;` + "`" + `]+)`)

// column is one stored column the rewrite reads: how to list the rows that
// hold a DataHub URN, and how to write one back. Each statement is written
// out rather than assembled, so the statement gate can parse and plan it.
type column struct {
	name   string
	shape  shape
	list   string
	update string
}

// shape is how a column holds its references.
type shape int

const (
	textShape        shape = iota // text that may mention references anywhere
	valueShape                    // a column whose whole value is one reference
	arrayShape                    // a JSON array of references
	objectArrayShape              // a JSON array of objects with a "urn" field
)

var columns = []column{
	{
		name: "memory_records.entity_urns", shape: arrayShape,
		list:   `SELECT id, entity_urns::text FROM memory_records WHERE strpos(entity_urns::text, 'urn:li:') > 0`,
		update: `UPDATE memory_records SET entity_urns = $2::jsonb WHERE id = $1`,
	},
	{
		name: "memory_records.related_columns", shape: objectArrayShape,
		list:   `SELECT id, related_columns::text FROM memory_records WHERE strpos(related_columns::text, 'urn:li:') > 0`,
		update: `UPDATE memory_records SET related_columns = $2::jsonb WHERE id = $1`,
	},
	{
		name: "memory_records.content", shape: textShape,
		list:   `SELECT id, content FROM memory_records WHERE strpos(content, 'urn:li:') > 0`,
		update: `UPDATE memory_records SET content = $2 WHERE id = $1`,
	},
	{
		name: "knowledge_changesets.target_urn", shape: valueShape,
		list:   `SELECT id, target_urn FROM knowledge_changesets WHERE target_urn LIKE 'urn:li:%'`,
		update: `UPDATE knowledge_changesets SET target_urn = $2 WHERE id = $1`,
	},
	{
		name: "call_records.targets", shape: arrayShape,
		list:   `SELECT id::text, targets::text FROM call_records WHERE strpos(targets::text, 'urn:li:') > 0`,
		update: `UPDATE call_records SET targets = $2::jsonb WHERE id = $1::uuid`,
	},
	{
		name: "call_records.promoted_urn", shape: valueShape,
		list:   `SELECT id::text, promoted_urn FROM call_records WHERE promoted_urn LIKE 'urn:li:%'`,
		update: `UPDATE call_records SET promoted_urn = $2 WHERE id = $1::uuid`,
	},
	{
		name: "portal_knowledge_pages.body", shape: textShape,
		list:   `SELECT id, body FROM portal_knowledge_pages WHERE strpos(body, 'urn:li:') > 0`,
		update: `UPDATE portal_knowledge_pages SET body = $2 WHERE id = $1`,
	},
	{
		name: "portal_knowledge_pages.summary", shape: textShape,
		list:   `SELECT id, summary FROM portal_knowledge_pages WHERE strpos(summary, 'urn:li:') > 0`,
		update: `UPDATE portal_knowledge_pages SET summary = $2 WHERE id = $1`,
	},
}

// Rewriter rewrites the DataHub URNs platform tables hold into catalog
// references.
type Rewriter struct {
	db *sql.DB
}

// NewRewriter rewrites over db.
func NewRewriter(db *sql.DB) *Rewriter { return &Rewriter{db: db} }

// Rewrite rewrites every stored reference resolve has a catalog reference
// for, and lists the rest in report.
func (rw *Rewriter) Rewrite(ctx context.Context, resolve Resolver, report *Report) error {
	out := &RewriteReport{Rewritten: map[string]int{}}
	report.Rewrite = out
	for _, c := range columns {
		if err := rw.column(ctx, c, resolve, report); err != nil {
			return err
		}
	}
	if err := rw.pageReferences(ctx, resolve, report); err != nil {
		return err
	}
	res, err := rw.db.ExecContext(ctx, `DELETE FROM catalog_datasets WHERE urn LIKE 'urn:li:%'`)
	if err != nil {
		return fmt.Errorf("dropping the dataset index's DataHub rows: %w", err)
	}
	n, _ := res.RowsAffected()
	out.IndexRowsDropped = int(n)
	return nil
}

// row is one row a column listed.
type row struct {
	id, value string
}

// column rewrites one column.
func (rw *Rewriter) column(ctx context.Context, c column, resolve Resolver, report *Report) error {
	rows, err := rw.list(ctx, c.list)
	if err != nil {
		return fmt.Errorf("reading %s: %w", c.name, err)
	}
	for _, r := range rows {
		where := c.name + " " + r.id
		next, err := rewriteValue(c.shape, r.value, func(urn string) string {
			ref := resolve(urn)
			if ref == "" {
				report.Rewrite.Unmapped++
				report.skip(urn, ReasonUnmapped, where)
			}
			return ref
		})
		if err != nil {
			report.skip("", fmt.Sprintf("its stored value could not be read: %v", err), where)
			continue
		}
		if next == r.value {
			continue
		}
		if _, err := rw.db.ExecContext(ctx, c.update, r.id, next); err != nil {
			return fmt.Errorf("rewriting %s: %w", where, err)
		}
		report.Rewrite.Rewritten[c.name]++
	}
	return nil
}

// list reads the id and value of every row a statement lists.
func (rw *Rewriter) list(ctx context.Context, query string) ([]row, error) {
	rs, err := rw.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names the column
	}
	defer func() { _ = rs.Close() }()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.id, &r.value); err != nil {
			return nil, err //nolint:wrapcheck // the caller names the column
		}
		out = append(out, r)
	}
	return out, rs.Err() //nolint:wrapcheck // the caller names the column
}

// rewriteValue rewrites the references in one stored value. A reference with
// no catalog counterpart stays as it was.
func rewriteValue(s shape, value string, resolve func(string) string) (string, error) {
	switch s {
	case valueShape:
		if ref := resolve(value); ref != "" {
			return ref, nil
		}
		return value, nil
	case arrayShape:
		return rewriteArray(value, resolve)
	case objectArrayShape:
		return rewriteObjects(value, resolve)
	default:
		return rewriteText(value, resolve), nil
	}
}

// rewriteText replaces every URN in text that has a catalog reference. The
// sentence punctuation a URN in prose is followed by is not part of it.
func rewriteText(text string, resolve func(string) string) string {
	return urnPattern.ReplaceAllStringFunc(text, func(match string) string {
		urn := strings.TrimRight(match, ".:!?")
		if ref := resolve(urn); ref != "" {
			return ref + match[len(urn):]
		}
		return match
	})
}

// rewriteArray rewrites a JSON array of references.
func rewriteArray(value string, resolve func(string) string) (string, error) {
	var refs []string
	if err := json.Unmarshal([]byte(value), &refs); err != nil {
		return "", fmt.Errorf("decoding a list of references: %w", err)
	}
	changed := false
	for i, v := range refs {
		if !strings.HasPrefix(v, "urn:li:") {
			continue
		}
		if ref := resolve(v); ref != "" {
			refs[i], changed = ref, true
		}
	}
	if !changed {
		return value, nil
	}
	return encode(refs)
}

// rewriteObjects rewrites the "urn" field of a JSON array of objects.
func rewriteObjects(value string, resolve func(string) string) (string, error) {
	var objs []map[string]any
	if err := json.Unmarshal([]byte(value), &objs); err != nil {
		return "", fmt.Errorf("decoding a list of column references: %w", err)
	}
	changed := false
	for _, o := range objs {
		urn, _ := o["urn"].(string)
		if !strings.HasPrefix(urn, "urn:li:") {
			continue
		}
		if ref := resolve(urn); ref != "" {
			o["urn"], changed = ref, true
		}
	}
	if !changed {
		return value, nil
	}
	return encode(objs)
}

// encode writes a value back as JSON.
func encode(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encoding: %w", err)
	}
	return string(b), nil
}

const listPageReferences = `
SELECT id, entity_urn FROM knowledge_page_entity_refs WHERE target_type = 'datahub'`

const updatePageReference = `
UPDATE knowledge_page_entity_refs SET entity_urn = $2, target_type = 'catalog' WHERE id = $1`

const deletePageReference = `DELETE FROM knowledge_page_entity_refs WHERE id = $1`

// pageReferences turns a knowledge page's citations of DataHub entities into
// citations of the catalog entities they became. A page that already cites
// that catalog entity keeps its one citation.
func (rw *Rewriter) pageReferences(ctx context.Context, resolve Resolver, report *Report) error {
	const name = "knowledge_page_entity_refs.entity_urn"
	rows, err := rw.list(ctx, listPageReferences)
	if err != nil {
		return fmt.Errorf("reading %s: %w", name, err)
	}
	for _, r := range rows {
		ref := resolve(r.value)
		if ref == "" {
			report.Rewrite.Unmapped++
			report.skip(r.value, ReasonUnmapped, name+" "+r.id)
			continue
		}
		_, err := rw.db.ExecContext(ctx, updatePageReference, r.id, ref)
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			_, err = rw.db.ExecContext(ctx, deletePageReference, r.id)
		}
		if err != nil {
			return fmt.Errorf("rewriting %s %s: %w", name, r.id, err)
		}
		report.Rewrite.Rewritten[name]++
	}
	return nil
}
