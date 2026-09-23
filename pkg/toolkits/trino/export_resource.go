package trino //nolint:revive // adapter types for cross-package wiring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// trino_export's managed-resource destination (#1663): the same formatted result
// this tool writes to a new portal asset, landed instead in the managed resource
// at a path, created the first time and re-versioned every time after.
//
// What that buys a recurring export is identity. A nightly "SELECT ... INTO a
// CSV" becomes one file with a version history rather than one asset per night,
// so the table registered over it, the dashboard referencing it, and the
// citation naming it all keep resolving without being re-pointed.

// landedResult is the formatted result a landing writes: the bytes, the type the
// formatter says they are, the tags the asset would have carried, and the row
// count the message reports.
type landedResult struct {
	body        []byte
	contentType string
	tags        []string
	rowCount    int
	// note is what a typed format could not keep of the query's columns
	// (#1833), said beside the landing the way an asset export says it.
	note string
}

// checkResourceDestination settles everything about a resource destination that
// can be settled before the query runs: that the deployment has a library to
// land in, that the call is not asking for asset-only behavior at the same time,
// and that the address is one this caller may write.
func checkResourceDestination(ctx context.Context, deps *ExportDeps, input exportInput) *mcp.CallToolResult {
	if deps.ResourceLander == nil {
		return exportError(resourceDestinationUnavailable)
	}
	if input.IdempotencyKey != "" {
		return exportError("idempotency_key cannot be used with a resource destination: it answers a repeat " +
			"call with the asset the first one made, and a resource destination already makes a repeat call the " +
			"next version of one file. Drop it.")
	}
	if input.CreatePublicLink {
		return exportError("create_public_link cannot be used with a resource destination: a public share link " +
			"is a portal asset's. Land the result in the resource, then save an asset that references it and " +
			"share that.")
	}
	if err := deps.ResourceLander.CheckResourceDestination(ctx, exportDestinationOf(input)); err != nil {
		return exportError(err.Error())
	}
	return nil
}

// exportDestinationOf builds the destination one call names, taking the file's
// labels from the metadata fields the tool already has: one name, one
// description and one tag set per call, rather than a second set that can
// disagree with them. The tags include the sensitivity tags inherited from the
// queried tables, so a file landed from restricted data carries the same
// markings the asset would have.
func exportDestinationOf(input exportInput) toolkit.ResourceDestination {
	dest := *input.Resource
	dest.DisplayName = input.Name
	dest.Description = input.Description
	dest.Tags = input.Tags
	// A library file is described or it is not searchable, and the description is
	// required at every other door into the library. Rather than refuse a call
	// for lacking one, the file is described by the query that produced it; a
	// caller's own description wins, and a later landing re-applies neither.
	if strings.TrimSpace(dest.Description) == "" {
		dest.Description = fmt.Sprintf("Exported by trino_export from: %s", input.SQL)
	}
	return dest
}

// landExport writes the formatted result into the destination and reports where
// it landed.
func (*Toolkit) landExport(
	ctx context.Context, deps *ExportDeps, input exportInput, res landedResult,
) (*exportOutput, *mcp.CallToolResult) {
	dest := exportDestinationOf(input)
	dest.Tags = res.tags
	landing, err := deps.ResourceLander.LandResource(ctx, dest, bytes.NewReader(res.body), res.contentType)
	if err != nil {
		return nil, exportError(err.Error())
	}
	return &exportOutput{
		Format:    input.Format,
		RowCount:  res.rowCount,
		SizeBytes: landing.SizeBytes,
		Resource:  landing,
		Message: strings.Join(nonEmpty(fmt.Sprintf("Exported %d rows as %s.", res.rowCount, input.Format),
			res.note, landing.Message), " "),
	}, nil
}

// resourceDestinationUnavailable is what a resource destination is told on a
// deployment with no managed-resource library. It names the missing piece and
// the destination that does work here, rather than reporting a generic failure.
const resourceDestinationUnavailable = "This deployment has no managed-resource library to land an export in: " +
	"it needs a database and an S3 connection for resource storage. Ask an administrator to configure one, or " +
	"drop the 'resource' argument to export to a portal asset instead. Nothing was written."

// resourceDestinationSchema renders the shared destination schema as the map this
// tool's schema is built from.
//
// It is unmarshalled from the one published description rather than restated as
// a map, so the four export tools cannot drift into describing the same
// destination differently. The source is a compile-time constant in this module,
// so a parse failure is impossible at run time and a panic here would be the
// only way to report one; the empty map a failure yields would instead publish
// the property with no shape, which TestExportInputSchema catches.
func resourceDestinationSchema() map[string]any {
	var out map[string]any
	_ = json.Unmarshal([]byte(toolkit.ResourceDestinationSchema), &out)
	return out
}

// nonEmpty drops the empty sentences from a message's parts, so a message
// joined from them carries no doubled space where a part said nothing.
func nonEmpty(parts ...string) []string {
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
