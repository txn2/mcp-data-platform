// Package resourcereclass applies the operator's resources.index rules again
// to the managed resources no writer chose an index policy for (#2111), in
// batches under an advisory lock, so a change to the rules reaches what is
// already stored without one burst of work per row.
package resourcereclass

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// reclassifyBatch is how many rows one reclassification statement reads and
// rewrites. Rewriting is a few column writes per row, so a batch is cheap;
// the pause between batches is what keeps a 100,000-row library from holding
// the table busy in one burst.
const reclassifyBatch = 500

// reclassifyPause is the rest between batches.
const reclassifyPause = 50 * time.Millisecond

// reclassifyLock is the advisory lock one reclassification holds, so two
// administrators, or two replicas, do not walk the table at once.
const reclassifyLock int64 = 0x7265736978 // "resix"

// ErrReclassifyRunning reports a reclassification already under way.
var ErrReclassifyRunning = errors.New("a reclassification is already running")

// ReclassifyResult is what one reclassification did.
type ReclassifyResult struct {
	Examined int `json:"examined"`
	Changed  int `json:"changed"`
	// Cleared is rows whose kept content text was removed because their
	// policy reads none.
	Cleared int `json:"cleared"`
}

// Reclassifier applies the operator's resources.index rules to the resources
// no writer chose a policy for, in batches.
type Reclassifier struct {
	db        *sql.DB
	resolver  *indexpolicy.Resolver
	resources *indexjobs.Producer
	folders   *indexjobs.Producer
	pause     time.Duration
}

// NewReclassifier builds a reclassifier. The producers receive the jobs a
// change owes: the resource's own when it becomes indexed, its folder's when
// it enters or leaves the set the folder is summarized over. Either may be nil.
func NewReclassifier(db *sql.DB, resolver *indexpolicy.Resolver, resources, folders *indexjobs.Producer) *Reclassifier {
	return &Reclassifier{db: db, resolver: resolver, resources: resources, folders: folders, pause: reclassifyPause}
}

// reclassRow is one resource as the rules read it.
type reclassRow struct {
	id, policy, path, mime, scope, scopeID, uploader string
	size                                             int64
	explicit, hasText                                bool
}

// Run walks every resource once, in id order and in batches. A policy a
// writer chose is left as it is; every other is decided by the rules again.
// A row whose policy changes drops its vector and its kept text, so the index
// worker rebuilds what its new policy reads and nothing else; a row under a
// policy that reads no content keeps none. It holds an advisory lock for the
// whole walk and answers ErrReclassifyRunning when another holds it.
func (r *Reclassifier) Run(ctx context.Context) (ReclassifyResult, error) {
	var out ReclassifyResult
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return out, fmt.Errorf("resourceindex: reclassify connection: %w", err)
	}
	defer conn.Close() //nolint:errcheck // returning the connection to the pool
	var got bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, reclassifyLock).Scan(&got); err != nil {
		return out, fmt.Errorf("resourceindex: reclassify lock: %w", err)
	}
	if !got {
		return out, ErrReclassifyRunning
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, reclassifyLock)
	}()

	cursor := ""
	for {
		rows, err := r.batch(ctx, conn, cursor)
		if err != nil {
			return out, err
		}
		if len(rows) == 0 {
			return out, nil
		}
		if err := r.apply(ctx, conn, rows, &out); err != nil {
			return out, err
		}
		cursor = rows[len(rows)-1].id
		if err := sleepCtx(ctx, r.pause); err != nil {
			return out, err
		}
	}
}

func (*Reclassifier) batch(ctx context.Context, conn *sql.Conn, cursor string) ([]reclassRow, error) {
	rows, err := conn.QueryContext(ctx, `SELECT id, index_policy, index_policy_explicit, path, mime_type,
		size_bytes, scope, COALESCE(scope_id, ''), uploader_sub, content_text <> ''
		FROM resources WHERE id > $1 ORDER BY id LIMIT 500`, cursor)
	if err != nil {
		return nil, fmt.Errorf("resourceindex: reclassify read: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	out := make([]reclassRow, 0, reclassifyBatch)
	for rows.Next() {
		var x reclassRow
		if err := rows.Scan(&x.id, &x.policy, &x.explicit, &x.path, &x.mime, &x.size,
			&x.scope, &x.scopeID, &x.uploader, &x.hasText); err != nil {
			return nil, fmt.Errorf("resourceindex: reclassify scan: %w", err)
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resourceindex: reclassify rows: %w", err)
	}
	return out, nil
}

// reclassPlan is what one batch changes: the rows moving to each policy, the
// rows whose kept text their policy no longer reads, and the folders whose
// entries the moves touch.
type reclassPlan struct {
	changed map[indexpolicy.Policy][]string
	stale   []string
	folders map[string]bool
}

// plan reads one batch against the rules.
func (r *Reclassifier) plan(rows []reclassRow, out *ReclassifyResult) reclassPlan {
	p := reclassPlan{changed: map[indexpolicy.Policy][]string{}, folders: map[string]bool{}}
	for _, x := range rows {
		out.Examined++
		next := indexpolicy.Policy(x.policy)
		if !x.explicit {
			next = r.resolver.Resolve(indexpolicy.File{
				Path: x.path, MIMEType: x.mime, SizeBytes: x.size, Scope: x.scope, Origin: originOfUploader(x.uploader),
			}, "").Policy
		}
		if string(next) != x.policy {
			p.changed[next] = append(p.changed[next], x.id)
			lib := resource.ScopeFilter{Scope: resource.Scope(x.scope), ScopeID: x.scopeID}
			p.folders[resource.FolderKey(lib, x.path)] = true
			continue
		}
		if !next.ReadsContent() && x.hasText {
			p.stale = append(p.stale, x.id)
		}
	}
	return p
}

// apply rewrites one batch: each changed row under its new policy, and the
// kept text of every row whose policy reads none.
func (r *Reclassifier) apply(ctx context.Context, conn *sql.Conn, rows []reclassRow, out *ReclassifyResult) error {
	p := r.plan(rows, out)
	if err := r.move(ctx, conn, p.changed, out); err != nil {
		return err
	}
	if len(p.stale) > 0 {
		if _, err := conn.ExecContext(ctx, `UPDATE resources SET content_text = '' WHERE id = ANY($1)`,
			pq.Array(p.stale)); err != nil {
			return fmt.Errorf("resourceindex: reclassify clear: %w", err)
		}
		out.Cleared += len(p.stale)
	}
	for key := range p.folders {
		r.folders.NotifyWrite(ctx, key)
	}
	return nil
}

// move writes each changed row under its new policy, dropping its vector and
// kept text, and enqueues the ones still indexed.
func (r *Reclassifier) move(ctx context.Context, conn *sql.Conn, changed map[indexpolicy.Policy][]string, out *ReclassifyResult) error {
	for policy, ids := range changed {
		// A writer's choice made after the batch was read is left alone.
		res, err := conn.ExecContext(ctx, `UPDATE resources SET index_policy = $1,
			content_text = '', content_indexed_at = NULL,
			embedding = NULL, embedding_model = '', embedding_text_hash = NULL
			WHERE id = ANY($2) AND NOT index_policy_explicit`, string(policy), pq.Array(ids))
		if err != nil {
			return fmt.Errorf("resourceindex: reclassify write: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("resourceindex: reclassify write: %w", err)
		}
		out.Changed += int(n)
		if policy.Indexed() {
			for _, id := range ids {
				r.resources.NotifyWrite(ctx, id)
			}
		}
	}
	return nil
}

// originOfUploader reads where a stored file came from for the rules: the
// principal a script, the webhook receiver or the catalog scan writes under. A file a person
// uploaded and one an agent or an export wrote cannot be told apart once
// stored, and both read as an upload.
func originOfUploader(sub string) indexpolicy.Origin {
	switch {
	case strings.HasPrefix(sub, "script:"):
		return indexpolicy.OriginScript
	case sub == "system:webhooks":
		return indexpolicy.OriginWebhook
	case sub == "system:catalog":
		return indexpolicy.OriginCatalog
	default:
		return indexpolicy.OriginUpload
	}
}

// sleepCtx rests d between batches, and stops a walk whose context ended:
// checked first, because a timer and a done context both ready leave select
// to pick either.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("resourcereclass: %w", err)
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("resourcereclass: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}
