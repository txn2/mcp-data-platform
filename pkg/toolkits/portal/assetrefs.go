package portal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/contentrefs"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Result keys the reference declaration adds to a write's response.
const (
	fieldReferencesDeclared   = "references_declared"
	fieldReferenceGrant       = "reference_grant"
	fieldUndeclaredReferences = "undeclared_references"
)

// linkRefusal refuses a body that uses a reference as a link target (#1875),
// naming each one, or returns nil when it uses none.
//
// A reference loads another file's content into the document: the serving
// rewrite turns it into a URL for the target's bytes, which an <img>, a
// stylesheet or a fetch can use and a reader cannot follow, since the frame a
// document renders in blocks navigation. Stored as a link it goes nowhere, so
// the write is refused before anything is written rather than accepted.
func linkRefusal(body, contentType string) *mcp.CallToolResult {
	linked := contentrefs.Linked(body, contentType)
	if len(linked) == 0 {
		return nil
	}
	return toolkit.ErrorResult(fmt.Sprintf(
		"links between assets are not supported, and this content uses %s as a link target "+
			"(an <a href> or a Markdown [text](...) link). A reference loads another file's content into "+
			"this document, as an <img src>, a stylesheet or a fetch; it cannot be followed. "+
			"Reference the asset to load its content, or name it in text. Nothing was written.",
		strings.Join(linked, ", ")))
}

// undeclaredRefs returns the references body names that the asset does not
// declare once this write is done (#1875): what the write passed in
// references, or, when it passed none, what the asset already declares.
//
// It reports and never refuses. A failure to read the existing declarations
// is logged and reports nothing, because naming a reference undeclared that
// is in fact declared would send the author to fix what is not broken.
func (t *Toolkit) undeclaredRefs(ctx context.Context, assetID, body string, passed []string) []string {
	declared := passed
	if declared == nil && assetID != "" {
		existing, err := t.contentRefs.DeclaredURIs(ctx, assetID)
		if err != nil {
			slog.Warn("undeclared references not checked",
				"asset_id", logsan.SanitizeForLog(assetID), "error", logsan.SanitizeForLog(err.Error()))
			return nil
		}
		declared = existing
	}
	return contentrefs.Undeclared(body, declared)
}

// undeclaredNotice is the sentence a write's message carries about the
// references its content names undeclared, in the run log's words; empty when
// there are none.
func undeclaredNotice(undeclared []string) string {
	if len(undeclared) == 0 {
		return ""
	}
	return fmt.Sprintf(" The content names %s, which references does not declare, so %s. "+
		"Add each one the content loads to references.",
		strings.Join(undeclared, ", "), contentrefs.UndeclaredConsequence)
}

// addUndeclaredFields reports undeclared references on a map-shaped result:
// the list, and the notice appended to the message.
func addUndeclaredFields(result map[string]any, undeclared []string) {
	if len(undeclared) == 0 {
		return
	}
	result[fieldUndeclaredReferences] = undeclared
	if msg, ok := result[fieldMessage].(string); ok {
		result[fieldMessage] = msg + undeclaredNotice(undeclared)
	}
}

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
		ActingFor(pc.OnBehalfOfEmail, pc.OnBehalfOfSub)
}
