package scriptexec

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/script"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// toolPutObject is the tool a delivery is issued as. Delivery is not a private
// path to object storage: it is one ordinary platform tool call, so the write
// crosses the same authentication, authorization, connection-scoping, and audit
// middleware that the same call typed by a person crosses.
//
// That is what makes the second enforcement layer real rather than claimed. The
// facade below refuses a destination the version's grant does not name; this
// call is then authorized independently against the persona the script's roles
// resolve to, which is the authority of record. A destination whose connection
// that persona cannot reach is refused even though the grant named it.
const toolPutObject = "s3_put_object"

// deliver writes one output as an object in a granted bucket.
//
// The bytes are identical to what the portal would have stored: the same
// formatter produced them, and the same size ceiling applies. What differs is
// where they land and who can read them afterwards — which is precisely the
// decision an approval makes, and the reason a destination is pinned to a
// version rather than named by the script.
func (w *outputWriter) deliver(ctx context.Context, req scriptrun.ExportRequest, formatter trinokit.Formatter, data []byte) (*scriptrun.ExportResult, script.RunOutput, error) {
	if w.caller == nil {
		return nil, script.RunOutput{}, fmt.Errorf("output %q cannot be delivered to %q: this deployment has no platform session to write it through",
			req.Name, req.Destination.Name)
	}
	key, err := deliveryKey(req, formatter.FileExtension())
	if err != nil {
		return nil, script.RunOutput{}, err
	}
	// The object key, not the output name, is what identifies a delivered file.
	// Two names can arrive at one key — "Q1 Sales" and "Q1/Sales" both become
	// Q1-Sales.csv, and two outputs can simply be given the same key — and the
	// second write would replace the first in a bucket the platform cannot read
	// back, while the run recorded both as delivered. Refusing is the only
	// answer that keeps the run record true.
	if prior, taken := w.delivered[objectAddress(req.Destination, key)]; taken {
		return nil, script.RunOutput{}, fmt.Errorf("output %q would be delivered to %s, where this run already wrote %q; give it its own key",
			req.Name, key, prior)
	}

	// Content crosses as base64 rather than as text. Every format written here
	// is textual today, but the argument is a JSON string either way, and bytes
	// that are not valid UTF-8 do not survive one intact — a single such byte
	// in a cell would be silently rewritten and the delivered file would differ
	// from the asset the same run stored.
	out, err := w.caller.CallTool(ctx, toolPutObject, map[string]any{
		"connection":   req.Destination.Connection,
		"bucket":       req.Destination.Bucket,
		"key":          key,
		"content":      base64.StdEncoding.EncodeToString(data),
		"is_base64":    true,
		"content_type": contenttype.Normalize(formatter.ContentType()),
	})
	if err != nil {
		return nil, script.RunOutput{}, fmt.Errorf("delivering output %q to %s: %w",
			req.Name, req.Destination.Label(), err)
	}
	w.delivered[objectAddress(req.Destination, key)] = req.Name
	slog.Info("scripts: delivered an output",
		logKeyRunID, w.run.ID, "output", req.Name,
		"destination", req.Destination.Name, "bucket", req.Destination.Bucket,
		"key", key, "bytes", len(data))

	bytes := deliveredBytes(out, len(data))
	record := script.RunOutput{
		Name: req.Name, Destination: req.Destination.Name,
		Bucket: req.Destination.Bucket, Key: key,
		Format: req.Format, RowCount: len(req.Rows), Bytes: bytes,
	}
	return &scriptrun.ExportResult{
		Bucket: req.Destination.Bucket, Key: key, Bytes: bytes,
	}, record, nil
}

// objectAddress identifies one delivered object: the bucket it lands in and the
// key it lands on. The connection is deliberately not part of it — two
// connections to the same bucket are the same object.
func objectAddress(destination script.Destination, key string) string {
	return destination.Bucket + "\x00" + key
}

// deliveryKey composes the object key one delivery writes, under the prefix the
// destination was granted.
//
// The script chooses the part beneath the prefix, because that part is the
// contract between this automation and whatever consumes its output: a
// partitioned drop ("2026/08/sales.csv") is a different arrangement from a
// fixed path a consumer polls, and only the author knows which the consumer
// expects. It chooses nothing above the prefix: the join is one-directional and
// the key was already refused if it could climb out of it.
func deliveryKey(req scriptrun.ExportRequest, extension string) (string, error) {
	key := req.Key
	if key == "" {
		key = defaultKeySegment(req.Name) + extension
	}
	// Re-validated here rather than trusted from the facade. This function is
	// the last thing between a key and a bucket, and a check that lives only at
	// the caller is a check the next caller will not have.
	if err := script.ValidateObjectKey(key); err != nil {
		return "", fmt.Errorf("output %q cannot be delivered to key %q: %w", req.Name, key, err)
	}
	full := path.Join(req.Destination.Prefix, key)
	if err := script.ValidateObjectKey(full); err != nil {
		return "", fmt.Errorf("output %q cannot be delivered to key %q under the granted prefix: %w", req.Name, full, err)
	}
	return full, nil
}

// defaultKeySegment turns an output name into the key it lands on when the
// script names none. It keeps the characters an object key reads well with and
// replaces the rest, so a name written for people ("Weekly Sales") still
// produces a key a consuming system can address.
func defaultKeySegment(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "output"
	}
	return b.String()
}

// deliveredBytes reads the size the write tool reports, falling back to what was
// sent. The tool's number is what the bucket received; the fallback keeps the
// run record honest against a tool that answers in a shape this does not know.
func deliveredBytes(out map[string]any, sent int) int {
	size, ok := out["size"].(float64)
	if !ok || size <= 0 {
		return sent
	}
	return int(size)
}
