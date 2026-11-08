package sitesvc

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/approval"
)

// Objects stores a release's files.
type Objects interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	DeleteObject(ctx context.Context, bucket, key string) error
}

// Applications finds the application a hostname maps to.
type Applications interface {
	GetByName(ctx context.Context, name string) (*application.Application, error)
}

// Approvals is the queue a promotion that requires approval goes through.
type Approvals interface {
	SubmitRelease(ctx context.Context, actor approval.Actor, in approval.SubmitRelease) (*approval.Request, error)
	ReleaseApproved(ctx context.Context, releaseID, target, digest string) bool
	RecordReleaseSelfApproval(ctx context.Context, actor approval.Actor, in approval.SubmitRelease) error
}

// Verifier decides whether a hostname's verification step is done.
type Verifier interface {
	Verify(ctx context.Context, hostname, token string) error
}

// Invalidate tells this replica's gateway a route changed; nil does nothing.
type Invalidate func(hostOrSite string)
