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

// RepairedVersion is the version of source's last repair, 0 when none ran.
func (r *Repairs) RepairedVersion(ctx context.Context, source string) (int, error) {
	var version int
	err := r.db.QueryRowContext(ctx, `SELECT version FROM webhook_imap_repairs WHERE source = $1`, source).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading webhook_imap_repairs: %w", err)
	}
	return version, nil
}

// RecordRepair records that source's repair by version ran and what it did.
// A source repaired before keeps what the earlier pass did (#2842): the
// moves, removals and collapses are added to it, and kept is replaced, since
// a second pass reads exactly the folders the first kept.
func (r *Repairs) RecordRepair(ctx context.Context, source string, version int, rep RepairReport) error {
	body, err := json.Marshal(rep)
	if err != nil {
		return fmt.Errorf("encoding the repair report: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO webhook_imap_repairs (source, version, moved, removed, removed_files, collapsed, collapsed_files, kept, report)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (source) DO UPDATE SET repaired_at = now(), version = EXCLUDED.version,
		   moved = webhook_imap_repairs.moved + EXCLUDED.moved,
		   removed = webhook_imap_repairs.removed + EXCLUDED.removed,
		   removed_files = webhook_imap_repairs.removed_files + EXCLUDED.removed_files,
		   collapsed = webhook_imap_repairs.collapsed + EXCLUDED.collapsed,
		   collapsed_files = webhook_imap_repairs.collapsed_files + EXCLUDED.collapsed_files,
		   kept = EXCLUDED.kept,
		   report = jsonb_build_object(
		     'moved', COALESCE(webhook_imap_repairs.report->'moved', '[]'::jsonb) || COALESCE(EXCLUDED.report->'moved', '[]'::jsonb),
		     'removed', COALESCE(webhook_imap_repairs.report->'removed', '[]'::jsonb) || COALESCE(EXCLUDED.report->'removed', '[]'::jsonb),
		     'collapsed', COALESCE(webhook_imap_repairs.report->'collapsed', '[]'::jsonb) || COALESCE(EXCLUDED.report->'collapsed', '[]'::jsonb),
		     'kept', COALESCE(EXCLUDED.report->'kept', '[]'::jsonb))`,
		source, version, len(rep.Moved), len(rep.Removed), rep.RemovedFiles(),
		len(rep.Collapsed), rep.CollapsedFiles(), len(rep.Kept), body)
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
	RepairedAt   time.Time `json:"repaired_at"`
	Moved        int       `json:"moved"`
	Removed      int       `json:"removed"`
	RemovedFiles int       `json:"removed_files"`
	// Collapsed and CollapsedFiles are the copies of a message no event names
	// deleted into the folder that stays, and the files they held (#2842).
	Collapsed      int          `json:"collapsed"`
	CollapsedFiles int          `json:"collapsed_files"`
	Kept           int          `json:"kept"`
	Report         RepairReport `json:"report"`
}

// Get returns source's repair, nil when it has not run.
func (r *Repairs) Get(ctx context.Context, source string) (*RepairRecord, error) {
	var (
		rec  RepairRecord
		body []byte
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT repaired_at, moved, removed, removed_files, collapsed, collapsed_files, kept, report
		   FROM webhook_imap_repairs WHERE source = $1`, source).
		Scan(&rec.RepairedAt, &rec.Moved, &rec.Removed, &rec.RemovedFiles, &rec.Collapsed, &rec.CollapsedFiles, &rec.Kept, &body)
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
	if rec.Report.Collapsed == nil {
		rec.Report.Collapsed = []RemovedFolder{}
	}
	rec.Report.Collapsed = rec.Report.Collapsed[:min(len(rec.Report.Collapsed), repairListLimit)]
	rec.Report.Kept = rec.Report.Kept[:min(len(rec.Report.Kept), repairListLimit)]
	return &rec, nil
}
