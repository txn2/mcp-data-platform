package capacity

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

// Reclaiming orphans (#2470). The listing counts an object no row references
// as an orphan; a reclaim deletes the ones old enough that no writer can still
// be on its way to recording them. The listing collects them (walk) and the
// reclaim decides, after the listing, which it may delete:
//
//   - only an object older than the reclaim age, apart from the counting
//     grace, since a multipart upload's LastModified is when it began;
//   - only one still unreferenced when every reference is read again after
//     the listing, so a row written while the listing ran keeps its object;
//   - never under a purpose where more than half of what the listing met is
//     an orphan (past a small floor): that is a deployment reading another
//     deployment's bucket, or a database restored apart from its bucket, and
//     deleting would empty it;
//   - at most maxReclaimPerPass a pass, so a backlog is cleared over several
//     passes and one pass's reach is bounded.
//
// A key carries an owner's and an asset's ids, so no key is logged, labeled or
// put in an error: the reclaim reports counts per purpose.

// maxReclaimPerPass is the most objects one pass collects and deletes.
const maxReclaimPerPass = 10000

// breakerFloor is how many candidates a purpose may have before the
// half-orphaned check applies: a purpose holding a handful of objects can be
// mostly orphans honestly, and deleting a handful reaches little.
const breakerFloor = 100

// candidate is one orphan old enough to reclaim.
type candidate struct {
	bucket  string
	key     string
	purpose string
	size    int64
}

// reclaimCandidates is the orphans one listing collected, at most
// maxReclaimPerPass of them, and how many it met per purpose, collected or
// not.
type reclaimCandidates struct {
	items []candidate
	count map[string]int64
}

func newReclaimCandidates() reclaimCandidates {
	return reclaimCandidates{count: map[string]int64{}}
}

// add counts c and keeps it while there is room.
func (r *reclaimCandidates) add(c candidate) {
	r.count[c.purpose]++
	if len(r.items) < maxReclaimPerPass {
		r.items = append(r.items, c)
	}
}

// isReclaimable reports whether an orphan is older than the reclaim cutoff.
// An object with no LastModified is never reclaimed: its age is unknown.
func isReclaimable(o s3adapter.WalkedObject, cutoff time.Time) bool {
	return !cutoff.IsZero() && !o.LastModified.IsZero() && o.LastModified.Before(cutoff)
}

// Reclaim decides which of the listing's candidates may be deleted and, in
// delete mode, deletes them, recording each deleted object in r.Reclaimed and
// taking its bytes off the listing's usage (the delete's own observation takes
// off the object). In report mode it deletes nothing and logs how many it
// would have. It returns how many it deleted.
func (s *Scanner) Reclaim(ctx context.Context, r *ScanResult, mode string) int64 {
	if mode != ReclaimReport && mode != ReclaimDelete {
		return 0
	}
	refused := r.refusedPurposes()
	for _, p := range refused {
		slog.WarnContext(ctx, "capacity: orphan reclaim refused: more than half of a purpose's objects read as orphans",
			"purpose", p, "orphans", r.candidates.count[p], "objects", r.purposeObjects[p])
	}
	if len(r.candidates.items) == 0 {
		return 0
	}
	refs, err := s.loadRefs(ctx)
	if err != nil {
		slog.WarnContext(ctx, "capacity: orphan reclaim skipped: reading references failed", "error", logsan.SanitizeForLog(err.Error()))
		return 0
	}
	eligible, deleted, failed := s.reclaimEach(ctx, r, mode, refs, refused)
	s.logReclaim(ctx, mode, eligible, deleted, failed)
	return deleted
}

// reclaimEach goes through the candidates: each still unreferenced, under a
// purpose not refused, is eligible, and in delete mode deleted.
func (s *Scanner) reclaimEach(ctx context.Context, r *ScanResult, mode string, refs map[string]bucketRefs, refused []string) (eligible map[string]int64, deleted, failed int64) {
	eligible = map[string]int64{}
	for _, c := range r.candidates.items {
		if slices.Contains(refused, c.purpose) || stillReferenced(refs, c) {
			continue
		}
		eligible[c.purpose]++
		switch {
		case mode != ReclaimDelete:
		case s.deleteCandidate(ctx, r, c):
			deleted++
		default:
			failed++
		}
	}
	return eligible, deleted, failed
}

// deleteCandidate deletes one orphan and records it.
func (s *Scanner) deleteCandidate(ctx context.Context, r *ScanResult, c candidate) bool {
	w := s.Walkers[c.bucket]
	if w == nil || w.DeleteObject(ctx, c.bucket, c.key) != nil {
		return false
	}
	r.Reclaimed[c.purpose]++
	k := usageKey{c.bucket, c.purpose}
	if u, ok := r.Usage[k]; ok {
		u.bytes = max(u.bytes-c.size, 0)
		r.Usage[k] = u
	}
	return true
}

// logReclaim writes one line for the pass, with counts only.
func (*Scanner) logReclaim(ctx context.Context, mode string, eligible map[string]int64, deleted, failed int64) {
	if len(eligible) == 0 {
		return
	}
	attrs := []any{"mode", mode, "deleted", deleted, "failed", failed}
	for _, p := range slices.Sorted(maps.Keys(eligible)) {
		attrs = append(attrs, "eligible_"+p, eligible[p])
	}
	slog.InfoContext(ctx, "capacity: orphan reclaim", attrs...)
}

// stillReferenced applies the listing's own orphan rule to c against the
// references read after the listing: a row now names it, it is a tile beside
// an object a row now names, or it is an operator's upload.
func stillReferenced(refs map[string]bucketRefs, c candidate) bool {
	if strings.HasPrefix(c.key, mapUploadsPrefix) {
		return true
	}
	b, ok := refs[c.bucket]
	if !ok {
		return true
	}
	if _, named := b.keys[c.key]; named {
		return true
	}
	return b.ownsTile(c.key)
}

// refusedPurposes is each purpose whose candidates are more than half of the
// objects the listing met under it, past breakerFloor, sorted.
func (r ScanResult) refusedPurposes() []string {
	var out []string
	for p, n := range r.candidates.count {
		if n > breakerFloor && n*2 > r.purposeObjects[p] {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}
