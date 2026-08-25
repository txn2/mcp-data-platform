package portaldomain

import (
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
// share link and an asset's resource reference -- and both are the same thing:
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

// ShareTarget identifies what a share is for: an asset, a collection, or a
// prompt. Exactly one field is set.
type ShareTarget struct {
	AssetID      string
	CollectionID string
	PromptID     string
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
	return s.RecipientEmail != "" || s.RecipientUserID != ""
}

// BuildShare validates a spec and constructs the Share it describes, minting
// the token. It performs no I/O: the caller inserts the returned Share and
// announces it. An error here is the message the caller reports verbatim.
func BuildShare(target ShareTarget, createdBy string, spec ShareSpec) (Share, error) {
	token, err := GenerateShareToken()
	if err != nil {
		return Share{}, errors.New("failed to generate share token")
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
		ID:               uuid.New().String(),
		AssetID:          target.AssetID,
		CollectionID:     target.CollectionID,
		PromptID:         target.PromptID,
		Token:            token,
		CreatedBy:        createdBy,
		SharedWithUserID: spec.RecipientUserID,
		SharedWithEmail:  spec.RecipientEmail,
		Permission:       perm,
		AccessMode:       mode,
		HideExpiration:   spec.HideExpiration,
		NoticeText:       noticeText,
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
