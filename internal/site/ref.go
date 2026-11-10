package site

import (
	"fmt"
	"strings"
)

// RefPrefix starts the reference that names a site's files to the tools that
// read and edit content.
const RefPrefix = "mcp:site:"

const refParts = 3

// Ref names part of an environment's file tree: one file, a folder when Path
// ends in a slash, or the whole tree when Path is empty.
type Ref struct {
	Site        string
	Environment string
	Path        string
}

// IsFile reports whether the reference names one file.
func (r Ref) IsFile() bool { return r.Path != "" && !strings.HasSuffix(r.Path, "/") }

// IsRef reports whether s is a site reference.
func IsRef(s string) bool { return strings.HasPrefix(s, RefPrefix) }

// ParseRef reads mcp:site:<site>/<environment>[/<path>].
func ParseRef(s string) (Ref, error) {
	rest, ok := strings.CutPrefix(s, RefPrefix)
	if !ok {
		return Ref{}, fmt.Errorf("not a site reference: %q", s)
	}
	parts := strings.SplitN(rest, "/", refParts)
	if len(parts) < refParts-1 || parts[0] == "" || parts[1] == "" {
		return Ref{}, fmt.Errorf("site reference %q: %s<site>/<environment>, then an optional file path", s, RefPrefix)
	}
	if err := ValidateEnvironment(parts[1]); err != nil {
		return Ref{}, fmt.Errorf("site reference %q: %w", s, err)
	}
	ref := Ref{Site: parts[0], Environment: parts[1]}
	if len(parts) == refParts {
		ref.Path = parts[2]
	}
	return ref, nil
}
