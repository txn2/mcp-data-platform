package toolkit

import (
	"context"
	"errors"
	"fmt"
)

// RunAssetWrite is the persistence an export tool performs for its portal
// asset, as the three calls every export toolkit already has, so the rule for
// a named export inside a managed-script run is written once for all of them
// (#1854).
type RunAssetWrite struct {
	// Lookup finds the asset a key names, reporting its id.
	Lookup func(ctx context.Context, key string) (assetID string, found bool)
	// Insert stores the new asset the export built, carrying key as its
	// idempotency key.
	Insert func(ctx context.Context, key string) error
	// Version records the export's content as the asset's next version and
	// reports the version number.
	Version func(ctx context.Context, assetID string) (int, error)
}

// ErrObjectUnreferenced marks a PersistRunAsset failure after which no row
// names the object the export uploaded: the caller deletes it, or it stays in
// the bucket with nothing pointing at it (#1903). A failure without it left
// the new asset row in place, which names the object.
var ErrObjectUnreferenced = errors.New("the uploaded object is not referenced")

// PersistRunAsset writes an export made inside a run under the script's output
// identity key: the next version of the asset the key names, or, the first
// time, the new asset newID carrying the key and its first version. Two runs
// racing on the first write both insert; the unique key makes one lose, and
// the loser writes its version onto the winner's asset.
func PersistRunAsset(ctx context.Context, key, newID string, w RunAssetWrite) (assetID string, version int, err error) {
	assetID = newID
	inserted := false
	if existing, found := w.Lookup(ctx, key); found {
		assetID = existing
	} else if insertErr := w.Insert(ctx, key); insertErr != nil {
		existing, found := w.Lookup(ctx, key)
		if !found {
			return "", 0, fmt.Errorf("saving the asset record: %w (%w)", insertErr, ErrObjectUnreferenced)
		}
		assetID = existing
	} else {
		inserted = true
	}
	version, err = w.Version(ctx, assetID)
	if err != nil {
		if !inserted {
			return "", 0, fmt.Errorf("recording the asset version: %w (%w)", err, ErrObjectUnreferenced)
		}
		return "", 0, fmt.Errorf("recording the asset version: %w", err)
	}
	return assetID, version, nil
}
