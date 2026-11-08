package sitesvc

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// redirectsFile is the release file read as the redirect map, not served.
const redirectsFile = "_redirects"

// Publishing's bounds.
const (
	parallelUploads   = 8
	minCompressBytes  = 1024
	brotliQuality     = 9
	worthwhilePercent = 95
	wholePercent      = 100
	firstPrintable    = 0x20
	deleteRune        = 0x7f
	maxPathLen        = 1024
	octetStream       = "application/octet-stream"
)

// activeTypes are the types a site serves that the shared filename table will
// not name.
var activeTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".htm": "text/html; charset=utf-8",
	".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8", ".svg": "image/svg+xml", ".json": "application/json",
	".wasm": "application/wasm", ".xml": "application/xml", ".txt": "text/plain; charset=utf-8",
}

// junkNames are files an archive tool or an OS leaves behind, dropped without
// complaint. Every other dotfile is refused, since a release is public.
var junkNames = []string{".DS_Store", "Thumbs.db"}

// InputFile is one file a publish was handed.
type InputFile struct {
	Path string
	Body []byte
}

// CleanPath is a file's path as the release keys it, or a refusal: relative,
// forward-slashed, with no parent reference, no control character, and no
// dotted segment other than .well-known, so a source tree's .env or .git never
// becomes a public URL.
func CleanPath(name string) (string, error) {
	p := strings.TrimPrefix(strings.TrimSpace(name), "./")
	switch {
	case unservablePath(p):
		return "", fmt.Errorf("file path %q cannot be served", name)
	case strings.ContainsFunc(p, isControlRune):
		return "", fmt.Errorf("file path %q holds a control character", name)
	case strings.HasPrefix(p, "/") || path.Clean(p) != p:
		return "", fmt.Errorf("file path %q must be relative and clean", name)
	}
	for seg := range strings.SplitSeq(p, "/") {
		if dottedSegment(seg) {
			return "", fmt.Errorf("file path %q has a dotted segment; a release is public, so only .well-known is served", name)
		}
	}
	return p, nil
}

// unservablePath reports a path no release can key: empty, overlong, not
// UTF-8, or holding a backslash or a NUL.
func unservablePath(p string) bool {
	return p == "" || len(p) > maxPathLen || !utf8.ValidString(p) || strings.ContainsAny(p, "\\\x00")
}

func isControlRune(r rune) bool { return r < firstPrintable || r == deleteRune }

// dottedSegment reports a parent reference or a hidden name; .well-known is
// the one dotted segment a release may hold.
func dottedSegment(seg string) bool {
	return seg == ".." || (strings.HasPrefix(seg, ".") && seg != ".well-known")
}

func isJunk(name string) bool {
	return slices.Contains(junkNames, path.Base(name)) || strings.HasPrefix(name, "__MACOSX/")
}

// contentTypeOf is the type a file is served as, from its name alone: never
// from its bytes or from what a source claimed.
func contentTypeOf(name string) string {
	// A site's own pages and scripts are what it is for: the shared table
	// leaves these out because elsewhere a filename must not make a file active.
	if ct, ok := activeTypes[strings.ToLower(path.Ext(name))]; ok {
		return ct
	}
	if ct := contenttype.TypeForFilename(name); ct != "" {
		return ct
	}
	return octetStream
}

// compressible reports whether a file of the type is worth compressing.
func compressible(ct string) bool {
	ct, _, _ = strings.Cut(ct, ";")
	return strings.HasPrefix(ct, "text/") || strings.HasSuffix(ct, "+xml") || strings.HasSuffix(ct, "+json") ||
		slices.Contains([]string{
			"application/json", "application/javascript", "application/xml", "application/wasm",
			"application/x-javascript", "image/svg+xml", "font/ttf", "font/otf",
		}, ct)
}

func gzipped(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	return buf.Bytes(), nil
}

func brotlied(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, brotliQuality)
	if _, err := w.Write(body); err != nil {
		return nil, fmt.Errorf("brotli: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("brotli: %w", err)
	}
	return buf.Bytes(), nil
}

// ObjectKey is where one form of a release's file is stored: "" for the file
// itself, "gz" or "br" for a compressed variant.
func ObjectKey(siteID, releaseID, encoding, filePath string) string {
	form := "files"
	if encoding != "" {
		form = encoding
	}
	return fmt.Sprintf("sites/%s/releases/%s/%s/%s", siteID, releaseID, form, filePath)
}

// Digest fixes a file set: every path and content digest, in path order.
func Digest(files []site.File) string {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b site.File) int { return strings.Compare(a.Path, b.Path) })
	h := sha256.New()
	for _, f := range sorted {
		_, _ = io.WriteString(h, f.Path+"\x00"+f.SHA256+"\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// built is a publish's files read and checked, before any is stored.
type built struct {
	inputs    []InputFile
	redirects []site.Redirect
	total     int64
}

// prepare checks a publish's files against the limits and pulls out the
// redirect map.
func (s *Service) prepare(in []InputFile) (*built, error) {
	b := &built{redirects: []site.Redirect{}}
	seen := make(map[string]bool, len(in))
	limits := s.Config.Limits()
	for _, f := range in {
		if isJunk(f.Path) {
			continue
		}
		if err := b.add(f, seen, limits); err != nil {
			return nil, err
		}
	}
	if err := b.checkTotals(limits); err != nil {
		return nil, err
	}
	return b, nil
}

// add takes one file into the release, or the redirect map when it is that.
func (b *built) add(f InputFile, seen map[string]bool, limits siteconfig.Config) error {
	name, err := CleanPath(f.Path)
	if err != nil {
		return refused("%s", err.Error())
	}
	if seen[name] {
		return refused("file %q appears twice", name)
	}
	seen[name] = true
	if name == redirectsFile {
		if b.redirects, err = site.ParseRedirects(string(f.Body)); err != nil {
			return refused("%s", err.Error())
		}
		return nil
	}
	if int64(len(f.Body)) > limits.MaxFileBytes {
		return refused("file %q is %d bytes, more than the %d sites.max_file_bytes allows", name, len(f.Body), limits.MaxFileBytes)
	}
	b.total += int64(len(f.Body))
	b.inputs = append(b.inputs, InputFile{Path: name, Body: f.Body})
	return nil
}

// checkTotals refuses a release with no files, or more than the limits hold.
func (b *built) checkTotals(limits siteconfig.Config) error {
	switch {
	case len(b.inputs) == 0:
		return refused("the publish holds no files to serve")
	case len(b.inputs) > limits.MaxFiles:
		return refused("the publish holds %d files, more than the %d sites.max_release_files allows", len(b.inputs), limits.MaxFiles)
	case b.total > limits.MaxReleaseBytes:
		return refused("the publish holds %d bytes, more than the %d sites.max_release_bytes allows", b.total, limits.MaxReleaseBytes)
	}
	return nil
}

// store writes every file and its compressed variants under the release's own
// prefix, a few at a time.
func (s *Service) store(ctx context.Context, siteID, releaseID string, b *built) ([]site.File, error) {
	files := make([]site.File, len(b.inputs))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelUploads)
	for i, in := range b.inputs {
		g.Go(func() error {
			f, err := s.storeFile(ctx, siteID, releaseID, in)
			if err != nil {
				return fmt.Errorf("storing %s: %w", in.Path, err)
			}
			files[i] = *f
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err //nolint:wrapcheck // each goroutine says which file
	}
	return files, nil
}

func (s *Service) storeFile(ctx context.Context, siteID, releaseID string, in InputFile) (*site.File, error) {
	sum := sha256.Sum256(in.Body)
	f := &site.File{Path: in.Path, SHA256: hex.EncodeToString(sum[:]), ContentType: contentTypeOf(in.Path), Size: int64(len(in.Body))}
	if err := s.Objects.PutObject(ctx, s.Bucket, ObjectKey(siteID, releaseID, "", in.Path), in.Body, f.ContentType); err != nil {
		return nil, fmt.Errorf("putting the file: %w", err)
	}
	if !compressible(f.ContentType) || len(in.Body) < minCompressBytes {
		return f, nil
	}
	stored := variantFile{siteID: siteID, releaseID: releaseID, in: in, contentType: f.ContentType}
	var err error
	if f.GzipSize, err = s.putVariant(ctx, stored, "gz", gzipped); err != nil {
		return nil, err
	}
	if f.BrotliSize, err = s.putVariant(ctx, stored, "br", brotlied); err != nil {
		return nil, err
	}
	return f, nil
}

// variantFile is the file a compressed form is stored for.
type variantFile struct {
	siteID, releaseID string
	in                InputFile
	contentType       string
}

// putVariant stores a compressed form when it saves enough to be worth
// serving, and reports its size, 0 for none.
func (s *Service) putVariant(ctx context.Context, v variantFile, enc string, compress func([]byte) ([]byte, error)) (int64, error) {
	body, err := compress(v.in.Body)
	if err != nil {
		return 0, err
	}
	if len(body)*wholePercent >= len(v.in.Body)*worthwhilePercent {
		return 0, nil
	}
	if err := s.Objects.PutObject(ctx, s.Bucket, ObjectKey(v.siteID, v.releaseID, enc, v.in.Path), body, v.contentType); err != nil {
		return 0, fmt.Errorf("putting the %s form: %w", enc, err)
	}
	return int64(len(body)), nil
}

// PublishRequest is one publish.
type PublishRequest struct {
	Site        string
	Environment string
	Source      string
	Note        string
	Files       []InputFile
}

// Published is what a publish answers.
type Published struct {
	Release    *site.Release `json:"release"`
	Env        string        `json:"environment"`
	PreviewURL string        `json:"preview_url"`
}

// Publish writes a new release and points the environment at it. An
// environment that requires approval is reached by promote, never by publish.
func (s *Service) Publish(ctx context.Context, actor Actor, req PublishRequest) (*Published, error) {
	st, err := s.Find(ctx, req.Site)
	if err != nil {
		return nil, err
	}
	if err := mayEdit(actor, st); err != nil {
		return nil, err
	}
	env, err := s.publishTarget(st, req)
	if err != nil {
		return nil, err
	}
	built, err := s.prepare(req.Files)
	if err != nil {
		return nil, err
	}
	rel, err := s.record(ctx, actor, st, req, built)
	if err != nil {
		return nil, err
	}
	if err := s.move(ctx, actor, repoint{st: st, env: env, to: rel.Number, action: site.ActionPublish}); err != nil {
		return nil, err
	}
	rel.Files = nil
	return &Published{Release: rel, Env: env.Name, PreviewURL: s.PreviewURL(st.Name, env.Name)}, nil
}

// publishTarget is the environment a publish lands in, or why it cannot.
func (s *Service) publishTarget(st *site.Site, req PublishRequest) (*site.Environment, error) {
	if s.Objects == nil || s.Bucket == "" {
		return nil, refused("this deployment serves no sites: it has no portal bucket, or sites.enabled is false")
	}
	name := req.Environment
	if name == "" {
		name = site.EnvDev
	}
	env := st.Environment(name)
	if env == nil {
		return nil, noEnvironment(name)
	}
	if env.RequiresApproval {
		return nil, refused("environment %q requires approval, so a release reaches it by promote; publish to another environment first", name)
	}
	if len(req.Note) > site.MaxNote {
		return nil, refused("note: at most %d bytes", site.MaxNote)
	}
	return env, nil
}

// record stores the files under a prefix of the release's own and writes the
// row, which is what numbers it: two publishes at once never share a prefix.
func (s *Service) record(ctx context.Context, actor Actor, st *site.Site, req PublishRequest, b *built) (*site.Release, error) {
	id := sitestore.NewReleaseID()
	files, err := s.store(ctx, st.ID, id, b)
	if err != nil {
		return nil, err
	}
	rel := &site.Release{
		ID: id, SiteID: st.ID, Source: req.Source, Note: req.Note,
		FileCount: len(files), TotalBytes: b.total, ManifestSHA256: Digest(files), Redirects: b.redirects,
		CreatedBy: actor.Email, Files: files,
	}
	if err := s.Store.RecordRelease(ctx, rel); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return rel, nil
}
