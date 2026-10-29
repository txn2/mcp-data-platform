package receiver

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// storedBytes is every segment the harness wrote, decompressed, so a test
// can search what reached object storage rather than what decoded from it.
func (f *fakeObjects) storedBytes(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var all strings.Builder
	for _, data := range f.objects {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		require.NoError(t, err)
		b, err := io.ReadAll(zr)
		require.NoError(t, err)
		_, _ = all.Write(b)
	}
	return all.String()
}

// A body_token source verifies the token its sender puts in the body and
// stores the rest of the body without it, in every element of a "[*]" path,
// in the segment and in the log (#2353).
func TestBodyTokenIsNotStored(t *testing.T) {
	const secret = "s3cr3t-token-2353"
	t.Run("mattermost form", func(t *testing.T) {
		h := newHarness(t, presetSource("mm", whsource.PresetMattermost, secret))
		form := url.Values{"token": {secret}, "text": {"deploy finished"}, "post_id": {"p1"}}.Encode()
		got := h.postBody(t, "/hooks/mm", form, "Content-Type", "application/x-www-form-urlencoded")
		require.Equal(t, http.StatusAccepted, got.status, got.body)

		evs := h.objects.events(t)
		require.Len(t, evs, 1)
		assert.JSONEq(t, `{"post_id":"p1","text":"deploy finished"}`, string(evs[0].Payload))
		assert.NotContains(t, h.objects.storedBytes(t), secret)
		assert.NotContains(t, h.logs.String(), secret)
	})
	t.Run("graph notifications", func(t *testing.T) {
		h := newHarness(t, presetSource("graph", whsource.PresetMicrosoftGraph, secret))
		body := `{"value":[{"clientState":"` + secret + `","id":"1"},{"clientState":"` + secret + `","id":"2"}]}`
		got := h.postBody(t, "/hooks/graph", body)
		require.Equal(t, http.StatusAccepted, got.status, got.body)

		stored := h.objects.storedBytes(t)
		assert.NotContains(t, stored, secret)
		assert.Contains(t, stored, `\"id\":\"1\"`)
		assert.Contains(t, stored, `\"id\":\"2\"`)
		assert.NotContains(t, h.logs.String(), secret)
	})
	t.Run("a form's payload field keeps its own token", func(t *testing.T) {
		h := newHarness(t, presetSource("mm", whsource.PresetMattermost, secret))
		form := url.Values{"token": {secret}, "payload": {`{"token":"the-senders-own","text":"hi"}`}}.Encode()
		got := h.postBody(t, "/hooks/mm", form, "Content-Type", "application/x-www-form-urlencoded")
		require.Equal(t, http.StatusAccepted, got.status, got.body)

		evs := h.objects.events(t)
		require.Len(t, evs, 1)
		assert.JSONEq(t, `{"text":"hi","token":"the-senders-own"}`, string(evs[0].Payload),
			"a value that is not the secret is the sender's data")
		assert.NotContains(t, h.objects.storedBytes(t), secret)
	})
}

// Two deliveries of one event, verified with different secrets (before and
// after a rotation), are one event: its id and content hash are computed
// once the token is gone (#2353).
func TestBodyTokenRotationKeepsTheEventID(t *testing.T) {
	h := newHarness(t,
		presetSource("before", whsource.PresetMattermost, "old-secret-2353"),
		presetSource("after", whsource.PresetMattermost, "new-secret-2353"),
	)
	for _, s := range []struct{ source, secret string }{{"before", "old-secret-2353"}, {"after", "new-secret-2353"}} {
		form := url.Values{"token": {s.secret}, "text": {"same event"}}.Encode()
		got := h.postBody(t, "/hooks/"+s.source, form, "Content-Type", "application/x-www-form-urlencoded")
		require.Equal(t, http.StatusAccepted, got.status, got.body)
	}
	evs := h.objects.events(t)
	require.Len(t, evs, 2)
	assert.Equal(t, evs[0].ContentHash, evs[1].ContentHash)
	assert.Equal(t, evs[0].EventID, evs[1].EventID)
}
