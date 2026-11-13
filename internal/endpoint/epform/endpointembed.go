package epform

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

// Endpoint embeds (#2457). An asset declares mcp:endpoint:<name>, and every
// viewing surface rewrites it to the endpoint's address carrying two query
// parameters: an embed marker, which says the platform wrote that address
// into an asset it served and lets the asset's sandboxed frame (whose origin
// is "null") call the endpoint, never with credentials; and for a signed-in
// reader their viewer token, the caller an authenticated endpoint runs for.
// Neither reaches the handler.
const (
	EndpointEmbedParam  = "endpoint_embed"
	EndpointViewerParam = "endpoint_viewer"
)

// endpointEmbedKind marks an endpoint embed marker, so a form's marker never
// opens as one and one never opens as a form's.
const endpointEmbedKind = "endpoint"

// endpointEmbedBody is what an endpoint embed marker carries, signed: the
// endpoint, the asset it was written into, and when it stops being honored.
type endpointEmbedBody struct {
	Kind     string `json:"k"`
	Endpoint string `json:"e"`
	Asset    string `json:"a"`
	Expires  int64  `json:"x"`
}

// SignEndpointEmbed is the embed marker for endpoint written into asset,
// honored until now plus EmbedTTL; "" without a key.
func SignEndpointEmbed(key []byte, endpoint, asset string, now time.Time) string {
	if len(key) == 0 {
		return ""
	}
	payload, err := json.Marshal(endpointEmbedBody{Kind: endpointEmbedKind, Endpoint: endpoint, Asset: asset, Expires: now.Add(EmbedTTL).Unix()})
	if err != nil {
		return ""
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(mac(key, body))
}

// OpenEndpointEmbed is the asset an endpoint embed marker signed with key for
// endpoint names, and whether it is one and not expired.
func OpenEndpointEmbed(key []byte, endpoint, token string, now time.Time) (string, bool) {
	if len(key) == 0 || token == "" || len(token) > maxTokenLen {
		return "", false
	}
	payload, ok := verified(key, token)
	if !ok {
		return "", false
	}
	var b endpointEmbedBody
	if json.Unmarshal(payload, &b) != nil || b.Kind != endpointEmbedKind || b.Endpoint != endpoint || now.Unix() > b.Expires {
		return "", false
	}
	return b.Asset, true
}
