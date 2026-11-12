package ephttp

import (
	"maps"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
)

// embedPath is endpoint's address as an asset's served copy writes it: the
// embed marker bound to asset, and the viewer token when there is a reader.
func embedPath(t *testing.T, endpoint, asset, viewerToken string, extra url.Values) string {
	t.Helper()
	q := url.Values{epform.EndpointEmbedParam: {epform.SignEndpointEmbed(epform.EmbedKey(viewerKey), endpoint, asset, time.Now())}}
	if viewerToken != "" {
		q.Set(epform.EndpointViewerParam, viewerToken)
	}
	maps.Copy(q, extra)
	return "/endpoints/" + endpoint + "/1?" + q.Encode()
}

// TestEmbed_AnAssetCallsAnAuthenticatedEndpointAsItsReader is #2457: a call
// carrying the marker an asset was served with is answered from the asset's
// opaque origin, never with credentials; the reader's viewer token is the
// caller; the marker and token never reach the handler, which sees the asset.
func TestEmbed_AnAssetCallsAnAuthenticatedEndpointAsItsReader(t *testing.T) {
	f := formFixture()
	f.runner.bindings["orders"].Callers = []string{"persona:analyst"}
	path := embedPath(t, "orders", "asset-9", viewer(t, "orders"), url.Values{"q": {"late"}})

	pre := f.do("OPTIONS", path, http.Header{"Origin": {"null"}, "Access-Control-Request-Method": {"GET"}}, "")
	assert.Equal(t, http.StatusNoContent, pre.Code)
	assert.Equal(t, "null", pre.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, pre.Header().Get("Access-Control-Allow-Credentials"))

	rec := f.do("GET", path, http.Header{"Origin": {"null"}}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "null", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "ann@example.com", fields(t, rec)["caller"], "the reader is the caller")
	served := f.runner.served[len(f.runner.served)-1]
	assert.Equal(t, "asset-9", served.Asset)
	assert.Equal(t, url.Values{"q": {"late"}}, url.Values(served.Query), "the marker and the token never reach the handler")
}

// TestEmbed_Refusals is #2457's other half: without a valid marker the
// opaque origin is refused as before and a viewer token is no caller, and a
// public share's copy (no reader) reaches an authenticated endpoint as nobody.
func TestEmbed_Refusals(t *testing.T) {
	f := formFixture()
	f.runner.bindings["open"] = binding("open", epbinding.AuthPublic)
	pre := http.Header{"Origin": {"null"}, "Access-Control-Request-Method": {"GET"}}
	formMarker := url.Values{epform.EndpointEmbedParam: {epform.SignEmbed(epform.EmbedKey(viewerKey), "orders", time.Now())}}
	expired := url.Values{epform.EndpointEmbedParam: {epform.SignEndpointEmbed(epform.EmbedKey(viewerKey), "orders", "a", time.Now().Add(-epform.EmbedTTL-time.Minute))}}
	for name, path := range map[string]string{
		"no marker":             "/endpoints/orders/1",
		"a marker for another":  embedPath(t, "open", "a", "", nil)[len("/endpoints/open"):],
		"a form's marker":       "/endpoints/orders/1?" + formMarker.Encode(),
		"an expired marker":     "/endpoints/orders/1?" + expired.Encode(),
		"a marker with no sign": "/endpoints/orders/1?endpoint_embed=e30",
	} {
		if name == "a marker for another" {
			path = "/endpoints/orders" + path
		}
		assert.Equal(t, http.StatusForbidden, f.do("OPTIONS", path, pre, "").Code, name)
	}

	noMarker := "/endpoints/orders/1?" + url.Values{epform.EndpointViewerParam: {viewer(t, "orders")}}.Encode()
	assert.Equal(t, http.StatusUnauthorized, f.do("GET", noMarker, nil, "").Code, "a viewer token without the marker is no caller")

	shared := embedPath(t, "orders", "asset-9", "", nil)
	assert.Equal(t, http.StatusUnauthorized, f.do("GET", shared, http.Header{"Origin": {"null"}}, "").Code,
		"a public share's copy has no reader, so an authenticated endpoint refuses it")
	public := embedPath(t, "open", "asset-9", "", nil)
	rec := f.do("GET", public, http.Header{"Origin": {"null"}}, "")
	assert.Equal(t, http.StatusOK, rec.Code, "and a public endpoint answers it")
	assert.Equal(t, "null", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestEmbed_NoKeyHonorsNoMarker(t *testing.T) {
	f := newFixture()
	path := embedPath(t, "open", "asset-9", "", nil)
	rec := f.do("OPTIONS", path, http.Header{"Origin": {"null"}, "Access-Control-Request-Method": {"GET"}}, "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
