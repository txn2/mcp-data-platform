package portaldomain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var linkNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func TestIsGeneralLink(t *testing.T) {
	past, future := linkNow.Add(-time.Minute), linkNow.Add(time.Hour)
	cases := []struct {
		name  string
		share Share
		want  bool
	}{
		{"a link for signed-in people", Share{AccessMode: AccessModeAuthenticated}, true},
		{"a public link still open", Share{AccessMode: AccessModePublic, ExpiresAt: &future}, true},
		{"a public link past its time", Share{AccessMode: AccessModePublic, ExpiresAt: &past}, false},
		{"revoked", Share{Revoked: true}, false},
		{"to a person", Share{SharedWithEmail: "a@example.com"}, false},
		{"to a person by id", Share{SharedWithUserID: "u1"}, false},
		{"to a group", Share{SharedWithPersona: "finance"}, false},
		{"a channel post", Share{Origin: OriginChannelPost}, false},
		{"posted somewhere", Share{PostedTo: "#general"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsGeneralLink(tc.share, linkNow))
		})
	}
}

func TestGeneralLinkIsTheNewest(t *testing.T) {
	shares := []Share{
		{ID: "old", CreatedAt: linkNow.Add(-2 * time.Hour)},
		{ID: "grant", SharedWithEmail: "a@example.com", CreatedAt: linkNow},
		{ID: "new", CreatedAt: linkNow.Add(-time.Hour)},
	}
	require.NotNil(t, GeneralLink(shares, linkNow))
	assert.Equal(t, "new", GeneralLink(shares, linkNow).ID)
	assert.Nil(t, GeneralLink(shares[1:2], linkNow))
	assert.Nil(t, GeneralLink(nil, linkNow))
}

func TestApplyLinkSpec(t *testing.T) {
	future := linkNow.Add(3 * time.Hour)
	signedIn := Share{AccessMode: AccessModeAuthenticated, NoticeText: "Old"}
	public := Share{AccessMode: AccessModePublic, ExpiresAt: &future, NoticeText: "Old"}
	text, empty, long := "New", "", string(make([]byte, MaxNoticeTextLength+1))
	yes := true

	u, err := ApplyLinkSpec(signedIn, LinkSpec{AccessMode: "public", ExpiresIn: "24h"}, linkNow)
	require.NoError(t, err)
	assert.Equal(t, AccessModePublic, u.AccessMode)
	assert.Equal(t, linkNow.Add(24*time.Hour), *u.ExpiresAt)
	assert.Equal(t, "Old", u.NoticeText, "an omitted banner keeps the link's")

	u, err = ApplyLinkSpec(public, LinkSpec{NoticeText: &text, HideExpiration: &yes}, linkNow)
	require.NoError(t, err)
	assert.Equal(t, AccessModePublic, u.AccessMode, "an omitted mode keeps the link's")
	assert.Equal(t, future, *u.ExpiresAt, "a public link kept public keeps its expiry")
	assert.Equal(t, "New", u.NoticeText)
	assert.True(t, u.HideExpiration)

	u, err = ApplyLinkSpec(public, LinkSpec{AccessMode: "authenticated", NoticeText: &empty}, linkNow)
	require.NoError(t, err)
	assert.Nil(t, u.ExpiresAt, "a link for signed-in people is revoke-only")
	assert.Empty(t, u.NoticeText, "an empty banner hides it")

	for _, tc := range []struct {
		name string
		cur  Share
		spec LinkSpec
		want error
	}{
		{"restricted", signedIn, LinkSpec{AccessMode: "restricted"}, ErrLinkMode},
		{"made public with no expiry", signedIn, LinkSpec{AccessMode: "public"}, ErrPublicShareNeedsExpiry},
		{"expiry on a signed-in link", signedIn, LinkSpec{ExpiresIn: "1h"}, ErrExpiryOnAuthenticatedShare},
		{"an unreadable expiry", signedIn, LinkSpec{AccessMode: "public", ExpiresIn: "soon"}, ErrInvalidExpiresIn},
		{"an expiry already past", signedIn, LinkSpec{AccessMode: "public", ExpiresIn: "-1h"}, ErrNonPositiveExpiresIn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ApplyLinkSpec(tc.cur, tc.spec, linkNow)
			assert.ErrorIs(t, err, tc.want)
		})
	}
	_, err = ApplyLinkSpec(signedIn, LinkSpec{NoticeText: &long}, linkNow)
	assert.Error(t, err, "a banner past its length")
}

func TestGrantPermission(t *testing.T) {
	ctx := context.Background()
	known := func(_ context.Context, email string) bool { return email == "member@example.com" }

	perm, err := GrantPermission(ctx, Share{SharedWithEmail: "member@example.com"}, "editor", known)
	require.NoError(t, err)
	assert.Equal(t, PermissionEditor, perm)

	perm, err = GrantPermission(ctx, Share{SharedWithEmail: "guest@example.org"}, "viewer", known)
	require.NoError(t, err)
	assert.Equal(t, PermissionViewer, perm)

	_, err = GrantPermission(ctx, Share{SharedWithEmail: "guest@example.org"}, "editor", known)
	assert.ErrorIs(t, err, ErrGuestEditor)

	perm, err = GrantPermission(ctx, Share{SharedWithPersona: "finance"}, "editor", known)
	require.NoError(t, err, "a group is not a guest")
	assert.Equal(t, PermissionEditor, perm)

	_, err = GrantPermission(ctx, Share{}, "editor", known)
	assert.ErrorIs(t, err, ErrLinkPermission)

	_, err = GrantPermission(ctx, Share{SharedWithEmail: "member@example.com"}, "owner", known)
	assert.Error(t, err)

	assert.NoError(t, GuestCheck(ctx, "guest@example.org", PermissionEditor, nil), "no directory treats every address as one")
}
