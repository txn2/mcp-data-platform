// Package srcdir is where a stored file's versions live and whether a table's
// location lies among them (#2131, #2136).
//
// A managed resource and a portal asset both keep every version of their file
// beneath a directory named by the source's id --
// resources/{scope}/{owner}/{id}/... and {prefix}/{owner}/{id}/... -- so a
// table over any version of the file reads somewhere under that directory and
// a table over anything else does not. That is the one fact a table carries
// about where it came from, and what lets the platform recognize a table it
// made and has no record of.
package srcdir

import "strings"

// Sep separates the segments of an object key.
const Sep = "/"

// Root is the directory, with its trailing slash, named by id in the key of a
// source's current file, or "" when no directory is.
func Root(id, headKey string) string {
	if id == "" {
		return ""
	}
	i := strings.LastIndex(headKey, Sep)
	if i < 0 {
		return ""
	}
	segments := strings.Split(headKey[:i], Sep)
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i] == id {
			return strings.Join(segments[:i+1], Sep) + Sep
		}
	}
	return ""
}

// Owns reports whether location lies inside the directory the source with id,
// whose current file is headKey in bucket, keeps every version under.
func Owns(bucket, id, headKey, location string) bool {
	root := Root(id, headKey)
	if root == "" || location == "" {
		return false
	}
	return strings.HasPrefix(strings.TrimSuffix(location, Sep)+Sep, "s3://"+bucket+Sep+root)
}

// Describe names where a table reads, for a refusal.
func Describe(location string) string {
	if location == "" {
		return "no external location"
	}
	return location
}

// Split reads an s3:// location into its bucket and key.
func Split(location string) (bucket, key string, ok bool) {
	rest, found := strings.CutPrefix(location, "s3://")
	if !found {
		return "", "", false
	}
	bucket, key, found = strings.Cut(rest, Sep)
	return bucket, key, found && bucket != ""
}

// CandidateIDs are the path segments of a key that could name a stored file,
// deepest first: a file's directory is named by its id with at most a version
// directory beneath.
func CandidateIDs(key string) []string {
	parts := strings.Split(strings.Trim(key, Sep), Sep)
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for i := len(parts) - 1; i >= 0; i-- {
		if p := parts[i]; p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// Same compares two locations as directories.
func Same(a, b string) bool {
	return strings.TrimSuffix(a, Sep) == strings.TrimSuffix(b, Sep)
}

// Under reports whether a location lies inside any of dirs.
func Under(location string, dirs []string) bool {
	loc := strings.TrimSuffix(location, Sep) + Sep
	for _, d := range dirs {
		if strings.HasPrefix(loc, strings.TrimSuffix(d, Sep)+Sep) {
			return true
		}
	}
	return false
}
