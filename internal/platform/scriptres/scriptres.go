// Package scriptres is the managed-resource text a script loads with
// load("res:<library>/<path>/<file>", NAME = "text") (#2113): a page script, a
// SQL file, an HTML template, kept as a file of its own and edited as one,
// rather than escaped into a Starlark string.
//
// A save resolves every such load under its author's access and pins the
// file's current version; every run of the version it creates reads that
// version. A draft pins each file's version as the draft starts, under the
// caller's access, and records it, which is what the save it leads to will pin. A run's or a
// draft's recording keeps the version of each file it read, and a test
// replaying it reads those versions, so a replay of an older run reads the
// text that run read.
package scriptres

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// MaxTextBytes is the most text one load reads.
const MaxTextBytes = 1 << 20

// URI is the address a res: module names, in the library's scheme.
func URI(module, scheme string) string {
	return scheme + "://" + strings.TrimPrefix(module, scriptlib.ResourcePrefix)
}

// Pin resolves every res: load of source under claims and returns the version
// each reads, keyed by module, with a sentence for each load that cannot be
// read. A source that loads none returns an empty set.
func Pin(ctx context.Context, r *scriptsrc.Reader, source string, claims resource.Claims) (pins map[string]script.ResourcePin, problems []string) {
	modules := scriptlib.ResourceModules(source)
	pins = make(map[string]script.ResourcePin, len(modules))
	if len(modules) == 0 {
		return pins, nil
	}
	if r == nil {
		return pins, []string{"this deployment has no managed-resource library to load a file from"}
	}
	for _, module := range modules {
		res, err := r.Resolve(ctx, URI(module, r.Scheme()), claims)
		if err != nil {
			problems = append(problems, fmt.Sprintf("load(%q): %v", module, err))
			continue
		}
		head, err := r.Head(ctx, res)
		if err != nil {
			problems = append(problems, fmt.Sprintf("load(%q): %v", module, err))
			continue
		}
		if _, err := r.Text(ctx, res, head, MaxTextBytes); err != nil {
			problems = append(problems, fmt.Sprintf("load(%q): %v", module, err))
			continue
		}
		pins[module] = script.ResourcePin{ResourceID: res.ID, Version: head}
	}
	return pins, problems
}

// Source is the library source an execution reads its loads through, with
// res: modules answered from the library: a pinned module reads its pinned
// version, and an unpinned one, in a draft or a test, reads the file as it is
// now under Claims. A run passes the pins its version holds and no claims, so
// a load its save did not pin is refused.
type Source struct {
	Libraries scriptlib.Source
	Reader    *scriptsrc.Reader
	Pins      map[string]script.ResourcePin
	Claims    *resource.Claims
}

// LibrarySource implements scriptlib.Source.
func (s Source) LibrarySource(ctx context.Context, name string, version int) (string, error) {
	if s.Libraries == nil {
		return "", scriptlib.ErrNotFound
	}
	return s.Libraries.LibrarySource(ctx, name, version) //nolint:wrapcheck // the library store's own answer
}

// ResourceText implements scriptlib.ResourceSource.
func (s Source) ResourceText(ctx context.Context, module string) (string, error) {
	if s.Reader == nil {
		return "", errors.New("no managed-resource library is available")
	}
	if pin, ok := s.Pins[module]; ok {
		res, err := s.Reader.ByID(ctx, pin.ResourceID)
		if err != nil {
			return "", err //nolint:wrapcheck // names the file
		}
		// A run reads what its version pinned under the identity it presents.
		// A test or a save replaying a run reads it under the access of the
		// person running them, so a replay hands nobody a file they cannot
		// read.
		if s.Claims != nil && !resource.CanAccessResource(*s.Claims, res) {
			return "", fmt.Errorf("load(%q): %w", module, scriptsrc.ErrNotFound)
		}
		return s.Reader.Text(ctx, res, pin.Version, MaxTextBytes) //nolint:wrapcheck // names the file and version
	}
	if s.Claims == nil {
		return "", errors.New("the script version does not pin this file; save the script again to pin it")
	}
	res, err := s.Reader.Resolve(ctx, URI(module, s.Reader.Scheme()), *s.Claims)
	if err != nil {
		return "", err //nolint:wrapcheck // names the reference
	}
	return s.Reader.Text(ctx, res, 0, MaxTextBytes) //nolint:wrapcheck // names the file
}

// With wraps libraries so an execution can read res: modules: pinned, or
// under claims when claims is not nil. A nil reader leaves libraries as they
// are, and a res: load is refused for want of a library.
func With(libraries scriptlib.Source, r *scriptsrc.Reader, pins map[string]script.ResourcePin, claims *resource.Claims) scriptlib.Source {
	if r == nil {
		return libraries
	}
	return Source{Libraries: libraries, Reader: r, Pins: pins, Claims: claims}
}

// Current resolves the version of each file source loads as it is now under
// claims, for a draft to read and record. A load that cannot be read is left
// out, so the draft fails at that load naming why.
func Current(ctx context.Context, r *scriptsrc.Reader, source string, claims resource.Claims) map[string]script.ResourcePin {
	if r == nil {
		return nil
	}
	pins, _ := Pin(ctx, r, source, claims)
	return pins
}

// PinsOf is the version of each file an execution reading src reads, keyed
// by module: what its recording keeps.
func PinsOf(src scriptlib.Source) map[string]script.ResourcePin {
	s, ok := src.(Source)
	if !ok || len(s.Pins) == 0 {
		return nil
	}
	return s.Pins
}

// Replaying is src reading each file a recording's run read at the version it
// read, and any other file as src reads it.
func Replaying(src scriptlib.Source, files map[string]script.ResourcePin) scriptlib.Source {
	s, ok := src.(Source)
	if !ok || len(files) == 0 {
		return src
	}
	pins := make(map[string]script.ResourcePin, len(files))
	maps.Copy(pins, s.Pins)
	maps.Copy(pins, files)
	s.Pins = pins
	return s
}
