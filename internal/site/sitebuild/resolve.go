package sitebuild

import (
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// candidateExts are tried, in order, after an import's own path, then as the
// file index in a folder of that name.
var candidateExts = []string{extTSX, extTS, extJSX, extJS, extMJS, extMTS, extCTS, extJSON, extCSS}

// resolve places every import esbuild meets. It answers every one, with a
// module or a refusal: an import left unanswered would fall to esbuild's own
// resolver, which reads the disk of the process.
func (c *compile) resolve(args api.OnResolveArgs) (api.OnResolveResult, error) {
	switch {
	case args.Kind == api.ResolveEntryPoint:
		return c.local(args.Path, sep)
	case cssReference(args):
		// A stylesheet's url() and its @import of a stylesheet on the web are
		// left as written: the browser fetches them from the served site or
		// the web, as it does a hand-written stylesheet's.
		return api.OnResolveResult{Path: args.Path, External: true}, nil
	case args.Namespace == nsURL:
		return fromURLModule(args)
	case strings.HasPrefix(args.Path, "https://"):
		return c.remote(args.Path)
	case strings.HasPrefix(args.Path, dot), strings.HasPrefix(args.Path, sep):
		return c.local(args.Path, args.Importer)
	case !isBare(args.Path):
		return api.OnResolveResult{}, fmt.Errorf("imports %q: a build imports a file of the site by a path starting ./, or a module by its https URL", args.Path)
	}
	u, ok := c.cfg.mapped(args.Path)
	if !ok {
		return api.OnResolveResult{}, fmt.Errorf("imports %q, which %s does not map; add it to \"imports\" with an https URL at an exact version", args.Path, ControlFile)
	}
	return c.remote(u)
}

// cssReference is a reference a stylesheet makes that is not a module: a
// url() token, or an @import of a stylesheet by URL.
func cssReference(args api.OnResolveArgs) bool {
	switch args.Kind {
	case api.ResolveCSSURLToken:
		return true
	case api.ResolveCSSImportRule:
		return strings.HasPrefix(args.Path, "https://") || strings.HasPrefix(args.Path, "http://")
	}
	return false
}

// remote is a URL module: left for the browser, or fetched and bundled.
func (c *compile) remote(u string) (api.OnResolveResult, error) {
	if !Pinned(u) {
		return api.OnResolveResult{}, fmt.Errorf("imports %q, which is not an https URL at an exact version, such as https://esm.sh/react@18.3.1", u)
	}
	if c.cfg.Remote == RemoteBundle {
		return api.OnResolveResult{Path: u, Namespace: nsURL}, nil
	}
	return api.OnResolveResult{Path: u, External: true}, nil
}

// fromURLModule places an import made by a fetched module: by its own URL, or
// by a path relative to the module's (a CDN's modules import one another by
// root-relative paths). A fetched module is the CDN's, so its imports are not
// held to the exact-version rule its importer was.
func fromURLModule(args api.OnResolveArgs) (api.OnResolveResult, error) {
	base, err := url.Parse(args.Importer)
	if err != nil {
		return api.OnResolveResult{}, fmt.Errorf("reading %s: %w", args.Importer, err)
	}
	ref, err := url.Parse(args.Path)
	if err != nil || (!strings.HasPrefix(args.Path, "https://") && !strings.HasPrefix(args.Path, sep) && !strings.HasPrefix(args.Path, dot)) {
		return api.OnResolveResult{}, fmt.Errorf("the module at %s imports %q, which is neither a URL nor a path", args.Importer, args.Path)
	}
	next := base.ResolveReference(ref)
	if next.Scheme != "https" {
		return api.OnResolveResult{}, fmt.Errorf("the module at %s imports %q, which is not https", args.Importer, args.Path)
	}
	return api.OnResolveResult{Path: next.String(), Namespace: nsURL}, nil
}

// local is a file of the tree an import names, relative to its importer or to
// the root. Every path it looks for is an input, so a file added later that the
// import would now find compiles the entry again.
func (c *compile) local(spec, importer string) (api.OnResolveResult, error) {
	name := spec
	if !strings.HasPrefix(spec, sep) {
		name = path.Join(path.Dir(importer), spec)
	}
	name = strings.TrimPrefix(path.Clean(sep+name), sep)
	if escapes(importer, spec) || !fs.ValidPath(name) || name == dot {
		return api.OnResolveResult{}, fmt.Errorf("imports %q, which is outside the site", spec)
	}
	for _, candidate := range candidates(name) {
		c.noteInput(candidate)
		if info, err := fs.Stat(c.src, candidate); err == nil && !info.IsDir() {
			return api.OnResolveResult{Path: sep + candidate, Namespace: nsSite}, nil
		}
	}
	return api.OnResolveResult{}, fmt.Errorf("imports %q: the site has no file %s", spec, name)
}

// escapes reports a relative import that climbs above the root of the tree,
// which path.Clean would otherwise quietly pin to the root.
func escapes(importer, spec string) bool {
	if strings.HasPrefix(spec, sep) {
		return false
	}
	dir := strings.Trim(path.Dir(importer), sep)
	depth := 0
	if dir != "" {
		depth = strings.Count(dir, sep) + 1
	}
	for seg := range strings.SplitSeq(spec, sep) {
		switch seg {
		case "..":
			depth--
			if depth < 0 {
				return true
			}
		case dot, "":
		default:
			depth++
		}
	}
	return false
}

// candidates are the files an import may mean, in the order they are tried: the
// path as written, the path with each extension, a .ts file a TypeScript import
// names as .js, and the index file of a folder.
func candidates(name string) []string {
	out := []string{name}
	for _, ext := range candidateExts {
		out = append(out, name+ext)
	}
	if stem, ok := strings.CutSuffix(name, extJS); ok {
		out = append(out, stem+extTS, stem+extTSX)
	}
	for _, ext := range candidateExts {
		out = append(out, name+sep+"index"+ext)
	}
	return out
}
