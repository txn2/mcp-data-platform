package indexjobs

import (
	"bytes"
	"crypto/sha256"
)

// TextHash returns the canonical SHA-256 of an item's embed text: the
// exact hash planVectors stores in Vector.TextHash and dedups against.
// It is exported so a Sink's FindGaps can detect content drift with the
// identical hash the worker uses, rather than re-deriving it and risking
// the two definitions diverging.
func TextHash(text string) []byte {
	sum := sha256.Sum256([]byte(text))
	return sum[:]
}

// ContentGap reports whether the live item set differs from the
// persisted vectors by membership or text: a unit was added or removed,
// or an existing item's embed text changed. It is the content-drift gap
// check a Sink whose corpus lives in the running process (not a
// countable DB table) uses in FindGaps, so the kind re-indexes on a real
// change and stays quiet otherwise.
//
// existing is keyed by item id, as Sink.ListExisting returns it. Model
// and dimension drift (an embedding-provider swap) is intentionally out
// of scope here: that requires a configuration change and restart, where
// the boot re-index re-embeds against the new provider through the
// worker's model-aware dedup (planVectors). The per-sweep reconciler
// check is about content, so a steady-state corpus produces no jobs.
func ContentGap(items []Item, existing map[string]Vector) bool {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		prev, ok := existing[item.ItemID]
		if !ok || !bytes.Equal(prev.TextHash, TextHash(item.Text)) {
			return true
		}
		seen[item.ItemID] = struct{}{}
	}
	// Every persisted unit must still be present in the live set;
	// comparing distinct seen ids (not len(items)) against existing
	// catches a removal without assuming items has no duplicate ItemIDs.
	return len(seen) != len(existing)
}
