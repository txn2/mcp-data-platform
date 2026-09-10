package resource

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ErrDeleteContent marks a delete that failed while removing the stored
// objects, before the record was touched.
//
// It is told apart from a failed record delete because the two leave the
// library in different states and the caller reports them differently: the
// content failure leaves the resource whole and readable, while a record
// failure leaves a row whose head object is gone.
var ErrDeleteContent = errors.New("deleting resource content")

// DeleteResource removes a managed resource: the objects its content lives in,
// and then the record that points at them. The version rows go with the record
// (ON DELETE CASCADE).
//
// The blobs go first so a failure cannot leave a live object no row points at,
// which is the leak nothing afterwards can find.
//
// Notifying the consumers keyed on the deleted resource -- the MCP resource
// registration under its URI, the tables registered over its file -- is the
// caller's, because the two doors into this differ on when it happens: the REST
// route answers its client before it runs them, and a tool call runs them
// before it reports.
func DeleteResource(ctx context.Context, deps Deps, res *Resource) error {
	if err := deleteAllBlobs(ctx, deps, res); err != nil {
		return fmt.Errorf("removing the resource's stored objects: %w (%w)", err, ErrDeleteContent)
	}
	if err := deps.Store.Delete(ctx, res.ID); err != nil {
		return fmt.Errorf("deleting the resource record: %w", err)
	}
	return nil
}

// deleteAllBlobs removes the head blob and every recorded version's blob.
//
// The head is the one that must succeed: leaving it behind is a live object no
// row points at, and the caller reports the failure. Prior versions are
// best-effort — an already-superseded blob that resists deletion must not make
// the resource undeletable — and a failure is logged for reclamation.
func deleteAllBlobs(ctx context.Context, deps Deps, res *Resource) error {
	if deps.S3Client == nil {
		return nil
	}
	if err := deps.S3Client.DeleteObject(ctx, deps.S3Bucket, res.S3Key); err != nil {
		return fmt.Errorf("deleting resource blob: %w", err)
	}
	if deps.Versions == nil {
		return nil
	}
	versions, err := deps.Versions.ListVersions(ctx, res.ID)
	if err != nil {
		slog.Warn("resource delete: version list failed, prior version blobs left behind",
			msgError, err, logKeyResourceID, res.ID) // #nosec G706 -- server-generated ID
		return nil
	}
	for _, v := range versions {
		if v.S3Key == res.S3Key {
			continue // already deleted above
		}
		if err := deps.S3Client.DeleteObject(ctx, deps.S3Bucket, v.S3Key); err != nil {
			slog.Warn("resource delete: version blob not deleted", msgError, err,
				logKeyResourceID, res.ID, // #nosec G706 -- server-generated ID
				pathParamVersion, v.Version)
		}
	}
	return nil
}
