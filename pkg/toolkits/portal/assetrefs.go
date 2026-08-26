package portal

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Result keys the reference declaration adds to a write's response.
const (
	fieldReferencesDeclared = "references_declared"
	fieldReferenceGrant     = "reference_grant"
)

// resolveRefs validates a write's declared references without recording
// anything, so a save that names something its author cannot read is refused
// before an asset exists to carry the refusal.
//
// A nil uris means the write never mentioned references and has decided nothing
// about them: it returns present=false and the caller leaves the asset's
// existing references alone. An empty (but non-nil) list is a decision -- it
// clears them.
//
// assetID is the asset being written, empty on a create, and is what lets a
// reference to the asset itself be refused before anything is stored.
//
// It returns the refusal as a tool result rather than as an error, the shape
// readAssetText already uses: the message an author reads is a complete
// sentence naming the URI they wrote, and every wrapping prefix on the way out
// would push that sentence further from the start of what they see.
func (t *Toolkit) resolveRefs(
	ctx context.Context, uris []string, assetID string,
) (declared []assetrefs.Declared, present bool, errResult *mcp.CallToolResult) {
	if uris == nil {
		return nil, false, nil
	}
	declared, err := t.contentRefs.Resolve(ctx, uris, t.refAuthor(ctx), assetID)
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
	return toolkit.ErrorResult("could not check the declared references: " + err.Error())
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
	refs, err := t.contentRefs.Apply(ctx, assetID, declared, resolveOwnerEmail(ctx))
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
	fields[fieldReferencesDeclared] = count
	if count > 0 {
		// The grant is stated at the moment it is made, in the terms it
		// matters in, rather than left for the author to infer from the fact
		// that the reference resolved.
		fields[fieldReferenceGrant] = assetrefs.GrantNotice
	}
}

// refAuthor builds the identity one declaration is checked against: the
// caller's managed-resource claims for an mcp:// URI, and the toolkit's own
// asset read gate for an mcp:asset:<id> reference (#1488).
//
// The asset arm is canReadAsset, the same gate every read this toolkit makes
// passes through, so an agent can reference exactly the assets it could open
// -- and a managed-script run can reference what its author OWNS, since shares
// are not inherited by a run.
func (t *Toolkit) refAuthor(ctx context.Context) assetrefs.Author {
	return assetrefs.Author{
		Claims: refClaims(ctx),
		ReadsAsset: func(ctx context.Context, asset *portal.Asset) bool {
			return t.canReadAsset(ctx, asset)
		},
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
// address (#1419), and both are passed: the address is what makes the run the
// author for the rules that turn on "is this you?", since the run's own
// principal owns no file and is in nobody's library. A script therefore
// declares, creates and replaces exactly what its author could (#1487).
func refClaims(ctx context.Context) resource.Claims {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return resource.Claims{}
	}
	return resource.BuildClaims(pc.UserID, pc.UserEmail, pc.PersonaName, pc.Roles, pc.IsAdmin).
		ActingFor(pc.OnBehalfOfEmail)
}
