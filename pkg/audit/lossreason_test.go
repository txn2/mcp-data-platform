package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// TestWriteLossReason: a write the deadline or Close cut off is a timeout,
// any other failure a failed write, and each lands on the drop counter and
// the write histogram under its own label (#1897).
func TestWriteLossReason(t *testing.T) {
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	cases := []struct {
		ctx    context.Context
		err    error
		reason string
		result string
	}{
		{ctx, nil, "", observability.StatusOK},
		{ctx, context.DeadlineExceeded, observability.AuditDropTimeout, observability.AuditDropTimeout},
		{canceled, errors.New("write: canceled"), observability.AuditDropTimeout, observability.AuditDropTimeout},
		{ctx, errors.New("relation missing"), observability.AuditDropWriteFailed, auditWriteResultError},
	}
	for _, c := range cases {
		reason := writeLossReason(c.ctx, c.err)
		if reason != c.reason {
			t.Errorf("writeLossReason(%v) = %q, want %q", c.err, reason, c.reason)
		}
		if got := writeResult(reason); got != c.result {
			t.Errorf("writeResult(%q) = %q, want %q", reason, got, c.result)
		}
	}
}
