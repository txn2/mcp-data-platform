package scriptsrc

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// What a site's media mount reads (#2591): whether a caller may mount a
// folder, and the file a mounted path names. A mount is a publishing act
// checked once, when it is made: the site's audience then reads the folder's
// files through it, as a declared reference grants an asset's readers.

// CheckFolder refuses a folder the caller may not read, or one the library
// does not hold. A library the caller cannot see is reported as absent, so the
// refusal reveals nothing about it.
func (r *Reader) CheckFolder(ctx context.Context, lib resource.ScopeFilter, folder string, claims resource.Claims) error {
	folder = strings.Trim(folder, pathSep)
	if !resource.CanSeeLibrary(claims, lib) {
		return fmt.Errorf("folder %s: %w", folder, ErrNotFound)
	}
	if fs, ok := r.store.(resource.FolderStore); ok {
		exists, err := fs.FolderExists(ctx, lib, folder)
		if err != nil {
			return fmt.Errorf("reading folder %s: %w", folder, err)
		}
		if exists {
			return nil
		}
	}
	page, _, err := r.store.List(ctx, resource.Filter{Scopes: []resource.ScopeFilter{lib}, Path: folder, Limit: 1})
	if err != nil {
		return fmt.Errorf("reading folder %s: %w", folder, err)
	}
	if len(page) == 0 {
		return fmt.Errorf("folder %s: %w", folder, ErrNotFound)
	}
	return nil
}

// AtPath is the file at tail ("folder/sub/name.jpg") in one library, nil when
// there is none. It checks no caller: what a mount serves was decided when it
// was made.
//
// Only a file filed at that path now is served. The store also answers the
// URI a file was moved away from, so its old links keep working; a mount does
// not follow that, or a file moved out of a mounted folder, into a library
// the site's audience could never read, would still be served at its old
// address. A tail no file can be filed at (bytes that are not UTF-8, a NUL or
// another control character) names nothing, and is not looked up.
func (r *Reader) AtPath(ctx context.Context, lib resource.ScopeFilter, tail string) (*resource.Resource, error) {
	if !fileableTail(tail) {
		return nil, nil //nolint:nilnil // no file can be at that path
	}
	uri := resource.URIInLibrary(r.scheme, lib.Scope, lib.ScopeID, tail)
	res, err := r.store.GetByURI(ctx, uri)
	if resource.IsNotFound(err) {
		return nil, nil //nolint:nilnil // no file at that path
	}
	if err != nil {
		return nil, fmt.Errorf("reading the managed resource: %w", err)
	}
	if res.URI != uri || res.Scope != lib.Scope || res.ScopeID != lib.ScopeID {
		return nil, nil //nolint:nilnil // the file was moved away from that path
	}
	return res, nil
}

// fileableTail reports whether a file can be filed at tail: UTF-8 text with
// no control character, which is all a resource path or name ever holds.
func fileableTail(tail string) bool {
	if !utf8.ValidString(tail) {
		return false
	}
	return !strings.ContainsFunc(tail, unicode.IsControl)
}

// Blobs is the store the library's bytes are kept in, and its bucket.
func (r *Reader) Blobs() (blobs resource.S3Client, bucket string) { return r.blobs, r.bucket }
