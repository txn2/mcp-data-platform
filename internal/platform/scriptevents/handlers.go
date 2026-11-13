package scriptevents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// ErrHandlerNotFound is a webhook handler that does not exist.
var ErrHandlerNotFound = errors.New("no such webhook handler")

// Handler is one webhook handler (#2434): each event the source stores
// enqueues one run of the script while it is enabled.
type Handler struct {
	ID        string    `json:"id"`
	ScriptID  string    `json:"script_id"`
	Source    string    `json:"event_source"`
	Enabled   bool      `json:"enabled"`
	CreatedBy string    `json:"created_by"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HandlerFilter selects webhook handlers. A nil ScriptIDs matches every
// script and an empty one none; an empty Source matches every source.
type HandlerFilter struct {
	ScriptIDs []string
	Source    string
}

const handlerColumns = `id, script_id, source, enabled, created_by, updated_by, created_at, updated_at`

func scanHandler(row interface{ Scan(...any) error }) (Handler, error) {
	var h Handler
	err := row.Scan(&h.ID, &h.ScriptID, &h.Source, &h.Enabled, &h.CreatedBy, &h.UpdatedBy, &h.CreatedAt, &h.UpdatedAt)
	return h, err //nolint:wrapcheck // each caller says what it was reading
}

// AddHandler binds a script to a source, or answers the handler that already
// binds it, re-enabled when it was off. created reports a new handler.
func (s *PostgresStore) AddHandler(ctx context.Context, scriptID, source, actor string) (h Handler, created bool, err error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO script_webhook_handlers (script_id, source, created_by, updated_by)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (script_id, source) DO UPDATE
		   SET enabled = TRUE, updated_by = EXCLUDED.updated_by, updated_at = NOW()
		RETURNING `+handlerColumns+`, (xmax = 0)`, scriptID, source, actor)
	err = row.Scan(&h.ID, &h.ScriptID, &h.Source, &h.Enabled, &h.CreatedBy, &h.UpdatedBy, &h.CreatedAt, &h.UpdatedAt, &created)
	if err != nil {
		return Handler{}, false, fmt.Errorf("binding script %s to webhook source %s: %w", scriptID, source, err)
	}
	return h, created, nil
}

// HandlerByID returns one webhook handler.
func (s *PostgresStore) HandlerByID(ctx context.Context, id string) (Handler, error) {
	h, err := scanHandler(s.db.QueryRowContext(ctx, `SELECT `+handlerColumns+` FROM script_webhook_handlers WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Handler{}, ErrHandlerNotFound
	}
	if err != nil {
		return Handler{}, fmt.Errorf("reading webhook handler %s: %w", id, err)
	}
	return h, nil
}

// Handlers lists the webhook handlers a filter matches, by source.
func (s *PostgresStore) Handlers(ctx context.Context, filter HandlerFilter) ([]Handler, error) {
	var ids any
	if filter.ScriptIDs != nil {
		ids = pq.Array(filter.ScriptIDs)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+handlerColumns+` FROM script_webhook_handlers
		 WHERE ($1::uuid[] IS NULL OR script_id = ANY($1::uuid[]))
		   AND ($2::text = '' OR source = $2::text)
		 ORDER BY source, created_at`, ids, filter.Source)
	if err != nil {
		return nil, fmt.Errorf("listing webhook handlers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Handler{}
	for rows.Next() {
		h, err := scanHandler(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning a webhook handler: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing webhook handlers: %w", err)
	}
	return out, nil
}

// EnableHandler turns one webhook handler on or off.
func (s *PostgresStore) EnableHandler(ctx context.Context, id string, enabled bool, actor string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE script_webhook_handlers
		   SET enabled = $2, updated_by = $3, updated_at = NOW() WHERE id = $1`, id, enabled, actor)
	if err != nil {
		return fmt.Errorf("changing webhook handler %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrHandlerNotFound
	}
	return nil
}

// DeleteHandler removes one webhook handler. The runs its events made keep
// their source and event.
func (s *PostgresStore) DeleteHandler(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM script_webhook_handlers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting webhook handler %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrHandlerNotFound
	}
	return nil
}
