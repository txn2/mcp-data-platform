package s3

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"
)

// ReservedBucket is a bucket the platform keeps its own data in (#2616). No
// S3 tool action and no bucket source reaches it, on any connection and for
// any persona, administrators included: what it holds has an owner and a
// scope (a personal resource, an unshared asset, a private site release, a
// webhook payload) that only the platform's own surfaces check.
type ReservedBucket struct {
	// Name is the bucket as configured.
	Name string
	// Holds says what the platform keeps there.
	Holds string
	// Use names the platform surface that reads it instead.
	Use string
}

// reserved is the source of the reserved buckets, read on every call so a
// toolkit created after boot (a connection added at run time) refuses them as
// well. Nil reserves none.
var reserved atomic.Pointer[func() []ReservedBucket]

// SetReservedBuckets installs the buckets every S3 toolkit refuses; nil
// reserves none.
func SetReservedBuckets(fn func() []ReservedBucket) {
	if fn == nil {
		reserved.Store(nil)
		return
	}
	reserved.Store(&fn)
}

// PlatformBuckets is the platform's two stores as reserved buckets: the portal
// bucket (assets, exports, script outputs and site releases) and the
// managed-resource bucket (the resource library, webhook events and the
// scratch catalog's tables, and street maps unless their settings name a
// bucket of their own). An empty name reserves nothing for that store.
func PlatformBuckets(portalBucket, resourceBucket string) func() []ReservedBucket {
	out := []ReservedBucket{}
	if portalBucket != "" {
		out = append(out, ReservedBucket{
			Name:  portalBucket,
			Holds: "portal assets, exports, script outputs and site releases",
			Use:   "read an asset with fetch or manage_asset, and a site with manage_site",
		})
	}
	if resourceBucket != "" {
		out = append(out, ReservedBucket{
			Name:  resourceBucket,
			Holds: "the managed-resource library, webhook events and registered tables",
			Use:   "read a resource with fetch or manage_resource (a script with platform.read_resource), and a registered table with trino_query",
		})
	}
	return func() []ReservedBucket { return out }
}

// IsReserved reports whether a bucket is one the platform keeps its own data
// in, and which.
func IsReserved(bucket string) (ReservedBucket, bool) {
	p := reserved.Load()
	if p == nil || bucket == "" {
		return ReservedBucket{}, false
	}
	want := bucketName(bucket)
	for _, r := range (*p)() {
		if r.Name != "" && bucketName(r.Name) == want {
			return r, true
		}
	}
	return ReservedBucket{}, false
}

// bucketName is a bucket name as it is compared with a reserved one: folded to
// lower case with spaces and slashes trimmed, and an ARN read as its last
// segment. Only the comparison reads it; the request is sent as the caller
// wrote it, so a name that compares equal here and differs at S3 is refused,
// never let through.
func bucketName(bucket string) string {
	b := strings.ToLower(strings.TrimSpace(bucket))
	if strings.HasPrefix(b, "arn:") {
		b = b[strings.LastIndex(b, ":")+1:]
		b = b[strings.LastIndex(b, "/")+1:]
	}
	return strings.Trim(b, "/ ")
}

// reservedRefusal is the refusal of a reserved bucket, in the one wording
// every surface answers with: the bucket the caller named, what the platform
// keeps there, and where to read it instead.
func reservedRefusal(bucket string, r ReservedBucket) string {
	return fmt.Sprintf("bucket %q is where the platform keeps %s; no S3 tool, bucket source or connection reaches it, for any persona: %s",
		bucket, r.Holds, r.Use)
}

// refuseReserved is the refusal of the first reserved bucket among buckets,
// nil when none is.
func refuseReserved(buckets ...string) *mcp.CallToolResult {
	for _, b := range buckets {
		if r, ok := IsReserved(b); ok {
			return s3tools.ErrorResult(reservedRefusal(b, r))
		}
	}
	return nil
}
