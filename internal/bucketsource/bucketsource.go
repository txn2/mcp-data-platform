// Package bucketsource reads the objects under a bucket prefix on an S3
// connection, as the caller (#2593): a site's source tree or a media folder of
// tens of thousands of files arrives from where it already sits, without
// passing through tool calls.
//
// Threat model.
//
//   - Boundary: the caller (a person in the portal, or an agent's tool call,
//     identified by the PlatformContext on the request) reaching a bucket on
//     one named S3 connection. The objects then cross a second boundary into
//     a store the caller writes: a site's release (manage_site publish) or a
//     folder of the managed-resource library (manage_resource extract), each
//     under the caller's own authority there, checked by that store.
//   - What a weaker persona can make it do: nothing beyond s3_list and
//     s3_object get on the same connection. The caller's persona must hold
//     both tools and the connection, by the persona rules every tool call is
//     authorized under (pkg/persona: a persona denied s3_object, or granted
//     s3_list alone, reads no object this way either); the bucket must be
//     inside the connection's bucket_prefix (#2584), by the S3 toolkit's own
//     check; and the connection is always named, so the default connection is
//     never read for a caller who named none (an empty connection name is
//     "platform-level" to the persona rules, and must not reach a bucket).
//   - What is stored: the bytes of each object, copied into the site's store
//     or the resource library at its path under the prefix. A name no folder
//     or release can hold (a parent reference, an empty or dotted segment, a
//     backslash, a control character) is refused before anything is read.
//   - Secrets: the connection's credentials stay inside its client. No
//     response, log, audit row or error carries them: a failure from the
//     object store is reported by its code (AccessDenied, NoSuchBucket) and
//     the bucket and prefix the caller named, never by the client's error
//     text, which can name the endpoint it was configured with.
//   - Size: every limit is checked from the listing, before a body is read.
//     An object can be replaced between the listing and the read, so each is
//     read as a range of at most its limit and one byte more, never whole,
//     and the bytes read are added up against the total limit as they
//     arrive: a bucket someone else writes cannot make a read hold more than
//     the limits allow.
package bucketsource

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync/atomic"
	"unicode"

	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// ErrRefused is a source the caller can act on: an unknown connection, one the
// persona may not use, a bucket outside the connection's limit, a name no
// store can hold, or a limit passed. The message says which.
var ErrRefused = errors.New("bucket source refused")

// pageSize is how many keys one listing call asks for, and slash the
// separator of a key's segments.
const (
	pageSize = 1000
	slash    = "/"
)

// The S3 toolkit's two tools, which reading a bucket stands in for: a persona
// must hold both. TestFromToolkits_ReadsTheS3ToolkitsOwnToolNames holds them
// to the toolkit's own list.
const (
	toolList   = "s3_list"
	toolObject = "s3_object"
)

// readTools are the tools a read of a bucket takes, in the order a refusal
// names the first one missing.
var readTools = []string{toolList, toolObject}

// Source names the objects under a prefix of a bucket on a connection.
type Source struct {
	Connection string `json:"connection"`
	Bucket     string `json:"bucket"`
	Prefix     string `json:"prefix,omitempty"`
}

// String is the source as a reader names it: s3://bucket/prefix on connection.
func (s Source) String() string {
	return fmt.Sprintf("s3://%s/%s on connection %s", s.Bucket, s.Prefix, s.Connection)
}

// Object is one object under the prefix: its key, its path relative to the
// prefix, and the size the listing reported.
type Object struct {
	Key  string
	Path string
	Size int64
}

// Limits bound a listing: how many objects, how large one may be, and how
// much they may add up to. The *Key fields are the configuration keys that
// lift each limit, which a refusal names. A non-positive bound is unbounded.
type Limits struct {
	MaxObjects     int
	MaxObjectBytes int64
	MaxTotalBytes  int64

	ObjectsKey, ObjectBytesKey, TotalBytesKey string
}

// Access opens sources as the caller.
type Access struct {
	Clients Clients
	// Allowed reports whether a persona may use a connection; nil allows
	// every connection, as a deployment with no personas allows every
	// connection to its tool calls.
	Allowed func(persona, connection string) bool
	// ToolAllowed reports whether a persona holds a tool; nil allows every
	// tool, as a deployment with no personas does.
	ToolAllowed func(persona, tool string) bool
}

// Bucket is one source opened for the caller.
type Bucket struct {
	client Client
	src    Source
	// lim is what the last List checked, which every Read is held to, and
	// read the bytes the reads have taken so far.
	lim  Limits
	read atomic.Int64
}

// Source is what the bucket was opened on.
func (b *Bucket) Source() Source { return b.src }

// Open checks the caller may read the source and returns it. The caller's
// persona comes from the request's PlatformContext.
func (a *Access) Open(ctx context.Context, src Source) (*Bucket, error) {
	if a == nil || a.Clients == nil {
		return nil, refused("this deployment has no S3 connections to read from")
	}
	src.Connection, src.Bucket = strings.TrimSpace(src.Connection), strings.TrimSpace(src.Bucket)
	src.Prefix = normalizePrefix(src.Prefix)
	switch {
	case src.Connection == "":
		return nil, refused("name the S3 connection to read from")
	case src.Bucket == "":
		return nil, refused("name the bucket to read from")
	case unsafeName(src.Prefix):
		return nil, refused("prefix %q is not a path an object can be read under", src.Prefix)
	}
	if err := a.permit(ctx, src.Connection); err != nil {
		return nil, err
	}
	client, err := a.Clients.ReadClient(src.Connection, src.Bucket)
	if err != nil {
		return nil, refused("%s", err.Error())
	}
	return &Bucket{client: client, src: src}, nil
}

// permit refuses a caller whose persona may not use the connection, or lacks
// a tool reading a bucket stands in for.
func (a *Access) permit(ctx context.Context, connection string) error {
	persona := ""
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		persona = pc.PersonaName
	}
	if a.Allowed != nil && !a.Allowed(persona, connection) {
		return refused("connection %q is not one your persona may use", connection)
	}
	if a.ToolAllowed == nil {
		return nil
	}
	for _, tool := range readTools {
		if !a.ToolAllowed(persona, tool) {
			return refused("your persona may not use %s, which reading a bucket takes", tool)
		}
	}
	return nil
}

// normalizePrefix is a prefix as a folder: no leading slash, and a trailing
// one when it names anything, so "site" never also matches "sitemap.xml".
func normalizePrefix(p string) string {
	p = strings.TrimLeft(strings.TrimSpace(p), slash)
	if p != "" && !strings.HasSuffix(p, slash) {
		p += slash
	}
	return p
}

// List is every object under the prefix, page by page, its limits checked
// from the listing before any body is read. A key ending in a slash is a
// folder marker and is passed over.
func (b *Bucket) List(ctx context.Context, lim Limits) ([]Object, error) {
	b.lim = lim
	l := &listing{out: []Object{}, lim: lim}
	token := ""
	for {
		page, err := b.client.ListObjects(ctx, b.src.Bucket, b.src.Prefix, "", pageSize, token)
		if err != nil {
			return nil, b.storeFailure(ctx, "listing", err)
		}
		if err := b.take(l, page.Objects); err != nil {
			return nil, err
		}
		if !page.IsTruncated || page.NextContinueToken == "" {
			return l.out, nil
		}
		token = page.NextContinueToken
	}
}

// listing is what a List has taken so far, against its limits.
type listing struct {
	out   []Object
	total int64
	lim   Limits
}

// take adds one page's objects to the listing, passing over folder markers
// and checking each against the limits before it counts.
func (b *Bucket) take(l *listing, page []s3client.ObjectInfo) error {
	for _, o := range page {
		obj, keep, err := b.object(o)
		if err != nil {
			return err
		}
		if !keep {
			continue
		}
		if err := l.lim.checkObject(obj, len(l.out)+1, l.total+obj.Size); err != nil {
			return err
		}
		l.total += obj.Size
		l.out = append(l.out, obj)
	}
	return nil
}

// object is one listed key as an object under the prefix, false for a folder
// marker, and a refusal for a name no store can hold.
func (b *Bucket) object(o s3client.ObjectInfo) (Object, bool, error) {
	rel := strings.TrimPrefix(o.Key, b.src.Prefix)
	if rel == "" || strings.HasSuffix(rel, slash) {
		return Object{}, false, nil
	}
	if unsafeName(rel) {
		return Object{}, false, refused("object %q has a name no folder can hold (an empty, dotted or parent segment, a backslash or a control character); move it, or read a prefix without it", o.Key)
	}
	return Object{Key: o.Key, Path: rel, Size: o.Size}, true, nil
}

// unsafeName reports a relative path with an empty, "." or ".." segment, a
// leading slash, a backslash, or a control character.
func unsafeName(p string) bool {
	if p == "" {
		return false
	}
	if strings.ContainsAny(p, "\\") || strings.ContainsFunc(p, unicode.IsControl) || strings.HasPrefix(p, slash) {
		return true
	}
	for seg := range strings.SplitSeq(strings.TrimSuffix(p, slash), slash) {
		if seg == "" || seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

func (lim Limits) checkObject(o Object, count int, total int64) error {
	switch {
	case lim.MaxObjects > 0 && count > lim.MaxObjects:
		return refused("the prefix holds more than the %d objects %s allows", lim.MaxObjects, lim.ObjectsKey)
	case lim.MaxObjectBytes > 0 && o.Size > lim.MaxObjectBytes:
		return refused("object %q is %d bytes, more than the %d %s allows", o.Key, o.Size, lim.MaxObjectBytes, lim.ObjectBytesKey)
	case lim.MaxTotalBytes > 0 && total > lim.MaxTotalBytes:
		return refused("the prefix holds more than the %d bytes %s allows", lim.MaxTotalBytes, lim.TotalBytesKey)
	}
	return nil
}

// Read is one object's bytes and the content type it was stored with, held
// to the limits the listing was checked against: the object can have been
// replaced since, so it is read as a range of at most its limit and one byte
// more, refused past the limit, and the bytes every read takes are added up
// against the total limit. Reads may run at once. The type is what its writer
// declared, which a reader treats as a create's declaration: a specific one
// is kept, a generic one is detected from the bytes. A zero-byte object has
// no byte a range can start at, and is read as empty with no type.
func (b *Bucket) Read(ctx context.Context, o Object) (body []byte, contentType string, err error) {
	length := int64(math.MaxInt64)
	if limit := b.lim.MaxObjectBytes; limit > 0 && limit < math.MaxInt64 {
		length = limit + 1
	}
	got, err := b.client.GetObjectRange(ctx, b.src.Bucket, o.Key, 0, length)
	if errors.Is(err, s3client.ErrRangeNotSatisfiable) {
		return []byte{}, "", nil
	}
	if err != nil {
		return nil, "", b.storeFailure(ctx, "reading "+o.Key, err)
	}
	if limit := b.lim.MaxObjectBytes; limit > 0 && int64(len(got.Body)) > limit {
		return nil, "", refused("object %q is more than the %d bytes %s allows; it grew after the prefix was listed", o.Key, limit, b.lim.ObjectBytesKey)
	}
	if total := b.read.Add(int64(len(got.Body))); b.lim.MaxTotalBytes > 0 && total > b.lim.MaxTotalBytes {
		return nil, "", refused("the objects under the prefix add up to more than the %d bytes %s allows; they grew after the prefix was listed", b.lim.MaxTotalBytes, b.lim.TotalBytesKey)
	}
	return got.Body, got.ContentType, nil
}

// storeFailure reports an object store failure by its code alone, never by
// the client's error text, and logs it sanitized for the operator.
func (b *Bucket) storeFailure(ctx context.Context, doing string, err error) error {
	code := "an error"
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) && coded.ErrorCode() != "" {
		code = coded.ErrorCode()
	}
	slog.WarnContext(ctx, "bucket source: the object store refused", "connection", logsan.SanitizeForLog(b.src.Connection),
		"bucket", logsan.SanitizeForLog(b.src.Bucket), "code", logsan.SanitizeForLog(code))
	return refused("the object store answered %s %s %s", logsan.SanitizeForLog(code), doing, b.src)
}

// refusalError is a refusal the caller can act on: its message alone, read by
// identity as ErrRefused.
type refusalError struct{ msg string }

func (r *refusalError) Error() string { return r.msg }

// Unwrap is ErrRefused.
func (*refusalError) Unwrap() error { return ErrRefused }

func refused(format string, args ...any) error {
	return &refusalError{msg: fmt.Sprintf(format, args...)}
}
