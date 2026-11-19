package portaldomain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidStatus(t *testing.T) {
	for s, want := range map[string]bool{
		StatusDraft: true, StatusPublished: true, StatusDeprecated: true,
		StatusNone: false, "": false, "Draft": false, "archived": false,
	} {
		assert.Equal(t, want, ValidStatus(s), s)
	}
}

func TestValidStatusFilter(t *testing.T) {
	for s, want := range map[string]bool{
		"": true, StatusNone: true, StatusDraft: true, StatusPublished: true, StatusDeprecated: true,
		"archived": false, "NONE": false,
	} {
		assert.Equal(t, want, ValidStatusFilter(s), s)
	}
}

func TestFinishMarksDerivesChangedSinceVerified(t *testing.T) {
	a := &Asset{CurrentVersion: 4, Verification: &AssetVerification{VerifiedVersion: 4}}
	a.FinishMarks()
	assert.False(t, a.Verification.ChangedSinceVerified, "the verified version is current")

	a.CurrentVersion = 5
	a.FinishMarks()
	assert.True(t, a.Verification.ChangedSinceVerified, "a newer version than the one verified")

	unverified := &Asset{CurrentVersion: 2}
	unverified.FinishMarks()
	assert.Nil(t, unverified.Verification, "no verification stays none")
}
