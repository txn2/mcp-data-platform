package siterender

import (
	"path"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
)

// The inputs that are not files: everything in one _data folder, and the front
// matter of every page.
const (
	inputData  = "data:"
	inputPages = "site:pages"
	// recordFile is the key a record carries its own file under.
	recordFile = "_file"
)

// Entry is one output and everything it read, as it read it: a dependency
// recorded when the page ran cannot miss what the page actually used.
type Entry struct {
	Output string   `json:"output"`
	Source string   `json:"source"`
	Inputs []string `json:"inputs"`
	// Build is the compiler version a compiled output was built under.
	Build string `json:"build,omitempty"`
}

// indexVersion names what a render's output is a function of beyond its tree: a
// new version of the renderer or its engine renders everything again.
const indexVersion = reviewedEngine + "/2"

// Index is what a render produced and what produced it.
type Index struct {
	Version string  `json:"version"`
	Entries []Entry `json:"entries"`
	// Builds are each compiled entry's report (#2783), compared with the
	// render before.
	Builds []sitebuild.Report `json:"builds,omitempty"`
	// PrivateMaps is true when the release's source maps are served only to
	// the site's managers (#2784).
	PrivateMaps bool `json:"private_maps,omitempty"`
}

// current is the index when a render of this version wrote it, nil otherwise.
func (i *Index) current() *Index {
	if i == nil || i.Version != indexVersion {
		return nil
	}
	return i
}

func (i *Index) byOutput() map[string]Entry {
	if i == nil || i.Version != indexVersion {
		return nil
	}
	out := make(map[string]Entry, len(i.Entries))
	for _, e := range i.Entries {
		out[e.Output] = e
	}
	return out
}

// changes answers whether a file or input differs from the previous render.
type changes struct {
	paths map[string]bool
	data  map[string]bool
	pages bool
}

func newChanges(paths []string) changes {
	c := changes{paths: make(map[string]bool, len(paths)), data: map[string]bool{}}
	for _, p := range paths {
		c.paths[p] = true
		first, rest, _ := strings.Cut(p, "/")
		switch first {
		case dataDir:
			name, _, _ := strings.Cut(rest, "/")
			c.data[name] = true
		case layoutsDir, includesDir:
		default:
			c.pages = c.pages || isTemplated(path.Base(p))
		}
	}
	return c
}

// touches reports whether an input is affected. What import.meta.site reads
// is the release, which every render is a new one of.
func (c changes) touches(input string) bool {
	if input == sitebuild.SiteMetaInput {
		return true
	}
	if input == inputPages {
		return c.pages
	}
	if name, ok := strings.CutPrefix(input, inputData); ok {
		return c.data[name]
	}
	return c.paths[input]
}
