// Package resourcedownload is the short-lived download link to a managed
// resource (#2847): manage_resource action=download_link, or Copy link in the
// portal, hands back a URL on the platform's public address that anybody
// holding it can GET the file from, with no session or key, until it expires.
// It is for handing a file to a connection that needs it now (an API that
// takes a PDF by URL), and comes in two kinds: a single download, which ends
// after the first full download or an hour, and one that works for six hours.
// A HEAD request or a byte-range read is not the download, since a fetcher
// may make one before it (a PDF reader reads by range).
//
// The link is the asset content URL's token (internal/portal/contenturl,
// #1848) under a key of its own, so a token minted for one is never valid as
// the other, every replica verifies what any replica minted, and nothing is
// stored.
package resourcedownload

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/contenturl"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// KeyLabel derives this package's key from the browser-session signing key.
const KeyLabel = "resource-download-link"

// RoutePrefix is where a link is served: the token, then the file's name, so
// a reader that judges a file by its URL sees what it is.
const RoutePrefix = "/api/v1/resources/downloads/"

// Route is the pattern a link is served on.
const Route = "GET " + RoutePrefix + "{token}/{filename}"

// The two kinds of link (a link's expires), and how long each lives.
const (
	// SingleDownload ends after its first full download, or after
	// SingleTTL.
	SingleDownload = "single_download"
	// SixHours works for anyone holding it for SixHoursTTL.
	SixHours = "6h"

	SingleTTL   = time.Hour
	SixHoursTTL = 6 * time.Hour
)

// keptAfterExpiry is how long a used single download is remembered past its
// link's expiry; using a link sweeps older ones.
const keptAfterExpiry = 24 * time.Hour

// Errors a caller tells apart.
var (
	// ErrUnavailable is a deployment that cannot hand out links.
	ErrUnavailable = errors.New("this deployment cannot give out download links: it needs auth.browser_session.signing_key " +
		"to sign them and portal.public_base_url to name them")
	// ErrBadExpiry is a kind of link there is none of.
	ErrBadExpiry = fmt.Errorf("expires is %q (ended by the first full download, or an hour) or %q (six hours)", SingleDownload, SixHours)
)

// Store reads the resource a link names.
type Store interface {
	Get(ctx context.Context, id string) (*resource.Resource, error)
}

// Uses records single downloads (postgresUses).
type Uses interface {
	// claim records the download of the single-download link id, false when
	// it was already recorded.
	claim(ctx context.Context, id, resourceID string, expires, now time.Time) (bool, error)
}

// Service mints links and serves the files they name.
type Service struct {
	key     []byte
	base    string
	store   Store
	objects resource.S3Client
	bucket  string
	uses    Uses
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Options is what a Service is made from.
type Options struct {
	// Master is the browser-session signing key the link key is derived from.
	Master []byte
	// Base is the platform's public address (portal.public_base_url).
	Base    string
	Store   Store
	Objects resource.S3Client
	Bucket  string
	// Uses records single downloads (NewUses).
	Uses Uses
}

// New is the service o describes, nil when it has no signing key, public
// address, store or record of single downloads: a deployment that cannot
// sign, name or count a link hands out none.
func New(o Options) *Service {
	base := strings.TrimRight(strings.TrimSpace(o.Base), "/")
	if len(o.Master) == 0 || base == "" || o.Store == nil || o.Uses == nil {
		return nil
	}
	return &Service{
		key: shareguest.DeriveKey(o.Master, KeyLabel), base: base, store: o.Store, objects: o.Objects, bucket: o.Bucket, uses: o.Uses,
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Link is a minted link.
type Link struct {
	URL       string    `json:"url" example:"https://data.example.com/api/v1/resources/downloads/eyJ...Q/report.pdf"`
	Expires   string    `json:"expires" example:"single_download"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Mint returns a link to res of the kind expires names, a single download
// when it names none. The caller has already checked that the person asking
// can read res.
func (s *Service) Mint(res *resource.Resource, expires string) (Link, error) {
	if s == nil {
		return Link{}, ErrUnavailable
	}
	target := contenturl.Target{ID: res.ID}
	switch expires {
	case "", SingleDownload:
		expires, target.Once = SingleDownload, uuid.NewString()
		target.Expires = s.now().Add(SingleTTL).Truncate(time.Second)
	case SixHours:
		target.Expires = s.now().Add(SixHoursTTL).Truncate(time.Second)
	default:
		return Link{}, ErrBadExpiry
	}
	token := contenturl.Sign(s.key, target)
	return Link{
		URL: s.base + RoutePrefix + token + "/" + url.PathEscape(res.Filename), Expires: expires, ExpiresAt: target.Expires.UTC(),
	}, nil
}

// Mount registers the route links are served on. It takes no session or key:
// the link is the credential.
func (s *Service) Mount(mux *http.ServeMux) {
	if s == nil {
		return
	}
	mux.HandleFunc(Route, s.serve)
}

// serve answers GET /api/v1/resources/downloads/{token}/{filename}.
//
// @Summary      Download a file through a download link
// @Description  Serves the managed resource a download link names (manage_resource action=download_link or the portal's Copy link, #2847), as a download, with no session or key. A single-download link ends after its first full download (a GET without Range; a HEAD or a byte-range read does not count) or after an hour, a six-hour link after six hours. An expired, used or altered link answers 403; a file since deleted, or a name that is not the file's, answers 404.
// @Tags         Resources
// @Produce      octet-stream
// @Param        token     path  string  true  "The link's signed token"
// @Param        filename  path  string  true  "The file's name"
// @Success      200  {file}  binary
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Router       /resources/downloads/{token}/{filename} [get]
func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	res, target, ok := s.resolve(w, r)
	if !ok || !s.spend(w, r, target) {
		return
	}
	opts := blobserve.Options{Name: res.Filename, ContentType: res.MIMEType, ModTime: res.UpdatedAt, ForceAttachment: true}
	if res.MIMEType != "" && blobserve.ServeRanged(w, r, opts, blobserve.Object{
		Store: s.objects, Bucket: s.bucket, Key: res.S3Key, Size: res.SizeBytes,
	}) {
		return
	}
	body, contentType, err := s.objects.GetObject(r.Context(), s.bucket, res.S3Key)
	if err != nil {
		slog.WarnContext(r.Context(), "download link: reading the file failed", "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusNotFound, "the file this link named is no longer there")
		return
	}
	opts.ContentType, opts.Data = cmp.Or(contentType, res.MIMEType), body
	blobserve.Serve(w, r, opts)
}

// resolve reads the file the request's link names, answering for a link that
// is expired, altered or names a file no longer there.
func (s *Service) resolve(w http.ResponseWriter, r *http.Request) (*resource.Resource, contenturl.Target, bool) {
	target, err := contenturl.Verify(s.key, r.PathValue("token"), s.now())
	switch {
	case errors.Is(err, contenturl.ErrExpired):
		httpjson.WriteError(w, http.StatusForbidden, "this download link has expired: ask for a new one")
		return nil, target, false
	case err != nil:
		httpjson.WriteError(w, http.StatusForbidden, "this download link is not valid")
		return nil, target, false
	}
	res, err := s.store.Get(r.Context(), target.ID)
	if err != nil || res == nil || res.Filename != r.PathValue("filename") {
		httpjson.WriteError(w, http.StatusNotFound, "the file this link named is no longer there")
		return nil, target, false
	}
	if s.objects == nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "blob storage not configured")
		return nil, target, false
	}
	return res, target, true
}

// spend records the download of a single-download link, answering for it when
// it was already used. Only a full GET is the download: a HEAD request and a
// byte-range read pass without spending it.
func (s *Service) spend(w http.ResponseWriter, r *http.Request, target contenturl.Target) bool {
	if target.Once == "" || r.Method != http.MethodGet || r.Header.Get("Range") != "" {
		return true
	}
	fresh, err := s.uses.claim(r.Context(), target.Once, target.ID, target.Expires, s.now())
	switch {
	case err != nil:
		slog.WarnContext(r.Context(), "download link: recording the download failed", "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusServiceUnavailable, "the download could not be recorded: try again")
		return false
	case !fresh:
		httpjson.WriteError(w, http.StatusForbidden, "this single-download link has been used: ask for a new one")
		return false
	}
	return true
}
