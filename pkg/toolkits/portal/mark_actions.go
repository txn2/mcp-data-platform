package portal

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/assetstatus"
	"github.com/txn2/mcp-data-platform/internal/portal/assetverify"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// The manage_asset actions that put and remove the Verified badge (#2503).
const (
	actionVerify   = "verify"
	actionUnverify = "unverify"
)

// Refusals the status and verification arguments answer with.
const (
	errMarksUnavailable = "asset status and verification are not available on this deployment"
	errVerifyPermission = "verifying an asset needs the Can verify assets permission (verify_assets), which an " +
		"administrator grants in the persona editor; administrators may verify any asset they can open"
	errUnverifyPermission = "only the person who verified the asset, a holder of the Can verify assets permission " +
		"or an administrator can remove its verification"
	errAssetIDRequired = "asset_id is required for the %s action"
	verifyHint         = "This is a permission, not an outage: ask an administrator."
)

// Marks are the stores an asset's status and Verified badge are written
// through (#2502, #2503), and whether a persona carries verify_assets.
type Marks struct {
	Status assetstatus.Store
	Verify assetverify.Store
	// VerifyAllowed reports whether a persona lets its people verify assets.
	// Nil allows none: only administrators verify.
	VerifyAllowed func(persona string) bool
}

// SetMarks wires the status and verification stores.
func (t *Toolkit) SetMarks(m Marks) { t.marks = m }

// statusArgs are the status arguments save_asset and manage_asset update take.
type statusArgs struct {
	// Status is draft, published or deprecated; "none" clears it (update
	// only). Empty leaves it alone.
	Status string `json:"status,omitempty"`
	// StatusNote is shown with the status; a deprecated asset shows it to its
	// readers.
	StatusNote string `json:"status_note,omitempty"`
}

// checkStatus refuses an unknown status before anything is written.
// allowNone admits "none", which clears it.
func checkStatus(status string, allowNone bool) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case s == "", portaldomain.ValidStatus(s):
		return ""
	case allowNone && s == portaldomain.StatusNone:
		return ""
	}
	return "status must be draft, published or deprecated" + map[bool]string{true: ", or none to clear it", false: ""}[allowNone]
}

// applyStatus writes the status the arguments name, or clears it for "none".
// No status writes nothing.
func (t *Toolkit) applyStatus(ctx context.Context, assetID string, args statusArgs) *mcp.CallToolResult {
	status := strings.ToLower(strings.TrimSpace(args.Status))
	if status == "" {
		return nil
	}
	if t.marks.Status == nil {
		return toolkit.ErrorResult(errMarksUnavailable)
	}
	actor := callerActor(ctx)
	var err error
	if status == portaldomain.StatusNone {
		_, err = t.marks.Status.Clear(ctx, assetID, actor)
	} else {
		err = t.marks.Status.Set(ctx, assetID, assetstatus.Change{Status: status, Note: args.StatusNote, Actor: actor})
	}
	switch {
	case errors.Is(err, assetstatus.ErrInvalid):
		return toolkit.ErrorResult(err.Error())
	case err != nil:
		return toolkit.ErrorResult("failed to set the status: " + err.Error())
	}
	return nil
}

// callerActor is the identity a change is recorded under.
// resolveOwnerEmail answers "anonymous" for a caller with no address, so the
// id is read off the platform context itself.
func callerActor(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	switch {
	case pc != nil && pc.UserEmail != "":
		return pc.UserEmail
	case pc != nil && pc.UserID != "":
		return pc.UserID
	}
	return anonymousUserName
}

// canVerify reports whether the caller may verify assets: an administrator,
// or a persona carrying verify_assets.
func (t *Toolkit) canVerify(ctx context.Context) bool {
	if t.isAdmin(ctx) {
		return true
	}
	pc := middleware.GetPlatformContext(ctx)
	return pc != nil && t.marks.VerifyAllowed != nil && t.marks.VerifyAllowed(pc.PersonaName)
}

// handleVerify marks an asset Verified at its current version.
func (t *Toolkit) handleVerify(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return toolkit.ErrorResult(strings.Replace(errAssetIDRequired, "%s", actionVerify, 1)), nil, nil
	}
	if t.marks.Verify == nil {
		return toolkit.ErrorResult(errMarksUnavailable), nil, nil
	}
	asset, refused := t.loadReadableAsset(ctx, input.AssetID)
	if refused != nil {
		return refused, nil, nil
	}
	if !t.canVerify(ctx) {
		return middleware.UnauthorizedResult(errVerifyPermission, verifyHint), nil, nil
	}
	expires, err := parseExpiry(input.ExpiresAt)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	err = t.marks.Verify.Verify(ctx, asset.ID, assetverify.Verification{
		Actor: callerActor(ctx), Note: input.Note, Version: asset.CurrentVersion, ExpiresAt: expires,
	})
	if err != nil {
		return toolkit.ErrorResult("failed to verify the asset: " + err.Error()), nil, nil
	}
	return t.markedAsset(ctx, asset.ID)
}

// handleUnverify removes an asset's Verified badge.
func (t *Toolkit) handleUnverify(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return toolkit.ErrorResult(strings.Replace(errAssetIDRequired, "%s", actionUnverify, 1)), nil, nil
	}
	if t.marks.Verify == nil {
		return toolkit.ErrorResult(errMarksUnavailable), nil, nil
	}
	asset, refused := t.loadReadableAsset(ctx, input.AssetID)
	if refused != nil {
		return refused, nil, nil
	}
	if !t.canVerify(ctx) {
		by, err := t.marks.Verify.VerifiedBy(ctx, asset.ID)
		actor := callerActor(ctx)
		if err != nil || by == "" || actor == anonymousUserName || !strings.EqualFold(by, actor) {
			return middleware.UnauthorizedResult(errUnverifyPermission, verifyHint), nil, nil
		}
	}
	if _, err := t.marks.Verify.Unverify(ctx, asset.ID, callerActor(ctx)); err != nil {
		return toolkit.ErrorResult("failed to remove the verification: " + err.Error()), nil, nil
	}
	return t.markedAsset(ctx, asset.ID)
}

// markedAsset answers with the asset as it now reads.
func (t *Toolkit) markedAsset(ctx context.Context, id string) (*mcp.CallToolResult, any, error) {
	asset, err := t.assetStore.Get(ctx, id)
	if err != nil {
		return toolkit.ErrorResult("failed to read the asset: " + err.Error()), nil, nil
	}
	asset.Provenance = portaldomain.BoundedProvenance(asset.Provenance, portaldomain.ProvenanceCapturesInline)
	return toolkit.JSONResultTyped(asset)
}

// parseExpiry reads a verification expiry: an RFC 3339 time, or a date, which
// expires at the end of that day in UTC. Empty never expires.
func parseExpiry(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil //nolint:nilnil // no expiry is a valid answer
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil, errors.New("expires_at must be a date (2026-12-31) or an RFC 3339 time")
	}
	end := d.Add(24*time.Hour - time.Second)
	return &end, nil
}
