package epform

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbedMarker_RoundTripAndRefusals(t *testing.T) {
	vk := ViewerKey(key)
	ek := EmbedKey(vk)
	require.NotEmpty(t, ek)
	assert.NotEqual(t, vk, ek, "the embed key is not the viewer key")
	now := time.Unix(1_800_000_000, 0)

	tok := SignEmbed(ek, "contact", now)
	require.NotEmpty(t, tok)
	assert.True(t, OpenEmbed(ek, "contact", tok, now.Add(time.Hour)))
	assert.True(t, OpenEmbed(ek, "contact", tok, now.Add(EmbedTTL)), "honored to the end of its window")

	assert.False(t, OpenEmbed(ek, "other", tok, now), "issued for another form")
	assert.False(t, OpenEmbed(ek, "contact", tok, now.Add(EmbedTTL+time.Second)), "expired")
	assert.False(t, OpenEmbed(vk, "contact", tok, now), "another key")
	viewer, err := SignViewer(ek, "contact", Viewer{UserID: "u"}, now)
	require.NoError(t, err)
	assert.False(t, OpenEmbed(ek, "contact", viewer, now), "a token of another kind names no endpoint here")
	body, sig, _ := strings.Cut(tok, ".")
	assert.False(t, OpenEmbed(ek, "contact", body+"x."+sig, now), "altered")
	assert.False(t, OpenEmbed(ek, "contact", "no-dot", now))
	assert.False(t, OpenEmbed(ek, "contact", "", now))
	assert.False(t, OpenEmbed(ek, "contact", strings.Repeat("a", maxTokenLen+1), now))
	unreadable := "bm90LWpzb24" // "not-json"
	assert.False(t, OpenEmbed(ek, "contact", unreadable+"."+encodeMAC(ek, unreadable), now))
	assert.False(t, OpenEmbed(ek, "contact", "!!."+encodeMAC(ek, "!!"), now))
}

func TestEmbedMarker_NoKey(t *testing.T) {
	assert.Nil(t, EmbedKey(nil))
	assert.Empty(t, SignEmbed(nil, "contact", time.Now()))
	assert.False(t, OpenEmbed(nil, "contact", "a.b", time.Now()))
}
