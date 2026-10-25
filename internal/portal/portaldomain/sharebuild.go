package portaldomain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
)

// tokenBytes is the number of random bytes used for the portal's capability
// tokens (256 bits).
const tokenBytes = 32

// generateToken mints one capability token. The portal has two kinds -- a
// share link and an asset's content reference -- and both are the same thing:
// a random string whose possession is the entire grant. One generator is what
// keeps them the same strength.
func generateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// GenerateShareToken generates a cryptographically random hex token for share
// links. Every door that mints a share -- the REST handlers, the export
// adapters, the asset toolkit -- goes through here, so a token means the same
// thing whatever created it.
func GenerateShareToken() (string, error) { return generateToken() }

// GenerateRefToken mints the capability one asset content reference is served
// under (#1474). It is the same generator and the same 256 bits a share token
// gets, because it is the same kind of secret: the URL is the authorization, so
// the token has to be as unguessable as the share link that carries a whole
// asset.
func GenerateRefToken() (string, error) { return generateToken() }

// ShareTarget identifies what a share is for: an asset, a collection, a
// prompt, or a managed resource (#2258). Exactly one field is set.
type ShareTarget struct {
	AssetID      string
	CollectionID string
	PromptID     string
	ResourceID   string
}

// ShareSpec is what a caller asks for when creating a share, independent of
// the surface that asked. The REST handlers fill it from their request body
// and the asset toolkit fills it from tool arguments, so both surfaces get one
// answer on what a recipient, a permission, an access mode and a lifetime mean
// together.
type ShareSpec struct {
	// RecipientEmail names the person the share is addressed to. Empty is a
	// link share, not an invalid address. It is normalized to the bare address
	// (a "Name <addr>" form is reduced) so every later comparison -- view-time
	// matching, the notification recipient -- sees one spelling.
	RecipientEmail string
	// RecipientUserID names the recipient by platform user ID, for a caller
	// that already resolved one.
	RecipientUserID string
	// RecipientPersona names a persona as the recipient (#2089): everyone
	// whose roles resolve to it when they open the item. Exclusive with the
	// email and user recipients.
	RecipientPersona string
	// Permission is "viewer" or "editor". Empty means viewer. A share naming
	// nobody is forced to viewer whatever this says: a link anyone signed in
	// can open must not carry write access.
	Permission string
	// AccessMode is "restricted", "authenticated", or "public". Empty means
	// the default for the share's shape: restricted when a recipient is named,
	// authenticated otherwise. "public" is never implied.
	AccessMode string
	// ExpiresIn is a Go duration string. It is required for a public link and
	// refused for every other mode (see applyExpiry).
	ExpiresIn string
	// NoticeText overrides the default confidentiality notice. nil keeps the
	// default, "" hides the notice, and any other value is shown as-is.
	NoticeText *string
	// HideExpiration hides the countdown from the viewer.
	HideExpiration bool
}

// hasRecipient reports whether the spec addresses a person rather than minting
// a bare link.
func (s ShareSpec) hasRecipient() bool {
	return s.RecipientEmail != "" || s.RecipientUserID != "" || s.RecipientPersona != ""
}

// ErrPersonaAndPerson is a share naming both a persona and a person.
var ErrPersonaAndPerson = errors.New("a share is addressed to a person or to a persona, not both")

// BuildShare validates a spec and constructs the Share it describes, minting
// the token. It performs no I/O: the caller inserts the returned Share and
// announces it. An error here is the message the caller reports verbatim.
func BuildShare(target ShareTarget, createdBy string, spec ShareSpec) (Share, error) {
	token, err := GenerateShareToken()
	if err != nil {
		return Share{}, errors.New("failed to generate share token")
	}
	if spec.RecipientPersona != "" && (spec.RecipientEmail != "" || spec.RecipientUserID != "") {
		return Share{}, ErrPersonaAndPerson
	}

	if spec.RecipientEmail != "" {
		addr, emailErr := ParseEmail(spec.RecipientEmail)
		if emailErr != nil {
			return Share{}, emailErr
		}
		spec.RecipientEmail = addr
	}

	noticeText := DefaultNoticeText
	if spec.NoticeText != nil {
		noticeText = *spec.NoticeText
		if err := ValidateNoticeText(noticeText); err != nil {
			return Share{}, err
		}
	}

	perm, permErr := resolveSharePermission(spec)
	if permErr != nil {
		return Share{}, permErr
	}

	mode, modeErr := shareaccess.Resolve(spec.AccessMode, spec.hasRecipient())
	if modeErr != nil {
		return Share{}, modeErr //nolint:wrapcheck // message is the verbatim error the caller reports
	}

	share := Share{
		ID:                uuid.New().String(),
		AssetID:           target.AssetID,
		CollectionID:      target.CollectionID,
		PromptID:          target.PromptID,
		ResourceID:        target.ResourceID,
		Token:             token,
		CreatedBy:         createdBy,
		SharedWithUserID:  spec.RecipientUserID,
		SharedWithEmail:   spec.RecipientEmail,
		SharedWithPersona: spec.RecipientPersona,
		Permission:        perm,
		AccessMode:        mode,
		HideExpiration:    spec.HideExpiration,
		NoticeText:        noticeText,
	}

	if expErr := applyExpiry(&share, mode, spec.ExpiresIn); expErr != nil {
		return Share{}, expErr
	}

	return share, nil
}

// resolveSharePermission returns the permission a share carries. A share that
// names nobody is forced to viewer: it resolves against anyone the mode admits
// rather than against one person, so an editor grant on it would hand write
// access to an audience the creator never enumerated.
func resolveSharePermission(spec ShareSpec) (SharePermission, error) {
	perm := PermissionViewer
	if spec.Permission != "" {
		if !ValidSharePermission(spec.Permission) {
			return "", errors.New("invalid permission: must be viewer or editor")
		}
		perm = SharePermission(spec.Permission)
	}
	if !spec.hasRecipient() {
		perm = PermissionViewer
	}
	return perm, nil
}

// Errors about a share's lifetime. Each names the shape it applies to, so a
// caller that hits one knows which half of the rule it is on.
var (
	ErrExpiryOnPersonShare = errors.New(
		"expires_in does not apply to a share addressed to a person; revoke the share to end access")
	ErrExpiryOnAuthenticatedShare = errors.New(
		"expires_in does not apply to a link only signed-in users can open; revoke the share to end access")
	ErrPublicShareNeedsExpiry = errors.New(
		"expires_in is required for access_mode public; a link that opens without signing in must have a bounded life")
	ErrInvalidExpiresIn     = errors.New("invalid expires_in duration")
	ErrNonPositiveExpiresIn = errors.New("expires_in must be a positive duration")
)

// applyExpiry sets share.ExpiresAt from expiresIn, enforcing the one rule that
// decides a share's lifetime: a public link expires, everything else is
// revoke-only.
//
// A public link is a bearer credential -- holding the URL is the whole of the
// access check -- so a bounded life is what limits how long a forwarded or
// leaked copy keeps opening for a holder who never signs in, and it is
// required rather than optional (#1279). (A signed-in viewer who opens one is
// promoted to a share of their own, which the owner sees and revokes; from
// that point their access is identity-resolved and the clock no longer governs
// it. See maybeAutoPromoteViewer in pkg/portal/public.go.) Every other share
// resolves against who the viewer is from the start: a named person, or any
// signed-in user. Access there ends when the owner revokes it, so a clock on
// top would expire a grant that is still meant to hold, and an expiry is
// refused rather than silently ignored.
func applyExpiry(share *Share, mode shareaccess.Mode, expiresIn string) error {
	if mode != shareaccess.ModePublic {
		if expiresIn == "" {
			return nil
		}
		if share.SharedWithEmail != "" || share.SharedWithUserID != "" {
			return ErrExpiryOnPersonShare
		}
		return ErrExpiryOnAuthenticatedShare
	}

	if expiresIn == "" {
		return ErrPublicShareNeedsExpiry
	}
	dur, err := time.ParseDuration(expiresIn)
	if err != nil {
		return ErrInvalidExpiresIn
	}
	// A share minted already expired is a dead link the creator would have to
	// discover by handing it to someone, so it is refused at creation.
	if dur <= 0 {
		return ErrNonPositiveExpiresIn
	}
	exp := time.Now().Add(dur)
	share.ExpiresAt = &exp
	return nil
}

// IsGeneralLink reports whether a share is an item's general link (#2291): a
// link addressed to nobody that was made from the share dialog rather than
// posted to a channel, still in force at now. An item has one; a share made by
// a post stays its own, so removing the post is what stops that link.
func IsGeneralLink(s Share, now time.Time) bool {
	if s.Revoked || s.SharedWithEmail != "" || s.SharedWithUserID != "" || s.SharedWithPersona != "" {
		return false
	}
	if s.Origin == OriginChannelPost || s.PostedTo != "" {
		return false
	}
	return s.ExpiresAt == nil || s.ExpiresAt.After(now)
}

// GeneralLink is the newest general link among shares, or nil. An item made
// before #2291 can hold several; the newest is the one the dialog changes.
func GeneralLink(shares []Share, now time.Time) *Share {
	var found *Share
	for i := range shares {
		if !IsGeneralLink(shares[i], now) {
			continue
		}
		if found == nil || shares[i].CreatedAt.After(found.CreatedAt) {
			found = &shares[i]
		}
	}
	return found
}

// LinkSpec is what the share dialog asks of an item's general link. An empty
// field keeps what the link holds.
type LinkSpec struct {
	// AccessMode is "authenticated" or "public".
	AccessMode string
	// ExpiresIn is a Go duration, for a public link only. A link made public
	// needs one; a public link kept public keeps its expiry without one.
	ExpiresIn      string
	NoticeText     *string
	HideExpiration *bool
}

// LinkUpdate is the general link's new state: its mode, expiry, notice and
// whether the viewer is shown the expiry. The token is not part of it, so a
// link already handed out follows the new rule.
type LinkUpdate struct {
	AccessMode     ShareAccessMode
	ExpiresAt      *time.Time
	NoticeText     string
	HideExpiration bool
}

// ErrLinkMode is a general link asked to be restricted, which names a
// recipient a link has none of.
var ErrLinkMode = errors.New("access_mode must be authenticated or public")

// ApplyLinkSpec is the update spec makes to the general link cur at now. The
// lifetime rule is BuildShare's: a public link expires, a link for signed-in
// people is revoke-only.
func ApplyLinkSpec(cur Share, spec LinkSpec, now time.Time) (LinkUpdate, error) {
	u := LinkUpdate{AccessMode: cur.AccessMode, ExpiresAt: cur.ExpiresAt, NoticeText: cur.NoticeText, HideExpiration: cur.HideExpiration}
	if spec.AccessMode != "" {
		u.AccessMode = ShareAccessMode(spec.AccessMode)
	}
	if u.AccessMode != AccessModeAuthenticated && u.AccessMode != AccessModePublic {
		return LinkUpdate{}, ErrLinkMode
	}
	if spec.NoticeText != nil {
		if err := ValidateNoticeText(*spec.NoticeText); err != nil {
			return LinkUpdate{}, err
		}
		u.NoticeText = *spec.NoticeText
	}
	if spec.HideExpiration != nil {
		u.HideExpiration = *spec.HideExpiration
	}
	if u.AccessMode != AccessModePublic {
		if spec.ExpiresIn != "" {
			return LinkUpdate{}, ErrExpiryOnAuthenticatedShare
		}
		u.ExpiresAt = nil
		return u, nil
	}
	expiry, err := publicExpiry(cur, spec.ExpiresIn, now)
	if err != nil {
		return LinkUpdate{}, err
	}
	u.ExpiresAt = expiry
	return u, nil
}

// publicExpiry is when a link anyone can open stops working.
func publicExpiry(cur Share, expiresIn string, now time.Time) (*time.Time, error) {
	if expiresIn == "" {
		if cur.AccessMode == AccessModePublic && cur.ExpiresAt != nil {
			return cur.ExpiresAt, nil
		}
		return nil, ErrPublicShareNeedsExpiry
	}
	dur, err := time.ParseDuration(expiresIn)
	if err != nil {
		return nil, ErrInvalidExpiresIn
	}
	if dur <= 0 {
		return nil, ErrNonPositiveExpiresIn
	}
	exp := now.Add(dur)
	return &exp, nil
}

// ErrGuestEditor is an editor grant to an address with no account: a guest
// opens an item through a link emailed to them, which only views (#2291).
var ErrGuestEditor = errors.New("an address with no account can only view: grant viewer, or share once they have signed in")

// ErrLinkPermission is a permission change asked of a link, which opens as
// viewer for everyone its mode admits.
var ErrLinkPermission = errors.New("a link opens as viewer; share with a person or a group to grant edit")

// GrantPermission is the permission a grant may be changed to (#2291): viewer or
// editor, for a share naming a person or a group, and viewer alone for an
// address the directory does not hold. The file shares apply the same rule.
func GrantPermission(ctx context.Context, share Share, requested string, hasAccount func(ctx context.Context, email string) bool) (SharePermission, error) {
	if !ValidSharePermission(requested) {
		return "", errors.New("invalid permission: must be viewer or editor")
	}
	perm := SharePermission(requested)
	if share.SharedWithEmail == "" && share.SharedWithUserID == "" && share.SharedWithPersona == "" {
		return "", ErrLinkPermission
	}
	if err := GuestCheck(ctx, share.SharedWithEmail, perm, hasAccount); err != nil {
		return "", err
	}
	return perm, nil
}

// GuestCheck refuses an editor grant to an address the directory does not
// hold (#2291). A grant to a person by user id or to a group, and any viewer
// grant, passes.
func GuestCheck(ctx context.Context, email string, perm SharePermission, hasAccount func(ctx context.Context, email string) bool) error {
	if perm != PermissionEditor || email == "" || hasAccount == nil {
		return nil
	}
	if !hasAccount(ctx, email) {
		return ErrGuestEditor
	}
	return nil
}
