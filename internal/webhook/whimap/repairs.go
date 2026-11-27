package whimap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Repairs stores each source's attachment repair in webhook_imap_repairs.
type Repairs struct {
	db *sql.DB
}

// NewRepairs returns the store, nil without a database.
func NewRepairs(db *sql.DB) *Repairs {
	if db == nil {
		return nil
	}
	return &Repairs{db: db}
}

// Repaired reports whether source's repair has run.
func (r *Repairs) Repaired(ctx context.Context, source string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM webhook_imap_repairs WHERE source = $1`, source).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading webhook_imap_repairs: %w", err)
	}
	return true, nil
}

// RecordRepair records that source's repair ran and what it did.
func (r *Repairs) RecordRepair(ctx context.Context, source string, rep RepairReport) error {
	body, err := json.Marshal(rep)
	if err != nil {
		return fmt.Errorf("encoding the repair report: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO webhook_imap_repairs (source, moved, removed, removed_files, kept, report)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (source) DO UPDATE SET repaired_at = now(), moved = EXCLUDED.moved, removed = EXCLUDED.removed,
		   removed_files = EXCLUDED.removed_files, kept = EXCLUDED.kept, report = EXCLUDED.report`,
		source, len(rep.Moved), len(rep.Removed), rep.RemovedFiles(), len(rep.Kept), body)
	if err != nil {
		return fmt.Errorf("recording webhook_imap_repairs: %w", err)
	}
	return nil
}

// repairListLimit is how many of each list of folders a read returns; the
// counts are always the whole. A mailbox read twice holds tens of thousands of
// later copies, and the source's page is read often.
const repairListLimit = 100

// RepairRecord is a source's repair as its page shows it: the whole counts,
// and the first repairListLimit folders of each list.
type RepairRecord struct {
	RepairedAt   time.Time    `json:"repaired_at"`
	Moved        int          `json:"moved"`
	Removed      int          `json:"removed"`
	RemovedFiles int          `json:"removed_files"`
	Kept         int          `json:"kept"`
	Report       RepairReport `json:"report"`
}

// Get returns source's repair, nil when it has not run.
func (r *Repairs) Get(ctx context.Context, source string) (*RepairRecord, error) {
	var (
		rec  RepairRecord
		body []byte
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT repaired_at, moved, removed, removed_files, kept, report FROM webhook_imap_repairs WHERE source = $1`, source).
		Scan(&rec.RepairedAt, &rec.Moved, &rec.Removed, &rec.RemovedFiles, &rec.Kept, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // no repair yet
	}
	if err != nil {
		return nil, fmt.Errorf("reading webhook_imap_repairs: %w", err)
	}
	if err := json.Unmarshal(body, &rec.Report); err != nil {
		return nil, fmt.Errorf("decoding the repair report: %w", err)
	}
	rec.Report.Moved = rec.Report.Moved[:min(len(rec.Report.Moved), repairListLimit)]
	rec.Report.Removed = rec.Report.Removed[:min(len(rec.Report.Removed), repairListLimit)]
	rec.Report.Kept = rec.Report.Kept[:min(len(rec.Report.Kept), repairListLimit)]
	return &rec, nil
}
