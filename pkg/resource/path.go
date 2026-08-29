package resource

import (
	"errors"
	"fmt"
	"strings"
)

// Folder-path limits (#1529). A path is the slash-separated folder chain a
// resource is filed under inside its library.
const (
	// MaxPathSegments bounds how deep a folder chain may go.
	MaxPathSegments = 8
	// MaxPathLen bounds the whole path, separators included.
	MaxPathLen = 200
	// MaxPathSegmentLen is the longest a single folder name may be. It is the
	// rule the flat category carried, kept unchanged so every path that was
	// legal before the tree existed is still legal.
	MaxPathSegmentLen = 31
)

// pathSeparator is what divides one folder from the next.
const pathSeparator = "/"

// ValidatePath checks a resource's folder path.
//
// Each segment keeps the rule the flat category was validated against, which is
// what makes every pre-tree row a legal one-segment path and every pre-tree URI
// unchanged. The rest bounds the tree: a depth, a total length, and a refusal of
// the three shapes that would make a path mean something other than a folder
// chain -- an empty segment, a leading or trailing slash, and a relative
// segment.
//
// Each refusal names the rule it broke rather than restating the whole grammar,
// because the person reading it typed one path and needs to know which part of
// it is the problem.
func ValidatePath(p string) error {
	if p == "" {
		return errors.New("path is required")
	}
	if strings.HasPrefix(p, pathSeparator) || strings.HasSuffix(p, pathSeparator) {
		return fmt.Errorf("path must not start or end with %q, got %q", pathSeparator, p)
	}
	segments := strings.Split(p, pathSeparator)
	// Depth is checked before total length. A path that breaks both is fixed by
	// removing folders, and "at most 8 folders" says which ones; "at most 200
	// characters" leaves the person shortening names that were never the
	// problem.
	if len(segments) > MaxPathSegments {
		return fmt.Errorf("path is %d folders deep, at most %d allowed", len(segments), MaxPathSegments)
	}
	if len(p) > MaxPathLen {
		return fmt.Errorf("path exceeds %d characters, got %d", MaxPathLen, len(p))
	}
	for _, s := range segments {
		if err := validateSegment(s, p); err != nil {
			return err
		}
	}
	return nil
}

// validateSegment checks one folder name, naming the whole path in the message
// so a refusal of a deep path says which path was refused as well as which
// segment.
func validateSegment(s, p string) error {
	switch s {
	case "":
		return fmt.Errorf("path has an empty folder name: %q", p)
	case ".", "..":
		return fmt.Errorf("path folder %q is a relative segment and names no folder", s)
	}
	if !pathSegmentRe.MatchString(s) {
		return fmt.Errorf("path folder %q must match %s", s, pathSegmentRe.String())
	}
	return nil
}

// PathSegments splits a path into its folder names. An empty path has none,
// which is the library root.
func PathSegments(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, pathSeparator)
}

// PathUnder reports whether p is prefix or lies beneath it. An empty prefix is
// the library root, which everything is under.
//
// The separator is part of the comparison: without it "data-archive" would read
// as being under "data", and a folder rename would drag in a sibling whose name
// merely starts with the same letters.
func PathUnder(p, prefix string) bool {
	if prefix == "" {
		return true
	}
	return p == prefix || strings.HasPrefix(p, prefix+pathSeparator)
}

// RepointPath rewrites p's from-prefix to to, which is what renaming or moving
// a folder does to every resource beneath it. A path that is not under from is
// returned unchanged, so a caller that hands it the whole library gets back only
// the affected rows changed.
func RepointPath(p, from, to string) string {
	if !PathUnder(p, from) {
		return p
	}
	if p == from {
		return to
	}
	return to + p[len(from):]
}
