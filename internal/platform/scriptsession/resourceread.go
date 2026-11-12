package scriptsession

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ResourceRead is platform.read_resource as the session answers it (#2616): a
// managed resource's bytes and their sha256, for a script that used to read
// them straight from the platform's bucket. It is a host call, not a tool, and
// a recording keeps it like any other, so a test replays the bytes the run
// read.
const ResourceRead = "resource.read"

// MaxResourceReadBytes is the most one read hands a script: its bytes cross
// the session base64-encoded, a third larger, inside the transport's 16 MiB
// frame (mcp.DefaultMaxLineLength).
const MaxResourceReadBytes = 8 << 20

// frameTooLarge is how the transport refuses a frame over its limit: a
// resource far past the cap is refused by the transport before its bytes reach
// the cap check, and is told the cap all the same.
const frameTooLarge = "exceeded the configured maximum line length"

// referencePrefix is the reference form manage_resource and fetch name a
// resource by.
const referencePrefix = "mcp:resource:"

// readResource reads one managed resource through the session's own
// resources/read, so the read is authenticated as the run, refused for a
// resource outside the run's scope by the check every resources/read crosses,
// and recorded as a read of the resource. A reference is resolved to the
// resource's address by manage_resource get, under the same identity.
func (c *SessionCaller) readResource(ctx context.Context, args map[string]any) (map[string]any, error) {
	ref, _ := args["resource"].(string)
	ref = strings.TrimSpace(ref)
	uri, err := c.resourceURI(ctx, ref)
	if err != nil {
		return nil, err
	}
	res, err := c.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil && strings.Contains(err.Error(), frameTooLarge) {
		return nil, fmt.Errorf("resource %s is larger than the %d bytes one read hands a script", uri, c.readLimit())
	}
	if err != nil {
		return nil, fmt.Errorf("no resource the run can read is at %s: %w", uri, err)
	}
	if res == nil || len(res.Contents) == 0 {
		return nil, fmt.Errorf("no resource the run can read is at %s", uri)
	}
	content := res.Contents[0]
	body := content.Blob
	if body == nil {
		body = []byte(content.Text)
	}
	if limit := c.readLimit(); len(body) > limit {
		return nil, fmt.Errorf("resource %s is %d bytes, over the %d bytes one read hands a script", uri, len(body), limit)
	}
	sum := sha256.Sum256(body)
	return map[string]any{
		"uri": uri, "content_type": content.MIMEType, "size": len(body),
		"sha256": hex.EncodeToString(sum[:]), "content": base64.StdEncoding.EncodeToString(body),
	}, nil
}

// readLimit is the most one read hands a script: MaxResourceReadBytes unless
// the caller was given another.
func (c *SessionCaller) readLimit() int {
	if c.maxRead > 0 {
		return c.maxRead
	}
	return MaxResourceReadBytes
}

// resourceURI is the address a read names: an address as given, or the one a
// reference resolves to.
func (c *SessionCaller) resourceURI(ctx context.Context, ref string) (string, error) {
	switch {
	case ref == "":
		return "", fmt.Errorf("name the resource by its address (%s) or its reference (%s<id>)", "mcp://...", referencePrefix)
	case strings.Contains(ref, "://"):
		return ref, nil
	case !strings.HasPrefix(ref, referencePrefix):
		return "", fmt.Errorf("the resource %q is neither a resource address nor a reference (%s<id>)", ref, referencePrefix)
	}
	out, err := c.CallTool(ctx, "manage_resource", map[string]any{"action": "get", "reference": ref})
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", ref, err)
	}
	uri, _ := out["uri"].(string)
	if uri == "" {
		return "", fmt.Errorf("nothing the run can read is filed under %s", ref)
	}
	return uri, nil
}
