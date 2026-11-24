package siterender

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
)

// readBuild reads the build file of a tree that has one, reporting it when it
// cannot be honored.
func (r *run) readBuild() {
	if !r.compiling {
		return
	}
	data, err := r.readFile(sitebuild.ControlFile)
	if err == nil {
		r.build, err = sitebuild.Parse(data)
	}
	if err != nil {
		r.report(Failure{File: sitebuild.ControlFile, Message: err.Error()})
	}
}

// compileAll compiles every entry the build file names, keeping the outputs of
// one whose inputs have not changed since the last render.
func (r *run) compileAll(index *Index) error {
	if r.build == nil {
		return nil
	}
	prior := r.previousBySource()
	for _, entry := range r.build.Entries {
		if kept, ok := r.currentBuild(prior[entry]); ok {
			for _, e := range kept {
				if err := r.keep(e, index); err != nil {
					return err
				}
			}
			continue
		}
		if err := r.compileEntry(entry, index); err != nil {
			return err
		}
		if r.exhausted() {
			return r.exhaustion()
		}
	}
	return nil
}

// previousBySource is the last render's compiled outputs by the entry they
// were compiled from.
func (r *run) previousBySource() map[string][]Entry {
	out := map[string][]Entry{}
	for _, e := range r.previous {
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
func (r *run) compileEntry(entry string, index *Index) error {
	limits := sitebuild.Limits{FileBytes: r.limits.TemplateBytes, OutputBytes: r.limits.PageBytes}
	res, err := sitebuild.Compile(r.ctx, r.src, entry, sitebuild.Options{Config: r.build, Limits: limits, Fetch: r.fetch})
	if err != nil {
		return r.compileFailed(entry, err)
	}
	for _, out := range res.Outputs {
		if r.collides(entry, out.Path) {
			continue
		}
		r.outputs[out.Path] = entry
		r.meter.total += int64(len(out.Body))
		if err := r.sink.Put(out.Path, out.Body); err != nil {
			r.sinkErr = err
			return fmt.Errorf("storing %s: %w", out.Path, err)
		}
		index.Entries = append(index.Entries, Entry{Output: out.Path, Source: entry, Inputs: res.Inputs, Build: sitebuild.Version})
	}
	return nil
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
