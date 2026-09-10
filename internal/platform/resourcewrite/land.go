package resourcewrite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// defaultLandingSummary labels the revision a landed export records when its
// caller named no change summary.
const defaultLandingSummary = "Content replaced by an export"

// Lander lands an export's bytes in the managed resource at a path, creating
// the file the first time and recording a new version of it every time after
// (#1663).
//
// It is the one implementation of toolkit.ResourceLander, shared by every export
// tool, and it is the Writer's own two writes reached by address instead of by
// id. That is the whole of what it adds: an export names where the file goes,
// the platform decides whether that is a create or a revision, and the file's
// id, its canonical URI and the tables registered over it are the same ones
// across every run.
//
// The bytes are streamed. Nothing in this path holds the content whole, which is
// what lets an ingestion script land a file far larger than anything it could
// pass through its own runtime.
type Lander struct {
	w *Writer
	// follow moves the tables registered over a replaced file onto the new
	// version and says what happened to each (#1536). Bound by the composition
	// root after the registrar exists; nil reports nothing.
	follow func(ctx context.Context, resourceID string, version int) []string
	// claims derives the acting identity from a request context, for the
	// surfaces whose caller is a request. A caller that holds the identity
	// itself uses Land.
	claims func(ctx context.Context) resource.Claims
	// maxBytes is the managed-resource library's own upload ceiling. It is the
	// destination's limit rather than the export's: a file this platform would
	// refuse at the upload form is one it refuses here, at the same size, so
	// the two doors into the library cannot disagree about what fits through.
	maxBytes int64
}

// LanderDeps is what a lander is assembled from.
type LanderDeps struct {
	Writer *Writer
	// Claims derives the acting identity from a request context. Empty takes
	// CallerClaims, which is what every request-driven surface uses; a test
	// substitutes its own.
	Claims func(ctx context.Context) resource.Claims
	// MaxUploadBytes is resources.managed.max_upload_bytes; non-positive takes
	// the library's default.
	MaxUploadBytes int64
}

// NewLander builds the lander, or nil when there is no writer to land through.
// A nil lander is what a deployment with no managed-resource library has, and
// the export tools bound to it report that reason rather than accepting a
// destination that goes nowhere.
func NewLander(d LanderDeps) *Lander {
	if d.Writer == nil {
		return nil
	}
	claims := d.Claims
	if claims == nil {
		claims = CallerClaims
	}
	return &Lander{
		w:        d.Writer,
		claims:   claims,
		maxBytes: resource.NormalizeMaxUploadBytes(d.MaxUploadBytes),
	}
}

// CallerClaims derives the managed-resource identity of whoever a tool call
// belongs to: the principal that made it, the persona it was authorized under,
// and the address of the person an unattended caller acts for.
//
// It is here, rather than at the composition root, so the derivation a landing is
// authorized by is the one a test of a landing exercises. A context with no
// platform identity yields empty claims, which reach only what an anonymous
// caller reaches.
func CallerClaims(ctx context.Context) resource.Claims {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return resource.Claims{}
	}
	return resource.BuildClaims(pc.UserID, pc.UserEmail, pc.PersonaName, pc.Roles, pc.IsAdmin).
		ActingFor(pc.OnBehalfOfEmail)
}

// SetTableFollower binds the callback that moves the tables registered over a
// replaced file. It is a setter rather than a constructor parameter because the
// table registrar is assembled after the export tools it serves, the same
// sequencing the asset toolkit's registrar has.
func (l *Lander) SetTableFollower(follow func(ctx context.Context, resourceID string, version int) []string) {
	if l == nil {
		return
	}
	l.follow = follow
}

// CheckResourceDestination validates the destination and the caller's authority
// over it, writing nothing.
//
// It exists so a refusal lands before the upstream call that produces the bytes:
// an export that cannot be stored anywhere should not have asked an upstream for
// anything, least of all through a method that changes something there.
func (l *Lander) CheckResourceDestination(ctx context.Context, dest toolkit.ResourceDestination) error {
	_, err := l.plan(ctx, dest, l.claims(ctx))
	return err
}

// LandResource streams content into the destination under the identity the
// request carries.
func (l *Lander) LandResource(
	ctx context.Context, dest toolkit.ResourceDestination, content io.Reader, contentType string,
) (*toolkit.ResourceLanding, error) {
	return l.Land(ctx, dest, content, contentType, l.claims(ctx))
}

// Land is LandResource for a caller that holds the acting identity itself
// rather than carrying it on the context: a managed-script run, whose writes go
// through the platform's own funnels rather than across the MCP middleware that
// would have put a principal on the context.
func (l *Lander) Land(
	ctx context.Context, dest toolkit.ResourceDestination,
	content io.Reader, contentType string, claims resource.Claims,
) (*toolkit.ResourceLanding, error) {
	plan, err := l.plan(ctx, dest, claims)
	if err != nil {
		return nil, err
	}
	w := write{
		content:      &ceilingReader{r: content, max: l.maxBytes},
		mimeType:     plan.mimeType(contentType),
		declaredType: contentType,
		dest:         dest,
		claims:       claims,
	}
	if err := resource.ValidateMIMEType(w.mimeType); err != nil {
		return nil, fmt.Errorf("the content cannot be stored as a managed resource: %w", err)
	}
	if plan.existing != nil {
		return l.replace(ctx, plan, w)
	}
	return l.create(ctx, plan, w)
}

// write is one landing's inputs past the address: the bytes, the type they are
// stored under, what their producer said they were, and who is writing them.
type write struct {
	content      *ceilingReader
	mimeType     string
	declaredType string
	dest         toolkit.ResourceDestination
	claims       resource.Claims
}

// create files the bytes as a new managed resource at the planned address.
func (l *Lander) create(ctx context.Context, plan landing, w write) (*toolkit.ResourceLanding, error) {
	res, err := l.w.Create(ctx, resource.NewResource{
		Scope: plan.scope, ScopeID: plan.scopeID,
		Path: plan.path, Filename: plan.filename,
		DisplayName: w.dest.DisplayName, Description: w.dest.Description,
		Tags:             normalizedTags(w.dest.Tags),
		Content:          w.content,
		MIMEType:         w.mimeType,
		DeclaredMIMEType: w.declaredType,
	}, w.claims)
	if err != nil {
		return nil, w.content.refuseOr(err)
	}
	return landingOf(res, 1, true, nil), nil
}

// replace records the bytes as the next version of the file already at the
// planned address, and follows the tables registered over it.
func (l *Lander) replace(ctx context.Context, plan landing, w write) (*toolkit.ResourceLanding, error) {
	summary := strings.TrimSpace(w.dest.ChangeSummary)
	if summary == "" {
		summary = defaultLandingSummary
	}
	res, version, err := l.w.Replace(ctx, plan.existing.ID, resource.RevisionUpload{
		Content: w.content, MIMEType: w.mimeType, ChangeSummary: summary,
	}, w.claims)
	if err != nil {
		return nil, w.content.refuseOr(err)
	}
	return landingOf(res, version, false, l.followTables(ctx, res.ID, version)), nil
}

// followTables reports what the new version did to the tables registered over
// the file. A deployment with no registrar has none to report.
func (l *Lander) followTables(ctx context.Context, resourceID string, version int) []string {
	if l.follow == nil {
		return nil
	}
	return l.follow(ctx, resourceID, version)
}

// landing is a validated destination: the address the bytes are going to, and
// the record already there when there is one.
type landing struct {
	scope    resource.Scope
	scopeID  string
	path     string
	filename string
	uri      string
	existing *resource.Resource
}

// mimeType settles the type the bytes are stored under.
//
// The producer's declaration stands when it says something specific, because it
// is the only party that saw the bytes. Where it could only say "text" or
// "bytes" the destination's own filename decides, and that is not a nicety: a
// CSV has no content signature, so an upstream serving one as text/plain would
// otherwise be stored as plain text and be unregisterable as a table, which is
// the whole point of landing it in a managed resource.
func (p landing) mimeType(declared string) string {
	norm := contenttype.Normalize(declared)
	if norm != "" && !contenttype.IsGeneric(norm) && norm != contenttype.PlainText {
		return norm
	}
	if named := contenttype.TypeForFilename(p.filename); named != "" {
		return named
	}
	if norm != "" {
		return norm
	}
	return contenttype.OctetStream
}

// plan validates a destination and resolves what is at its address.
//
// The address is built the way every managed resource's address is built, so the
// file a landing finds is the file the mcp:// URI in a citation names, and the
// alias trail a move left behind is followed: a file somebody refiled is still
// the file that path names, and the landing reports the address it actually
// wrote rather than the one it was asked for.
func (l *Lander) plan(ctx context.Context, dest toolkit.ResourceDestination, claims resource.Claims) (landing, error) {
	if l.w.deps.Versions == nil {
		return landing{}, fmt.Errorf("this deployment keeps no version history for managed resources, so an "+
			"export cannot land in one: a second export to the same path would have nowhere to record the "+
			"version it wrote: %w", ErrUnavailable)
	}
	p, err := l.address(dest, claims)
	if err != nil {
		return landing{}, err
	}
	existing, err := l.w.deps.Store.GetByURI(ctx, p.uri)
	if err != nil && !resource.IsNotFound(err) {
		// A store that could not answer is not a store that answered "no".
		// Creating on a failed read would file a second file at an address that
		// already has one, and the caller would never learn which it wrote.
		return landing{}, fmt.Errorf("could not read what is filed at %s: %w", p.uri, err)
	}
	if existing == nil {
		if !resource.CanWriteScope(claims, p.scope, p.scopeID) {
			return landing{}, fmt.Errorf("you cannot write to %s: %w", ScopePhrase(p.scope, p.scopeID), ErrRefused)
		}
		return p, nil
	}
	if !resource.CanAccessResource(claims, existing) || !resource.CanModifyResource(claims, existing) {
		return landing{}, fmt.Errorf("there is already a file at %s that you cannot replace: %w", p.uri, ErrRefused)
	}
	p.existing = existing
	return p, nil
}

// address validates the destination's parts and composes the canonical URI they
// name. Every rule is the managed-resource layer's own, so an address an export
// may write is one an upload may write.
func (l *Lander) address(dest toolkit.ResourceDestination, claims resource.Claims) (landing, error) {
	scope, scopeID := resource.ResolveScopeFor(resource.Scope(strings.TrimSpace(dest.Scope)),
		strings.TrimSpace(dest.ScopeID), claims)
	if err := resource.ValidateScope(scope, scopeID); err != nil {
		return landing{}, fmt.Errorf("the destination library is not one this platform has: %w", err)
	}
	path := strings.TrimSpace(dest.Path)
	if err := resource.ValidatePath(path); err != nil {
		return landing{}, fmt.Errorf("%w. A path is the folder chain the file is filed under inside the "+
			"library, for example \"datasets\" or \"datasets/media-manager/shows\"", err)
	}
	filename, err := resource.SanitizeFilename(dest.Filename)
	if err != nil {
		return landing{}, fmt.Errorf("the destination needs a plain file name: %w", err)
	}
	if err := resource.ValidateDisplayName(dest.DisplayName); err != nil {
		return landing{}, err //nolint:wrapcheck // the validator's sentence names the field and the rule
	}
	if err := resource.ValidateDescription(dest.Description); err != nil {
		return landing{}, err //nolint:wrapcheck // the validator's sentence names the field and the rule
	}
	if err := resource.ValidateTags(normalizedTags(dest.Tags)); err != nil {
		return landing{}, err //nolint:wrapcheck // the validator's sentence names the field and the rule
	}
	return landing{
		scope: scope, scopeID: scopeID, path: path, filename: filename,
		uri: resource.BuildURI(l.w.deps.URIScheme, scope, scopeID, path, filename),
	}, nil
}

// landingOf renders a written resource as the result every export tool reports.
func landingOf(res *resource.Resource, version int, created bool, tables []string) *toolkit.ResourceLanding {
	out := &toolkit.ResourceLanding{
		ResourceID:  res.ID,
		Reference:   knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetResource, ResourceID: res.ID}.URN(),
		URI:         res.URI,
		Filename:    res.Filename,
		Scope:       string(res.Scope),
		ScopeID:     res.ScopeID,
		Path:        res.Path,
		ContentType: res.MIMEType,
		SizeBytes:   res.SizeBytes,
		Version:     version,
		Created:     created,
		Tables:      tables,
	}
	out.Message = landingMessage(out)
	return out
}

// landingMessage says what the write did to the file's identity, because that is
// what separates this destination from an export asset: the reference and the
// URI are the same next time, and whatever reads them reads the new bytes
// without being re-pointed.
func landingMessage(out *toolkit.ResourceLanding) string {
	message := fmt.Sprintf("Replaced the content of the managed resource %s (%d bytes), recorded as version %d "+
		"and restorable from the file's version history. The id, uri and filename are unchanged, so "+
		"everything referencing this file now serves the new bytes.", out.URI, out.SizeBytes, out.Version)
	if out.Created {
		message = fmt.Sprintf("Created the managed resource %s (%d bytes) and recorded it as version 1. "+
			"Export to the same path again to record the next version of this same file: the id, the "+
			"reference and the uri above do not change, so an asset referencing it and a table registered "+
			"over it follow the new content.", out.URI, out.SizeBytes)
	}
	return strings.Join(append([]string{message}, out.Tables...), " ")
}

// normalizedTags substitutes an empty list for an absent one, which is what the
// store records rather than a null.
func normalizedTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// ErrTooLarge reports content that passed the managed-resource library's upload
// ceiling. It is its own answer because it is the caller's to act on: the file
// is too big for this library, and no retry of the same export changes that.
var ErrTooLarge = errors.New("content exceeds the managed-resource upload ceiling")

// ceilingReader bounds a stream at the library's upload ceiling. Past it the
// read fails, which aborts the storage write (the transfer manager abandons the
// incomplete multipart upload, so no partial object is left), and the exceeded
// flag is what tells the caller's failure apart from a storage fault.
type ceilingReader struct {
	r        io.Reader
	max      int64
	n        int64
	exceeded bool
}

func (c *ceilingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.max > 0 && c.n > c.max {
		c.exceeded = true
		return n, fmt.Errorf("content is larger than %s: %w", resource.DescribeUploadLimit(c.max), ErrTooLarge)
	}
	return n, err //nolint:wrapcheck // transparent pass-through of the wrapped reader's error
}

// refuseOr names the ceiling as the cause when the stream passed it, and
// otherwise returns the write's own error. The flag decides rather than the
// error text, because the storage layer wraps a read failure in its own.
func (c *ceilingReader) refuseOr(err error) error {
	if c.exceeded {
		return fmt.Errorf("the response is larger than %s, the managed-resource upload ceiling "+
			"(resources.managed.max_upload_bytes); nothing was written: %w",
			resource.DescribeUploadLimit(c.max), ErrTooLarge)
	}
	return err
}

// Ref is the resource destination a toolkit is wired to before the lander
// behind it exists.
//
// The export tools are assembled with the portal layer, which is built before
// the managed-resource layer that lands a resource: a direct lander would be
// nil at the only moment it could be passed. A Ref is handed over instead and
// bound once the library exists, so a deployment that has one gains the
// destination and a deployment that has none answers the reason rather than
// failing in a way that reads as a fault.
type Ref struct {
	mu sync.RWMutex
	l  *Lander
}

// Bind publishes the lander every export tool holding this Ref will land
// through. Binding nil leaves the destination unavailable, which is what a
// deployment with no managed-resource library has.
func (r *Ref) Bind(l *Lander) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.l = l
}

// CheckResourceDestination validates the destination, or reports that this
// deployment has no managed-resource library to land in.
func (r *Ref) CheckResourceDestination(ctx context.Context, dest toolkit.ResourceDestination) error {
	l := r.lander()
	if l == nil {
		return ErrNoLibrary
	}
	return l.CheckResourceDestination(ctx, dest)
}

// LandResource streams the content through the bound lander.
func (r *Ref) LandResource(
	ctx context.Context, dest toolkit.ResourceDestination, content io.Reader, contentType string,
) (*toolkit.ResourceLanding, error) {
	l := r.lander()
	if l == nil {
		return nil, ErrNoLibrary
	}
	return l.LandResource(ctx, dest, content, contentType)
}

// Land streams the content through the bound lander under an identity the caller
// holds itself, which is what a managed-script run has: its principal is on its
// MCP session, not on the context its own output writes cross.
func (r *Ref) Land(
	ctx context.Context, dest toolkit.ResourceDestination,
	content io.Reader, contentType string, claims resource.Claims,
) (*toolkit.ResourceLanding, error) {
	l := r.lander()
	if l == nil {
		return nil, ErrNoLibrary
	}
	return l.Land(ctx, dest, content, contentType, claims)
}

// lander reads what is bound. A nil Ref reads as unbound rather than panicking:
// the composition root hands one over unconditionally, and a deployment with no
// portal layer to hold it has no library either, so "no library" is the true
// answer and a nil check at every call site would be four copies of it.
func (r *Ref) lander() *Lander {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.l
}

// ErrNoLibrary is what an export naming a resource destination is told on a
// deployment that has no managed-resource library: a database and an S3
// connection for resource storage. It names the missing piece rather than
// reporting a failure, and it is said instead of a write, never after one.
var ErrNoLibrary = errors.New("this deployment has no managed-resource library to land in: it needs a database " +
	"and an S3 connection for resource storage. Ask an administrator to configure one, or export to a portal " +
	"asset instead. Nothing was written")
