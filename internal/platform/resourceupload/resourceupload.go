// Package resourceupload is the single-use upload link (#2639): manage_resource
// action=upload_link issues a short-lived URL on the platform's own public
// origin, and one PUT of a file to it from the agent's shell files the file as
// a managed resource at the address the link was issued for, as the person who
// asked for it, with the checks a portal upload gets. The bytes never pass
// through a tool call, and nothing about object storage has to be public.
//
// The link's token is a bearer credential for one write. It is handed to the
// caller once and never stored: a link is found by the token's SHA-256, claimed
// by one atomic statement that also refuses a used or expired link, and bound
// to its address, its declared type and its size.
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

// How long a link lives: DefaultTTL unless the caller asks for less, never
// more than MaxTTL.
const (
	DefaultTTL = 15 * time.Minute
	MaxTTL     = time.Hour
)

// tokenBytes is the token's randomness: 256 bits, which no one guesses.
const tokenBytes = 32

// keptAfterExpiry is how long a link's row is kept once it has expired, as
// the record of what it wrote; issuing a link sweeps older ones.
const keptAfterExpiry = 7 * 24 * time.Hour

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
}

// Store keeps the links (PostgresStore).
type Store interface {
	// insert keeps l, found by tokenSHA256 alone.
	insert(ctx context.Context, l link, tokenSHA256 string) error
	// claim marks the link tokenSHA256 names used and returns it, or
	// ErrNoLink when no unused link that has not expired at now answers to it.
	claim(ctx context.Context, tokenSHA256 string, now time.Time) (*link, error)
	// finish records what the link wrote, or why it wrote nothing.
	finish(ctx context.Context, id, resourceID, failure string) error
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
	// MaxBytes is the most the file may be.
	MaxBytes int64
	// TTL is how long the link lives; zero is DefaultTTL.
	TTL time.Duration
}

// Issued is a link as the caller is handed it, once.
type Issued struct {
	URL       string    `json:"upload_url"`
	Method    string    `json:"method"`
	ExpiresAt time.Time `json:"expires_at"`
	MaxBytes  int64     `json:"max_bytes"`
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
		MaxBytes: in.MaxBytes, ExpiresAt: now.Add(ttl),
	}
	_ = s.Store.sweep(ctx, now.Add(-keptAfterExpiry)) // the record of old links; a failed sweep is retried by the next issue
	if err := s.Store.insert(ctx, l, sum); err != nil {
		return Issued{}, fmt.Errorf("issuing an upload link: %w", err)
	}
	return Issued{URL: base + RoutePrefix + token, Method: "PUT", ExpiresAt: l.ExpiresAt, MaxBytes: l.MaxBytes}, nil
}

// check refuses a link that could not be issued as asked.
func (s *Service) check(in IssueRequest, claims resource.Claims) error {
	switch {
	case !claims.CanWriteFolder(in.Scope, in.ScopeID, in.Path):
		return fmt.Errorf("you cannot write to that library, so no upload link is issued for it: %w", ErrRefused)
	case in.MaxBytes <= 0:
		return fmt.Errorf("size_bytes is required: the most the file may be, in bytes: %w", ErrRefused)
	case in.MaxBytes > s.ceiling():
		return fmt.Errorf("size_bytes %d is over this deployment's upload limit of %d bytes: %w", in.MaxBytes, s.ceiling(), ErrRefused)
	case in.TTL > MaxTTL:
		return fmt.Errorf("an upload link lives at most %d seconds: %w", int(MaxTTL.Seconds()), ErrRefused)
	}
	return nil
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

// Upload files body, PUT to the link token names, at the link's address as
// the person who asked for it. declared is the request's Content-Length, -1
// when it sent none. The link is spent whatever the outcome.
func (s *Service) Upload(ctx context.Context, token string, body io.Reader, declared int64) (*resource.Resource, error) {
	if token == "" || len(token) > 2*tokenBytes {
		return nil, ErrNoLink
	}
	l, err := s.Store.claim(ctx, Hash(token), s.now())
	if err != nil {
		return nil, fmt.Errorf("upload link: %w", err)
	}
	res, err := s.write(ctx, l, body, declared)
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
	return res, err
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
