//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2359: an S3 connection's session_token and a
// session_login connection's sign-in headers are encrypted in the
// connection_instances row and redacted when the connection is read back.
//
// Wire forms: the admin routes take one JSON object; the database is read
// with one SELECT.

func TestIssue2359_SessionCredentialsAreEncryptedAndRedacted(t *testing.T) {
	c := connect(t)
	db := issue1870DB(t)
	stamp := strings.ToLower(unique1579())
	token := "FwoGZXIvYXdz-2359-" + stamp
	loginKey := "login-2359-" + stamp

	s3 := "acc-2359-s3-" + stamp
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/s3/"+s3, map[string]any{
		"config": map[string]any{
			"endpoint": issue1757S3Endpoint(), "region": "us-east-1", "use_path_style": true,
			"access_key_id": "dev-access-key", "secret_access_key": "dev-secret-key", "session_token": token,
		},
		"description": "Acceptance #2359",
	}); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the s3 connection answered %d", status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/s3/"+s3, http.NoBody) })

	api := "acc-2359-api-" + stamp
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+api, map[string]any{
		"config": map[string]any{
			"base_url": "http://127.0.0.1:9", "auth_mode": "session_login",
			"session_login_url": "/signin", "session_login_body": `{"user":"x","key":"{{secret}}"}`,
			"session_login_secret": "pw-2359", "session_token_source": "body:token", "session_token_header": "X-Session",
			"session_login_headers": map[string]any{"X-Login-Key": loginKey},
			"connect_timeout":       "2s", "call_timeout": "5s",
		},
		"description": "Acceptance #2359",
	}); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the session_login connection answered %d", status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+api, http.NoBody) })

	for kind, name := range map[string]string{"s3": s3, "api": api} {
		var stored string
		if err := db.QueryRow(`SELECT config::text FROM connection_instances WHERE kind = $1 AND name = $2`, kind, name).Scan(&stored); err != nil {
			t.Fatalf("reading %s/%s: %v", kind, name, err)
		}
		if strings.Contains(stored, token) || strings.Contains(stored, loginKey) {
			t.Errorf("%s/%s is stored with the credential in clear: %s", kind, name, stored)
		}
		status, read := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/"+kind+"/"+name, http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("reading %s/%s back answered %d", kind, name, status)
		}
		raw, _ := json.Marshal(read)
		if strings.Contains(string(raw), token) || strings.Contains(string(raw), loginKey) {
			t.Errorf("%s/%s is read back with the credential: %s", kind, name, raw)
		}
	}
}
