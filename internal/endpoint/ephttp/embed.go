package ephttp

import (
	"net/http"
	"net/url"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
)

// embedded is the asset an endpoint embed marker on r says the platform
// served this endpoint's address into (#2457), "" for none. A request with
// one is answered from an asset's sandboxed frame, whose origin is "null",
// never with credentials; its caller is the viewer token beside it. Only the
// platform's own address honors one: a page origin has its own viewer.
func (h *Handler) embedded(r *http.Request, t *eprun.Target) string {
	token := r.URL.Query().Get(epform.EndpointEmbedParam)
	if token == "" || pageOf(r) != nil || len(h.ViewerKey) == 0 {
		return ""
	}
	asset, ok := epform.OpenEndpointEmbed(epform.EmbedKey(h.ViewerKey), t.Binding.Name, token, time.Now())
	if !ok {
		return ""
	}
	return asset
}

// handlerQuery is the request's query as a handler reads it: the embed marker
// and viewer token an asset's call carries (#2457) are the platform's, and
// never reach req["query"].
func handlerQuery(r *http.Request) url.Values {
	q := r.URL.Query()
	q.Del(epform.EndpointEmbedParam)
	q.Del(epform.EndpointViewerParam)
	return q
}
