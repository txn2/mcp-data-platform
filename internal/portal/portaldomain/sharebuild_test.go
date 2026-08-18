package portaldomain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
)

func TestGenerateShareToken(t *testing.T) {
	tok1, err := GenerateShareToken()
	require.NoError(t, err)
	assert.Len(t, tok1, tokenBytes*2) // hex encoding doubles the byte count

	tok2, err := GenerateShareToken()
	require.NoError(t, err)
	assert.NotEqual(t, tok1, tok2)
}

// A share addressed to a person is restricted, keeps the permission it asked
// for, and carries the target it was built for.
func TestBuildSharePersonDefaults(t *testing.T) {
	share, err := BuildShare(ShareTarget{AssetID: "a1"}, "owner@example.com", ShareSpec{
		RecipientEmail: "Bob Jones <Bob@Example.com>",
		Permission:     string(PermissionEditor),
	})
	require.NoError(t, err)

	assert.Equal(t, "a1", share.AssetID)
	assert.Equal(t, "owner@example.com", share.CreatedBy)
	assert.Equal(t, "bob@example.com", share.SharedWithEmail, "the display name is stripped at the door")
	assert.Equal(t, PermissionEditor, share.Permission)
	assert.Equal(t, AccessModeRestricted, share.AccessMode)
	assert.Equal(t, DefaultNoticeText, share.NoticeText)
	assert.Nil(t, share.ExpiresAt, "a share addressed to a person ends on revocation")
	assert.NotEmpty(t, share.ID)
	assert.NotEmpty(t, share.Token)
}

// A share naming nobody admits an audience the creator never enumerated, so it
// is viewer whatever it asked for.
func TestBuildShareLinkForcedToViewer(t *testing.T) {
	share, err := BuildShare(ShareTarget{CollectionID: "c1"}, "owner@example.com", ShareSpec{
		Permission: string(PermissionEditor),
	})
	require.NoError(t, err)
	assert.Equal(t, "c1", share.CollectionID)
	assert.Equal(t, PermissionViewer, share.Permission)
	assert.Equal(t, AccessModeAuthenticated, share.AccessMode)
}

// A recipient named only by user ID is still a person share.
func TestBuildShareByUserID(t *testing.T) {
	share, err := BuildShare(ShareTarget{PromptID: "p1"}, "owner@example.com", ShareSpec{
		RecipientUserID: "u1",
		Permission:      string(PermissionEditor),
	})
	require.NoError(t, err)
	assert.Equal(t, "p1", share.PromptID)
	assert.Equal(t, "u1", share.SharedWithUserID)
	assert.Equal(t, PermissionEditor, share.Permission)
	assert.Equal(t, AccessModeRestricted, share.AccessMode)
}

func TestBuildShareRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		spec ShareSpec
		want string
	}{
		{"invalid email", ShareSpec{RecipientEmail: "not-an-address"}, "invalid email address"},
		{
			"invalid permission",
			ShareSpec{RecipientEmail: "bob@example.com", Permission: "admin"},
			"invalid permission: must be viewer or editor",
		},
		{"invalid access mode", ShareSpec{AccessMode: "everyone"}, "invalid access_mode"},
		{
			"restricted without recipient",
			ShareSpec{AccessMode: string(AccessModeRestricted)},
			"requires shared_with_email",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildShare(ShareTarget{AssetID: "a1"}, "owner@example.com", tt.spec)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestBuildShareNoticeText(t *testing.T) {
	hidden := ""
	share, err := BuildShare(ShareTarget{AssetID: "a1"}, "owner@example.com", ShareSpec{
		NoticeText: &hidden, HideExpiration: true,
	})
	require.NoError(t, err)
	assert.Empty(t, share.NoticeText, "an empty notice hides it rather than restoring the default")
	assert.True(t, share.HideExpiration)

	tooLong := string(make([]byte, MaxNoticeTextLength+1))
	_, err = BuildShare(ShareTarget{AssetID: "a1"}, "owner@example.com", ShareSpec{NoticeText: &tooLong})
	require.Error(t, err)
}

// The lifetime rule (#1279): only a public link is minted with an expiry, and
// it is required there.
func TestBuildShareExpiry(t *testing.T) {
	tests := []struct {
		name      string
		spec      ShareSpec
		wantErr   error
		wantTimed bool
	}{
		{
			name:      "public link with expiry",
			spec:      ShareSpec{AccessMode: string(AccessModePublic), ExpiresIn: "24h"},
			wantTimed: true,
		},
		{
			name:    "public link without expiry",
			spec:    ShareSpec{AccessMode: string(AccessModePublic)},
			wantErr: ErrPublicShareNeedsExpiry,
		},
		{
			name:    "public link with unparseable expiry",
			spec:    ShareSpec{AccessMode: string(AccessModePublic), ExpiresIn: "soon"},
			wantErr: ErrInvalidExpiresIn,
		},
		{
			name:    "public link already expired",
			spec:    ShareSpec{AccessMode: string(AccessModePublic), ExpiresIn: "-1h"},
			wantErr: ErrNonPositiveExpiresIn,
		},
		{
			name:    "authenticated link with expiry",
			spec:    ShareSpec{ExpiresIn: "24h"},
			wantErr: ErrExpiryOnAuthenticatedShare,
		},
		{
			name:    "person share with expiry",
			spec:    ShareSpec{RecipientEmail: "bob@example.com", ExpiresIn: "24h"},
			wantErr: ErrExpiryOnPersonShare,
		},
		{
			name:    "public link addressed to a person still needs an expiry",
			spec:    ShareSpec{RecipientEmail: "bob@example.com", AccessMode: string(AccessModePublic)},
			wantErr: ErrPublicShareNeedsExpiry,
		},
		{
			name: "public link addressed to a person is timed",
			spec: ShareSpec{
				RecipientEmail: "bob@example.com",
				AccessMode:     string(AccessModePublic),
				ExpiresIn:      "1h",
			},
			wantTimed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			share, err := BuildShare(ShareTarget{AssetID: "a1"}, "owner@example.com", tt.spec)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			if !tt.wantTimed {
				assert.Nil(t, share.ExpiresAt)
				return
			}
			require.NotNil(t, share.ExpiresAt)
			assert.True(t, share.ExpiresAt.After(time.Now()))
		})
	}
}

// A share the builder produced must be one the viewer gate treats as live: the
// two halves of the rule are written in different packages, so this pins that
// they agree.
func TestBuiltShareOpensAtTheGate(t *testing.T) {
	share, err := BuildShare(ShareTarget{AssetID: "a1"}, "owner@example.com",
		ShareSpec{RecipientEmail: "bob@example.com"})
	require.NoError(t, err)
	assert.Empty(t, shareaccess.Availability(share.Revoked, share.ExpiresAt, time.Now()))

	msg, ok := shareaccess.Authorize(shareaccess.Share{
		Mode:           share.AccessMode,
		RecipientEmail: share.SharedWithEmail,
		CreatorEmail:   share.CreatedBy,
	}, &shareaccess.Viewer{Email: "bob@example.com"})
	assert.True(t, ok, msg)
}
