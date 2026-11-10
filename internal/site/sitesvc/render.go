package sitesvc

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitefs"
	"github.com/txn2/mcp-data-platform/internal/site/siterender"
)

const (
	// renderTimeout bounds one render, whatever its budgets allow.
	renderTimeout = 5 * time.Minute
	// renderIndexFile names the record of what each output read.
	renderIndexFile = "index.json.gz"
	maxIndexBytes   = 256 << 20
)

// renderedBasis fills in what a rendered release adds to a basis.
func (s *Service) renderedBasis(ctx context.Context, siteID string, rel *site.Release, output []site.File) (*basis, error) {
	source, err := s.Store.Tree(ctx, rel.ID, site.TreeSource)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	b := newBasis(rel, source)
	b.output = output
	for _, f := range output {
		b.noteDigest(f)
	}
	if b.index, err = s.readIndex(ctx, siteID, rel); err != nil {
		return nil, err
	}
	return b, nil
}

// readIndex is what a release's outputs read, nil when it has no record of it
// or the record cannot be read: the next render is then a full one.
func (s *Service) readIndex(ctx context.Context, siteID string, rel *site.Release) (*siterender.Index, error) {
	files, err := s.Store.Tree(ctx, rel.ID, site.TreeRender)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if len(files) == 0 {
		return nil, nil //nolint:nilnil // no record is a full render
	}
	data, _, err := s.Objects.GetObject(ctx, s.Bucket, ObjectKey(siteID, files[0], ""))
	if err != nil {
		return nil, nil //nolint:nilnil,nilerr // a lost record costs a full render, never the publish
	}
	return decodeIndex(data), nil
}

func decodeIndex(data []byte) *siterender.Index {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	index := &siterender.Index{}
	if err := json.NewDecoder(io.LimitReader(zr, maxIndexBytes)).Decode(index); err != nil {
		return nil
	}
	return index
}

// builtTrees are the trees of a release about to be written.
type builtTrees struct {
	rendered bool
	// source and stored are the whole source tree and the part that is new.
	source, stored []site.File
	removed        []string
	output         []site.File
	index          *site.File
}

// fill writes the trees onto a release: only what differs from the parent when
// the release can amend it, and the whole of each tree otherwise.
func (t *builtTrees) fill(rel *site.Release, base *basis, derived bool) {
	rel.Files = t.output
	if t.rendered {
		rel.SourceFiles = t.source
	}
	if !derived || base.release == nil || base.release.ManifestDepth+1 >= maxManifestDepth || base.release.Rendered != t.rendered {
		return
	}
	rel.ManifestParent, rel.ManifestDepth = base.release.ID, base.release.ManifestDepth+1
	rel.Files, rel.Removed = deltaOf(base.output, t.output)
	if t.rendered {
		rel.SourceFiles, rel.SourceRemoved = t.stored, t.removed
	}
}

// deltaOf is what target changes from base, and the paths it no longer holds.
func deltaOf(base, target []site.File) (changed []site.File, removed []string) {
	before := pathIndex(base)
	changed = []site.File{}
	for _, f := range target {
		if prior, ok := before[f.Path]; !ok || prior.SHA256 != f.SHA256 || prior.ContentType != f.ContentType {
			changed = append(changed, f)
		}
		delete(before, f.Path)
	}
	for p := range before {
		removed = append(removed, p)
	}
	slices.Sort(removed)
	return changed, removed
}

// buildTrees stores a publish's source and, for a site with layouts, renders it.
func (s *Service) buildTrees(ctx context.Context, p publication) (*builtTrees, error) {
	stored, err := s.store(ctx, p.st.ID, p.built, p.base, p.req)
	if err != nil {
		return nil, err
	}
	whole := append(slices.Clone(p.built.kept), stored...)
	trees := &builtTrees{source: whole, stored: stored, removed: p.built.removed, output: whole}
	fsys := s.treeFS(ctx, p.st.ID, whole, p.built.inputs)
	if !siterender.Active(fsys) {
		return trees, nil
	}
	sink := s.newOutputSink(ctx, p, whole, false)
	index, err := s.render(ctx, renderJob{fsys, whole}, p, sink)
	if err != nil {
		return nil, err
	}
	trees.rendered, trees.output = true, sink.output()
	if trees.index, err = s.storeIndex(ctx, p.st.ID, index); err != nil {
		return nil, err
	}
	return trees, nil
}

// preview answers a dry run: the same render, storing nothing.
func (s *Service) preview(ctx context.Context, p publication) (*Published, error) {
	whole := previewSource(p.built)
	current := *p.base.release
	current.Files = nil
	out := &Published{Release: &current, Env: p.env.Name, Address: s.Address(ctx, p.st, p.env.Name)}
	fsys := s.treeFS(ctx, p.st.ID, whole, p.built.inputs)
	if !siterender.Active(fsys) {
		out.OutputsChanged = len(p.built.inputs) + len(p.built.removed)
		return out, nil
	}
	sink := s.newOutputSink(ctx, p, whole, true)
	if _, err := s.render(ctx, renderJob{fsys, whole}, p, sink); err != nil {
		return nil, err
	}
	changed, removed := deltaOf(p.base.output, sink.output())
	out.OutputsChanged = len(changed) + len(removed)
	return out, nil
}

// previewSource is the source tree a dry run renders, its new files not stored.
func previewSource(b *built) []site.File {
	whole := slices.Clone(b.kept)
	for _, in := range b.inputs {
		whole = append(whole, site.File{Path: in.Path, SHA256: in.sum, ContentType: contentTypeOf(in.Path), Size: int64(len(in.Body))})
	}
	return whole
}

// renderJob is one render's input.
type renderJob struct {
	fsys  fs.FS
	whole []site.File
}

func (s *Service) render(ctx context.Context, job renderJob, p publication, sink *outputSink) (*siterender.Index, error) {
	s.rendering.Lock()
	defer s.rendering.Unlock()
	if p.req.Phase != nil {
		p.req.Phase("Rendering")
	}
	ctx, cancel := context.WithTimeout(ctx, renderTimeout)
	defer cancel()
	limits := s.cfg().Limits()
	index, err := siterender.Render(ctx, job.fsys, siterender.Options{
		Limits:   siterender.Limits{PageBytes: limits.MaxFileBytes, TotalBytes: limits.MaxReleaseBytes, Pages: limits.MaxFiles},
		Previous: p.base.index, Changed: changedPaths(p.base.files, job.whole),
	}, sink)
	if werr := sink.wait(); err == nil {
		err = werr
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the renderer's own sentence, naming the files
	}
	return index, nil
}

// changedPaths is every source path that differs between two trees.
func changedPaths(base, target []site.File) []string {
	changed, removed := deltaOf(base, target)
	out := removed
	for _, f := range changed {
		out = append(out, f.Path)
	}
	return out
}

func (s *Service) storeIndex(ctx context.Context, siteID string, index *siterender.Index) (*site.File, error) {
	raw, err := json.Marshal(index)
	if err != nil {
		return nil, fmt.Errorf("encoding the render index: %w", err)
	}
	body, err := gzipped(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	f := &site.File{Path: renderIndexFile, SHA256: hex.EncodeToString(sum[:]), ContentType: "application/gzip", Size: int64(len(body))}
	if err := s.Objects.PutObject(ctx, s.Bucket, ObjectKey(siteID, *f, ""), body, f.ContentType); err != nil {
		return nil, fmt.Errorf("storing the render index: %w", err)
	}
	return f, nil
}

// outputSink stores a render's output as it is produced, a few files at a time,
// so that what is held is bounded by those few and not by the size of the site.
type outputSink struct {
	s      *Service
	ctx    context.Context
	siteID string
	base   *basis
	source map[string]site.File
	before map[string]site.File
	limits siteconfig.Config
	dryRun bool
	g      *errgroup.Group
	mu     sync.Mutex
	files  []site.File
	total  int64
}

func (s *Service) newOutputSink(ctx context.Context, p publication, whole []site.File, dryRun bool) *outputSink {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelUploads)
	return &outputSink{
		s: s, ctx: gctx, siteID: p.st.ID, base: p.base, source: pathIndex(whole), before: pathIndex(p.base.output),
		limits: s.cfg().Limits(), dryRun: dryRun, g: g,
	}
}

func (o *outputSink) add(f site.File) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.files = append(o.files, f)
	o.total += f.Size
	switch {
	case len(o.files) > o.limits.MaxFiles:
		return refused("the render produced more than the %d files sites.max_release_files allows", o.limits.MaxFiles)
	case o.total > o.limits.MaxReleaseBytes:
		return refused("the render produced more than the %d bytes sites.max_release_bytes allows", o.limits.MaxReleaseBytes)
	}
	return nil
}

// Put stores one rendered file, or only measures it on a dry run.
func (o *outputSink) Put(name string, body []byte) error {
	clean, err := CleanPath(name)
	if err != nil {
		return refused("%s", err.Error())
	}
	sum := sha256.Sum256(body)
	in := InputFile{Path: clean, Body: body, sum: hex.EncodeToString(sum[:])}
	if o.dryRun {
		return o.add(site.File{Path: clean, SHA256: in.sum, ContentType: contentTypeOf(clean), Size: int64(len(body))})
	}
	o.g.Go(func() error {
		f, err := o.s.storeFile(o.ctx, o.siteID, in, &basis{byDigest: o.base.byDigest})
		if err != nil {
			return fmt.Errorf("storing %s: %w", clean, err)
		}
		return o.add(*f)
	})
	return nil
}

// Copy serves a source file as it is, under the object it already has.
func (o *outputSink) Copy(name string) error {
	f, ok := o.source[name]
	if !ok {
		return refused("the render copied %q, which is not in the source", name)
	}
	return o.add(f)
}

// Keep carries an output of the release this one is based on.
func (o *outputSink) Keep(name string) error {
	f, ok := o.before[name]
	if !ok {
		return refused("the render kept %q, which the base release does not serve", name)
	}
	return o.add(f)
}

func (o *outputSink) wait() error {
	if err := o.g.Wait(); err != nil {
		return err //nolint:wrapcheck // each file's own sentence
	}
	return nil
}

func (o *outputSink) output() []site.File {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.files)
}

// treeFS is a release's source as a file system, its new files read from memory
// and the rest from the objects they are stored under when they are opened.
func (s *Service) treeFS(ctx context.Context, siteID string, files []site.File, pending []InputFile) fs.FS {
	bodies := make(map[string][]byte, len(pending))
	for _, in := range pending {
		bodies[in.Path] = in.Body
	}
	entries := make([]sitefs.Entry, 0, len(files))
	byPath := pathIndex(files)
	for _, f := range files {
		entries = append(entries, sitefs.Entry{Path: f.Path, Size: f.Size})
	}
	return sitefs.New(entries, func(name string) ([]byte, error) {
		if body, ok := bodies[name]; ok {
			return slices.Clone(body), nil
		}
		body, _, err := s.Objects.GetObject(ctx, s.Bucket, ObjectKey(siteID, byPath[name], ""))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		return body, nil
	})
}
