package receiver

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// answered is a response with its body read.
type answered struct {
	status      int
	contentType string
	body        string
}

// postBody posts body to path with the headers given and reads the answer.
func (h *harness) postBody(t *testing.T, path, body string, hdr ...string) answered {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := testClient.Do(req) // #nosec G704 -- the httptest server this test started
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return answered{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), body: string(b)}
}

// presetSource is a served source named name, configured with a preset.
func presetSource(name, preset, secret string) whsource.Source {
	return whsource.Source{
		Name: name, Enabled: true, Connection: "scratch", Auth: whsource.Auth{Preset: preset, Secret: secret},
	}.WithDefaults()
}

func TestSetupChecksAnswerAndStoreNothing(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	h := newHarness(t,
		presetSource("slack", whsource.PresetSlack, "sec"),
		presetSource("monday", whsource.PresetMonday, "sec"),
		presetSource("zoom", whsource.PresetZoom, "zsec"),
		presetSource("discord", whsource.PresetDiscord, hex.EncodeToString(pub)),
		presetSource("graph", whsource.PresetMicrosoftGraph, "state"),
	)
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	t.Run("slack url_verification", func(t *testing.T) {
		got := h.postBody(t, "/hooks/slack", `{"token":"x","challenge":"c-123","type":"url_verification"}`)
		assert.Equal(t, http.StatusOK, got.status)
		assert.JSONEq(t, `{"challenge":"c-123"}`, got.body)
	})
	t.Run("monday challenge, unsigned", func(t *testing.T) {
		got := h.postBody(t, "/hooks/monday", `{"challenge":"m-1"}`)
		assert.Equal(t, http.StatusOK, got.status)
		assert.JSONEq(t, `{"challenge":"m-1"}`, got.body)
	})
	t.Run("an event with a challenge field is not a check", func(t *testing.T) {
		got := h.postBody(t, "/hooks/monday", `{"challenge":"m-1","event":{"type":"x"}}`)
		assert.Equal(t, http.StatusUnauthorized, got.status, "it is verified as an event, and this one is unsigned")
	})
	t.Run("zoom url_validation, signed", func(t *testing.T) {
		body := `{"payload":{"plainToken":"qgg8vlvZRS6UYooatFL8Aw"},"event_ts":1654503849680,"event":"endpoint.url_validation"}`
		got := h.postBody(t, "/hooks/zoom", body, "x-zm-signature", zoomSig("zsec", ts, body), "x-zm-request-timestamp", ts)
		assert.Equal(t, http.StatusOK, got.status)
		m := hmac.New(sha256.New, []byte("zsec"))
		_, _ = m.Write([]byte("qgg8vlvZRS6UYooatFL8Aw"))
		assert.JSONEq(t, `{"plainToken":"qgg8vlvZRS6UYooatFL8Aw","encryptedToken":"`+hex.EncodeToString(m.Sum(nil))+`"}`, got.body)
	})
	t.Run("zoom url_validation, unsigned, is refused", func(t *testing.T) {
		body := `{"payload":{"plainToken":"anything"},"event":"endpoint.url_validation"}`
		got := h.postBody(t, "/hooks/zoom", body)
		assert.Equal(t, http.StatusUnauthorized, got.status, "an unverified answer would sign any text for anyone")
	})
	t.Run("discord ping", func(t *testing.T) {
		body := `{"type":1}`
		sig := hex.EncodeToString(ed25519.Sign(priv, []byte(ts+body)))
		got := h.postBody(t, "/hooks/discord", body, "X-Signature-Ed25519", sig, "X-Signature-Timestamp", ts)
		assert.Equal(t, http.StatusOK, got.status)
		assert.JSONEq(t, `{"type":1}`, got.body)
		unsigned := h.postBody(t, "/hooks/discord", body, "X-Signature-Timestamp", ts)
		assert.Equal(t, http.StatusUnauthorized, unsigned.status)
	})
	t.Run("graph validationToken", func(t *testing.T) {
		got := h.postBody(t, "/hooks/graph?validationToken=Validation%3A+Testing+client+application+reachability", "")
		assert.Equal(t, http.StatusOK, got.status)
		assert.Equal(t, "text/plain; charset=utf-8", got.contentType)
		assert.Equal(t, "Validation: Testing client application reachability", got.body)
	})

	assert.Zero(t, h.objects.count(), "a setup check writes nothing")
	assert.Equal(t, 1, h.metrics.requests["slack/"+OutcomeSetupCheck])
	assert.Equal(t, 1, h.metrics.requests["graph/"+OutcomeSetupCheck])
}

func TestEventsStillLandBesideASetupCheck(t *testing.T) {
	h := newHarness(t, presetSource("graph", whsource.PresetMicrosoftGraph, "state"))
	good := h.postBody(t, "/hooks/graph", `{"value":[{"clientState":"state","id":"1"},{"clientState":"state","id":"2"}]}`)
	assert.Equal(t, http.StatusAccepted, good.status)
	bad := h.postBody(t, "/hooks/graph", `{"value":[{"clientState":"state"},{"clientState":"other"}]}`)
	assert.Equal(t, http.StatusUnauthorized, bad.status)
	assert.Equal(t, 1, h.objects.count())
}

func zoomSig(secret, ts, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte("v0:" + ts + ":" + body))
	return "v0=" + hex.EncodeToString(m.Sum(nil))
}
