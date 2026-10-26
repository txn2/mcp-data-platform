package epform

import (
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// ViewerLabel domain-separates the viewer token key from the form token key
// it is derived from.
const ViewerLabel = "endpoint-form-viewer-v1"

// ViewerHeader carries a viewer token on a request to a form.
const ViewerHeader = "X-Form-Viewer"

// ViewerParam is the query parameter a form's page is handed a viewer token
// in; the page sends it on as ViewerHeader and does not pass it to the form.
const ViewerParam = "form_viewer"

// ViewerTTL is how long a viewer token is honored after the asset it was
// written into was opened.
const ViewerTTL = 12 * time.Hour

// Viewer is who a signed-in person was when an asset embedding a form was
// opened for them (#2177). A form inside an asset is framed with the asset's
// sandbox, whose opaque origin carries no session, so the platform writes
// this, signed, into the form's address as it serves the asset, and an
// authenticated form takes it as the caller.
type Viewer struct {
	UserID   string `json:"u"`
	Email    string `json:"m"`
	AuthType string `json:"t,omitempty"`
	Persona  string `json:"p,omitempty"`
	Admin    bool   `json:"a,omitempty"`
}

// viewerBody is what a viewer token carries, signed.
type viewerBody struct {
	Endpoint string `json:"e"`
	Viewer   Viewer `json:"v"`
	Expires  int64  `json:"x"`
}

// ViewerKey is the viewer token key, derived from the form token key; nil
// when that is nil.
func ViewerKey(formKey []byte) []byte {
	if len(formKey) == 0 {
		return nil
	}
	return shareguest.DeriveKey(formKey, ViewerLabel)
}

// SignViewer is the viewer token for v on endpoint, honored until now plus
// ViewerTTL.
func SignViewer(key []byte, endpoint string, v Viewer, now time.Time) (string, error) {
	if len(key) == 0 {
		return "", errNoViewerKey
	}
	payload, err := json.Marshal(viewerBody{Endpoint: endpoint, Viewer: v, Expires: now.Add(ViewerTTL).Unix()})
	if err != nil {
		return "", fmt.Errorf("encoding the viewer token: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(mac(key, body)), nil
}

// OpenViewer is the viewer a token carries, or ErrRefused: altered, signed
// with another key, issued for another endpoint, or expired.
func OpenViewer(key []byte, endpoint, token string, now time.Time) (Viewer, error) {
	if len(key) == 0 {
		return Viewer{}, errNoViewerKey
	}
	vb, err := readViewer(key, token)
	if err != nil {
		return Viewer{}, err
	}
	switch {
	case vb.Endpoint != endpoint:
		return Viewer{}, refused("its viewer token was issued for another form")
	case now.Unix() > vb.Expires:
		return Viewer{}, refused("its viewer token has expired; open the page again")
	case vb.Viewer.UserID == "" && vb.Viewer.Email == "":
		return Viewer{}, refused("its viewer token names nobody")
	}
	return vb.Viewer, nil
}

// readViewer is what a viewer token signed with key carries, or the refusal
// of one altered, signed with another key, or unreadable.
func readViewer(key []byte, token string) (viewerBody, error) {
	body, sig, ok := strings.Cut(token, ".")
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if !ok || len(token) > maxTokenLen || err != nil || !hmac.Equal(got, mac(key, body)) {
		return viewerBody{}, refused("its viewer token is not one this platform issued, or was changed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return viewerBody{}, refused("its viewer token cannot be read")
	}
	var vb viewerBody
	if err := json.Unmarshal(payload, &vb); err != nil {
		return viewerBody{}, refused("its viewer token cannot be read")
	}
	return vb, nil
}

var errNoViewerKey = refused("this deployment cannot sign a viewer token: it needs auth.browser_session.signing_key")
