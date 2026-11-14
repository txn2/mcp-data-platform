package siterender

import (
	"path"
	"strings"
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
}

// indexVersion names what a render's output is a function of beyond its tree: a
// new version of the renderer or its engine renders everything again.
const indexVersion = reviewedEngine + "/2"

// Index is what a render produced and what produced it.
type Index struct {
	Version string  `json:"version"`
	Entries []Entry `json:"entries"`
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

// touches reports whether an input is affected.
func (c changes) touches(input string) bool {
	if input == inputPages {
		return c.pages
	}
	if name, ok := strings.CutPrefix(input, inputData); ok {
		return c.data[name]
	}
	return c.paths[input]
}
