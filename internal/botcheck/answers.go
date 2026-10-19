package botcheck

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AnswerStore records the challenges answered, which is what refuses a replay
// on every replica and what an endpoint's recent rate is counted from.
type AnswerStore interface {
	// Use records challengeID as answered for endpoint, kept until expires,
	// and reports false when it was answered before.
	Use(ctx context.Context, challengeID, endpoint string, expires time.Time) (bool, error)
	// Recent counts the challenges endpoint had answered since since.
	Recent(ctx context.Context, endpoint string, since time.Time) (int, error)
	// Sweep deletes the answers whose challenge expired before now.
	Sweep(ctx context.Context, now time.Time) (int64, error)
}

// PostgresAnswers is the AnswerStore over the bot_check_answers table.
type PostgresAnswers struct {
	db *sql.DB
}

// NewAnswerStore is the answer store over db.
func NewAnswerStore(db *sql.DB) *PostgresAnswers { return &PostgresAnswers{db: db} }

// Use inserts the answer, and the primary key decides whether it was the
// first: two replicas handed the same answer at once both insert, and one
// insert finds the row the other wrote.
func (p *PostgresAnswers) Use(ctx context.Context, challengeID, endpoint string, expires time.Time) (bool, error) {
	var id string
	err := p.db.QueryRowContext(ctx, `
		INSERT INTO bot_check_answers (challenge_id, endpoint, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (challenge_id) DO NOTHING
		RETURNING challenge_id`, challengeID, endpoint, expires).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("recording a bot check answer: %w", err)
	}
	return true, nil
}

// Recent counts endpoint's answers since since.
func (p *PostgresAnswers) Recent(ctx context.Context, endpoint string, since time.Time) (int, error) {
	var n int
	if err := p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bot_check_answers WHERE endpoint = $1 AND used_at >= $2`, endpoint, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting an endpoint's bot check answers: %w", err)
	}
	return n, nil
}

// Sweep deletes the answers whose challenge can no longer be answered.
func (p *PostgresAnswers) Sweep(ctx context.Context, now time.Time) (int64, error) {
	res, err := p.db.ExecContext(ctx, `DELETE FROM bot_check_answers WHERE expires_at < $1`, now)
	if err != nil {
		return 0, fmt.Errorf("sweeping bot check answers: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
