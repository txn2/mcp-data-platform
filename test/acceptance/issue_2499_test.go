//go:build integration

package acceptance

import "testing"

// Issue #2499: with portal.login_redirect on, Sign out went back through the
// identity provider and signed the user in again: /portal/auth/logout never
// ran, so neither the session cookie nor the identity provider's session was
// ended. The browser case runs in ui/e2e/login-redirect against a replica
// started with the setting on (issue2242Start), signed in on the dev stack's
// Keycloak.
//
// Wire forms: save_asset with its content as a string, its one form; the
// browser's own navigations and a form post to Keycloak.

// TestIssue2499_SignOutEndsTheSessionUnderLoginRedirect signs in, clicks Sign
// Out, and asserts the logout route and the identity provider's end-session
// endpoint were both requested, the identity provider then asks for a sign-in,
// and the portal session is gone.
func TestIssue2499_SignOutEndsTheSessionUnderLoginRedirect(t *testing.T) {
	r := issue2242Start(t, true, true)
	issue2242Browser(t, r.base, "2499", "LOGIN_REDIRECT_ASSET_ID="+issue2242Asset(t))
}
