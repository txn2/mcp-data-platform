// Package scriptsrc reads the managed-resource text a managed script is made
// from: the source a create or an update names by reference instead of
// carrying inline (#2114), and the page script, SQL or template a script loads
// as a string with load("res:...") (#2113).
//
// A reference is resolved under the caller's own access, as fetch resolves
// one: a file the caller could not read is not there. The text is read from a
// version of the file -- its head, or the version a script pinned when it was
// saved -- so a run and a replay of it read the bytes the save read.
package scriptsrc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// referencePrefix is the reference form a search hit and a write report.
const referencePrefix = "mcp:resource:"

// ErrNotFound is a reference that names no file the caller may read.
var ErrNotFound = errors.New("no managed resource you can read is at that reference")

// Reader reads text from the managed-resource library.
type Reader struct {
	store    resource.Store
	versions resource.VersionStore
	blobs    resource.S3Client
	bucket   string
	scheme   string
}

// New builds a reader over the library, or nil when there is no library to
// read: no record store or no blob storage.
func New(store resource.Store, blobs resource.S3Client, bucket, scheme string) *Reader {
	if store == nil || blobs == nil {
		return nil
	}
	versions, _ := store.(resource.VersionStore)
	if scheme == "" {
		scheme = resource.DefaultURIScheme
	}
	return &Reader{store: store, versions: versions, blobs: blobs, bucket: bucket, scheme: scheme}
}

// Resolve finds the file a reference names: an mcp:resource:<id> reference,
// or the mcp:// URI the file answers at. A file the caller may not read is
// reported as absent, never as forbidden, so a reference reveals nothing.
func (r *Reader) Resolve(ctx context.Context, ref string, claims resource.Claims) (*resource.Resource, error) {
	ref = strings.TrimSpace(ref)
	var (
		res *resource.Resource
		err error
	)
	switch {
	case strings.HasPrefix(ref, referencePrefix):
		res, err = r.store.Get(ctx, strings.TrimPrefix(ref, referencePrefix))
	case strings.HasPrefix(ref, r.scheme+"://"):
		res, err = r.store.GetByURI(ctx, ref)
	default:
		return nil, fmt.Errorf("reference %q is not a managed-resource reference: pass mcp:resource:<id>, or the file's %s:// uri", ref, r.scheme)
	}
	if err != nil && !resource.IsNotFound(err) {
		return nil, fmt.Errorf("reading the managed resource: %w", err)
	}
	if res == nil || err != nil || !resource.CanAccessResource(claims, res) {
		return nil, fmt.Errorf("reference %s: %w", ref, ErrNotFound)
	}
	return res, nil
}

// Head is the version number the file's current content was recorded as, or
// 0 on a deployment that keeps no version history.
func (r *Reader) Head(ctx context.Context, res *resource.Resource) (int, error) {
	if r.versions == nil {
		return 0, nil
	}
	list, err := r.versions.ListVersions(ctx, res.ID)
	if err != nil {
		return 0, fmt.Errorf("reading the versions of %s: %w", res.URI, err)
	}
	if len(list) == 0 {
		return 0, nil
	}
	return list[0].Version, nil
}

// Text reads a version of the file as text, at most limit bytes. Version 0 is
// the file's current content. A file that is not UTF-8 text is refused, as is
// one past the limit.
func (r *Reader) Text(ctx context.Context, res *resource.Resource, version int, limit int64) (string, error) {
	key, size := res.S3Key, res.SizeBytes
	if version > 0 {
		if r.versions == nil {
			return "", fmt.Errorf("file %s: this deployment keeps no version history to read version %d from", res.URI, version)
		}
		v, err := r.versions.GetVersion(ctx, res.ID, version)
		if resource.IsNotFound(err) {
			return "", fmt.Errorf("file %s: version %d is no longer kept: %w", res.URI, version, ErrNotFound)
		}
		if err != nil {
			return "", fmt.Errorf("reading version %d of %s: %w", version, res.URI, err)
		}
		key, size = v.S3Key, v.SizeBytes
	}
	if size > limit {
		return "", fmt.Errorf("file %s is %d bytes, more than the %d a script reads as text", res.URI, size, limit)
	}
	body, _, err := r.blobs.GetObject(ctx, r.bucket, key)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", res.URI, err)
	}
	if int64(len(body)) > limit {
		return "", fmt.Errorf("file %s is %d bytes, more than the %d a script reads as text", res.URI, len(body), limit)
	}
	if !utf8.Valid(body) {
		return "", fmt.Errorf("file %s is not UTF-8 text", res.URI)
	}
	return string(body), nil
}

// Source reads the text a save names by reference as a script's source
// (#2114): the file's current content, held to the source size a script may
// have, under the caller's own access.
func (r *Reader) Source(ctx context.Context, ref string, claims resource.Claims, limit int64) (string, error) {
	if r == nil {
		return "", errors.New("this deployment has no managed-resource library to read a source from; send source")
	}
	res, err := r.Resolve(ctx, ref, claims)
	if err != nil {
		return "", err
	}
	return r.Text(ctx, res, 0, limit)
}

// ByID reads the file a script version pinned, by id. The pin was resolved
// under its author's access when the version was saved; a run reads what the
// save read.
func (r *Reader) ByID(ctx context.Context, id string) (*resource.Resource, error) {
	res, err := r.store.Get(ctx, id)
	if resource.IsNotFound(err) {
		return nil, fmt.Errorf("the file was deleted: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading the managed resource: %w", err)
	}
	return res, nil
}

// Scheme is the URI scheme the library's files answer at.
func (r *Reader) Scheme() string { return r.scheme }
