//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issue #2242: portal.login_redirect sends a signed-out portal visitor
// straight to the identity provider instead of the login screen, and
// portal.allow_api_key_login decides whether /portal/?login=api_key still
// offers the API key form.
//
// The dev stack keeps the setting off (dev/platform.yaml), so these criteria
// start replicas of their own with it on, beside the stack and on its
// database. Each replica is the same binary and configuration as the stack's
// with DEV_PORTAL_LOGIN_REDIRECT and DEV_PORTAL_ALLOW_API_KEY_LOGIN set. The
// identity provider is the stack's Keycloak; its callback is the stack's
// public address, which takes the sign-in a replica started, since the state
// cookie is scoped to the host and not the port.
//
// What the login screen does is decided in the browser, so criteria 1 to 6 run
// ui/e2e/login-redirect in Chromium against the replica and assert it passed.
//
// Wire forms: save_asset with its content as a string, its one form; GET of
// the public branding route, which takes no parameters; the browser's own
// navigations, a form post to Keycloak, and the API key sent by the login
// screen as X-API-Key.

const (
	issue2242IdPUser     = "analyst@example.com"
	issue2242IdPPassword = "analyst-password"
	// The dev stack's browser-session signing key and cookie name
	// (dev/platform.yaml, auth.browser_session).
	issue2242SigningKey = "ZGV2LWJyb3dzZXItc2Vzc2lvbi1obWFjLWtleS0zMmJ5dGVzLnBs"
	issue2242CookieName = "mcp_session"
)

// issue2242Replica is a platform process this file started.
type issue2242Replica struct {
	cmd  *exec.Cmd
	base string
	log  string
}

// issue2242Start starts a replica with the two settings and stops it when the
// test ends.
func issue2242Start(t *testing.T, redirect, allowKeys bool) *issue2242Replica {
	t.Helper()
	key := fmt.Sprintf("%t-%t", redirect, allowKeys)
	bin, err := issue2121Bin()
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../..")
	api, metrics := issue2121FreePort(t), issue2121FreePort(t)
	// The replica advertises the stack's address, as dev/start.sh's second
	// replica does: Keycloak calls back there, and the stack's address is
	// what it accepts. dev/.dev-ports.env records the stack's listen port
	// and not this one.
	public := issue2121Env("DEV_PUBLIC_API_PORT", issue2121Env("DEV_API_PORT", "8080"))
	env := append(os.Environ(), "DEV_API_PORT="+api, "DEV_PUBLIC_API_PORT="+public, "OTEL_METRICS_ADDR=127.0.0.1:"+metrics,
		"DEV_WEBHOOK_SPOOL_DIR="+t.TempDir(),
		fmt.Sprintf("DEV_PORTAL_LOGIN_REDIRECT=%t", redirect),
		fmt.Sprintf("DEV_PORTAL_ALLOW_API_KEY_LOGIN=%t", allowKeys),
		"AWS_CA_BUNDLE="+filepath.Join(root, "dev", ".tls", "seaweedfs", "public.crt"),
	)
	env = append(env, issue2121DotEnv(filepath.Join(root, ".env"))...)

	if err := os.MkdirAll(filepath.Join(root, "build", "2242"), 0o750); err != nil {
		t.Fatal(err)
	}
	r := &issue2242Replica{
		// localhost, not 127.0.0.1: the stack's public address is localhost, and
		// the OIDC state cookie set here has to reach its callback.
		base: "http://localhost:" + api,
		log:  filepath.Join(root, "build", "2242", "replica-"+key+"-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".log"),
	}
	logFile, err := os.Create(r.log)
	if err != nil {
		t.Fatal(err)
	}
	r.cmd = exec.Command(bin, "--config", filepath.Join(root, "dev", "platform.yaml"))
	r.cmd.Dir = root
	r.cmd.Env = env
	r.cmd.Stdout, r.cmd.Stderr = logFile, logFile
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = r.cmd.Wait(); _ = logFile.Close() }()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		res, err := http.Get(r.base + "/healthz") // #nosec G107 -- the replica this test started
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the replica did not answer within two minutes; its log is %s:\n%s", r.log, issue2121Tail(r.log))
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Cleanup(r.stop)
	return r
}

// stop ends the replica as a rollout does.
func (r *issue2242Replica) stop() {
	if r.cmd == nil || r.cmd.Process == nil {
		return
	}
	_ = r.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _, _ = r.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(45 * time.Second):
		_ = r.cmd.Process.Kill()
	}
	r.cmd = nil
}

// issue2242Branding reads the public branding route at base.
func issue2242Branding(t *testing.T, base string) map[string]any {
	t.Helper()
	res, err := http.Get(base + "/api/v1/admin/public/branding") // #nosec G107 -- a platform this suite reaches
	if err != nil {
		t.Fatalf("GET branding: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // read-only
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET branding: status %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decoding branding: %v", err)
	}
	return body
}

// issue2242Asset saves an asset whose portal page criterion 1 opens.
func issue2242Asset(t *testing.T) string {
	t.Helper()
	c := connect(t)
	out := c.call("save_asset", map[string]any{
		"name":         fmt.Sprintf("acceptance-2242-%d", time.Now().UnixNano()),
		"content":      "# Signed in\n\nAcceptance #2242.",
		"content_type": "text/markdown",
		"description":  "Acceptance #2242: an asset page opened while signed out.",
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id
}

// issue2242ExpiredSession is a session cookie the stack signed, for the
// Keycloak analyst, that expired an hour ago.
func issue2242ExpiredSession(t *testing.T) string {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(issue2242SigningKey)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": issue2242IdPUser, "email": issue2242IdPUser, "roles": []string{"analyst"},
		"iat": past.Unix(), "exp": past.Add(time.Hour).Unix(),
	}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// issue2242Browser runs one case of ui/e2e/login-redirect against base.
func issue2242Browser(t *testing.T, base, criterion string, extra ...string) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/login-redirect/playwright.config.ts",
		"--grep", "criterion "+criterion+":")
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(),
		"LOGIN_REDIRECT_BASE_URL="+base,
		"LOGIN_REDIRECT_IDP_USER="+issue2242IdPUser,
		"LOGIN_REDIRECT_IDP_PASSWORD="+issue2242IdPPassword,
		"LOGIN_REDIRECT_API_KEY="+devAPIKey(),
		"LOGIN_REDIRECT_COOKIE_NAME="+issue2242CookieName,
	)
	cmd.Env = append(cmd.Env, extra...)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright, criterion %s, against %s:\n%s", criterion, base, out)
	if err != nil {
		t.Fatalf("the browser case for criterion %s failed: %v", criterion, err)
	}
}

// TestIssue2242_ASignedOutAssetPageGoesToTheIdentityProvider is criterion 1.
func TestIssue2242_ASignedOutAssetPageGoesToTheIdentityProvider(t *testing.T) {
	r := issue2242Start(t, true, true)
	issue2242Browser(t, r.base, "1", "LOGIN_REDIRECT_ASSET_ID="+issue2242Asset(t))
}

// TestIssue2242_AnExpiredSessionGoesToTheIdentityProvider is criterion 2.
func TestIssue2242_AnExpiredSessionGoesToTheIdentityProvider(t *testing.T) {
	r := issue2242Start(t, true, true)
	issue2242Browser(t, r.base, "2", "LOGIN_REDIRECT_EXPIRED_SESSION="+issue2242ExpiredSession(t))
}

// TestIssue2242_ARefusedSignInIsNotRedirectedAgain is criterion 3.
func TestIssue2242_ARefusedSignInIsNotRedirectedAgain(t *testing.T) {
	r := issue2242Start(t, true, true)
	issue2242Browser(t, r.base, "3")
}

// TestIssue2242_TheAPIKeyFormStaysReachable is criterion 4.
func TestIssue2242_TheAPIKeyFormStaysReachable(t *testing.T) {
	r := issue2242Start(t, true, true)
	issue2242Browser(t, r.base, "4")
}

// TestIssue2242_TheAPIKeyFormCanBeTurnedOff is criterion 5.
func TestIssue2242_TheAPIKeyFormCanBeTurnedOff(t *testing.T) {
	r := issue2242Start(t, true, false)
	issue2242Browser(t, r.base, "5")
}

// TestIssue2242_WithTheSettingOffTheLoginScreenIsUnchanged is criterion 6,
// on the stack itself, which leaves the setting off.
func TestIssue2242_WithTheSettingOffTheLoginScreenIsUnchanged(t *testing.T) {
	issue2242Browser(t, baseURL(), "6", "LOGIN_REDIRECT_ASSET_ID="+issue2242Asset(t))
}

// TestIssue2242_TheBrandingRouteReportsTheSetting is criterion 7.
func TestIssue2242_TheBrandingRouteReportsTheSetting(t *testing.T) {
	cases := []struct {
		name               string
		base               func() string
		redirect, keysOpen bool
	}{
		{name: "the stack, setting off", base: baseURL, keysOpen: true},
		{name: "setting on", base: func() string { return issue2242Start(t, true, true).base }, redirect: true, keysOpen: true},
		{name: "setting on, key form off", base: func() string { return issue2242Start(t, true, false).base }, redirect: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := issue2242Branding(t, tc.base())
			if body["oidc_enabled"] != true {
				t.Fatalf("oidc_enabled is %v; the dev stack signs in through Keycloak", body["oidc_enabled"])
			}
			if body["login_redirect"] != tc.redirect {
				t.Errorf("login_redirect is %v, want %v", body["login_redirect"], tc.redirect)
			}
			if body["allow_api_key_login"] != tc.keysOpen {
				t.Errorf("allow_api_key_login is %v, want %v", body["allow_api_key_login"], tc.keysOpen)
			}
		})
	}
}
