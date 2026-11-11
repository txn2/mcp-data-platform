package sitewire

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitegw"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// mediaFolders checks a folder a site mounts against the caller (#2591).
type mediaFolders struct{ reader *scriptsrc.Reader }

func libraryOf(m site.Mount) resource.ScopeFilter {
	return resource.ScopeFilter{Scope: resource.Scope(m.Scope), ScopeID: m.ScopeID}
}

// CheckFolder refuses a folder the caller may not read.
func (f mediaFolders) CheckFolder(ctx context.Context, m site.Mount) error {
	if err := f.reader.CheckFolder(ctx, libraryOf(m), m.Folder, resourcewrite.CallerClaims(ctx)); err != nil {
		return fmt.Errorf("mounting %s: %w", m.Prefix, err)
	}
	return nil
}

// mediaSource is what the gateway serves a mounted path from.
type mediaSource struct {
	store  *sitestore.Store
	reader *scriptsrc.Reader
}

// Mounts is a site's mounts.
func (m mediaSource) Mounts(ctx context.Context, siteID string) ([]site.Mount, error) {
	return m.store.Media(ctx, siteID) //nolint:wrapcheck // the store's own sentence
}

// Find is the file at rel under a mount, read from the library's bucket by
// the key its record holds, never from the request.
func (m mediaSource) Find(ctx context.Context, mt site.Mount, rel string) (*sitegw.MediaFile, error) {
	res, err := m.reader.AtPath(ctx, libraryOf(mt), mt.Folder+"/"+rel)
	if err != nil || res == nil {
		return nil, err //nolint:wrapcheck // the reader's own sentence
	}
	blobs, bucket := m.reader.Blobs()
	return &sitegw.MediaFile{
		Name: res.Filename, ContentType: res.MIMEType, ModTime: res.UpdatedAt,
		Object: blobserve.Object{Store: blobs, Bucket: bucket, Key: res.S3Key, Size: res.SizeBytes},
	}, nil
}
