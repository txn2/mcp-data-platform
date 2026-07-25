package resource

import "strings"

// IsObjectNotFound reports whether a blob-store GetObject error indicates the
// object does not exist (an orphaned resource: the metadata row survived its
// content), as opposed to a transient or permission failure that a retry might
// resolve.
//
// The mcp-s3 client wraps the underlying AWS/SeaweedFS error without a typed
// not-found, so detection is by the standard S3 not-found signatures present in
// the wrapped message. It lives here because both readers of resource blobs
// depend on the distinction and must draw it identically: the resources/read
// middleware self-heals a confirmed orphan by pruning the row, and the search
// index consumer clears a confirmed-orphan's indexed content instead of leaving
// stale text behind. A per-caller copy of this heuristic would let the two
// diverge on exactly the case that matters.
func IsObjectNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "nosuchkey") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "notfound") ||
		strings.Contains(msg, "status code: 404") ||
		strings.Contains(msg, "404 not found")
}
