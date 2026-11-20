//go:build integration

package acceptance

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2727: every answer to a signed-in portal caller names the
// build that served it, as the version and commit --version reports (read here
// from the admin system route, which reports the same two values), so an open
// portal page can tell a new build from its own; an anonymous caller is not
// told it.
//
// Wire forms: GET /api/v1/admin/system/info and GET /api/v1/portal/me with
// the administrator's bearer key and no body; GET /api/v1/portal/me and
// GET /portal/view/<unknown token> with no credential at all.

// issue2727Get issues a request and returns its status, the build header, the
// replica that answered and the decoded body.
func issue2727Get(t *testing.T, path, key string) (code int, build, instance string, body map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL()+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &body) //nolint:errcheck // a non-object body leaves body nil
	return res.StatusCode, res.Header.Get("X-Platform-Build"), res.Header.Get("X-Platform-Instance"), body
}

// TestIssue2727_EveryPortalAnswerNamesTheBuild: a signed-in caller's portal
// answer carries X-Platform-Build, equal to the version and commit the
// replica that answered reports for itself. Replicas are compared one at a
// time, by X-Platform-Instance, since two requests through the proxy may reach
// two processes.
func TestIssue2727_EveryPortalAnswerNamesTheBuild(t *testing.T) {
	builds := map[string]string{}
	for range 12 {
		code, _, instance, info := issue2727Get(t, "/api/v1/admin/system/info", devAPIKey())
		if code != http.StatusOK || instance == "" {
			t.Fatalf("system info: %d from %q %v", code, instance, info)
		}
		version, _ := info["version"].(string)
		commit, _ := info["commit"].(string)
		want := version
		if commit != "" && commit != "none" {
			want = version + "+" + commit
		}
		builds[instance] = want
	}
	seen := map[string]bool{}
	for range 12 {
		code, got, instance, _ := issue2727Get(t, "/api/v1/portal/me", devAPIKey())
		if code != http.StatusOK {
			t.Fatalf("GET /api/v1/portal/me: %d", code)
		}
		want, ok := builds[instance]
		if !ok {
			continue
		}
		seen[instance] = true
		if got != want {
			t.Fatalf("replica %s answered X-Platform-Build %q; it reports itself as %q", instance, got, want)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no portal answer came from a replica whose build was read")
	}
}

// TestIssue2727_AnAnonymousCallerIsNotToldTheBuild: an answer to a caller who
// is not signed in, the portal API's refusal and a public share's page among
// them, names no build.
func TestIssue2727_AnAnonymousCallerIsNotToldTheBuild(t *testing.T) {
	for _, path := range []string{"/api/v1/portal/me", "/portal/view/" + strings.Repeat("0", 64)} {
		code, got, _, _ := issue2727Get(t, path, "")
		if got != "" {
			t.Errorf("GET %s with no credential (%d) named the build %q", path, code, got)
		}
	}
}
