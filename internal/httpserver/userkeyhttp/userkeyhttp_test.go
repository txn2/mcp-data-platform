package userkeyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/apikeyissue"
	"github.com/txn2/mcp-data-platform/internal/platform/apikeystore"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const (
	alice = "alice@example.com"
	bob   = "bob@example.com"
)

// fakeManager mints values and refreshes the copy. It refuses a name the
// inventory already holds, as the real one does.
type fakeManager struct {
	keys []auth.APIKeySummary
}

func (f *fakeManager) GenerateKey(def auth.APIKey) (string, error) {
	for _, k := range f.keys {
		if k.Name == def.Name {
			// The sentinel the real minter raises, which is what tells a name
			// already held from a shortage of entropy.
			return "", fmt.Errorf("api key name %q is already held: %w", def.Name, auth.ErrKeyNameTaken)
		}
	}
	return "generated-value", nil
}

func (*fakeManager) SyncHashedKeys(context.Context) error { return nil }

func (f *fakeManager) ListKeys() []auth.APIKeySummary { return f.keys }

// fakeStore records what was stored and hands the inventory the new row, so a
// create is visible to the listing that follows it.
type fakeStore struct {
	manager *fakeManager
	err     error
}

func (f *fakeStore) Create(_ context.Context, def apikeystore.Definition) error {
	if f.err != nil {
		return f.err
	}
	f.manager.keys = append(f.manager.keys, auth.APIKeySummary{
		Name:        def.Name,
		Email:       def.Email,
		Description: def.Description,
		Roles:       def.Roles,
		ExpiresAt:   def.ExpiresAt,
		UserEmail:   def.UserEmail,
	})
	return nil
}

// fakePrincipals knows the people the platform has seen sign in.
type fakePrincipals struct {
	people map[string]auth.BoundPrincipal
}

func (f *fakePrincipals) BoundPrincipal(_ context.Context, email string) (*auth.BoundPrincipal, error) {
	person, ok := f.people[email]
	if !ok {
		return nil, auth.ErrNoBoundPrincipal
	}
	return &person, nil
}

// newTestAPI wires the surface over fakes, authenticated as caller ("" for an
// unauthenticated request), and returns it with the mux it serves on.
func newTestAPI(caller string) (*API, http.Handler, *fakeManager) {
	return newTestAPIAs(caller, middleware.AuthTypeOIDC)
}

// newTestAPIAs is newTestAPI for a caller who authenticated some other way,
// which is what decides whether they may manage keys at all.
func newTestAPIAs(caller, callerAuthType string) (*API, http.Handler, *fakeManager) {
	manager := &fakeManager{}
	store := &fakeStore{manager: manager}
	api := &API{
		Issuer: &apikeyissue.Issuer{
			Manager: manager,
			Store:   store,
			Principals: &fakePrincipals{people: map[string]auth.BoundPrincipal{
				alice: {Subject: "sub-alice", Email: alice, Roles: []string{"analyst"}},
				bob:   {Subject: "sub-bob", Email: bob, Roles: []string{"viewer"}},
			}},
		},
		Keys: manager,
		Revoke: func(_ *http.Request, storedName string) error {
			for i, k := range manager.keys {
				if k.Name == storedName {
					manager.keys = append(manager.keys[:i], manager.keys[i+1:]...)
					return nil
				}
			}
			return ErrNoSuchKey
		},
		Refresh: func(*http.Request) {},
		Caller:  func(*http.Request) (string, string) { return caller, callerAuthType },
	}
	mux := http.NewServeMux()
	api.Register(mux)
	return api, mux, manager
}

// serve issues one request against the surface and returns the recorder.
func serve(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// decode reads a JSON object response.
func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %q: %v", w.Body.String(), err)
	}
	return out
}

// TestCreateIssuesAgainstTheCallersOwnAccount holds that a person's key is
// bound to them, carries their roles, and is stored under a name scoped to
// them -- which is what keeps two people's "laptop" apart.
func TestCreateIssuesAgainstTheCallersOwnAccount(t *testing.T) {
	_, mux, manager := newTestAPI(alice)

	w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"chatgpt","description":"desktop"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body)
	}
	body := decode(t, w)
	if body["key"] != "generated-value" {
		t.Errorf("key = %v, want the minted value", body["key"])
	}
	if body["name"] != "chatgpt" {
		t.Errorf("name = %v -- a person is shown the name they chose", body["name"])
	}
	roles, _ := body["roles"].([]any)
	if len(roles) != 1 || roles[0] != "analyst" {
		t.Errorf("roles = %v, want [analyst]: a key carries what its owner holds", roles)
	}

	if len(manager.keys) != 1 {
		t.Fatalf("stored %d keys, want 1", len(manager.keys))
	}
	stored := manager.keys[0]
	if stored.UserEmail != alice {
		t.Errorf("UserEmail = %q, want %q", stored.UserEmail, alice)
	}
	if stored.Name != "user:"+alice+":chatgpt" {
		t.Errorf("stored name = %q, want it scoped to its owner", stored.Name)
	}
}

// TestCreateRefusesAFieldTheRouteDoesNotHave holds that a person cannot widen
// their own key, or name whose key it is: the decoder refuses the field rather
// than quietly ignoring it.
func TestCreateRefusesAFieldTheRouteDoesNotHave(t *testing.T) {
	for _, body := range []string{
		`{"name":"k","roles":["admin"]}`,
		`{"name":"k","user_email":"` + bob + `"}`,
	} {
		_, mux, manager := newTestAPI(alice)
		w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, w.Code)
		}
		if len(manager.keys) != 0 {
			t.Errorf("%s: a key was issued", body)
		}
	}
}

// TestCreateChecksTheName holds the refusals for a name that cannot be stored
// or cannot be addressed back.
func TestCreateChecksTheName(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty", `{"name":""}`},
		{"whitespace only", `{"name":"   "}`},
		{"too long", `{"name":"` + strings.Repeat("x", maxNameLength+1) + `"}`},
		{"carries the separator", `{"name":"a:b"}`},
		{"carries a path separator", `{"name":"a/b"}`},
		{"carries a control character", `{"name":"a\u0000b"}`},
		{"not an object", `[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mux, manager := newTestAPI(alice)
			if w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", tt.body); w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", w.Code, w.Body)
			}
			if len(manager.keys) != 0 {
				t.Error("a key was issued under a name that cannot be stored")
			}
		})
	}
}

// TestCreateReadsTheExpiry holds that a lifetime is accepted and a nonsensical
// one refused.
func TestCreateReadsTheExpiry(t *testing.T) {
	_, mux, manager := newTestAPI(alice)

	if w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"k","expires_in":"720h"}`); w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body)
	}
	if manager.keys[0].ExpiresAt == nil {
		t.Error("the key was stored with no expiry")
	}
	if got := time.Until(*manager.keys[0].ExpiresAt); got < 700*time.Hour {
		t.Errorf("expiry is %v away, want about 720h", got)
	}

	for _, bad := range []string{`{"name":"a","expires_in":"soon"}`, `{"name":"b","expires_in":"-1h"}`} {
		if w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", bad, w.Code)
		}
	}
}

// TestCreateWithNoExpiryLastsUntilRevoked holds the default: a key a person
// makes for themselves has no end date unless they ask for one.
func TestCreateWithNoExpiryLastsUntilRevoked(t *testing.T) {
	_, mux, manager := newTestAPI(alice)
	if w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"k"}`); w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body)
	}
	if manager.keys[0].ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want none", manager.keys[0].ExpiresAt)
	}
}

// TestCreateRefusesADuplicateNameInTheCallersOwnWords holds that the conflict
// names the key the person chose, never the stored name they did not.
func TestCreateRefusesADuplicateName(t *testing.T) {
	_, mux, _ := newTestAPI(alice)
	if w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"chatgpt"}`); w.Code != http.StatusCreated {
		t.Fatalf("first create: %d %s", w.Code, w.Body)
	}
	w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"chatgpt"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if msg, _ := decode(t, w)["error"].(string); !strings.Contains(msg, `"chatgpt"`) || strings.Contains(msg, "user:") {
		t.Errorf("refusal = %q, want it to name only the key the person chose", msg)
	}
}

// TestTwoPeopleMayUseOneName holds what the scoped stored name is for.
func TestTwoPeopleMayUseOneName(t *testing.T) {
	_, aliceMux, manager := newTestAPI(alice)
	if w := serve(aliceMux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"laptop"}`); w.Code != http.StatusCreated {
		t.Fatalf("alice: %d %s", w.Code, w.Body)
	}

	// Bob's surface shares the inventory, as two people on one deployment do.
	bobAPI, bobMux, _ := newTestAPI(bob)
	bobAPI.Keys = manager
	bobAPI.Issuer.Manager = manager
	bobAPI.Issuer.Store = &fakeStore{manager: manager}
	if w := serve(bobMux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"laptop"}`); w.Code != http.StatusCreated {
		t.Fatalf("bob: %d %s -- one person's name must not take another's", w.Code, w.Body)
	}
	if len(manager.keys) != 2 {
		t.Fatalf("stored %d keys, want 2", len(manager.keys))
	}
}

// TestListHoldsOnlyTheCallersOwnKeys holds the self-scope of the listing.
func TestListHoldsOnlyTheCallersOwnKeys(t *testing.T) {
	aliceAPI, aliceMux, manager := newTestAPI(alice)
	if w := serve(aliceMux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"laptop"}`); w.Code != http.StatusCreated {
		t.Fatalf("alice create: %d", w.Code)
	}
	// A service key, and a key of somebody else's, share the inventory.
	manager.keys = append(manager.keys,
		auth.APIKeySummary{Name: "etl", Roles: []string{"service"}},
		auth.APIKeySummary{Name: "user:" + bob + ":laptop", UserEmail: bob})

	w := serve(aliceMux, http.MethodGet, "/api/v1/portal/api-keys", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := decode(t, w)
	if body["total"] != float64(1) {
		t.Fatalf("total = %v, want 1: %s", body["total"], w.Body)
	}
	keys, _ := body["keys"].([]any)
	entry, _ := keys[0].(map[string]any)
	if entry["name"] != "laptop" {
		t.Errorf("name = %v, want the label its owner chose", entry["name"])
	}
	_ = aliceAPI
}

// TestRevokeRemovesTheCallersOwnKey holds that a person revokes their own key
// by the name they gave it, and that revoking again is a 404.
func TestRevokeRemovesTheCallersOwnKey(t *testing.T) {
	_, mux, manager := newTestAPI(alice)
	if w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"laptop"}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}

	if w := serve(mux, http.MethodDelete, "/api/v1/portal/api-keys/laptop", ""); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	if len(manager.keys) != 0 {
		t.Errorf("the key survived the revoke: %+v", manager.keys)
	}
	if w := serve(mux, http.MethodDelete, "/api/v1/portal/api-keys/laptop", ""); w.Code != http.StatusNotFound {
		t.Errorf("revoking again: status = %d, want 404", w.Code)
	}
}

// TestRevokeCannotReachAnotherPersonsKey holds that the path names only the
// caller's own key: the stored name is composed from the authenticated
// identity, so another person's key has no spelling here that reaches it.
func TestRevokeCannotReachAnotherPersonsKey(t *testing.T) {
	_, mux, manager := newTestAPI(alice)
	manager.keys = append(manager.keys, auth.APIKeySummary{Name: "user:" + bob + ":laptop", UserEmail: bob})

	for _, path := range []string{
		"/api/v1/portal/api-keys/laptop",
		"/api/v1/portal/api-keys/" + "user:" + bob + ":laptop",
	} {
		if w := serve(mux, http.MethodDelete, path, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, w.Code)
		}
	}
	if len(manager.keys) != 1 {
		t.Error("another person's key was revoked")
	}
}

// TestUnauthenticatedIsRefusedOnEveryRoute holds that none of these answer
// without an authenticated caller.
func TestUnauthenticatedIsRefusedOnEveryRoute(t *testing.T) {
	_, mux, _ := newTestAPI("")
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/portal/api-keys", ""},
		{http.MethodPost, "/api/v1/portal/api-keys", `{"name":"k"}`},
		{http.MethodDelete, "/api/v1/portal/api-keys/k", ""},
	} {
		if w := serve(mux, call.method, call.path, call.body); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", call.method, call.path, w.Code)
		}
	}
}

// TestCreateStopsAtTheKeyLimit holds the bound on one person's key list.
func TestCreateStopsAtTheKeyLimit(t *testing.T) {
	_, mux, manager := newTestAPI(alice)
	for i := range maxKeysPerUser {
		manager.keys = append(manager.keys, auth.APIKeySummary{
			Name:      storedName(string(rune('a'+i%26))+string(rune('0'+i/26)), alice),
			UserEmail: alice,
		})
	}
	w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"one-too-many"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body)
	}
}

// TestStoreFailureIsNotReportedAsSomethingElse holds that a write that fails is
// a 500, not a conflict or a silent success.
func TestStoreFailureIsNotReportedAsSomethingElse(t *testing.T) {
	api, mux, manager := newTestAPI(alice)
	api.Issuer.Store = &fakeStore{manager: manager, err: errors.New("database unavailable")}

	if w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"k"}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body)
	}
}

// TestRevokeWithNoStoreIsRefused holds that a deployment that cannot revoke
// says so rather than reporting a key gone that is not.
func TestRevokeWithNoStoreIsRefused(t *testing.T) {
	api, mux, _ := newTestAPI(alice)
	api.Revoke = nil
	if w := serve(mux, http.MethodDelete, "/api/v1/portal/api-keys/k", ""); w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// TestRevokeReportsAFailedWrite holds that a store error on a key the caller
// does hold is a 500, distinct from the 404 that means it is not theirs.
func TestRevokeReportsAFailedWrite(t *testing.T) {
	api, mux, _ := newTestAPI(alice)
	if w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"k"}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	api.Revoke = func(*http.Request, string) error { return errors.New("database unavailable") }
	if w := serve(mux, http.MethodDelete, "/api/v1/portal/api-keys/k", ""); w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// TestListHoldsAnAdminIssuedKeyToo holds that a person sees every credential
// that authenticates as them, not only the ones they issued here, and that a
// key with no roles of its own is listed by what it actually carries rather
// than as carrying none.
func TestListHoldsAnAdminIssuedKeyToo(t *testing.T) {
	_, mux, manager := newTestAPI(alice)
	if w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"laptop"}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}
	// An administrator issued this one against the same account, under a name
	// of their own choosing.
	manager.keys = append(manager.keys, auth.APIKeySummary{Name: "nifi-ingest", UserEmail: alice})

	w := serve(mux, http.MethodGet, "/api/v1/portal/api-keys", "")
	body := decode(t, w)
	if body["total"] != float64(2) {
		t.Fatalf("total = %v, want 2: %s", body["total"], w.Body)
	}
	keys, _ := body["keys"].([]any)
	byName := map[string][]any{}
	for _, raw := range keys {
		entry, _ := raw.(map[string]any)
		name, _ := entry["name"].(string)
		roles, _ := entry["roles"].([]any)
		byName[name] = roles
	}
	for _, name := range []string{"laptop", "nifi-ingest"} {
		roles, ok := byName[name]
		if !ok {
			t.Fatalf("the list does not hold %q: %s", name, w.Body)
		}
		if len(roles) != 1 || roles[0] != "analyst" {
			t.Errorf("%s: roles = %v, want [analyst] -- a key with none of its own carries its owner's", name, roles)
		}
	}
}

// TestRevokeAnAdminIssuedKey holds that a person may withdraw a credential an
// administrator issued against their account: it is their identity.
func TestRevokeAnAdminIssuedKey(t *testing.T) {
	_, mux, manager := newTestAPI(alice)
	manager.keys = append(manager.keys, auth.APIKeySummary{Name: "nifi-ingest", UserEmail: alice})

	if w := serve(mux, http.MethodDelete, "/api/v1/portal/api-keys/nifi-ingest", ""); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	if len(manager.keys) != 0 {
		t.Errorf("the key survived: %+v", manager.keys)
	}
}

// TestServiceKeysAreNobodysOwn holds that a key bound to nobody never appears
// in anybody's list, however its name is spelled.
func TestServiceKeysAreNobodysOwn(t *testing.T) {
	_, mux, manager := newTestAPI(alice)
	manager.keys = append(manager.keys, auth.APIKeySummary{Name: "etl", Roles: []string{"service"}})

	w := serve(mux, http.MethodGet, "/api/v1/portal/api-keys", "")
	if decode(t, w)["total"] != float64(0) {
		t.Fatalf("a service key was listed as somebody's own: %s", w.Body)
	}
	if w := serve(mux, http.MethodDelete, "/api/v1/portal/api-keys/etl", ""); w.Code != http.StatusNotFound {
		t.Errorf("revoking a service key: status = %d, want 404", w.Code)
	}
	if len(manager.keys) != 1 {
		t.Error("a service key was revoked through a person's own route")
	}
}

// TestLabelReturnsAnUncomposedNameUnchanged holds that a listing renders a key
// stored under some other scheme without mangling it.
func TestLabelReturnsAnUncomposedNameUnchanged(t *testing.T) {
	if got := label("etl", alice); got != "etl" {
		t.Errorf("label = %q, want etl", got)
	}
	if got := label(storedName("laptop", alice), alice); got != "laptop" {
		t.Errorf("label = %q, want laptop", got)
	}
}

// TestListWithNoInventoryIsEmpty holds that a surface with nothing to read
// answers an empty list rather than failing.
func TestListWithNoInventoryIsEmpty(t *testing.T) {
	api, mux, _ := newTestAPI(alice)
	api.Keys = nil
	w := serve(mux, http.MethodGet, "/api/v1/portal/api-keys", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if decode(t, w)["total"] != float64(0) {
		t.Errorf("total = %v, want 0", decode(t, w)["total"])
	}
}

// TestAKeyMayNotManageKeys is the escalation this surface must refuse. Every
// route here acts on "this is the person, signed in"; an API key is not that.
//
// Two concrete paths it closes. A key an administrator issued against Alice's
// account with a narrower role set and a 24-hour life could otherwise mint
// itself a second key for Alice carrying her full roles and no expiry at all,
// discarding both limits. And a service key configured with Alice's address
// could list, issue and revoke Alice's keys outright.
func TestAKeyMayNotManageKeys(t *testing.T) {
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/portal/api-keys", ""},
		{http.MethodPost, "/api/v1/portal/api-keys", `{"name":"escalate"}`},
		{http.MethodDelete, "/api/v1/portal/api-keys/laptop", ""},
	} {
		_, mux, manager := newTestAPIAs(alice, middleware.AuthTypeAPIKey)
		manager.keys = append(manager.keys, auth.APIKeySummary{
			Name: storedName("laptop", alice), UserEmail: alice,
		})

		w := serve(mux, call.method, call.path, call.body)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", call.method, call.path, w.Code)
		}
		if len(manager.keys) != 1 {
			t.Errorf("%s %s: the inventory changed", call.method, call.path)
		}
	}
}

// TestASignedInSessionMayManageKeys is the other side of that check: the rule
// is about how the caller authenticated, and an ordinary session is unaffected.
func TestASignedInSessionMayManageKeys(t *testing.T) {
	for _, authType := range []string{middleware.AuthTypeOIDC, middleware.AuthTypeOAuth} {
		_, mux, _ := newTestAPIAs(alice, authType)
		if w := serve(mux, http.MethodPost, "/api/v1/portal/api-keys", `{"name":"laptop"}`); w.Code != http.StatusCreated {
			t.Errorf("%s: status = %d, want 201: %s", authType, w.Code, w.Body)
		}
	}
}
