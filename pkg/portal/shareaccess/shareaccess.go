// Package shareaccess decides who may open a portal share token.
//
// Before #999 a share token was a bearer credential: anyone holding the URL
// could read the shared asset, including shares created for one named person
// and emailed to them. This package holds the mode domain and the access
// decision, independent of HTTP, so the portal's public routes all reach the
// same verdict through one function.
package shareaccess

import (
	"errors"
	"strings"
	"time"
)

// Mode determines who may open a share token.
type Mode string

const (
	// ModeRestricted admits only the share's named recipient and its
	// creator. A restricted share without a recipient is invalid.
	ModeRestricted Mode = "restricted"
	// ModeAuthenticated admits any signed-in platform user.
	ModeAuthenticated Mode = "authenticated"
	// ModePublic admits anyone holding the token, without sign-in. It is
	// never a default: the create-share request must ask for it.
	ModePublic Mode = "public"
)

// Denial messages. They name the reason so a viewer can act on it (sign in, or
// ask the sender) instead of guessing.
const (
	MsgSignInRequired = "This link requires you to sign in. " +
		"Open it again after signing in to the platform."
	MsgNotRecipient = "This link is restricted to the person it was shared with. " +
		"You are signed in as a different user. Ask the sender to share it with you."
	MsgRevoked = "This share link has been revoked."
	MsgExpired = "This share link has expired."
)

// Availability returns the message to render when a share cannot be opened by
// anyone — revoked, or past its expiry — and "" while it is still live. It is
// the check that precedes Authorize: who the caller is does not matter yet.
func Availability(revoked bool, expiresAt *time.Time, now time.Time) string {
	if revoked {
		return MsgRevoked
	}
	if expiresAt != nil && expiresAt.Before(now) {
		return MsgExpired
	}
	return ""
}

// Errors returned when a create-share request asks for an impossible mode.
var (
	ErrInvalidMode = errors.New(
		"invalid access_mode: must be restricted, authenticated, or public")
	ErrRestrictedNeedsRecipient = errors.New(
		"access_mode restricted requires shared_with_email or shared_with_user_id")
)

// Share is the part of a share row the decision depends on.
type Share struct {
	Mode            Mode
	RecipientUserID string
	RecipientEmail  string
	CreatorEmail    string
}

// Viewer is the authenticated caller. A nil *Viewer is an anonymous request.
type Viewer struct {
	UserID string
	Email  string
}

// Default returns the mode a share takes when its creator did not choose one:
// restricted when the share names a recipient, authenticated when it does not.
// ModePublic is never a default — it must be requested.
func Default(hasRecipient bool) Mode {
	if hasRecipient {
		return ModeRestricted
	}
	return ModeAuthenticated
}

// Resolve validates a requested mode and applies the default for the share's
// shape. A restricted share with no named recipient is rejected: there would
// be nobody it resolves for.
func Resolve(requested string, hasRecipient bool) (Mode, error) {
	if requested == "" {
		return Default(hasRecipient), nil
	}
	mode := Mode(requested)
	switch mode {
	case ModeRestricted:
		if !hasRecipient {
			return "", ErrRestrictedNeedsRecipient
		}
	case ModeAuthenticated, ModePublic:
	default:
		return "", ErrInvalidMode
	}
	return mode, nil
}

// Authorize decides whether viewer may open share, returning the message to
// render when it may not. viewer is nil for an anonymous request.
//
// A share whose mode is empty (a row written before the mode column existed,
// or by a caller that left it blank) is treated as its shape's default rather
// than as public, so an unset value can never widen access.
func Authorize(share Share, viewer *Viewer) (string, bool) {
	mode := share.Mode
	if mode == "" {
		mode = Default(share.RecipientUserID != "" || share.RecipientEmail != "")
	}

	if mode == ModePublic {
		return "", true
	}
	if viewer == nil {
		return MsgSignInRequired, false
	}
	if mode == ModeAuthenticated {
		return "", true
	}
	if namesViewer(share, viewer) {
		return "", true
	}
	return MsgNotRecipient, false
}

// namesViewer reports whether viewer is the share's named recipient or its
// creator. The creator is admitted so the person who made the link can open it
// to see what the recipient will see.
func namesViewer(share Share, viewer *Viewer) bool {
	if share.RecipientUserID != "" && share.RecipientUserID == viewer.UserID {
		return true
	}
	return emailsEqual(share.RecipientEmail, viewer.Email) ||
		emailsEqual(share.CreatorEmail, viewer.Email)
}

// emailsEqual compares two addresses case-insensitively, treating an empty
// address as matching nothing.
func emailsEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
