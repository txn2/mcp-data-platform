package sitewire

import (
	"context"
	"errors"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/bucketsource"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// A publish from a bucket prefix (#2593): the objects under a prefix on an S3
// connection the caller's persona may use with s3_list and s3_object, inside
// the connection's bucket_prefix, each at its path under the prefix. The
// threat model is bucketsource's; what this adds is the release's limits,
// checked from the listing before any object is read and held again by every
// read: an object read as a range of at most sites.max_file_bytes and one
// byte, and the release refused once what was read passes
// sites.max_release_bytes, so at most parallelReads objects past the total
// are ever held.

// parallelReads is how many objects one publish reads at once.
const parallelReads = 8

// CheckBucket refuses a source the caller cannot read, before a job is
// started for it.
func (s sources) CheckBucket(ctx context.Context, src bucketsource.Source) error {
	_, err := s.open(ctx, src)
	return err
}

// Bucket reads the objects under the prefix as the files of a release.
func (s sources) Bucket(ctx context.Context, src bucketsource.Source) ([]sitesvc.InputFile, error) {
	b, err := s.open(ctx, src)
	if err != nil {
		return nil, err
	}
	limits := s.limits()
	objects, err := b.List(ctx, bucketsource.Limits{
		MaxObjects: limits.MaxFiles, MaxObjectBytes: limits.MaxFileBytes, MaxTotalBytes: limits.MaxReleaseBytes,
		ObjectsKey: "sites.max_release_files", ObjectBytesKey: "sites.max_file_bytes", TotalBytesKey: "sites.max_release_bytes",
	})
	if err != nil {
		return nil, refusal(err)
	}
	files := make([]sitesvc.InputFile, len(objects))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelReads)
	for i, o := range objects {
		g.Go(func() error {
			body, _, err := b.Read(gctx, o)
			if err != nil {
				return err //nolint:wrapcheck // a refusal naming the object
			}
			files[i] = sitesvc.InputFile{Path: o.Path, Body: body}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, refusal(err)
	}
	return files, nil
}

func (s sources) open(ctx context.Context, src bucketsource.Source) (*bucketsource.Bucket, error) {
	if s.buckets == nil {
		return nil, &sitesvc.RefusedError{Msg: "this deployment cannot publish from a bucket: it has no S3 connections"}
	}
	b, err := s.buckets.Open(ctx, src)
	if err != nil {
		return nil, refusal(err)
	}
	return b, nil
}

// refusal is a bucket source's refusal in the words the service answers with.
func refusal(err error) error {
	if errors.Is(err, bucketsource.ErrRefused) {
		return &sitesvc.RefusedError{Msg: "nothing was published: " + err.Error()}
	}
	return err
}
