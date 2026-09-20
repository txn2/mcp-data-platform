package s3

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
)

// ProbeConnection opens the named connection and lists its buckets, which is
// the smallest request that proves the endpoint is reachable and the
// credential is accepted (#1805).
//
// A connection carrying a bucket prefix lists what the prefix admits, so the
// count reported is the buckets this connection can actually see rather than
// the account's.
//
// Implements connprobe.Prober.
func (t *Toolkit) ProbeConnection(ctx context.Context, name string) connprobe.Result {
	if t.s3Toolkit == nil {
		return connprobe.Failure("no S3 client is configured", nil)
	}
	client, err := t.s3Toolkit.GetClient(name)
	if err != nil {
		return connprobe.Failure(fmt.Sprintf("connection %q could not be opened", name), err)
	}
	buckets, err := client.ListBuckets(ctx)
	if err != nil {
		return connprobe.Failure(
			fmt.Sprintf("connection %q is configured but the endpoint refused a bucket listing", name), err)
	}
	detail := fmt.Sprintf("the endpoint answered; %d bucket(s) visible to this connection", len(buckets))
	if t.settings(name).readOnly {
		detail += "; this connection is read_only and refuses writes"
	}
	return connprobe.Success(detail)
}

// Verify interface compliance.
var _ connprobe.Prober = (*Toolkit)(nil)
