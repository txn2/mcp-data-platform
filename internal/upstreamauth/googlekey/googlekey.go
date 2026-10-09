// Package googlekey reads a Google service account's JSON key file (#2061):
// the fields the jwt_bearer grant is filled from, the identity an
// administrator is shown in place of the redacted file, and the operator's
// next step for each of Google's refusals. It knows nothing about
// connections; internal/upstreamauth applies what it reads.
package googlekey

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
)

// ConfigKey is the connection-config key holding the whole key file. Encrypted
// at rest (pkg/platform/fieldcrypt) and read back as "[REDACTED]".
const ConfigKey = "google_service_account_json" // #nosec G101 -- map key, not a credential

// TokenURL is Google's OAuth token endpoint, the token_uri every key file
// Google issues carries. A connection whose key is a stored secret exchanges
// there unless it sets oauth_token_url, because the file is not read until the
// first call.
const TokenURL = "https://oauth2.googleapis.com/token" // #nosec G101 -- a public endpoint URL, not a credential

// keyType is the type field of a service account key file.
const keyType = "service_account"

// KeyFile is the part of a key file the platform reads.
type KeyFile struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

// Identity is what a key file says about whose key it is: nothing in it
// is secret, which is why the admin API reads it back beside the redacted file,
// so an administrator can tell which account and which key a connection uses.
type Identity struct {
	ClientEmail  string `json:"client_email"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
}

// Parse reads a key file. Every refusal names the field,
// never a value: the input is a private key.
func Parse(raw string) (KeyFile, error) {
	var sa KeyFile
	if err := json.Unmarshal([]byte(raw), &sa); err != nil {
		return KeyFile{}, errors.New("the Google service account key is not a JSON key file")
	}
	if sa.Type != keyType {
		return KeyFile{}, fmt.Errorf("the Google service account key file's type is %q, not %q; download a key for a service account, not an OAuth client",
			sa.Type, keyType)
	}
	for _, f := range []struct{ name, value string }{
		{"private_key", sa.PrivateKey}, {"client_email", sa.ClientEmail}, {"token_uri", sa.TokenURI},
	} {
		if strings.TrimSpace(f.value) == "" {
			return KeyFile{}, fmt.Errorf("the Google service account key file has no %s", f.name)
		}
	}
	return sa, nil
}

// Identity is the non-secret half of the key file.
func (sa KeyFile) Identity() Identity {
	return Identity{ClientEmail: sa.ClientEmail, ProjectID: sa.ProjectID, PrivateKeyID: sa.PrivateKeyID}
}

// IdentityOf reads the identity out of a connection config holding a key
// file, for the admin API to show beside the redacted value. ok is false when
// the config holds none, or a file that does not parse.
func IdentityOf(cfg map[string]any) (Identity, bool) {
	cfg, err := Normalize(cfg)
	if err != nil {
		return Identity{}, false
	}
	raw := cfgmap.String(cfg, ConfigKey)
	if raw == "" {
		return Identity{}, false
	}
	sa, err := Parse(raw)
	if err != nil {
		return Identity{}, false
	}
	return sa.Identity(), true
}

// Normalize returns cfg with google_service_account_json as a
// string. A JSON client may send the key file as the object it is rather than a
// string of JSON; both are accepted, and the object is written back as its
// text, because the at-rest encryption encrypts a string value and would store
// an object's private key as it came. cfg is returned unchanged when the key is
// absent or already a string, and a copy otherwise. A value of any other type
// is refused.
func Normalize(cfg map[string]any) (map[string]any, error) {
	raw, ok := cfg[ConfigKey]
	if !ok || raw == nil {
		return cfg, nil
	}
	switch v := raw.(type) {
	case string:
		return cfg, nil
	case map[string]any:
		text, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("the value of %s could not be read as a key file", ConfigKey)
		}
		out := maps.Clone(cfg)
		out[ConfigKey] = string(text)
		return out, nil
	default:
		return nil, fmt.Errorf("the value of %s must be the key file, as a JSON object or a string of JSON", ConfigKey)
	}
}

// TokenHint is the operator's next step for a refusal Google's token
// endpoint answers with, or "" when the refusal is not one of Google's. Each is
// fixed outside the platform, which is why it rides with the upstream's words.
func TokenHint(code, description string) string {
	d := strings.ToLower(description)
	switch {
	case code == "invalid_scope":
		return "check oauth_scope: each scope is a full URL, such as https://www.googleapis.com/auth/cloud-platform"
	case code == "invalid_grant" && strings.Contains(d, "short-lived"):
		return "the assertion's times are outside what Google accepts, which is clock skew: check the host clock and jwt_issued_at_skew"
	case code == "invalid_grant" && (strings.Contains(d, "signature") || strings.Contains(d, "not found") || strings.Contains(d, "disabled")):
		return "the key was deleted or disabled, or the service account was removed: create a new key, or check the account still exists in Google Cloud"
	}
	return ""
}

// apiError is the error body a Google API answers a refused call with.
type apiError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

// APIHint is the operator's next step for a Google API refusing a call a
// service-account connection made, or "" when the response is not one. A valid
// token is not access: the API must be enabled in the account's Cloud project,
// and the account granted inside the product. body is the decoded response.
func APIHint(status int, body any) string {
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return ""
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	var e apiError
	if json.Unmarshal(raw, &e) != nil {
		return ""
	}
	reasons := make([]string, 0, len(e.Error.Details))
	for _, d := range e.Error.Details {
		reasons = append(reasons, d.Reason)
	}
	msg := strings.ToLower(e.Error.Message)
	switch {
	case slices.Contains(reasons, "SERVICE_DISABLED"):
		return "the API is not enabled in the service account's Google Cloud project; enable it there (the error names the project and the API)"
	case e.Error.Status == "PERMISSION_DENIED" || strings.Contains(msg, "cannot get profile"):
		return "the token is valid but the service account has no access in this product; grant the service account's email access inside the product"
	}
	return ""
}
