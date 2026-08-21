package portal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// directoryCandidateLimit caps how many directory matches a recipient lookup
// considers. A name that matches more people than this is ambiguous many times
// over, and the error only has to prove that.
const directoryCandidateLimit = 25

// shareKind is the notification's word for what was shared.
const shareKind = "asset"

// shareOutput is what the share action reports back: enough for the agent to
// tell the person what was done and hand them the link.
type shareOutput struct {
	ShareID    string     `json:"share_id"`
	AssetID    string     `json:"asset_id"`
	ShareURL   string     `json:"share_url,omitempty"`
	SharedWith string     `json:"shared_with,omitempty"`
	Permission string     `json:"permission"`
	AccessMode string     `json:"access_mode"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	// Notified reports whether the recipient was emailed. A link share
	// addresses nobody, and a deployment with no notification substrate mails
	// nothing, so the agent must not tell the user "John has been emailed"
	// unless this says so.
	Notified bool   `json:"notified"`
	Message  string `json:"message"`
}

// shareView is one active share as list_shares reports it.
type shareView struct {
	ShareID     string     `json:"share_id"`
	SharedWith  string     `json:"shared_with,omitempty"`
	Permission  string     `json:"permission"`
	AccessMode  string     `json:"access_mode"`
	ShareURL    string     `json:"share_url,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	AccessCount int        `json:"access_count"`
	CreatedAt   time.Time  `json:"created_at"`
}

// handleShare grants access to an asset the caller owns, either to a named
// person or as a link (#1280).
//
// The share itself is built by portaldomain.BuildShare, the same constructor
// the REST share routes use, so what a permission, an access mode and an
// expiry mean here is what they mean in the portal.
func (t *Toolkit) handleShare(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	asset, denial := t.loadOwnedAsset(ctx, input.AssetID, actionShare, "share this asset")
	if denial != nil {
		return denial, nil, nil
	}

	email, lookupErr := t.resolveRecipient(ctx, input.Recipient)
	if lookupErr != nil {
		return toolkit.ErrorResult(lookupErr.Error()), nil, nil
	}

	share, err := portaldomain.BuildShare(
		portaldomain.ShareTarget{AssetID: asset.ID},
		resolveOwnerEmail(ctx),
		portaldomain.ShareSpec{
			RecipientEmail: email,
			Permission:     input.Permission,
			AccessMode:     input.AccessMode,
			ExpiresIn:      input.ExpiresIn,
		},
	)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	if err := t.shareStore.Insert(ctx, share); err != nil {
		return toolkit.ErrorResult("failed to create share: " + err.Error()), nil, nil
	}

	notified := t.announceShare(ctx, &share, asset)
	return toolkit.JSONResultTyped(shareOutput{
		ShareID:    share.ID,
		AssetID:    asset.ID,
		ShareURL:   t.shareURL(share.Token),
		SharedWith: share.SharedWithEmail,
		Permission: string(share.Permission),
		AccessMode: string(share.AccessMode),
		ExpiresAt:  share.ExpiresAt,
		Notified:   notified,
		Message:    shareMessage(share, notified),
	})
}

// handleListShares reports the shares that currently grant access to an asset
// the caller owns.
func (t *Toolkit) handleListShares(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	asset, denial := t.loadOwnedAsset(ctx, input.AssetID, actionListShares, "see who this asset is shared with")
	if denial != nil {
		return denial, nil, nil
	}

	shares, err := t.shareStore.ListByAsset(ctx, asset.ID)
	if err != nil {
		return toolkit.ErrorResult("failed to list shares: " + err.Error()), nil, nil
	}

	now := time.Now()
	views := make([]shareView, 0, len(shares))
	for _, s := range shares {
		// A revoked or expired share is listed by the REST view because the
		// portal renders its dead state; an agent asking who has access is
		// asking about grants that still hold, so the same liveness rule the
		// viewer gate applies decides what appears here.
		if shareaccess.Availability(s.Revoked, s.ExpiresAt, now) != "" {
			continue
		}
		views = append(views, shareView{
			ShareID:     s.ID,
			SharedWith:  s.SharedWithEmail,
			Permission:  string(s.Permission),
			AccessMode:  string(s.AccessMode),
			ShareURL:    t.shareURL(s.Token),
			ExpiresAt:   s.ExpiresAt,
			AccessCount: s.AccessCount,
			CreatedAt:   s.CreatedAt,
		})
	}

	return toolkit.JSONResultTyped(map[string]any{
		fieldAssetID: asset.ID,
		"shares":     views,
		fieldTotal:   len(views),
	})
}

// handleRevokeShare ends one share of an asset the caller owns. The share is
// found first: the asset it belongs to is what the ownership check is run
// against, so a caller cannot revoke someone else's share by naming their own
// asset.
func (t *Toolkit) handleRevokeShare(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if input.ShareID == "" {
		return toolkit.ErrorResult("share_id is required for revoke_share action"), nil, nil
	}
	if resolveOwnerID(ctx) == anonymousUserName {
		return toolkit.ErrorResult(shareIdentityRequired), nil, nil
	}

	share, err := t.shareStore.GetByID(ctx, input.ShareID)
	if err != nil || share == nil {
		return middleware.NotFoundResult("share not found",
			"Call manage_asset action=list_shares with the asset_id to see its shares."), nil, nil
	}
	if share.AssetID == "" {
		return toolkit.ErrorResult(
			"that share is on a collection or a prompt, not an asset; revoke it from the portal"), nil, nil
	}

	if _, denial := t.loadOwnedAsset(ctx, share.AssetID, actionRevokeShare, "revoke this share"); denial != nil {
		return denial, nil, nil
	}

	if err := t.shareStore.Revoke(ctx, share.ID); err != nil {
		return toolkit.ErrorResult("failed to revoke share: " + err.Error()), nil, nil
	}

	return toolkit.JSONResultTyped(map[string]any{
		"share_id":   share.ID,
		fieldAssetID: share.AssetID,
		fieldMessage: revokedMessage(*share),
	})
}

// shareIdentityRequired is the refusal an unauthenticated caller gets. Sharing
// records who granted the access and, for a person share, mails them; neither
// is meaningful without a caller identity, and the "anonymous" owner sentinel
// is shared by every unauthenticated session, so matching on it would let one
// anonymous caller share another's asset.
const shareIdentityRequired = "sharing requires an authenticated user"

// loadOwnedAsset fetches the asset a share action names and verifies the caller
// may exercise owner authority over it. verb completes "only the owner can
// <verb>". Returns an error result (and nil asset) when the action must stop.
func (t *Toolkit) loadOwnedAsset(ctx context.Context, assetID, action, verb string) (*portal.Asset, *mcp.CallToolResult) {
	if assetID == "" {
		return nil, toolkit.ErrorResult("asset_id is required for " + action + " action")
	}
	if resolveOwnerID(ctx) == anonymousUserName {
		return nil, toolkit.ErrorResult(shareIdentityRequired)
	}
	asset, err := t.assetStore.Get(ctx, assetID)
	if err != nil || asset == nil {
		return nil, middleware.NotFoundResult("asset not found", assetNotFoundHint)
	}
	if asset.DeletedAt != nil {
		return nil, toolkit.ErrorResult("asset has been deleted")
	}
	// Sharing is owner authority, not editing: an editor share never carries
	// the right to hand that access on to someone else (the REST routes hold
	// the same line). An admin is unrestricted, as everywhere else.
	if !t.isAdmin(ctx) && !ownsResource(ctx, asset.OwnerID, asset.OwnerEmail) {
		return nil, toolkit.ErrorResult("only the owner can " + verb)
	}
	return asset, nil
}

// resolveRecipient turns what the caller typed into the bare address a share is
// addressed to. An omitted recipient stays empty: that is a link share, not a
// missing recipient. A recipient that is present but blank is refused rather
// than read as one, because silently turning "share with <nothing>" into a link
// any signed-in user can open widens the audience the caller asked for. An
// address is taken as written (the directory is a convenience, not an
// allowlist — a share may name someone who has never signed in); anything else
// is looked up as a person's name.
func (t *Toolkit) resolveRecipient(ctx context.Context, recipient string) (string, error) {
	if recipient == "" {
		return "", nil
	}
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return "", errors.New(
			"recipient is blank; name a person by email address or by name, or omit it to create a link")
	}
	if addr, err := portaldomain.ParseEmail(recipient); err == nil {
		return addr, nil
	}
	if t.directory == nil {
		return "", fmt.Errorf(
			"cannot look up %q: this deployment has no user directory; give the recipient's email address instead",
			recipient)
	}

	match, err := t.lookupPeople(ctx, recipient)
	if err != nil {
		return "", err
	}
	switch len(match.people) {
	case 0:
		return "", fmt.Errorf(
			"nobody in the directory matches %q; give the recipient's email address instead", recipient)
	case 1:
		return match.people[0].Email, nil
	default:
		return "", fmt.Errorf("more than one person matches %q (%d%s): %s; name one of them by email address",
			recipient, len(match.people), match.moreLabel(), strings.Join(describePeople(match.people), ", "))
	}
}

// peopleMatch is what a directory lookup found, and whether the directory had
// more to say. A page that came back full may have been cut short, so the count
// is reported as a floor rather than as the answer.
type peopleMatch struct {
	people    []user.User
	truncated bool
}

func (m peopleMatch) moreLabel() string {
	if m.truncated {
		return " or more"
	}
	return ""
}

// lookupPeople resolves free text to directory entries.
//
// The store matches one substring at a time against email, first name and last
// name, so a full name ("John Smith") matches no single column. When the whole
// string finds nobody and it holds more than one word, the longest word is
// queried instead and the results are narrowed to the entries every word
// appears in — which is how a person is usually named out loud.
func (t *Toolkit) lookupPeople(ctx context.Context, query string) (peopleMatch, error) {
	people, total, err := t.directory.List(ctx, user.Filter{Query: query, Limit: directoryCandidateLimit})
	if err != nil {
		return peopleMatch{}, fmt.Errorf("looking up %q in the user directory: %w", query, err)
	}
	words := strings.Fields(query)
	if len(people) > 0 || len(words) < 2 {
		return peopleMatch{people: people, truncated: total > len(people)}, nil
	}

	people, total, err = t.directory.List(ctx, user.Filter{Query: longestWord(words), Limit: directoryCandidateLimit})
	if err != nil {
		return peopleMatch{}, fmt.Errorf("looking up %q in the user directory: %w", query, err)
	}
	// The narrowing runs over one page, so a page the directory cut short may
	// hold matches this never sees.
	return peopleMatch{people: matchingAllWords(people, words), truncated: total > len(people)}, nil
}

// longestWord returns the most selective word to query the directory with. The
// longest is the best available guess: a query is a substring match, so a short
// word ("de", "van") matches half the directory.
func longestWord(words []string) string {
	longest := words[0]
	for _, w := range words[1:] {
		if len(w) > len(longest) {
			longest = w
		}
	}
	return longest
}

// matchingAllWords keeps the people whose name or address contains every word
// the caller typed, so "John Smith" resolves John Smith and not John Baker.
func matchingAllWords(people []user.User, words []string) []user.User {
	matched := make([]user.User, 0, len(people))
	for _, p := range people {
		haystack := strings.ToLower(strings.Join([]string{p.FirstName, p.LastName, p.Email}, " "))
		if containsAll(haystack, words) {
			matched = append(matched, p)
		}
	}
	return matched
}

// containsAll reports whether haystack (already lower-cased) contains every
// word.
func containsAll(haystack string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(haystack, strings.ToLower(w)) {
			return false
		}
	}
	return true
}

// describePeople renders ambiguous candidates for the error that refuses to
// guess between them, sorted so the same ambiguity always reads the same way.
func describePeople(people []user.User) []string {
	described := make([]string, 0, len(people))
	for _, p := range people {
		name := strings.TrimSpace(p.FirstName + " " + p.LastName)
		if name == "" {
			described = append(described, p.Email)
			continue
		}
		described = append(described, fmt.Sprintf("%s <%s>", name, p.Email))
	}
	sort.Strings(described)
	return described
}

// announceShare mails the recipient of a person share and reports whether
// anything was sent. A link share addresses nobody, and a deployment without
// the notification substrate has no notifier, so both leave the share silent.
func (t *Toolkit) announceShare(ctx context.Context, share *portal.Share, asset *portal.Asset) bool {
	if share.SharedWithEmail == "" || t.notifier == nil {
		return false
	}
	t.notifier.NotifyShare(ctx, share, portal.ShareEvent{
		Kind: shareKind, ItemID: asset.ID, ItemTitle: asset.Name,
	})
	return true
}

// shareURL renders the viewer link for a token, or "" when the deployment
// configured no base URL. Returning a bare token in its place would put a
// non-URL value in the model-visible share_url field.
func (t *Toolkit) shareURL(token string) string {
	if t.baseURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/portal/view/%s", t.baseURL, token)
}

// shareMessage states what the share grants and how it ends, because those are
// the two things the person who asked for it needs to hear back.
func shareMessage(share portal.Share, notified bool) string {
	var b strings.Builder
	switch {
	case share.SharedWithEmail != "":
		fmt.Fprintf(&b, "Shared with %s as %s.", share.SharedWithEmail, share.Permission)
		if notified {
			b.WriteString(" They have been emailed the link.")
		}
	case share.AccessMode == portal.AccessModePublic:
		b.WriteString("Created a public link: anyone holding it can open the asset without signing in.")
	default:
		b.WriteString("Created a link any signed-in platform user can open.")
	}
	if share.ExpiresAt != nil {
		fmt.Fprintf(&b, " It expires at %s.", share.ExpiresAt.UTC().Format(time.RFC3339))
	} else {
		b.WriteString(" It lasts until revoked (manage_asset action=revoke_share).")
	}
	return b.String()
}

// revokedMessage names who lost access, so a revoke by share id reads back as
// something the caller can check.
func revokedMessage(share portal.Share) string {
	if share.SharedWithEmail != "" {
		return fmt.Sprintf("Revoked the share with %s. They can no longer open the asset.", share.SharedWithEmail)
	}
	return "Revoked the link. It no longer opens the asset."
}
