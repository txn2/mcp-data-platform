package scripthandler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// Handler is one handler as every list reads it, from the script_handlers
// view: its kind, its type (the kind, or form for an endpoint on a form
// script), the script it starts and what it answers to.
type Handler struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Type       string    `json:"type"`
	ScriptID   string    `json:"script_id"`
	ScriptName string    `json:"script_name"`
	OwnerEmail string    `json:"owner_email"`
	Target     string    `json:"target"`
	Enabled    bool      `json:"enabled"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}

// Filter selects handlers. Every field narrows; a zero field does not. A nil
// ScriptIDs matches every script and an empty one none.
type Filter struct {
	// Types narrows to handlers of these types (endpoint, form, schedule,
	// webhook); none is a script-list type and matches no handler.
	Types     []string
	ScriptIDs []string
	// OwnerEmail narrows to the handlers of one person's scripts.
	OwnerEmail string
	Enabled    *bool
	// Search narrows to the handlers whose script name or target contains it.
	Search string
	Limit  int
	Offset int
}

// MaxListLimit bounds one page of a handler list.
const MaxListLimit = 500

// View reads the script_handlers view.
type View struct{ DB *sql.DB }

const viewColumns = `h.id, h.kind, h.type, h.script_id, s.name, s.owner_email, h.target, h.enabled, h.created_by, h.created_at`

// viewList is one page of the handlers a filter matches, and how many it
// matches in all.
const viewList = `SELECT ` + viewColumns + `, COUNT(*) OVER ()
  FROM script_handlers h JOIN scripts s ON s.id = h.script_id
 WHERE ($1::text[] IS NULL OR h.type = ANY($1::text[]))
   AND ($2::uuid[] IS NULL OR h.script_id = ANY($2::uuid[]))
   AND ($3::text = '' OR lower(s.owner_email) = lower($3::text))
   AND ($4::boolean IS NULL OR h.enabled = $4::boolean)
   AND ($5::text = '' OR strpos(lower(s.name), lower($5::text)) > 0 OR strpos(lower(h.target), lower($5::text)) > 0)
 ORDER BY s.name, h.kind, h.created_at, h.id
 LIMIT $6 OFFSET $7`

func scanHandler(row interface{ Scan(...any) error }, extra ...any) (Handler, error) {
	var h Handler
	dest := append([]any{&h.ID, &h.Kind, &h.Type, &h.ScriptID, &h.ScriptName, &h.OwnerEmail, &h.Target, &h.Enabled, &h.CreatedBy, &h.CreatedAt}, extra...)
	err := row.Scan(dest...)
	return h, err //nolint:wrapcheck // each caller says what it was reading
}

// List is one page of the handlers filter matches, by script name, and how
// many it matches in all.
func (v View) List(ctx context.Context, f Filter) ([]Handler, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > MaxListLimit {
		limit = MaxListLimit
	}
	var types, ids any
	if len(f.Types) > 0 {
		types = pq.Array(f.Types)
	}
	if f.ScriptIDs != nil {
		ids = pq.Array(f.ScriptIDs)
	}
	var enabled sql.NullBool
	if f.Enabled != nil {
		enabled = sql.NullBool{Bool: *f.Enabled, Valid: true}
	}
	rows, err := v.DB.QueryContext(ctx, viewList, types, ids, f.OwnerEmail, enabled, f.Search, limit, max(f.Offset, 0))
	if err != nil {
		return nil, 0, fmt.Errorf("listing handlers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Handler{}
	total := 0
	for rows.Next() {
		h, err := scanHandler(rows, &total)
		if err != nil {
			return nil, 0, fmt.Errorf("scanning a handler: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("listing handlers: %w", err)
	}
	return out, total, nil
}

// Get is one handler, ErrNotFound when none has the id.
func (v View) Get(ctx context.Context, id string) (Handler, error) {
	h, err := scanHandler(v.DB.QueryRowContext(ctx, `SELECT `+viewColumns+`
		  FROM script_handlers h JOIN scripts s ON s.id = h.script_id WHERE h.id::text = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Handler{}, fmt.Errorf("no handler %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Handler{}, fmt.Errorf("reading handler %s: %w", id, err)
	}
	return h, nil
}

// TypesOf is each script's handler types, sorted and once each; a script
// with no handler is absent.
func (v View) TypesOf(ctx context.Context, scriptIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(scriptIDs) == 0 {
		return out, nil
	}
	rows, err := v.DB.QueryContext(ctx, `SELECT DISTINCT script_id, type FROM script_handlers
		 WHERE script_id = ANY($1::uuid[]) ORDER BY script_id, type`, pq.Array(scriptIDs))
	if err != nil {
		return nil, fmt.Errorf("reading scripts' handler types: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, t string
		if err := rows.Scan(&id, &t); err != nil {
			return nil, fmt.Errorf("scanning a script's handler type: %w", err)
		}
		out[id] = append(out[id], t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading scripts' handler types: %w", err)
	}
	return out, nil
}
