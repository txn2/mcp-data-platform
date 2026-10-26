package epform

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewerToken_RoundTripAndRefusals(t *testing.T) {
	vk := ViewerKey(key)
	require.NotEmpty(t, vk)
	assert.NotEqual(t, Key(key), vk, "the viewer key is not the form token key")
	now := time.Unix(1_800_000_000, 0)
	who := Viewer{UserID: "u1", Email: "ana@example.com", Persona: "analyst", AuthType: "oidc"}

	tok, err := SignViewer(vk, "contact", who, now)
	require.NoError(t, err)
	got, err := OpenViewer(vk, "contact", tok, now.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, who, got)

	refusedWith := func(t *testing.T, err error, want string) {
		t.Helper()
		require.ErrorIs(t, err, ErrRefused)
		assert.Contains(t, err.Error(), want)
	}
	_, err = OpenViewer(vk, "other", tok, now)
	refusedWith(t, err, "another form")
	_, err = OpenViewer(vk, "contact", tok, now.Add(ViewerTTL+time.Second))
	refusedWith(t, err, "expired")
	_, err = OpenViewer(ViewerKey([]byte("another-master-key-0123456789abc")), "contact", tok, now)
	refusedWith(t, err, "not one this platform issued")
	body, sig, _ := strings.Cut(tok, ".")
	_, err = OpenViewer(vk, "contact", body+"x."+sig, now)
	refusedWith(t, err, "not one this platform issued")
	_, err = OpenViewer(vk, "contact", "no-dot", now)
	refusedWith(t, err, "not one this platform issued")

	nobody, err := SignViewer(vk, "contact", Viewer{}, now)
	require.NoError(t, err)
	_, err = OpenViewer(vk, "contact", nobody, now)
	refusedWith(t, err, "names nobody")

	unreadable := "bm90LWpzb24" // "not-json"
	_, err = OpenViewer(vk, "contact", unreadable+"."+encodeMAC(vk, unreadable), now)
	refusedWith(t, err, "cannot be read")
	_, err = OpenViewer(vk, "contact", "!!."+encodeMAC(vk, "!!"), now)
	refusedWith(t, err, "cannot be read")
}

func TestViewerToken_NoKey(t *testing.T) {
	assert.Nil(t, ViewerKey(nil))
	_, err := SignViewer(nil, "contact", Viewer{UserID: "u"}, time.Now())
	require.ErrorIs(t, err, ErrRefused)
	_, err = OpenViewer(nil, "contact", "a.b", time.Now())
	require.ErrorIs(t, err, ErrRefused)
}

func TestSpec_Sensitive(t *testing.T) {
	s := contact()
	assert.Empty(t, s.Sensitive())
	s.Fields[2].Sensitive = true
	s.Fields[0].Sensitive = true
	assert.Equal(t, []string{"name", "email"}, s.Sensitive())
}

func encodeMAC(k []byte, body string) string {
	return base64.RawURLEncoding.EncodeToString(mac(k, body))
}
