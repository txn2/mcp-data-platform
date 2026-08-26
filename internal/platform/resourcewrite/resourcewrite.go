// Package resourcewrite lets something other than a browser write a managed
// resource (#1487).
//
// A managed resource is the only kind of file an asset can reference, and until
// now the only way to put one there was a person at an upload form. That left
// the data half of a referencing asset unrefreshable by the platform itself: an
// agent could rewrite the report on a schedule and could not rewrite the CSV
// the report reads.
//
// This is the same two writes the REST surface makes, against the same store,
// the same blob client and the same version trail, with the same scope
// permission rule applied to the caller's own claims. A revision written here
// is indistinguishable from one uploaded through the portal: same id, same
// canonical URI, same filename, same history, same retention -- which is what
// keeps every citation, prompt attachment and asset reference pointing at it
// resolving across the write.
package resourcewrite

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// The three ways a write does not happen, told apart because each is the
// caller's to act on differently.
var (
	// ErrRefused is a permission decision: the caller may not write where they
	// asked. The message it wraps states what to change.
	ErrRefused = errors.New("managed-resource write refused")
	// ErrNoSuchResource is a replacement naming a file that is not there, or
	// one the caller cannot see. The two are deliberately the same answer: a
	// caller who should not learn the resource exists must not be able to tell
	// them apart.
	ErrNoSuchResource = errors.New("no such managed resource")
	// ErrUnavailable is the deployment lacking the layer the write needs. It is
	// reported as itself rather than as a failure, so a caller is never told a
	// write happened that could not have.
	ErrUnavailable = errors.New("managed-resource write unavailable")
)

// Writer creates managed resources and replaces their content.
type Writer struct {
	deps resource.Deps
	// registered re-registers the written resource with the MCP server and
	// fires resources/list_changed, so a client holding the old content
	// re-reads it. Without it a replacement is invisible to every client that
	// has already listed.
	registered func(*resource.Resource)
}

// Deps is what a writer is assembled from: the record store, the blob client
// the content lives in, and the callback that tells connected clients the
// resource list moved.
type Deps struct {
	Store       resource.Store
	Blobs       resource.S3Client
	Bucket      string
	URIScheme   string
	MaxVersions int
	Registered  func(*resource.Resource)
}

// New builds the writer, or nil when the deployment has no managed-resource
// layer to write into: no record store, or nowhere to put the bytes. A nil
// writer is what a deployment without managed resources has, and the surfaces
// bound to it report that reason rather than accepting a write that goes
// nowhere.
//
// A store with no version trail still yields a writer. Creating does not need
// one -- the trail is recorded when it is there and skipped when it is not,
// exactly as the upload route treats it -- while replacing does, and says so.
func New(d Deps) *Writer {
	if d.Store == nil || d.Blobs == nil {
		return nil
	}
	versions, _ := d.Store.(resource.VersionStore)
	return &Writer{
		deps: resource.Deps{
			Store:       d.Store,
			Versions:    versions,
			S3Client:    d.Blobs,
			S3Bucket:    d.Bucket,
			URIScheme:   d.URIScheme,
			MaxVersions: d.MaxVersions,
		},
		registered: d.Registered,
	}
}

// Create files new content as a managed resource under the caller's identity.
//
// The scope permission is checked before anything is written, and the refusal
// names the scope rather than the file: what the caller has to change is where
// they filed it, not what they filed.
func (w *Writer) Create(
	ctx context.Context, in resource.NewResource, claims resource.Claims,
) (*resource.Resource, error) {
	if !resource.CanWriteScope(claims, in.Scope, in.ScopeID) {
		return nil, fmt.Errorf("you cannot write to %s: %w", ScopePhrase(in.Scope, in.ScopeID), ErrRefused)
	}
	res, err := resource.CreateResource(ctx, w.deps, &claims, in)
	if err != nil {
		return nil, fmt.Errorf("could not create the managed resource: %w", err)
	}
	w.notify(res)
	return res, nil
}

// Replace records new content as the resource's next revision. The id, the
// canonical mcp:// URI and the filename are unchanged by contract -- the
// revision path keys the new blob on a fresh per-revision directory and moves
// the head onto it -- so every asset referencing the resource resolves to the
// new bytes without being re-saved.
//
// It returns the version number the content was recorded as, which is what
// makes the write checkable from the version history rather than only from the
// bytes.
func (w *Writer) Replace(
	ctx context.Context, id string, up resource.RevisionUpload, claims resource.Claims,
) (*resource.Resource, int, error) {
	res, err := w.Get(ctx, id, claims)
	if err != nil {
		return nil, 0, err
	}
	if !resource.CanModifyResource(claims, res) {
		return nil, 0, fmt.Errorf("you cannot replace the content of a file in %s: %w",
			ScopePhrase(res.Scope, res.ScopeID), ErrRefused)
	}
	if w.deps.Versions == nil {
		return nil, 0, fmt.Errorf("this deployment keeps no version history for managed resources, "+
			"so content cannot be replaced: %w", ErrUnavailable)
	}

	updated, version, err := resource.ReviseContent(ctx, w.deps, res, &claims, up)
	if err != nil {
		return nil, 0, fmt.Errorf("could not replace the managed resource's content: %w", err)
	}
	w.notify(updated)
	return updated, version.Version, nil
}

// Get reads a resource the caller may see, so a surface can settle what a
// replacement is about to change before it changes it -- the stored filename a
// replacement must keep, and whether the file is there at all, before a payload
// is decoded.
//
// A resource the caller cannot see reads as absent: those two are deliberately
// one answer, because a caller who should not learn the resource exists must not
// be able to tell them apart. A read that FAILED is a third answer and stays
// one, for the reason given at the check below.
func (w *Writer) Get(ctx context.Context, id string, claims resource.Claims) (*resource.Resource, error) {
	res, err := w.deps.Store.Get(ctx, id)
	if err != nil && !resource.IsNotFound(err) {
		// A store that could not answer is not a store that answered "no". A
		// scheduled run told its file is gone would stop refreshing it and say
		// so in the run log; told the lookup failed, it retries on its next
		// fire. Only the absent-or-forbidden pair is deliberately one answer.
		return nil, fmt.Errorf("could not read the managed resource: %w", err)
	}
	if res == nil || !resource.CanAccessResource(claims, res) {
		return nil, fmt.Errorf("there is no managed resource %q you can see: %w", id, ErrNoSuchResource)
	}
	return res, nil
}

func (w *Writer) notify(res *resource.Resource) {
	if w.registered != nil {
		w.registered(res)
	}
}

// ScopePhrase names a scope the way a refusal has to name it: the thing the
// caller must change, in words they can act on, never the id of a record they
// may not be allowed to know exists.
func ScopePhrase(scope resource.Scope, scopeID string) string {
	switch scope {
	case resource.ScopeGlobal:
		return "the global scope, which is administrators only"
	case resource.ScopePersona:
		return fmt.Sprintf("the %q persona scope, which is that persona's administrators only", scopeID)
	case resource.ScopeUser:
		return "another user's scope"
	default:
		return fmt.Sprintf("the %q scope", scope)
	}
}
