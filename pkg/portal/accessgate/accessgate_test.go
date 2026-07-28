package accessgate_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/accessgate"
)

const (
	analystRole  = "analyst"
	deniedEmail  = "nobody@example.com"
	acceptHTML   = "text/html,application/xhtml+xml"
	problemMedia = "application/problem+json"
)

// mappedResolver maps exactly the analyst role to a persona and nothing else.
func mappedResolver(roles []string) *portal.PersonaInfo {
	if slices.Contains(roles, analystRole) {
		return &portal.PersonaInfo{Name: "analyst", Tools: []string{"search"}}
	}
	return nil
}

// reached is a terminal handler that records whether the gate let a request
// through, so a test can tell "allowed" from "denied with a 200-shaped body".
type reached struct{ hit bool }

func (h *reached) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.hit = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("protected"))
}

func newGate() *accessgate.Gate {
	return accessgate.New(mappedResolver, accessgate.Brand{Name: "ACME Data"})
}

// requestAs builds a request already carrying an authenticated portal user, the
// state RequirePortalAuth leaves behind for the gate to judge.
func requestAs(t *testing.T, method, accept string, user *portal.User) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, "/api/v1/portal/knowledge-pages", http.NoBody)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	if user != nil {
		r = r.WithContext(portal.ContextWithUser(r.Context(), user))
	}
	return r
}

func TestRequire_AllowsMappedRoles(t *testing.T) {
	next := &reached{}
	rec := httptest.NewRecorder()

	newGate().Require(next).ServeHTTP(rec, requestAs(t, http.MethodGet, "", &portal.User{
		UserID: "u1", Email: "a@example.com", Roles: []string{analystRole},
	}))

	if !next.hit {
		t.Fatal("a caller whose role maps to a persona was refused")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// The bug this gate exists for: an account the identity provider authenticates
// but that carries no role an operator ever granted.
func TestRequire_DeniesUnmappedRoles(t *testing.T) {
	for _, roles := range [][]string{nil, {}, {"some-unrelated-role"}} {
		next := &reached{}
		rec := httptest.NewRecorder()

		newGate().Require(next).ServeHTTP(rec, requestAs(t, http.MethodGet, "", &portal.User{
			UserID: "u2", Email: deniedEmail, Roles: roles,
		}))

		if next.hit {
			t.Errorf("roles %v reached the protected handler", roles)
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("roles %v: status = %d, want %d", roles, rec.Code, http.StatusForbidden)
		}
	}
}

// A missing user means the gate was composed outside its authentication
// middleware, or auth was skipped. Either way it must not fall through.
func TestRequire_DeniesWhenNoUserOnContext(t *testing.T) {
	next := &reached{}
	rec := httptest.NewRecorder()

	newGate().Require(next).ServeHTTP(rec, requestAs(t, http.MethodGet, "", nil))

	if next.hit {
		t.Error("a request with no authenticated user reached the protected handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// A gate that cannot evaluate access refuses rather than admits.
func TestRequire_NilResolverDeniesEveryone(t *testing.T) {
	next := &reached{}
	rec := httptest.NewRecorder()

	gate := accessgate.New(nil, accessgate.Brand{})
	gate.Require(next).ServeHTTP(rec, requestAs(t, http.MethodGet, "", &portal.User{
		UserID: "u3", Roles: []string{analystRole},
	}))

	if next.hit {
		t.Error("a gate with no resolver admitted a caller")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestAllows(t *testing.T) {
	gate := newGate()
	if !gate.Allows([]string{analystRole}) {
		t.Error("Allows() = false for a mapped role")
	}
	if gate.Allows([]string{"other"}) {
		t.Error("Allows() = true for an unmapped role")
	}
	if gate.Allows(nil) {
		t.Error("Allows() = true for no roles")
	}
	var nilGate *accessgate.Gate
	if nilGate.Allows([]string{analystRole}) {
		t.Error("a nil gate allowed a caller")
	}
}

// A browser navigation gets the branded page naming the refused account, so the
// person reading it knows which account to ask an administrator to grant.
func TestDeny_BrowserNavigationGetsBrandedPage(t *testing.T) {
	rec := httptest.NewRecorder()
	newGate().Deny(rec, requestAs(t, http.MethodGet, acceptHTML, nil), deniedEmail)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("branded page served without a Content-Security-Policy")
	}
	body := rec.Body.String()
	for _, want := range []string{deniedEmail, "ACME Data", "/portal/auth/logout"} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
}

// The SPA and API clients get a parseable body, not a page: the SPA reads the
// 403 to render its own refusal instead of bouncing back to the login form.
func TestDeny_NonNavigationGetsProblemDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	newGate().Deny(rec, requestAs(t, http.MethodGet, "application/json", nil), deniedEmail)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if ct := rec.Header().Get("Content-Type"); ct != problemMedia {
		t.Errorf("Content-Type = %q, want %q", ct, problemMedia)
	}
	var body struct {
		Status int    `json:"status"`
		Detail string `json:"detail"`
		Email  string `json:"email"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding problem details: %v", err)
	}
	if body.Status != http.StatusForbidden {
		t.Errorf("body status = %d, want %d", body.Status, http.StatusForbidden)
	}
	if body.Email != deniedEmail {
		t.Errorf("body email = %q, want %q", body.Email, deniedEmail)
	}
	if body.Detail == "" {
		t.Error("problem details carried no detail")
	}
}

// A POST that merely advertises text/html is not a navigation; answering it
// with a page would hand an HTML body to a client expecting JSON.
func TestDeny_NonGETWithHTMLAcceptGetsProblemDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	newGate().Deny(rec, requestAs(t, http.MethodPost, acceptHTML, nil), deniedEmail)

	if ct := rec.Header().Get("Content-Type"); ct != problemMedia {
		t.Errorf("Content-Type = %q, want %q", ct, problemMedia)
	}
}

// The page must render for a caller the server could not name an address for.
func TestDeny_PageWithoutEmail(t *testing.T) {
	rec := httptest.NewRecorder()
	newGate().Deny(rec, requestAs(t, http.MethodGet, acceptHTML, nil), "")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if strings.Contains(rec.Body.String(), "Signed in as") {
		t.Error("page showed a Signed in as row with no address to name")
	}
}

// An unbranded deployment still gets a titled page rather than an empty header.
func TestDeny_DefaultBrandName(t *testing.T) {
	rec := httptest.NewRecorder()
	accessgate.New(mappedResolver, accessgate.Brand{}).
		Deny(rec, requestAs(t, http.MethodGet, acceptHTML, nil), deniedEmail)

	if !strings.Contains(rec.Body.String(), "MCP Data Platform") {
		t.Error("unbranded page is missing the default brand name")
	}
}

// The implementor chrome renders when configured, matching the share-guest page.
func TestDeny_RendersImplementorChrome(t *testing.T) {
	rec := httptest.NewRecorder()
	accessgate.New(mappedResolver, accessgate.Brand{
		Name:            "ACME Data",
		ImplementorName: "ACME Consulting",
		ImplementorURL:  "https://consulting.example.com",
	}).Deny(rec, requestAs(t, http.MethodGet, acceptHTML, nil), deniedEmail)

	body := rec.Body.String()
	for _, want := range []string{"ACME Consulting", "https://consulting.example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
}
