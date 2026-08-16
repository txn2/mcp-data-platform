// Package access holds the portal's authorization core: one checker that
// answers every "may this user see or change this thing" question the portal
// asks, over assets, collections, prompts, knowledge pages and threads.
//
// Before this package the answers were methods scattered across the handler
// files, reachable only from *Handler. Splitting the handler layer into seams
// made that untenable: a seam would have had to receive each check as an
// injected func, which is not a seam but a second copy of the handler's
// dependency graph. The checks are also the portal's security boundary, so a
// single implementation both the parent and the seams call is what keeps a
// permission from quietly meaning two things in two places.
//
// Every method is pure with respect to HTTP: it answers the question and never
// writes a response. Callers that must deny with a specific status keep their
// own thin wrapper, so the status and message wording stay with the route.
package access

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Config carries the stores and identity settings the checks read. Every field
// is optional in the sense that a nil store disables the checks that need it
// (they deny rather than panic), which is what a portal deployed without
// collections or prompts requires.
type Config struct {
	Assets      portaldomain.AssetStore
	Collections portaldomain.CollectionStore
	Shares      portaldomain.ShareStore
	Prompts     prompt.Store
	// AdminRoles are the roles that grant admin access in the portal.
	AdminRoles []string
	// PersonaTools resolves a user's roles to the tool names their persona
	// grants. nil means no persona resolution is wired, in which case only
	// the admin arm of the capability checks can grant.
	PersonaTools func(roles []string) []string
}

// Checker answers the portal's authorization questions.
type Checker struct {
	cfg Config
}

// New returns a Checker over cfg.
func New(cfg Config) *Checker { return &Checker{cfg: cfg} }

// IsAdmin reports whether the user holds one of the portal's admin roles.
func (c *Checker) IsAdmin(user *User) bool {
	if user == nil {
		return false
	}
	return HasAnyRole(user.Roles, c.cfg.AdminRoles)
}

// CanManage reports whether the user holds owner authority over a thing owned
// by ownerID: the owner, or an admin.
//
// It is the single seam behind every "only the owner can ..." gate the portal
// applies to assets and collections, so admin reach is decided once instead of
// being re-derived at each route. Admin only ever widens here, and never grants
// more than the admin already has: an admin reads, edits and deletes any asset
// through the admin API, so refusing them the weaker rights to share it, list
// its shares, or revoke one was an artifact of the gate being written as a bare
// ID comparison rather than a decision (#1293).
func (c *Checker) CanManage(ownerID string, user *User) bool {
	if user == nil {
		return false
	}
	return ownerID == user.UserID || c.IsAdmin(user)
}

// CanManageEmail is CanManage for the entities whose ownership is recorded as
// an email address rather than a user ID (prompts). The address comparison is
// case-insensitive, matching OwnsPersonalPrompt and the share-recipient match,
// because addresses reach the platform from several identity providers.
func (c *Checker) CanManageEmail(ownerEmail string, user *User) bool {
	if user == nil {
		return false
	}
	if ownerEmail != "" && strings.EqualFold(ownerEmail, user.Email) {
		return true
	}
	return c.IsAdmin(user)
}

// HasTool reports whether the user's resolved persona grants the named tool,
// or the user is an admin. It is the shared capability check behind the
// apply_knowledge and DataHub write authorizations; the admin arm only widens
// access (a separate write-enabled-connection check still applies to DataHub
// writes, so admin cannot mutate a read-only connection).
func (c *Checker) HasTool(user *User, tool string) bool {
	if user == nil {
		return false
	}
	if c.cfg.PersonaTools != nil && slices.Contains(c.cfg.PersonaTools(user.Roles), tool) {
		return true
	}
	return c.IsAdmin(user)
}

// ApplyKnowledgeTool is the persona tool whose access gates insight review and
// canonical-knowledge writes. It is the single capability the REST path checks,
// matching the MCP path's persona tool-visibility gate so both agree on who may
// promote knowledge.
const ApplyKnowledgeTool = "apply_knowledge"

// HasApplyKnowledge reports whether the user holds the apply_knowledge
// capability. It grants when the user's resolved persona lists the tool (the
// same Tools the frontend reads from GET /me and the MCP path gates on, so a
// non-admin persona granted apply_knowledge can review and promote), OR when
// the user is an admin.
//
// Admins are always treated as holding the capability for two reasons: their
// persona normally grants every registered tool, and the tool may not be
// registered at all on a given deployment (apply_knowledge is absent when
// Knowledge.Apply.Enabled is false, its default), in which case the resolved
// Tools list can never contain it. Without the admin arm, enabling capability
// gating would lock admins out of knowledge writes wherever apply is disabled,
// a regression from the prior admin-role gate. The admin arm only widens access;
// the capability still grants non-admins, which is the behavior #661 requires.
func (c *Checker) HasApplyKnowledge(user *User) bool {
	return c.HasTool(user, ApplyKnowledgeTool)
}

// HasAnyRole returns true if any role in userRoles is also in targetRoles. It
// is exported for the few callers that hold a role list rather than a Checker
// (the prompt mutation gate takes its admin roles as an argument).
func HasAnyRole(userRoles, targetRoles []string) bool {
	for _, r := range userRoles {
		if slices.Contains(targetRoles, r) {
			return true
		}
	}
	return false
}

// IsShareActive returns true if the share is not revoked and not expired.
func IsShareActive(s portaldomain.Share) bool {
	if s.Revoked {
		return false
	}
	return s.ExpiresAt == nil || !s.ExpiresAt.Before(time.Now())
}

// GrantsEdit reports whether a resolved share permission carries edit rights.
// It is the one place the Editor level is compared, so a caller that already
// holds a resolved permission — getCollection reports one alongside the record
// it is answering with — applies the same rule CanEditCollection applies,
// without repeating the comparison or paying for a second lookup.
func GrantsEdit(perm portaldomain.SharePermission) bool {
	return perm == portaldomain.PermissionEditor
}

// permissionRank orders share permissions so the highest grant wins when a user
// holds access through more than one path (editor > viewer > none).
func permissionRank(p portaldomain.SharePermission) int {
	switch p {
	case portaldomain.PermissionEditor:
		return 2
	case portaldomain.PermissionViewer:
		return 1
	default:
		return 0
	}
}

// AssetSharePermission returns the highest permission level a user has for a
// shared asset. It returns the empty permission if the asset is not shared with
// this user.
func (c *Checker) AssetSharePermission(ctx context.Context, assetID string, user *User) (portaldomain.SharePermission, error) {
	shares, err := c.cfg.Shares.ListByAsset(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("checking share permission: %w", err)
	}
	var best portaldomain.SharePermission
	for _, s := range shares {
		if !IsShareActive(s) {
			continue
		}
		matched := s.SharedWithUserID == user.UserID ||
			(user.Email != "" && strings.EqualFold(s.SharedWithEmail, user.Email))
		if !matched {
			continue
		}
		if s.Permission == portaldomain.PermissionEditor {
			return portaldomain.PermissionEditor, nil // highest possible, short-circuit
		}
		if best == "" {
			best = s.Permission
		}
	}
	return best, nil
}

// ResolveAssetPermission returns the highest permission a non-owner user holds
// for an asset, combining a direct share (AssetSharePermission) with a
// collection share (GetUserAssetPermissionViaCollection). A direct editor
// short-circuits the collection lookup because editor is the ceiling. The
// returned error is the direct-share store error (nil on success); a
// collection-lookup error is treated as no collection access, matching the
// best-effort cascade the view checks use. Owner access must be checked by the
// caller, which treats a non-nil error as 500.
//
// This is for callers that need the effective permission value (getAsset
// reports it as SharePermission; the edit check compares it to editor). The
// bool view checks (CanViewAsset) deliberately do not use it: they only need
// "any grant" and short-circuit on a direct share to avoid a collection query
// per asset.
func (c *Checker) ResolveAssetPermission(ctx context.Context, assetID string, user *User) (portaldomain.SharePermission, error) {
	direct, err := c.AssetSharePermission(ctx, assetID, user)
	if err == nil && direct == portaldomain.PermissionEditor {
		return direct, nil // editor is the ceiling; no need to consult the cascade
	}
	collPerm, _ := c.cfg.Shares.GetUserAssetPermissionViaCollection(ctx, assetID, user.UserID, user.Email)
	best := direct
	if permissionRank(collPerm) > permissionRank(best) {
		best = collPerm
	}
	return best, err
}

// CanViewAsset reports whether the user may view the asset (owner, a direct
// share, or a collection share). A direct-share store error is tolerated: a
// collection grant still allows access. It short-circuits on a direct grant to
// avoid a collection query on the hot path, where callers resolve many assets
// in a loop.
func (c *Checker) CanViewAsset(ctx context.Context, assetID string, asset *portaldomain.Asset, user *User) bool {
	if asset.OwnerID == user.UserID {
		return true
	}
	if perm, err := c.AssetSharePermission(ctx, assetID, user); err == nil && perm != "" {
		return true
	}
	collPerm, _ := c.cfg.Shares.GetUserAssetPermissionViaCollection(ctx, assetID, user.UserID, user.Email)
	return collPerm != ""
}

// AssetViewGrant reports whether the user may view the asset, distinguishing a
// denial from a failure to determine one. A non-nil error means the direct-share
// lookup failed and no collection grant covered for it, which callers surface as
// 500 rather than 403.
func (c *Checker) AssetViewGrant(ctx context.Context, assetID string, asset *portaldomain.Asset, user *User) (bool, error) {
	if asset.OwnerID == user.UserID {
		return true, nil
	}
	perm, err := c.AssetSharePermission(ctx, assetID, user)
	if err != nil {
		return false, err
	}
	if perm != "" {
		return true, nil
	}
	collPerm, _ := c.cfg.Shares.GetUserAssetPermissionViaCollection(ctx, assetID, user.UserID, user.Email)
	return collPerm != "", nil
}

// CanEditAssetSilent reports owner-or-admin-or-editor access to an asset. A
// missing or soft-deleted asset denies.
func (c *Checker) CanEditAssetSilent(ctx context.Context, assetID string, user *User) bool {
	asset, err := c.cfg.Assets.Get(ctx, assetID)
	if err != nil || asset.DeletedAt != nil {
		return false
	}
	if c.CanManage(asset.OwnerID, user) {
		return true
	}
	perm, _ := c.AssetSharePermission(ctx, assetID, user)
	return GrantsEdit(perm)
}

// CollectionSharePermission returns the highest share permission for a user on
// a collection. A store error, or no share store at all, yields the empty
// permission: a deployment without shares grants none rather than panicking on
// the write gates that now consult it.
func (c *Checker) CollectionSharePermission(ctx context.Context, collectionID string, user *User) portaldomain.SharePermission {
	if c.cfg.Shares == nil || user == nil {
		return ""
	}
	perm, err := c.cfg.Shares.GetUserCollectionPermission(ctx, collectionID, user.UserID, user.Email)
	if err != nil {
		return ""
	}
	return perm
}

// CanViewCollection reports whether the user may view the collection (owner or
// any share).
func (c *Checker) CanViewCollection(ctx context.Context, coll *portaldomain.Collection, user *User) bool {
	if coll.OwnerID == user.UserID {
		return true
	}
	return c.CollectionSharePermission(ctx, coll.ID, user) != ""
}

// CanEditCollection reports whether the user may change the collection itself:
// its name, description, settings, sections and thumbnail. It grants to the
// owner, an admin, and the holder of an Editor share on the collection.
//
// The Editor arm is what makes an Editor share on a collection mean anything
// about the collection rather than only about the assets inside it (#1294): a
// person trusted to rewrite the contents of every asset in a collection is not
// a plausible person to refuse a fix to the collection's own title. Deleting a
// collection, sharing it, and reading its share list stay on CanManage —
// destruction and re-granting access are owner rights, not editing rights, so
// an Editor deliberately holds neither.
func (c *Checker) CanEditCollection(ctx context.Context, coll *portaldomain.Collection, user *User) bool {
	if user == nil {
		return false
	}
	if c.CanManage(coll.OwnerID, user) {
		return true
	}
	return GrantsEdit(c.CollectionSharePermission(ctx, coll.ID, user))
}

// CanEditCollectionSilent is CanEditCollection for callers that hold only an
// ID. A missing or soft-deleted collection denies.
func (c *Checker) CanEditCollectionSilent(ctx context.Context, collectionID string, user *User) bool {
	if c.cfg.Collections == nil {
		return false
	}
	coll, err := c.cfg.Collections.Get(ctx, collectionID)
	if err != nil || coll.DeletedAt != nil {
		return false
	}
	return c.CanEditCollection(ctx, coll, user)
}

// CanViewPrompt reports whether the user can see the prompt: global prompts
// are visible to all; personal prompts to their owner, admins, or share grantees.
func (c *Checker) CanViewPrompt(ctx context.Context, user *User, pr *prompt.Prompt) bool {
	if pr.Scope != prompt.ScopePersonal {
		return true
	}
	if c.IsAdmin(user) || (pr.OwnerEmail != "" && strings.EqualFold(pr.OwnerEmail, user.Email)) {
		return true
	}
	refs, err := c.cfg.Shares.ListSharedPromptsWithUser(ctx, user.UserID, user.Email)
	if err != nil {
		return false
	}
	for _, ref := range refs {
		if ref.PromptID == pr.ID {
			return true
		}
	}
	return false
}

// OwnsPersonalPrompt reports whether the user owns the given personal prompt.
func (c *Checker) OwnsPersonalPrompt(ctx context.Context, promptID string, user *User) bool {
	if c.cfg.Prompts == nil {
		return false
	}
	pr, err := c.cfg.Prompts.GetByID(ctx, promptID)
	return err == nil && pr != nil && pr.Scope == prompt.ScopePersonal &&
		pr.OwnerEmail != "" && strings.EqualFold(pr.OwnerEmail, user.Email)
}

// OwnedAssetIDs filters ids down to the live assets the user owns.
func (c *Checker) OwnedAssetIDs(ctx context.Context, ids []string, user *User) []string {
	if c.cfg.Assets == nil {
		return nil
	}
	assets, err := c.cfg.Assets.GetByIDs(ctx, ids)
	if err != nil {
		return nil
	}
	owned := make([]string, 0, len(ids))
	for _, id := range ids {
		if a, ok := assets[id]; ok && a != nil && a.DeletedAt == nil && a.OwnerID == user.UserID {
			owned = append(owned, id)
		}
	}
	return owned
}

// OwnedCollectionIDs filters ids down to the live collections the user owns.
func (c *Checker) OwnedCollectionIDs(ctx context.Context, ids []string, user *User) []string {
	if c.cfg.Collections == nil {
		return nil
	}
	owned := make([]string, 0, len(ids))
	for _, id := range ids {
		coll, err := c.cfg.Collections.Get(ctx, id)
		if err == nil && coll.DeletedAt == nil && coll.OwnerID == user.UserID {
			owned = append(owned, id)
		}
	}
	return owned
}

// OwnedTargetIDs filters ids down to the targets of the given type the user
// owns. An unsupported target type owns nothing.
func (c *Checker) OwnedTargetIDs(ctx context.Context, targetType string, ids []string, user *User) []string {
	switch targetType {
	case portaldomain.TargetTypeAsset:
		return c.OwnedAssetIDs(ctx, ids, user)
	case portaldomain.TargetTypeCollection:
		return c.OwnedCollectionIDs(ctx, ids, user)
	default:
		return nil
	}
}

// CanModerateThread reports whether the user may change a thread's status or
// delete it: the thread author, an admin, or an owner/editor of the target.
// Standalone threads are moderated only by their author or an admin.
func (c *Checker) CanModerateThread(ctx context.Context, user *User, thread *threads.Thread) bool {
	if c.IsAdmin(user) || thread.AuthorID == user.UserID {
		return true
	}
	switch thread.TargetType {
	case portaldomain.TargetTypeAsset:
		return c.CanEditAssetSilent(ctx, thread.AssetID, user)
	case portaldomain.TargetTypeCollection:
		return c.CanEditCollectionSilent(ctx, thread.CollectionID, user)
	case portaldomain.TargetTypePrompt:
		return c.OwnsPersonalPrompt(ctx, thread.PromptID, user)
	case portaldomain.TargetTypeKnowledgePage:
		// Pages are edited by apply_knowledge holders, so they also moderate
		// page feedback (author/admin already handled above).
		return c.HasApplyKnowledge(user)
	default:
		return false // standalone: only author/admin (handled above)
	}
}
