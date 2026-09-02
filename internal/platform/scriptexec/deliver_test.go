package scriptexec

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// acmeDrop is a configured external destination: a named platform connection, a
// bucket, and the prefix everything written there sits under.
func acmeDrop() script.Destination {
	return script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}
}

// deliveryRequest is one output addressed to a configured bucket.
func deliveryRequest(name, key string) scriptrun.ExportRequest {
	req := csvRequest(name)
	req.Destination = acmeDrop()
	req.Key = key
	return req
}

// TestOutputWriter_DeliversThroughThePlatformSession is the delivery path end to
// end: the bytes go out as an ordinary platform tool call, addressed only by
// what the configured destination pins.
func TestOutputWriter_DeliversThroughThePlatformSession(t *testing.T) {
	h := newWriterHarness(t)
	h.caller.result = map[string]any{"bucket": "acme-exports", "key": "weekly/2026/sales.csv", "size": float64(41)}

	result, err := h.writer.Export(context.Background(), deliveryRequest("daily", "2026/sales.csv"))
	require.NoError(t, err)
	assert.Equal(t, "acme-exports", result.Bucket)
	assert.Equal(t, "weekly/2026/sales.csv", result.Key)
	assert.Equal(t, 41, result.Bytes, "the write tool's own accounting is what landed")
	assert.Empty(t, result.AssetID, "a delivered object is not a portal asset")

	require.Len(t, h.caller.calls, 1)
	call := h.caller.calls[0]
	assert.Equal(t, "s3_object", call.tool)
	assert.Equal(t, "put", call.args["action"])
	assert.Equal(t, "acme-s3", call.args["connection"], "the connection comes from the destination, never from the script")
	assert.Equal(t, "acme-exports", call.args["bucket"])
	assert.Equal(t, "weekly/2026/sales.csv", call.args["key"])
	assert.Equal(t, "text/csv", call.args["content_type"])
	assert.Equal(t, true, call.args["is_base64"])
	content, ok := call.args["content"].(string)
	require.True(t, ok)
	decoded, err := base64.StdEncoding.DecodeString(content)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(decoded), "region,total"), string(decoded))

	// Nothing about the portal was touched, and the run records where the
	// object went.
	assert.Empty(t, h.versions.created)
	assert.Empty(t, h.s3.objects)
	require.Len(t, h.runs.outputs, 1)
	assert.Equal(t, "acme-drop", h.runs.outputs[0].Destination)
	assert.Equal(t, "acme-exports", h.runs.outputs[0].Bucket)
	assert.Equal(t, "weekly/2026/sales.csv", h.runs.outputs[0].Key)
	assert.Equal(t, 41, h.runs.outputs[0].Bytes)
}

// TestOutputWriter_DeliveryKeyDefaultsToTheOutputName covers the arrangement a
// consumer polling one fixed path depends on.
func TestOutputWriter_DeliveryKeyDefaultsToTheOutputName(t *testing.T) {
	h := newWriterHarness(t)

	result, err := h.writer.Export(context.Background(), deliveryRequest("Daily Sales", ""))
	require.NoError(t, err)
	assert.Equal(t, "weekly/Daily-Sales.csv", result.Key,
		"a name written for people still has to address an object")
}

// TestOutputWriter_OneNameReachesBothSinks is the case the pair identity exists
// for: a dashboard keeps its versioned asset while another system receives the
// same rows as a file.
func TestOutputWriter_OneNameReachesBothSinks(t *testing.T) {
	h := newWriterHarness(t)
	ctx := context.Background()

	stored, err := h.writer.Export(ctx, csvRequest("daily"))
	require.NoError(t, err)
	delivered, err := h.writer.Export(ctx, deliveryRequest("daily", ""))
	require.NoError(t, err)

	assert.NotEmpty(t, stored.AssetID)
	assert.Equal(t, "weekly/daily.csv", delivered.Key)
	require.Len(t, h.runs.outputs, 2)
	assert.Equal(t, script.DestinationPortal, h.runs.outputs[0].Destination)
	assert.Equal(t, "acme-drop", h.runs.outputs[1].Destination)
}

// TestOutputWriter_RefusesASecondDeliveryToOneDestination is the same
// exactly-once rule the portal path has, applied per destination: two results
// under one identity must fail rather than have one silently dropped.
func TestOutputWriter_RefusesASecondDeliveryToOneDestination(t *testing.T) {
	h := newWriterHarness(t)
	ctx := context.Background()

	_, err := h.writer.Export(ctx, deliveryRequest("daily", ""))
	require.NoError(t, err)

	_, err = h.writer.Export(ctx, deliveryRequest("daily", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `already written to "acme-drop" by this run`)
	assert.Len(t, h.caller.calls, 1, "the second call never left")
}

// TestOutputWriter_ReclaimedRunDoesNotDeliverTwice is the idempotency the queue
// needs on the sharpest path: a run reclaimed after delivering re-executes from
// the top, and the object must not be written to an external system again.
func TestOutputWriter_ReclaimedRunDoesNotDeliverTwice(t *testing.T) {
	h := newWriterHarness(t)
	ctx := context.Background()

	first, err := h.writer.Export(ctx, deliveryRequest("daily", ""))
	require.NoError(t, err)

	reclaimed := newOutputWriter(h.writer.deps, h.runs, h.run, h.writer.script, h.caller)
	again, err := reclaimed.Export(ctx, deliveryRequest("daily", ""))
	require.NoError(t, err)

	assert.Equal(t, first.Key, again.Key)
	assert.Equal(t, first.Bucket, again.Bucket)
	assert.Len(t, h.caller.calls, 1, "the object was already delivered")
	assert.Len(t, h.runs.outputs, 1)

	// A script that writes one name to one place twice is refused the same way
	// whether or not the run was reclaimed: without that, the bug the first
	// attempt would have failed on passes silently on the second.
	_, err = reclaimed.Export(ctx, deliveryRequest("daily", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already written")
}

// TestOutputWriter_DeliveryFailures covers every way a delivery can fail to
// land, because an output reported as delivered that is not there is the one
// outcome an external consumer cannot detect.
func TestOutputWriter_DeliveryFailures(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(writerHarness)
		request scriptrun.ExportRequest
		wantErr string
	}{
		{
			"the write is refused", func(h writerHarness) {
				h.caller.err = errors.New("not authorized: connection not allowed for persona: analyst")
			},
			deliveryRequest("daily", ""), "not authorized",
		},
		{
			"a key climbing out of the configured prefix", func(writerHarness) {},
			deliveryRequest("daily", "../../elsewhere/sales.csv"), "'.' or '..'",
		},
		{
			"an unknown format", func(writerHarness) {},
			func() scriptrun.ExportRequest {
				req := deliveryRequest("daily", "")
				req.Format = "parquet"
				return req
			}(),
			"unsupported format",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWriterHarness(t)
			tt.arrange(h)
			_, err := h.writer.Export(context.Background(), tt.request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, h.runs.outputs, "nothing landed, so nothing is recorded as landed")
		})
	}
}

// TestOutputWriter_DeliveryNeedsAPlatformSession covers the deployment that can
// run a script but has no assembled server to write through: the run fails
// rather than reporting a delivery it never made.
func TestOutputWriter_DeliveryNeedsAPlatformSession(t *testing.T) {
	h := newWriterHarness(t)
	h.writer.caller = nil

	_, err := h.writer.Export(context.Background(), deliveryRequest("daily", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no platform session")
}

// TestOutputWriter_PortalOutputNeedsItsStores is the same honesty on the other
// path: a deployment with no asset store fails the output that needs one, and
// says which one.
func TestOutputWriter_PortalOutputNeedsItsStores(t *testing.T) {
	h := newWriterHarness(t)
	h.writer.deps = ExportDeps{}

	_, err := h.writer.Export(context.Background(), csvRequest("daily"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no portal asset store or object storage")

	// The same deployment can still deliver: a run that only writes to a configured
	// bucket needs no portal at all.
	_, err = h.writer.Export(context.Background(), deliveryRequest("daily", ""))
	require.NoError(t, err)
}

// TestDeliveredBytes prefers what the write tool reports, because that is what
// the bucket received.
func TestDeliveredBytes(t *testing.T) {
	assert.Equal(t, 41, deliveredBytes(map[string]any{"size": float64(41)}, 12))
	assert.Equal(t, 12, deliveredBytes(map[string]any{}, 12), "an unknown size is not zero bytes")
	assert.Equal(t, 12, deliveredBytes(map[string]any{"size": "big"}, 12))
}

func TestDefaultKeySegment(t *testing.T) {
	assert.Equal(t, "daily-sales.csv", defaultKeySegment("daily-sales.csv"))
	assert.Equal(t, "Q3-report", defaultKeySegment("Q3 report"))
	assert.Equal(t, "output", defaultKeySegment(""))
}

// TestOutputWriter_RefusesTwoOutputsOnOneObject covers what the (name,
// destination) rule alone does not: the object KEY identifies a delivered file,
// two names can arrive at one key, and the second write would replace the first
// in a bucket the platform cannot read back while the run recorded both as
// delivered.
func TestOutputWriter_RefusesTwoOutputsOnOneObject(t *testing.T) {
	h := newWriterHarness(t)
	ctx := context.Background()

	_, err := h.writer.Export(ctx, deliveryRequest("Q1 Sales", ""))
	require.NoError(t, err)

	// A different output name that sanitizes onto the same key.
	_, err = h.writer.Export(ctx, deliveryRequest("Q1/Sales", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already wrote")
	assert.Len(t, h.caller.calls, 1, "the second object never left")
	assert.Len(t, h.runs.outputs, 1)

	// And the same collision reached deliberately, by naming one key twice. The
	// key is relative to the configured prefix, which is what the destination adds.
	_, err = h.writer.Export(ctx, deliveryRequest("other", "Q1-Sales.csv"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already wrote")
}
