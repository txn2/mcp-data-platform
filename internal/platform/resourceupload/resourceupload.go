// Package resourceupload is the single-use upload link (#2639): manage_resource
// action=upload_link issues a short-lived URL on the platform's own public
// origin, and one PUT of a file to it from the agent's shell files the file as
// a managed resource at the address the link was issued for, as the person who
// asked for it, with the checks a portal upload gets. The bytes never pass
// through a tool call, and nothing about object storage has to be public.
//
// The link's token is a bearer credential for the files it was issued for. It
// is handed to the caller once and never stored: a link is found by the
// token's SHA-256, a place on it taken by one atomic statement that also
// refuses a full, revoked or expired link, and it is bound to its folder, its
// declared type and its size.
//
// A link an external service uses (#2844) lives up to a day, so it outlasts a
// job queue, takes up to max_files files, and, issued with no filename, has
// the service name each file in the URL. A PUT that files nothing gives its
// place back, so the service can try again. The person who asked for a link
// sees it, and can revoke it, on the portal's Resources page.
package resourceupload

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// RoutePrefix is where a link is PUT to, followed by its token.
const RoutePrefix = "/api/v1/resources/uploads/"

// How long a link lives: DefaultTTL unless the caller asks for another
// lifetime, never more than MaxTTL, which a job waiting in a queue fits in
// (#2844).
const (
	DefaultTTL = 15 * time.Minute
	MaxTTL     = 24 * time.Hour
)

// MaxFiles is the most files one link takes.
const MaxFiles = 100

// tokenBytes is the token's randomness: 256 bits, which no one guesses.
const tokenBytes = 32

// keptAfterExpiry is how long a link's row is kept once it has expired, as
// the record of what it wrote; issuing a link sweeps older ones.
const keptAfterExpiry = 7 * 24 * time.Hour

// linkDescription describes a file a link issued with no description files.
const linkDescription = "Filed through an upload link."

// changeSummary labels a revision written through a link.
const changeSummary = "Uploaded through an upload link"

// Errors the HTTP surface answers by identity.
var (
	// ErrNoLink is a token no link answers to: unknown, used or expired,
	// which are told apart to no one.
	ErrNoLink = errors.New("this upload link is unknown, used or expired: ask for a new one with manage_resource action=upload_link")
	// ErrTooLarge is a file past the size the link was issued for.
	ErrTooLarge = errors.New("the file is larger than the size this upload link was issued for")
	// ErrRefused is a link that cannot be issued as asked.
	ErrRefused = errors.New("refused")
	// ErrBadName is a PUT whose file name the link does not take: none for a
	// link issued without one, another for a link issued with one, or a name
	// that is not a plain file name.
	ErrBadName = errors.New("bad file name")
)

// link is one issued Link as it is kept.
type link struct {
	ID          string
	UserID      string
	UserEmail   string
	Claims      resource.Claims
	Scope       resource.Scope
	ScopeID     string
	Path        string
	Filename    string
	DisplayName string
	Description string
	Tags        []string
	ContentType string
	IfExists    string
	MaxBytes    int64
	ExpiresAt   time.Time
	// MaxFiles is how many files the link takes, FilesUsed how many places
	// are taken, this PUT's among them (#2844).
	MaxFiles  int
	FilesUsed int
}

// Open is a link that can still take a file, as its issuer sees it on the
// Resources page. It never carries the token.
type Open struct {
	ID          string    `json:"id"`
	Scope       string    `json:"scope"`
	ScopeID     string    `json:"scope_id,omitempty"`
	Path        string    `json:"path"`
	Filename    string    `json:"filename,omitempty"`
	ContentType string    `json:"content_type"`
	MaxBytes    int64     `json:"max_bytes"`
	MaxFiles    int       `json:"max_files"`
	FilesUsed   int       `json:"files_used"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// Store keeps the links (PostgresStore).
type Store interface {
	// insert keeps l, found by tokenSHA256 alone.
	insert(ctx context.Context, l link, tokenSHA256 string) error
	// claim takes a place on the link tokenSHA256 names and returns it, or
	// ErrNoLink when no link with a place left, not revoked and not expired
	// at now answers to it.
	claim(ctx context.Context, tokenSHA256 string, now time.Time) (*link, error)
	// finish records the file a place filed, or, with no resourceID, why it
	// filed nothing, and gives the place back.
	finish(ctx context.Context, id, resourceID, failure string) error
	// open lists the links a person asked for that can still take a file.
	open(ctx context.Context, userID, email string, now time.Time) ([]Open, error)
	// revoke ends a link its issuer (or an administrator) names, ErrNoLink
	// when there is none to end.
	revoke(ctx context.Context, id string, by resource.Claims, now time.Time) error
	// sweep removes the links that expired before cutoff.
	sweep(ctx context.Context, cutoff time.Time) error
}

// Writer is what files the upload: the writer behind manage_resource.
type Writer interface {
	Create(ctx context.Context, in resource.NewResource, claims resource.Claims) (*resource.Resource, error)
	Replace(ctx context.Context, id string, up resource.RevisionUpload, claims resource.Claims) (*resource.Resource, int, error)
	Locate(ctx context.Context, addr toolkit.ResourceAddress, claims resource.Claims) (*resource.Resource, string, error)
}

// Service issues links and files what is PUT to them.
type Service struct {
	Store  Store
	Writer Writer
	// BaseURL is the platform's public origin (portal.public_base_url); a
	// link is not issued without one, since there is nothing to name.
	BaseURL func() string
	// MaxUploadBytes is the deployment's upload ceiling; non-positive is
	// resource.MaxUploadBytes.
	MaxUploadBytes int64
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// IssueRequest is a link as the caller asks for it, its address already
// resolved and checked by manage_resource.
type IssueRequest struct {
	Scope       resource.Scope
	ScopeID     string
	Path        string
	Filename    string
	DisplayName string
	Description string
	Tags        []string
	ContentType string
	IfExists    string
	// MaxBytes is the most each file may be.
	MaxBytes int64
	// TTL is how long the link lives; zero is DefaultTTL.
	TTL time.Duration
	// MaxFiles is how many files the link takes; zero is one.
	MaxFiles int
}

// Issued is a link as the caller is handed it, once. With a filename the
// file is PUT to URL; without one each file is PUT to URL/<filename>.
type Issued struct {
	ID        string    `json:"id"`
	URL       string    `json:"upload_url"`
	Method    string    `json:"method"`
	ExpiresAt time.Time `json:"expires_at"`
	MaxBytes  int64     `json:"max_bytes"`
	MaxFiles  int       `json:"max_files"`
	// NameInURL says each file is named by the path segment after URL.
	NameInURL bool `json:"name_in_url,omitempty"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) ceiling() int64 {
	if s.MaxUploadBytes > 0 {
		return s.MaxUploadBytes
	}
	return resource.MaxUploadBytes
}

// Issue keeps a link for in, writable by claims, and returns it.
func (s *Service) Issue(ctx context.Context, in IssueRequest, claims resource.Claims, userID string) (Issued, error) {
	if in.Filename != "" {
		// The name a create would file it under, from the tool or the portal.
		clean, err := resource.SanitizeFilename(in.Filename)
		if err != nil {
			return Issued{}, fmt.Errorf("filename must be a plain file name: %w: %w", err, ErrRefused)
		}
		in.Filename = clean
	}
	if err := s.check(in, claims); err != nil {
		return Issued{}, err
	}
	base := ""
	if s.BaseURL != nil {
		base = strings.TrimRight(strings.TrimSpace(s.BaseURL()), "/")
	}
	if base == "" {
		return Issued{}, fmt.Errorf("this deployment has no public address (portal.public_base_url) to give an upload link: %w", ErrRefused)
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	token, sum, err := newToken()
	if err != nil {
		return Issued{}, err
	}
	now := s.now()
	l := link{
		ID: uuid.NewString(), UserID: userID, UserEmail: claims.Email, Claims: claims,
		Scope: in.Scope, ScopeID: in.ScopeID, Path: in.Path, Filename: in.Filename, DisplayName: in.DisplayName,
		Description: in.Description, Tags: in.Tags, ContentType: in.ContentType, IfExists: in.IfExists,
		MaxBytes: in.MaxBytes, ExpiresAt: now.Add(ttl), MaxFiles: max(in.MaxFiles, 1),
	}
	_ = s.Store.sweep(ctx, now.Add(-keptAfterExpiry)) // the record of old links; a failed sweep is retried by the next issue
	if err := s.Store.insert(ctx, l, sum); err != nil {
		return Issued{}, fmt.Errorf("issuing an upload link: %w", err)
	}
	return Issued{
		ID: l.ID, URL: base + RoutePrefix + token, Method: "PUT", ExpiresAt: l.ExpiresAt, MaxBytes: l.MaxBytes,
		MaxFiles: l.MaxFiles, NameInURL: l.Filename == "",
	}, nil
}

// check refuses a link that could not be issued as asked: the rules a create
// of the same file meets before it reads any content, for the tool and the
// portal alike.
func (s *Service) check(in IssueRequest, claims resource.Claims) error {
	if err := checkPlacement(in); err != nil {
		return fmt.Errorf("the link's folder or type: %w: %w", err, ErrRefused)
	}
	switch {
	case !claims.CanWriteFolder(in.Scope, in.ScopeID, in.Path):
		return fmt.Errorf("you cannot write to that library, so no upload link is issued for it: %w", ErrRefused)
	case in.MaxBytes <= 0:
		return fmt.Errorf("size_bytes is required: the most the file may be, in bytes: %w", ErrRefused)
	case in.MaxBytes > s.ceiling():
		return fmt.Errorf("size_bytes %d is over this deployment's upload limit of %d bytes: %w", in.MaxBytes, s.ceiling(), ErrRefused)
	case in.TTL > MaxTTL:
		return fmt.Errorf("an upload link lives at most %d seconds: %w", int(MaxTTL.Seconds()), ErrRefused)
	case in.MaxFiles < 0 || in.MaxFiles > MaxFiles:
		return fmt.Errorf("max_files is how many files the link takes, 1 to %d: %w", MaxFiles, ErrRefused)
	case in.MaxFiles > 1 && in.Filename != "":
		return fmt.Errorf("a link for several files takes no filename: the service names each file in the URL: %w", ErrRefused)
	}
	return nil
}

// checkPlacement is the address and type a link files at, checked as a create
// checks them.
func checkPlacement(in IssueRequest) error {
	if err := resource.ValidateScope(in.Scope, in.ScopeID); err != nil {
		return err //nolint:wrapcheck // the rule's own sentence
	}
	if err := resource.ValidatePath(in.Path); err != nil {
		return err //nolint:wrapcheck // the rule's own sentence
	}
	if strings.TrimSpace(in.ContentType) == "" {
		return errors.New("content_type is required: the type of the file the link takes")
	}
	if err := resource.ValidateMIMEType(in.ContentType); err != nil {
		return err //nolint:wrapcheck // the rule's own sentence
	}
	switch in.IfExists {
	case "", "fail", "replace":
		return nil
	default:
		return fmt.Errorf("if_exists %q is not one a link takes: fail or replace", in.IfExists)
	}
}

// newToken is a fresh token and its SHA-256, the form it is kept by.
func newToken() (token, sum string, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("making an upload link: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, Hash(token), nil
}

// Hash is the form a token is kept and found by.
func Hash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// Upload files body, PUT to the link token names, in the link's folder as the
// person who asked for it. name is the file name the URL carries after the
// token, empty when it carries none; declared is the request's
// Content-Length, -1 when it sent none. A file filed takes one of the link's
// places; a PUT that files nothing gives its place back. remaining is how many
// more files the link takes.
func (s *Service) Upload(
	ctx context.Context, token, name string, body io.Reader, declared int64,
) (res *resource.Resource, remaining int, err error) {
	if token == "" || len(token) > 2*tokenBytes {
		return nil, 0, ErrNoLink
	}
	l, err := s.Store.claim(ctx, Hash(token), s.now())
	if err != nil {
		return nil, 0, fmt.Errorf("upload link: %w", err)
	}
	res, err = s.named(ctx, l, name, body, declared)
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	resourceID := ""
	if res != nil {
		resourceID = res.ID
	}
	if ferr := s.Store.finish(ctx, l.ID, resourceID, failure); ferr != nil && err == nil {
		err = ferr
	}
	remaining = l.MaxFiles - l.FilesUsed
	if err != nil {
		remaining++
	}
	return res, remaining, err
}

// named files body under the name the link was issued for, or, for a link
// issued with none, the name the URL carries.
func (s *Service) named(ctx context.Context, l *link, name string, body io.Reader, declared int64) (*resource.Resource, error) {
	switch {
	case l.Filename == "" && name == "":
		return nil, fmt.Errorf("this link takes each file's name in the URL: PUT to the link followed by /<filename>: %w", ErrBadName)
	case l.Filename != "" && name != "" && name != l.Filename:
		return nil, fmt.Errorf("this link was issued for %q: PUT to the link itself: %w", l.Filename, ErrBadName)
	case l.Filename == "":
		clean, err := resource.SanitizeFilename(name)
		if err != nil || clean != name {
			return nil, fmt.Errorf("the name %q is not a plain file name: %w", name, ErrBadName)
		}
		l.Filename = clean
		if l.MaxFiles > 1 {
			// One display name for several files would name them all alike.
			l.DisplayName = clean
		}
	}
	describe(l)
	return s.write(ctx, l, body, declared)
}

// describe gives a file a link issued with no display name or description,
// as the portal's are, its own name and the description that says where it
// came from, since a create requires both (#2844).
func describe(l *link) {
	if strings.TrimSpace(l.DisplayName) == "" {
		l.DisplayName = l.Filename
	}
	if strings.TrimSpace(l.Description) == "" {
		l.Description = linkDescription
	}
}

// Opened lists the links the person claims names asked for that can still
// take a file.
func (s *Service) Opened(ctx context.Context, claims resource.Claims) ([]Open, error) {
	return s.Store.open(ctx, claims.Sub, claims.Email, s.now())
}

// Revoke ends the link id, which its issuer or an administrator may.
func (s *Service) Revoke(ctx context.Context, id string, claims resource.Claims) error {
	return s.Store.revoke(ctx, id, claims, s.now())
}

// write files body at l's address: its next version when l says replace and a
// file is there, a new file otherwise.
func (s *Service) write(ctx context.Context, l *link, body io.Reader, declared int64) (*resource.Resource, error) {
	// The grants are read now, when the file arrives: a grant revoked since
	// the link was issued no longer covers it (#2684).
	l.Claims = l.Claims.WithFolderGrants(ctx)
	if declared > l.MaxBytes {
		return nil, ErrTooLarge
	}
	bounded := &capped{r: body, left: l.MaxBytes}
	prefix := make([]byte, contenttype.StructuredSniffLen)
	n, err := io.ReadFull(bounded, prefix)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("reading the file: %w", err)
	}
	prefix = prefix[:n]
	mimeType := contenttype.DetectFile(l.ContentType, l.Filename, prefix)
	if err := resource.ValidateMIMEType(mimeType); err != nil {
		return nil, err //nolint:wrapcheck // the type rule's own refusal
	}
	content := io.MultiReader(bytes.NewReader(prefix), bounded)
	if l.IfExists == "replace" {
		existing, _, err := s.Writer.Locate(ctx, toolkit.ResourceAddress{
			Scope: string(l.Scope), ScopeID: l.ScopeID, Path: l.Path, Filename: l.Filename,
		}, l.Claims)
		if err != nil {
			return nil, err //nolint:wrapcheck // the writer's own refusal
		}
		if existing != nil {
			res, _, err := s.Writer.Replace(ctx, existing.ID, resource.RevisionUpload{
				Content: content, MIMEType: mimeType, ChangeSummary: changeSummary, Origin: indexpolicy.OriginAgent,
			}, l.Claims)
			return res, s.sized(err, bounded)
		}
	}
	res, err := s.Writer.Create(ctx, resource.NewResource{
		Scope: l.Scope, ScopeID: l.ScopeID, Path: l.Path, Filename: l.Filename,
		DisplayName: l.DisplayName, Description: l.Description, Tags: l.Tags,
		Content: content, MIMEType: mimeType, DeclaredMIMEType: l.ContentType, Origin: indexpolicy.OriginAgent,
	}, l.Claims)
	return res, s.sized(err, bounded)
}

// sized is err, or ErrTooLarge when the file ran past the link's size.
func (*Service) sized(err error, c *capped) error {
	if c.over {
		return ErrTooLarge
	}
	return err
}

// capped reads at most left bytes and fails the read that would go past it.
type capped struct {
	r    io.Reader
	left int64
	over bool
}

func (c *capped) Read(p []byte) (int, error) {
	if c.left <= 0 {
		var one [1]byte
		if n, _ := c.r.Read(one[:]); n > 0 {
			c.over = true
			return 0, ErrTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err //nolint:wrapcheck // a reader's own error, passed through
}
