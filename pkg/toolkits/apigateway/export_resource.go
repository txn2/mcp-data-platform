package apigateway //nolint:revive // adapter types for cross-package wiring

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// api_export's managed-resource destination (#1663): the same upstream response
// this tool streams into a new portal asset, landed instead in the managed
// resource at a path, created the first time and re-versioned every time after.
//
// Nothing in this path holds the response whole. The body streams from the
// upstream connection into the resource's object exactly as it streams into an
// asset's, which is what makes a 200 MB nightly CSV an ordinary call rather
// than something that has to be cut up to fit through a model or a script.

// exportDestinationOf builds the destination one call names, taking the file's
// labels from the metadata fields the tool already has: a resource export has
// one name, one description and one tag set, not a second set that can disagree
// with them.
func exportDestinationOf(in exportInput) toolkit.ResourceDestination {
	dest := *in.Resource
	dest.DisplayName = in.Name
	dest.Description = in.Description
	dest.Tags = in.Tags
	// A library file is described or it is not searchable, and the description
	// is required at every other door into the library. Rather than refuse a
	// call for lacking one, the file is described by where it came from, which
	// is both true and the thing a person finding it later needs to know. A
	// caller's own description wins, and neither is re-applied by a later
	// landing: the description belongs to the file, not to the run that
	// refreshed it.
	if strings.TrimSpace(dest.Description) == "" {
		method, _ := validateMethod(in.Method)
		dest.Description = fmt.Sprintf("Exported by api_export from the %q connection: %s %s.",
			in.Connection, method, in.Path)
	}
	return dest
}

// checkResourceDestination settles everything about a resource destination that
// can be settled before the upstream is called: that the deployment has one to
// land in, that the call is not asking for asset-only behavior at the same time,
// and that the address is one this caller may write.
//
// It runs before the request for the reason the two-call lander exists: an
// export can be a POST, and a POST whose result has nowhere to go should not
// have been sent.
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
			"share link is a portal asset's. Land the response in the resource, then save an asset that " +
			"references it and share that.")
	}
	if err := deps.ResourceLander.CheckResourceDestination(ctx, exportDestinationOf(in)); err != nil {
		return toolkit.ErrorResult(err.Error())
	}
	return nil
}

// refuseUnsuccessfulLanding refuses to land a response the upstream did not
// answer successfully.
//
// An asset destination keeps a failed response, and should: it is a new file
// every time, the status is in the result, and a recorded error body is
// evidence. A resource destination is the opposite situation. The address has
// readers -- an asset referencing the file, a table registered over it, a
// citation naming it -- so landing an error page there would publish
// "<html>Service Unavailable</html>" as this week's dataset and move a
// registered table onto it. The previous version stays the head instead, and
// the call says what the upstream answered.
func refuseUnsuccessfulLanding(status int, dest toolkit.ResourceDestination) error {
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return nil
	}
	return fmt.Errorf("upstream answered %d %s, and a managed-resource destination lands only a successful "+
		"response: %s/%s is unchanged. Call api_invoke_endpoint to read what the upstream said about the "+
		"failure, or export to a portal asset if you want the failed response kept as a file",
		status, http.StatusText(status), dest.Path, dest.Filename)
}

// upstreamAnswer is the response a landing streams: the body, the type it is
// stored under after detection, and the status the upstream returned.
type upstreamAnswer struct {
	body        io.Reader
	contentType string
	status      int
}

// landExport streams the response into the destination and reports where it
// landed.
func landExport(ctx context.Context, deps *ExportDeps, in exportInput, answer upstreamAnswer) (*exportOutput, error) {
	dest := exportDestinationOf(in)
	landing, err := deps.ResourceLander.LandResource(ctx, dest, answer.body, answer.contentType)
	if err != nil {
		return nil, err //nolint:wrapcheck // the lander's sentence is written for whoever made the call
	}
	method, _ := validateMethod(in.Method)
	return &exportOutput{
		ContentType: landing.ContentType,
		Status:      answer.status,
		SizeBytes:   landing.SizeBytes,
		Resource:    landing,
		Message: fmt.Sprintf("Exported %d bytes from %s %s. %s",
			landing.SizeBytes, method, in.Path, landing.Message),
	}, nil
}

// resourceDestinationUnavailable is what a resource destination is told on a
// deployment with no managed-resource library. It names the missing piece and
// the destination that does work here, rather than reporting a generic failure.
const resourceDestinationUnavailable = "This deployment has no managed-resource library to land an export in: " +
	"it needs a database and an S3 connection for resource storage. Ask an administrator to configure one, or " +
	"drop the 'resource' argument to export to a portal asset instead. Nothing was written."
