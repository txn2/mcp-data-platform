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

// msgContentMissing is what a reader is told when a resource's row survived its
// stored file. It is a 404 about the CONTENT, not about the resource: the
// record is still listed, still editable and still deletable, and saying "not
// found" flatly would contradict the page the reader is standing on.
//
// The portal was the only reader of resource blobs that did not draw this
// distinction. The resources/read middleware and the search-index consumer both
// call IsObjectNotFound and act on a confirmed orphan; the REST content route
// reported one as a 500, so a resource whose file had never been stored — every
// row a metadata-only seed writes — answered the portal with a server error.
const msgContentMissing = "This resource's stored file is missing. " +
	"The record is still here, but its content is not in storage."

// msgContentUnavailable is a read the blob store would not answer, as opposed
// to one it answered with "no such object". Carries no colon: writeError
// truncates a 5xx body at the first one.
const msgContentUnavailable = "Could not read this resource's stored file. " +
	"The storage backend did not answer."
