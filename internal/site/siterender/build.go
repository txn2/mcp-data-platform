package siterender

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
)

// readBuild reads the build file of a tree that has one, reporting it when it
// cannot be honored, and fills in the imports a project's package files give
// it (#2785).
func (r *run) readBuild() {
	if !r.compiling {
		return
	}
	data, err := r.readFile(sitebuild.ControlFile)
	if err == nil {
		r.build, err = sitebuild.Parse(data)
	}
	if err == nil {
		err = r.packageImports()
	}
	if err != nil {
		r.report(Failure{File: sitebuild.ControlFile, Message: err.Error()})
		r.build = nil
	}
}

// compileAll compiles every entry the build file names, keeping the outputs of
// one whose inputs have not changed since the last render.
func (r *run) compileAll(index *Index) error {
	if r.build == nil {
		return nil
	}
	styles, err := r.stylesheets()
	if err != nil {
		// Reported with the others; the render goes on to report them all.
		r.report(Failure{File: sitebuild.ControlFile, Message: err.Error()})
		return nil //nolint:nilerr // reported, with the others, when the render ends
	}
	prior := r.previousBySource()
	for _, entry := range r.build.Entries {
		if kept, ok := r.currentBuild(prior[entry]); ok {
			if err := r.keepBuild(entry, kept, index); err != nil {
				return err
			}
			continue
		}
		if err := r.compileEntry(entry, styles, index); err != nil {
			return err
		}
		if r.exhausted() {
			return r.exhaustion()
		}
	}
	return nil
}

// stylesheets is the stylesheet compiler the build file asks for, nil for
// none.
func (r *run) stylesheets() (sitebuild.StylesheetCompiler, error) {
	if r.build.Tailwind == "" {
		return nil, nil //nolint:nilnil // none asked for
	}
	if r.opts.Stylesheets == nil {
		return nil, errors.New("this deployment cannot compile Tailwind CSS at publish; remove tailwind from the build file")
	}
	return r.opts.Stylesheets(r.ctx, r.build, r.src)
}

// keepBuild carries an entry's previous outputs, refusing one a file of the
// release now stands at (#2777): an overlay that adds app.js beside a kept
// app.tsx changes none of the entry's inputs.
func (r *run) keepBuild(entry string, kept []Entry, index *Index) error {
	for _, e := range kept {
		if r.sharedChunk(e.Output) {
			index.Entries = append(index.Entries, e)
			continue
		}
		if r.collides(entry, e.Output) {
			continue
		}
		if err := r.keep(e, index); err != nil {
			return err
		}
	}
	for _, b := range r.priorBuilds(entry) {
		// Kept unchanged: nothing to compare.
		b.Outputs = slices.Clone(b.Outputs)
		for i := range b.Outputs {
			b.Outputs[i].PreviousBytes, b.Outputs[i].Change = nil, ""
		}
		r.builds = append(r.builds, b)
	}
	return nil
}

// sharedChunk reports a chunk another entry already wrote: a chunk's name is a
// hash of its content, so two entries loading one module share its file.
func (r *run) sharedChunk(output string) bool {
	return strings.HasPrefix(output, sitebuild.ChunkDir+"/") && r.outputs[output] != ""
}

// previousBySource is the last render's compiled outputs by the entry they
// were compiled from.
func (r *run) previousBySource() map[string][]Entry {
	out := map[string][]Entry{}
	if r.prior == nil {
		return out
	}
	for _, e := range r.prior.Entries {
		if e.Build != "" {
			out[e.Source] = append(out[e.Source], e)
		}
	}
	return out
}

// currentBuild is an entry's previous outputs, when they were built by this
// compiler and nothing they read or looked for has changed.
func (r *run) currentBuild(prior []Entry) ([]Entry, bool) {
	if len(prior) == 0 {
		return nil, false
	}
	for _, e := range prior {
		if e.Build != sitebuild.Version || slices.ContainsFunc(e.Inputs, r.changes.touches) {
			return nil, false
		}
	}
	slices.SortFunc(prior, func(a, b Entry) int { return strings.Compare(a.Output, b.Output) })
	return prior, true
}

// compileEntry compiles one entry and hands its outputs to the sink.
func (r *run) compileEntry(entry string, styles sitebuild.StylesheetCompiler, index *Index) error {
	limits := sitebuild.Limits{FileBytes: r.limits.TemplateBytes, OutputBytes: r.limits.PageBytes}
	res, err := sitebuild.Compile(r.ctx, r.src, entry, sitebuild.Options{Config: r.build, Limits: limits, Fetch: r.fetch, Site: r.opts.Site, Stylesheets: styles})
	if err != nil {
		return r.compileFailed(entry, err)
	}
	for _, out := range res.Outputs {
		put, ok := r.placeOutput(entry, out)
		if !ok {
			continue
		}
		index.Entries = append(index.Entries, Entry{Output: out.Path, Source: entry, Inputs: res.Inputs, Build: sitebuild.Version})
		if !put {
			continue
		}
		r.outputs[out.Path] = entry
		r.meter.total += int64(len(out.Body))
		if err := r.sink.Put(out.Path, out.Body); err != nil {
			r.sinkErr = err
			return fmt.Errorf("storing %s: %w", out.Path, err)
		}
	}
	if res.Report != nil {
		res.Report.Compare(r.priorBuilds(entry))
		r.builds = append(r.builds, *res.Report)
	}
	return nil
}

// placeOutput decides an output: written (put), recorded without writing it
// again (a chunk another entry wrote with the same bytes), or refused.
func (r *run) placeOutput(entry string, out sitebuild.Output) (put, ok bool) {
	if strings.HasPrefix(out.Path, sitebuild.ChunkDir+"/") {
		sum := sha256.Sum256(out.Body)
		if prev, seen := r.chunks[out.Path]; seen {
			if prev != sum {
				r.report(Failure{File: entry, Message: fmt.Sprintf("compiles to %s, which %s also produces with other content", out.Path, r.outputs[out.Path])})
				return false, false
			}
			return false, true
		}
		if r.outputs[out.Path] != "" {
			return false, true // kept from a previous render under the same hash
		}
		r.chunks[out.Path] = sum
	}
	if r.collides(entry, out.Path) {
		return false, false
	}
	return true, true
}

// priorBuilds is the previous render's report of an entry.
func (r *run) priorBuilds(entry string) []sitebuild.Report {
	if r.prior == nil {
		return nil
	}
	var out []sitebuild.Report
	for _, b := range r.prior.Builds {
		if b.Entry == entry {
			out = append(out, b)
		}
	}
	return out
}

// collides reports, and refuses, an output another file of the release
// already is.
func (r *run) collides(entry, output string) bool {
	if other := r.outputs[output]; other != "" {
		r.report(Failure{File: entry, Message: fmt.Sprintf("compiles to %s, which %s also produces", output, other)})
		return true
	}
	if info, err := fs.Stat(r.src, output); err == nil && !info.IsDir() {
		r.report(Failure{File: entry, Message: fmt.Sprintf("compiles to %s, which the release also holds as a file; remove %s or rename the entry", output, output)})
		return true
	}
	return false
}

// compileFailed reports what stopped an entry, ending the render only when
// the render as a whole has run out.
func (r *run) compileFailed(entry string, err error) error {
	var be *sitebuild.Error
	var le *sitebuild.LimitError
	switch {
	case r.ctx.Err() != nil:
		return fmt.Errorf("compiling %s: %w", entry, r.ctx.Err())
	case errors.As(err, &be):
		for _, m := range be.Messages {
			file := m.File
			if file == "" {
				file = entry
			}
			r.report(Failure{File: file, Line: m.Line, Column: m.Column, Message: m.Text})
		}
	case errors.As(err, &le):
		r.report(Failure{File: entry, Message: le.Error()})
	default:
		r.report(Failure{File: entry, Message: err.Error()})
	}
	return nil
}
