package googlekey

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const testEmail = "reporting@acme-analytics.iam.gserviceaccount.com"

func keyFile() map[string]any {
	return map[string]any{
		"type": "service_account", "project_id": "acme-analytics", "private_key_id": "k1",
		"private_key":  "-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----\n",
		"client_email": testEmail, "token_uri": TokenURL,
	}
}

func text(t *testing.T, m map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestParse(t *testing.T) {
	kf, err := Parse(text(t, keyFile()))
	if err != nil {
		t.Fatal(err)
	}
	if kf.Identity() != (Identity{ClientEmail: testEmail, ProjectID: "acme-analytics", PrivateKeyID: "k1"}) {
		t.Fatalf("identity = %+v", kf.Identity())
	}
	without := func(key string) string {
		m := keyFile()
		delete(m, key)
		return text(t, m)
	}
	client := keyFile()
	client["type"] = "authorized_user"
	for name, tc := range map[string]struct{ raw, want string }{
		"not JSON":          {"-----BEGIN PRIVATE KEY-----", "is not a JSON key file"},
		"an OAuth client":   {text(t, client), `type is "authorized_user", not "service_account"`},
		"no private key":    {without("private_key"), "has no private_key"},
		"no client email":   {without("client_email"), "has no client_email"},
		"no token endpoint": {without("token_uri"), "has no token_uri"},
	} {
		_, err := Parse(tc.raw)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
			continue
		}
		if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), testEmail) {
			t.Errorf("%s: the refusal repeats the file: %v", name, err)
		}
	}
}

// The key file arrives as a string of JSON or as the object it is; Normalize
// writes the object back as text, the form the at-rest encryption encrypts.
func TestNormalizeAndIdentityOf(t *testing.T) {
	cfg := map[string]any{ConfigKey: keyFile()}
	got, err := Normalize(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := got[ConfigKey].(string); !ok || !strings.Contains(s, testEmail) {
		t.Fatalf("the object was not written back as text: %T", got[ConfigKey])
	}
	if _, isMap := cfg[ConfigKey].(map[string]any); !isMap {
		t.Fatal("the caller's map was changed")
	}
	for _, form := range []map[string]any{cfg, got} {
		if id, ok := IdentityOf(form); !ok || id.ClientEmail != testEmail {
			t.Fatalf("no identity from %T", form[ConfigKey])
		}
	}
	for _, unchanged := range []map[string]any{{}, {ConfigKey: nil}, {ConfigKey: "x"}} {
		if out, err := Normalize(unchanged); err != nil || len(out) != len(unchanged) {
			t.Fatalf("%v -> %v, %v", unchanged, out, err)
		}
	}
	if _, err := Normalize(map[string]any{ConfigKey: 42}); err == nil {
		t.Fatal("a number was accepted as a key file")
	}
	for _, cfg := range []map[string]any{{}, {ConfigKey: "nope"}, {ConfigKey: 42}} {
		if _, ok := IdentityOf(cfg); ok {
			t.Errorf("an identity was read from %v", cfg)
		}
	}
}

func TestTokenHint(t *testing.T) {
	for _, tc := range []struct{ code, desc, want string }{
		{"invalid_scope", "", "check oauth_scope"},
		{"invalid_grant", "Invalid JWT Signature.", "create a new key"},
		{"invalid_grant", "Account not found", "check the account still exists"},
		{"invalid_grant", "Invalid JWT: Token must be a short-lived token (60 minutes)", "check the host clock"},
		{"invalid_grant", "user hasn't approved this consumer", ""},
		{"invalid_client", "", ""},
	} {
		if got := TokenHint(tc.code, tc.desc); (tc.want == "" && got != "") || !strings.Contains(got, tc.want) {
			t.Errorf("TokenHint(%q, %q) = %q, want %q", tc.code, tc.desc, got, tc.want)
		}
	}
}

func TestAPIHint(t *testing.T) {
	decode := func(raw string) any {
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	disabled := decode(`{"error":{"code":403,"message":"Display & Video 360 API has not been used in project 1234567890 before or it is disabled.","status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED"}]}}`)
	denied := decode(`{"error":{"code":403,"message":"The caller does not have permission","status":"PERMISSION_DENIED"}}`)
	profile := decode(`{"error":{"code":401,"message":"Cannot get profile for this user.","status":"UNAUTHENTICATED"}}`)
	for name, tc := range map[string]struct {
		status int
		body   any
		want   string
	}{
		"an API not enabled":     {http.StatusForbidden, disabled, "not enabled in the service account's Google Cloud project"},
		"no access in product":   {http.StatusForbidden, denied, "grant the service account's email access"},
		"no Bid Manager profile": {http.StatusUnauthorized, profile, "grant the service account's email access"},
		"a success":              {http.StatusOK, denied, ""},
		"not Google's shape":     {http.StatusForbidden, decode(`{"message":"forbidden"}`), ""},
		"a body that is text":    {http.StatusForbidden, "forbidden", ""},
		"an unmarshalable body":  {http.StatusForbidden, func() {}, ""},
	} {
		if got := APIHint(tc.status, tc.body); (tc.want == "" && got != "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: hint = %q, want %q", name, got, tc.want)
		}
	}
}
