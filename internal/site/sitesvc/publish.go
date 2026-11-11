package sitesvc

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/contentenc"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/siterender"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// redirectsFile is the release file read as the redirect map, not served.
const redirectsFile = "_redirects"

// Publishing's bounds.
const (
	parallelUploads = 8
	firstPrintable  = 0x20
	deleteRune      = 0x7f
	maxPathLen      = 1024
	octetStream     = "application/octet-stream"
	// maxManifestDepth is how many derived releases may stand between a
	// release and one holding its whole file set: reading a file set walks
	// them, and a site can take thousands of patches.
	maxManifestDepth = 32
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
	// sum is Body's digest, set when the file is taken into a release.
	sum string
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

// ObjectKey is where one form of a file is stored: "" for the file itself,
// "gz" or "br" for a compressed variant. Objects are kept by digest, per site,
// so a release that keeps a file costs nothing and a caller's path never
// reaches a key; a file written before that stays under its release's prefix.
func ObjectKey(siteID string, f site.File, encoding string) string {
	if f.StoredIn != "" {
		form := "files"
		if encoding != "" {
			form = encoding
		}
		return fmt.Sprintf("sites/%s/releases/%s/%s/%s", siteID, f.StoredIn, form, f.Path)
	}
	key := fmt.Sprintf("sites/%s/objects/%s", siteID, f.SHA256)
	if encoding != "" {
		key += "." + encoding
	}
	return key
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
	// inputs is what has to be written, kept what comes from the base as it is.
	inputs    []InputFile
	kept      []site.File
	removed   []string
	redirects []site.Redirect
	total     int64
}

// basis is the release a publish starts from: the one it overlays, or the one
// the environment serves, whose files need not be written again.
type basis struct {
	release *site.Release
	// files is what an edit starts from: the release's source when it has one,
	// and otherwise the files it serves.
	files  []site.File
	byPath map[string]site.File
	// byDigest holds only files kept by digest: a file under a release's own
	// prefix is stored at its path, so it cannot stand in for another path.
	byDigest map[string]site.File
	// output is what the release serves, and index what each output read.
	output []site.File
	index  *siterender.Index
}

func newBasis(rel *site.Release, files []site.File) *basis {
	b := &basis{release: rel, files: files, output: files, byPath: make(map[string]site.File, len(files)), byDigest: make(map[string]site.File, len(files))}
	for _, f := range files {
		b.byPath[f.Path] = f
		b.noteDigest(f)
	}
	return b
}

func (b *basis) noteDigest(f site.File) {
	if f.StoredIn == "" {
		b.byDigest[f.SHA256] = f
	}
}

// reusable is the stored file that already holds in's bytes under the type in
// is served as, so nothing has to be compressed or written.
func (b *basis) reusable(in InputFile, contentType string) (site.File, bool) {
	if f, ok := b.byPath[in.Path]; ok && f.SHA256 == in.sum && f.ContentType == contentType {
		return f, true
	}
	if f, ok := b.byDigest[in.sum]; ok && f.ContentType == contentType {
		f.Path = in.Path
		return f, true
	}
	return site.File{}, false
}

// basisOf reads what the environment serves now, an empty basis for none.
func (s *Service) basisOf(ctx context.Context, st *site.Site, env *site.Environment) (*basis, error) {
	if env.Release == 0 {
		return newBasis(nil, nil), nil
	}
	rel, err := s.Store.Release(ctx, st.ID, env.Release)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	output, err := s.Store.Files(ctx, rel.ID)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if !rel.Rendered {
		return newBasis(rel, output), nil
	}
	return s.renderedBasis(ctx, st.ID, rel, output)
}

// prepare checks a publish's files against the limits and pulls out the
// redirect map. An overlay starts from the base's files and redirects.
func (s *Service) prepare(req PublishRequest, base *basis) (*built, error) {
	b := &built{redirects: []site.Redirect{}}
	if req.Base != 0 {
		b.redirects = base.release.Redirects
	}
	seen := make(map[string]bool, len(req.Files))
	limits := s.cfg().Limits()
	for _, f := range req.Files {
		if isJunk(f.Path) {
			continue
		}
		if err := b.add(f, seen, limits); err != nil {
			return nil, err
		}
	}
	if req.Base != 0 {
		if err := b.overlay(base, req.Remove, seen); err != nil {
			return nil, err
		}
	} else if len(req.Remove) > 0 {
		return nil, refused("a file can only be deleted from a base: name the release with base")
	}
	if err := b.checkTotals(limits); err != nil {
		return nil, err
	}
	return b, nil
}

// overlay fills in what the publish leaves alone: the base's files it neither
// replaces nor removes, and the ones it sends back unchanged.
func (b *built) overlay(base *basis, remove []string, seen map[string]bool) error {
	gone, err := b.takeRemovals(base, remove, seen)
	if err != nil {
		return err
	}
	changed := b.inputs[:0]
	for _, in := range b.inputs {
		if f, ok := base.byPath[in.Path]; ok && f.SHA256 == in.sum {
			b.kept = append(b.kept, f)
			continue
		}
		changed = append(changed, in)
	}
	b.inputs = changed
	for _, f := range base.files {
		if !seen[f.Path] && !gone[f.Path] {
			b.kept = append(b.kept, f)
			b.total += f.Size
		}
	}
	return nil
}

// takeRemovals checks the paths a publish removes from its base and records
// them; deleting the _redirects file empties the map, which is no file.
func (b *built) takeRemovals(base *basis, remove []string, seen map[string]bool) (map[string]bool, error) {
	gone := make(map[string]bool, len(remove))
	for _, p := range remove {
		name, err := CleanPath(p)
		if err != nil {
			return nil, refused("%s", err.Error())
		}
		if seen[name] {
			return nil, refused("file %q is both sent and deleted", name)
		}
		if name == redirectsFile {
			b.redirects = []site.Redirect{}
			continue
		}
		if _, ok := base.byPath[name]; !ok {
			return nil, refused("there is no file %q to delete in release %d", name, base.release.Number)
		}
		gone[name] = true
		b.removed = append(b.removed, name)
	}
	slices.Sort(b.removed)
	return gone, nil
}

// unchanged reports an overlay that would make a release identical to its base.
func (b *built) unchanged(base *basis) bool {
	return base.release != nil && len(b.inputs) == 0 && len(b.removed) == 0 && slices.Equal(b.redirects, base.release.Redirects)
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
	sum := sha256.Sum256(f.Body)
	b.inputs = append(b.inputs, InputFile{Path: name, Body: f.Body, sum: hex.EncodeToString(sum[:])})
	return nil
}

// checkTotals refuses a release with no files, or more than the limits hold.
func (b *built) checkTotals(limits siteconfig.Config) error {
	count := len(b.inputs) + len(b.kept)
	switch {
	case count == 0:
		return refused("the publish holds no files to serve")
	case count > limits.MaxFiles:
		return refused("the publish holds %d files, more than the %d sites.max_release_files allows", count, limits.MaxFiles)
	case b.total > limits.MaxReleaseBytes:
		return refused("the publish holds %d bytes, more than the %d sites.max_release_bytes allows", b.total, limits.MaxReleaseBytes)
	}
	return nil
}

// store writes each new file and its compressed variants, a few at a time, and
// skips any whose bytes the site already holds.
func (s *Service) store(ctx context.Context, siteID string, b *built, base *basis, req PublishRequest) ([]site.File, error) {
	files := make([]site.File, len(b.inputs))
	var done atomic.Int64
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelUploads)
	for i, in := range b.inputs {
		g.Go(func() error {
			f, err := s.storeFile(ctx, siteID, in, base)
			if err != nil {
				return fmt.Errorf("storing %s: %w", in.Path, err)
			}
			files[i] = *f
			if req.Progress != nil {
				req.Progress(int(done.Add(1)), len(b.inputs))
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err //nolint:wrapcheck // each goroutine says which file
	}
	return files, nil
}

func (s *Service) storeFile(ctx context.Context, siteID string, in InputFile, base *basis) (*site.File, error) {
	contentType := contentTypeOf(in.Path)
	if f, ok := base.reusable(in, contentType); ok {
		return &f, nil
	}
	if err := sitestore.HoldOf(ctx).Renew(ctx); err != nil {
		return nil, err //nolint:wrapcheck // the hold's own sentence
	}
	f := &site.File{Path: in.Path, SHA256: in.sum, ContentType: contentType, Size: int64(len(in.Body))}
	if err := s.Objects.PutObject(ctx, s.Bucket, ObjectKey(siteID, *f, ""), in.Body, contentType); err != nil {
		return nil, fmt.Errorf("putting the file: %w", err)
	}
	if !contentenc.Compressible(contentType) || len(in.Body) < contentenc.MinBytes {
		return f, nil
	}
	stored := variantFile{siteID: siteID, file: *f, source: in.Body}
	var err error
	if f.GzipSize, err = s.putVariant(ctx, stored, "gz", contentenc.Gzip); err != nil {
		return nil, err
	}
	if f.BrotliSize, err = s.putVariant(ctx, stored, "br", contentenc.Brotli); err != nil {
		return nil, err
	}
	return f, nil
}

// variantFile is the file a compressed form is stored for.
type variantFile struct {
	siteID string
	file   site.File
	source []byte
}

// putVariant stores a compressed form when it saves enough to be worth
// serving, and reports its size, 0 for none.
func (s *Service) putVariant(ctx context.Context, v variantFile, enc string, compress func([]byte) ([]byte, error)) (int64, error) {
	body, err := compress(v.source)
	if err != nil {
		return 0, err
	}
	if !contentenc.Worthwhile(len(v.source), len(body)) {
		return 0, nil
	}
	if err := s.Objects.PutObject(ctx, s.Bucket, ObjectKey(v.siteID, v.file, enc), body, v.file.ContentType); err != nil {
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
	// Base, when not 0, makes Files an overlay on that release, which must be
	// the one the default environment serves. Files it does not name carry over.
	Base int
	// Remove is the paths taken out of Base.
	Remove []string
	// DryRun does everything but store a file or write a release.
	DryRun bool
	// Progress is told each time a file is stored, and Phase when the work
	// moves on; nil reports nothing.
	Progress func(done, total int)
	Phase    func(phase string)
}

// Published is what a publish answers.
type Published struct {
	Release *site.Release `json:"release"`
	Env     string        `json:"environment"`
	// Address is where the environment answers, "" before it has one.
	Address string `json:"address"`
	// Unchanged is true when the overlay changed nothing: no release was
	// written, and Release is the one the environment already serves.
	Unchanged bool `json:"unchanged,omitempty"`
	// OutputsChanged is how many served files a dry run would add, change or
	// remove.
	OutputsChanged int `json:"outputs_changed,omitempty"`
}

// Publish writes a new release and points the environment at it. An
// environment that requires approval is reached by promote, never by publish.
func (s *Service) Publish(ctx context.Context, actor Actor, req PublishRequest) (*Published, error) {
	st, err := s.Find(ctx, req.Site)
	if err != nil {
		return nil, err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, err
	}
	env, err := s.publishTarget(st, req)
	if err != nil {
		return nil, err
	}
	base, err := s.basisOf(ctx, st, env)
	if err != nil {
		return nil, err
	}
	built, err := s.prepare(req, base)
	if err != nil {
		return nil, err
	}
	if req.Base != 0 && built.unchanged(base) {
		rel := *base.release
		rel.Files = nil
		return &Published{Release: &rel, Env: env.Name, Address: s.Address(ctx, st, env.Name), Unchanged: true}, nil
	}
	p := publication{st: st, env: env, req: req, built: built, base: base}
	if req.DryRun {
		return s.preview(ctx, p)
	}
	rel, err := s.record(ctx, actor, p)
	if err != nil {
		return nil, err
	}
	if s.Invalidate != nil {
		s.Invalidate(st.Name)
	}
	rel.Files, rel.Removed = nil, nil
	return &Published{Release: rel, Env: env.Name, Address: s.Address(ctx, st, env.Name)}, nil
}

// publishTarget is the environment a publish lands in, or why it cannot.
func (s *Service) publishTarget(st *site.Site, req PublishRequest) (*site.Environment, error) {
	if s.Objects == nil || s.Bucket == "" {
		return nil, refused("this deployment serves no sites: it has no portal bucket, or sites.enabled is false")
	}
	name, _ := site.CanonicalEnvironment(cmp.Or(req.Environment, site.EnvDev))
	if name != site.EnvDev {
		return nil, readOnly(name)
	}
	env := st.Environment(name)
	if env == nil {
		return nil, noEnvironment(name)
	}
	if len(req.Note) > site.MaxNote {
		return nil, refused("note: at most %d bytes", site.MaxNote)
	}
	if req.Base != 0 {
		if err := checkBase(env, req.Base); err != nil {
			return nil, err
		}
	}
	return env, nil
}

// checkBase refuses to build on anything but what the working environment
// serves now: an edit made against an older release would undo whatever came
// after it.
func checkBase(env *site.Environment, base int) error {
	if base != env.Release {
		return textpatch.StaleBaseError(base, env.Release)
	}
	return nil
}

// readOnly is the refusal of a publish, patch or edit aimed at an environment
// people use: agents and people change dev, and a release reaches every other
// environment by promote.
func readOnly(env string) error {
	return refused("environment %q is read-only: publish, patch and edits change %s, and a release reaches %s by promote", env, site.EnvDev, env)
}

// publication is one publish decided and ready to write.
type publication struct {
	st    *site.Site
	env   *site.Environment
	req   PublishRequest
	built *built
	base  *basis
}

// record stores the files and writes the release with the move to it, in the
// one transaction: a move that loses to another leaves no release behind. The
// site's stored files are held from the first file stored until the release
// is recorded, so a release sweep cannot delete one in between
// (sitestore/holds.go).
func (s *Service) record(ctx context.Context, actor Actor, p publication) (*site.Release, error) {
	hold, err := s.holdUpload(ctx, p.st.ID)
	if err != nil {
		return nil, err
	}
	defer hold.Release(ctx)
	ctx = sitestore.WithHold(ctx, hold)
	trees, err := s.buildTrees(ctx, p)
	if err != nil {
		return nil, holdRefusal(err)
	}
	rel := &site.Release{
		ID: sitestore.NewReleaseID(), SiteID: p.st.ID, Source: p.req.Source, Note: p.req.Note, Base: p.req.Base, Rendered: trees.rendered,
		FileCount: len(trees.output), TotalBytes: totalSize(trees.output), ManifestSHA256: Digest(trees.output), Redirects: p.built.redirects,
		CreatedBy: actor.Email, RenderIndex: trees.index,
	}
	trees.fill(rel, p.base, p.req.Base != 0)
	if p.req.Phase != nil {
		p.req.Phase("Switching " + p.env.Name + " to the new release")
	}
	move := &site.Pointer{SiteID: p.st.ID, Environment: p.env.Name, After: p.env.Release, Action: site.ActionPublish, Actor: actor.Email}
	if err := s.Store.RecordRelease(ctx, rel, move); err != nil {
		return nil, holdRefusal(err)
	}
	return rel, nil
}

// uploadHolder is a store that holds a site's stored files for a publish; the
// PostgreSQL store is one, and a store that is not holds nothing.
type uploadHolder interface {
	HoldUpload(ctx context.Context, siteID string) (*sitestore.Hold, error)
}

func (s *Service) holdUpload(ctx context.Context, siteID string) (*sitestore.Hold, error) {
	h, ok := s.Store.(uploadHolder)
	if !ok {
		return nil, nil //nolint:nilnil // a nil hold holds nothing
	}
	hold, err := h.HoldUpload(ctx, siteID)
	return hold, holdRefusal(err)
}

// holdRefusal is a publish stopped by its hold on the site's stored files, in
// the hold's words; any other error as it was.
func holdRefusal(err error) error {
	for _, held := range []error{sitestore.ErrCleanupRunning, sitestore.ErrHoldExpired} {
		if errors.Is(err, held) {
			return &RefusedError{Err: held, Msg: held.Error()}
		}
	}
	return err
}

func totalSize(files []site.File) int64 {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	return total
}
