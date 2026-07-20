package shareaccess

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorize(t *testing.T) {
	recipient := &Viewer{UserID: "u-bob", Email: "bob@example.com"}
	stranger := &Viewer{UserID: "u-eve", Email: "eve@example.com"}
	creator := &Viewer{UserID: "u-alice", Email: "alice@example.com"}

	restricted := Share{
		Mode:           ModeRestricted,
		CreatorEmail:   "alice@example.com",
		RecipientEmail: "bob@example.com",
	}

	tests := []struct {
		name    string
		share   Share
		viewer  *Viewer
		allowed bool
		msg     string
	}{
		{"public admits anonymous", Share{Mode: ModePublic}, nil, true, ""},
		{"public admits signed-in", Share{Mode: ModePublic}, stranger, true, ""},
		{"authenticated refuses anonymous", Share{Mode: ModeAuthenticated}, nil, false, MsgSignInRequired},
		{"authenticated admits any user", Share{Mode: ModeAuthenticated}, stranger, true, ""},
		{"restricted refuses anonymous", restricted, nil, false, MsgSignInRequired},
		{"restricted admits recipient by email", restricted, recipient, true, ""},
		{"restricted admits creator", restricted, creator, true, ""},
		{"restricted refuses other user", restricted, stranger, false, MsgNotRecipient},
		{
			"restricted admits recipient by user id",
			Share{Mode: ModeRestricted, RecipientUserID: "u-bob"},
			recipient, true, "",
		},
		{
			"restricted matches email case-insensitively",
			Share{Mode: ModeRestricted, RecipientEmail: "BOB@Example.com"},
			recipient, true, "",
		},
		{
			"empty mode with recipient behaves as restricted",
			Share{RecipientEmail: "bob@example.com"},
			stranger, false, MsgNotRecipient,
		},
		{"empty mode without recipient refuses anonymous", Share{}, nil, false, MsgSignInRequired},
		{"empty mode without recipient admits signed-in user", Share{}, stranger, true, ""},
		{
			"viewer without an email cannot match an email-only recipient",
			Share{Mode: ModeRestricted, RecipientEmail: "bob@example.com"},
			&Viewer{UserID: "u-nomail"}, false, MsgNotRecipient,
		},
		{
			"a blank recipient user id does not match a blank viewer id",
			Share{Mode: ModeRestricted, RecipientEmail: "bob@example.com"},
			&Viewer{Email: "eve@example.com"}, false, MsgNotRecipient,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, ok := Authorize(tc.share, tc.viewer)
			assert.Equal(t, tc.allowed, ok)
			assert.Equal(t, tc.msg, msg)
		})
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name         string
		requested    string
		hasRecipient bool
		want         Mode
		wantErr      error
	}{
		{"default with recipient is restricted", "", true, ModeRestricted, nil},
		{"default without recipient is authenticated", "", false, ModeAuthenticated, nil},
		{"explicit public is honored", "public", false, ModePublic, nil},
		{"explicit public with recipient is honored", "public", true, ModePublic, nil},
		{"explicit authenticated is honored", "authenticated", true, ModeAuthenticated, nil},
		{"explicit restricted with recipient is honored", "restricted", true, ModeRestricted, nil},
		{"restricted without recipient is rejected", "restricted", false, "", ErrRestrictedNeedsRecipient},
		{"unknown mode is rejected", "everyone", false, "", ErrInvalidMode},
		{"case-sensitive: Public is not public", "Public", false, "", ErrInvalidMode},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.requested, tc.hasRecipient)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDefault(t *testing.T) {
	assert.Equal(t, ModeRestricted, Default(true))
	assert.Equal(t, ModeAuthenticated, Default(false))
}

func TestAvailability(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name      string
		revoked   bool
		expiresAt *time.Time
		want      string
	}{
		{"live share with no expiry", false, nil, ""},
		{"live share before expiry", false, &future, ""},
		{"revoked share", true, nil, MsgRevoked},
		{"expired share", false, &past, MsgExpired},
		{"revoked wins over expiry", true, &past, MsgRevoked},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Availability(tc.revoked, tc.expiresAt, now))
		})
	}
}
