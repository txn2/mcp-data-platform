package graphql

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// graphql_export's managed-resource destination (#1663): the same result this
// tool writes to a new portal asset, landed instead in the managed resource at a
// path, created the first time and re-versioned every time after.
//
// A GraphQL endpoint is the shape of upstream this matters most for: one
// document, run on a schedule, whose answer is the current state of something.
// Landed at a path it is one file with a history, so what reads it, references
// it, or is registered over it does not have to be re-pointed every run.

// checkResourceDestination settles everything about a resource destination that
// can be settled before the document is sent: that the deployment has a library
// to land in, that the call is not asking for asset-only behavior at the same
// time, and that the address is one this caller may write.
func checkResourceDestination(ctx context.Context, deps *ExportDeps, in exportInput) *mcp.CallToolResult {
	if deps.ResourceLander == nil {
		return toolkit.ErrorResult(resourceDestinationUnavailable)
	}
	if in.IdempotencyKey != "" {
		return toolkit.ErrorResult("idempotency_key cannot be used with a resource destination: it answers a " +
			"repeat call with the asset the first one made, and a resource destination already makes a repeat " +
			"call the next version of one file. Drop it.")
	}
	if in.CreatePublicLink {
		return toolkit.ErrorResult("create_public_link cannot be used with a resource destination: a public " +
			"share link is a portal asset's. Land the result in the resource, then save an asset that " +
			"references it and share that.")
	}
	if err := deps.ResourceLander.CheckResourceDestination(ctx, exportDestinationOf(in)); err != nil {
		return toolkit.ErrorResult(err.Error())
	}
	return nil
}

// exportDestinationOf builds the destination one call names, taking the file's
// labels from the metadata fields the tool already has: one name, one
// description and one tag set per call, rather than a second set that can
// disagree with them.
func exportDestinationOf(in exportInput) toolkit.ResourceDestination {
	dest := *in.Resource
	dest.DisplayName = in.Name
	dest.Description = in.Description
	dest.Tags = in.Tags
	// A library file is described or it is not searchable, and the description is
	// required at every other door into the library. Rather than refuse a call
	// for lacking one, the file is described by where it came from; a caller's own
	// description wins, and a later landing re-applies neither.
	if strings.TrimSpace(dest.Description) == "" {
		dest.Description = fmt.Sprintf("Exported by graphql_export from the %q connection.", in.Connection)
	}
	return dest
}

// landExport writes the result into the destination and reports where it landed,
// alongside what the document itself did: the operations it invoked, the errors
// the endpoint returned in the body, and the pages walked.
func landExport(
	ctx context.Context, deps *ExportDeps, in exportInput, payload []byte, result *QueryOutput,
) (*exportOutput, error) {
	landing, err := deps.ResourceLander.LandResource(ctx, exportDestinationOf(in),
		bytes.NewReader(payload), exportContentType)
	if err != nil {
		return nil, err //nolint:wrapcheck // the lander's sentence is written for whoever made the call
	}
	return &exportOutput{
		ContentType: landing.ContentType,
		SizeBytes:   landing.SizeBytes,
		Operations:  result.Operations,
		Errors:      result.Errors,
		Pagination:  result.Pagination,
		Resource:    landing,
		Message: fmt.Sprintf("Exported %d bytes from connection %s. %s",
			landing.SizeBytes, in.Connection, landing.Message),
	}, nil
}

// resourceDestinationUnavailable is what a resource destination is told on a
// deployment with no managed-resource library. It names the missing piece and
// the destination that does work here, rather than reporting a generic failure.
const resourceDestinationUnavailable = "This deployment has no managed-resource library to land an export in: " +
	"it needs a database and an S3 connection for resource storage. Ask an administrator to configure one, or " +
	"drop the 'resource' argument to export to a portal asset instead. Nothing was written."
