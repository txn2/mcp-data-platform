package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/txn2/mcp-data-platform/internal/portal/reachstore"
)

// UnusedItem is one asset nobody has opened since a cutoff. It carries counts,
// never who opened.
type UnusedItem struct {
	ID          string `json:"id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	Name        string `json:"name" example:"Vendor list export"`
	ContentType string `json:"content_type,omitempty" example:"text/csv"`
	OwnerEmail  string `json:"owner_email,omitempty" example:"jane@example.com"`
	// Status is the asset's status (draft, published, deprecated), empty when
	// it has none.
	Status string `json:"status,omitempty" example:"draft"`
	// Opens is how many times it was opened in the portal, by anyone, since
	// opens were first recorded.
	Opens int64 `json:"opens" example:"0"`
	// LastOpened is the last open by anyone, nil when it was never opened.
	LastOpened *time.Time `json:"last_opened,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// openTotals sums each asset's opens across everyone.
const openTotals = `(SELECT target_id, SUM(open_count) AS opens, MAX(last_opened_at) AS last_opened
	FROM portal_opens WHERE target_type = 'asset' GROUP BY target_id) o ON o.target_id = a.id`

// Unused lists the assets in scope nobody has opened since the cutoff, never
// opened first.
func (s *Store) Unused(ctx context.Context, scope Scope, since time.Time, limit, offset int) (Page[UnusedItem], error) {
	query, args, err := buildUnused(scope, since, ClampLimit(limit), max(offset, 0))
	if err != nil {
		return Page[UnusedItem]{}, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[UnusedItem]{}, fmt.Errorf("reading unused assets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	page := Page[UnusedItem]{Items: []UnusedItem{}}
	for rows.Next() {
		var it UnusedItem
		var last sql.NullTime
		if err := rows.Scan(&it.ID, &it.Name, &it.ContentType, &it.OwnerEmail, &it.Status,
			&it.Opens, &last, &it.CreatedAt, &page.Total); err != nil {
			return Page[UnusedItem]{}, fmt.Errorf("scanning an unused asset: %w", err)
		}
		if last.Valid {
			it.LastOpened = &last.Time
		}
		page.Items = append(page.Items, it)
	}
	if err := rows.Err(); err != nil {
		return Page[UnusedItem]{}, fmt.Errorf("reading unused assets: %w", err)
	}
	return page, nil
}

func buildUnused(scope Scope, since time.Time, limit, offset int) (query string, args []any, err error) {
	b := sq.StatementBuilder.PlaceholderFormat(sq.Dollar).
		Select("a.id", "a.name", "a.content_type", "a.owner_email", "COALESCE(st.status, '')",
			"COALESCE(o.opens, 0)", "o.last_opened", "a.created_at", "COUNT(*) OVER ()").
		From("portal_assets a").
		LeftJoin(openTotals).
		LeftJoin("portal_asset_status st ON st.asset_id = a.id").
		Where("a.deleted_at IS NULL").
		Where(sq.Or{sq.Expr("o.last_opened IS NULL"), sq.Lt{"o.last_opened": since}}).
		OrderBy("o.last_opened ASC NULLS FIRST", "a.created_at ASC", "a.id").
		Limit(uint64(limit)).Offset(uint64(offset)) // #nosec G115 -- both clamped non-negative above
	if !scope.Everyone {
		b = b.Where(reachstore.OwnerPredicate(scope.Owner))
	}
	query, args, err = b.ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("building the unused query: %w", err)
	}
	return query, args, nil
}
