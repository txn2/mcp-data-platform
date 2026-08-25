package portal

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Result keys the reference declaration adds to a write's response.
const (
	fieldResourcesReferenced = "resources_referenced"
	fieldResourceGrant       = "resource_grant"
)

// resolveRefs validates a write's declared resource URIs without recording
// anything, so a save that names a resource its author cannot read is refused
// before an asset exists to carry the refusal.
//
// A nil uris means the write never mentioned resources and has decided nothing
// about them: it returns present=false and the caller leaves the asset's
// existing references alone. An empty (but non-nil) list is a decision -- it
// clears them.
//
// It returns the refusal as a tool result rather than as an error, the shape
// readAssetText already uses: the message an author reads is a complete
// sentence naming the URI they wrote, and every wrapping prefix on the way out
// would push that sentence further from the start of what they see.
func (t *Toolkit) resolveRefs(
	ctx context.Context, uris []string,
) (declared []assetrefs.Declared, present bool, errResult *mcp.CallToolResult) {
	if uris == nil {
		return nil, false, nil
	}
	declared, err := t.resourceRefs.Resolve(ctx, uris, refClaims(ctx))
	if err != nil {
		return nil, false, refResult(err)
	}
	return declared, true, nil
}

// refResult turns a declaration failure into the result the agent sees.
//
// The two cases are told apart because they are the author's to act on in
// different ways: a refusal is a decision about what they declared and states
// what to change, while anything else is the platform failing to check and is
// not theirs to fix. Reporting a storage fault in the words of a permission
// decision would send an author looking for a permission they already hold.
func refResult(err error) *mcp.CallToolResult {
	if errors.Is(err, assetrefs.ErrRefused) {
		return toolkit.ErrorResult(err.Error())
	}
	return toolkit.ErrorResult("could not check the declared resource references: " + err.Error())
}

// applyRefs records a validated declaration against the asset and reports how
// many references it now has. It is called after the content write, so a write
// that fails leaves the references describing the content that is actually
// stored.
//
// A write that declared nothing reports -1, which callers read as "this write
// said nothing about references" and leave out of their response entirely.
func (t *Toolkit) applyRefs(
	ctx context.Context, assetID string, declared []assetrefs.Declared, present bool,
) (int, *mcp.CallToolResult) {
	if !present {
		return -1, nil
	}
	refs, err := t.resourceRefs.Apply(ctx, assetID, declared, resolveOwnerEmail(ctx))
	if err != nil {
		return 0, refResult(err)
	}
	return len(refs), nil
}

// addRefFields reports a completed declaration on a map-shaped tool response.
// A count below zero means the write said nothing about references, and adds
// nothing.
func addRefFields(fields map[string]any, count int) {
	if count < 0 {
		return
	}
	fields[fieldResourcesReferenced] = count
	if count > 0 {
		// The grant is stated at the moment it is made, in the terms it
		// matters in, rather than left for the author to infer from the fact
		// that the reference resolved.
		fields[fieldResourceGrant] = assetrefs.GrantNotice
	}
}

// refClaims builds the resource-permission claims a declaration is checked
// against, from the identity the tool call carries.
//
// It is resource.BuildClaims over the PlatformContext, the same derivation the
// resources middleware and prompt attachment serving use, so "may this author
// read this file?" cannot come to mean two things. A call with no platform
// context resolves as nobody, which reaches only global resources -- the set an
// unauthenticated reader already sees.
//
// For a managed-script run the context carries the version author's roles and
// address (#1419), so a script declares exactly what its author could declare.
func refClaims(ctx context.Context) resource.Claims {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return resource.Claims{}
	}
	return resource.BuildClaims(pc.UserID, pc.UserEmail, pc.PersonaName, pc.Roles, pc.IsAdmin)
}
