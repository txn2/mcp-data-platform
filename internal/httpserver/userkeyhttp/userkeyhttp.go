// Package userkeyhttp serves the self-scoped API key routes a person manages
// their own keys through (#1759).
//
// It registers onto the portal's authenticated mux through a registrar hook
// (the notifyhttp pattern) rather than owning a server of its own, and it is
// server-side self-scoped: the authenticated caller's address is the only
// account it ever issues a key against, lists keys for, or revokes a key of.
// A person cannot name whose key they are making, and cannot widen one -- a key
// they issue authenticates as them and carries the roles they hold, which is
// what makes it the same identity as their signed-in session rather than a
// second one they could grant themselves more through.
//
// The list is every key that authenticates as the caller, whether they issued
// it here or an administrator issued it against their account: seeing every
// credential that can act as you is the honest answer, and withdrawing one is
// theirs to do. Scoping is by ownership, checked against the inventory on every
// read and every revoke, so a name that is not theirs is not found.
//
// Key names are unique across the deployment, so one a person chooses here is
// stored under a name scoped to them: two people may both call a key "laptop",
// and neither learns the other's exists.
package userkeyhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/apikeyissue"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// logKeyError is the structured-logging key for an error value.
const logKeyError = "error"

// maxKeysPerUser bounds how many keys one person may hold at once. It is not a
// security boundary -- every one of them authenticates as the same person, so
// holding ten is no more access than holding one -- but an unbounded list is a
// page nobody can read and a bcrypt comparison per key on every refused token.
const maxKeysPerUser = 25

// maxNameLength bounds a key name. It is a label in a list, not a document.
const maxNameLength = 60

// namePrefix opens the stored name of every key a person issued for themselves.
// It is the issuing core's, so the surface that composes such a name and the
// one that refuses to let a key bound to nobody impersonate it agree.
const namePrefix = apikeyissue.SelfIssuedPrefix

// ErrNoSuchKey is what Revoke reports for a key the store does not hold. The
// composition root translates the store's own sentinel into it, so this surface
// answers 404 without naming the store it asked.
var ErrNoSuchKey = errors.New("no such key")

// KeyLister is what this surface reads to answer a person's own key list.
// Implemented by *auth.APIKeyAuthenticator.
type KeyLister interface {
	ListKeys() []auth.APIKeySummary
}

// API serves the self-scoped key routes.
type API struct {
	// Issuer mints and stores a key. It is the same core the admin route
	// issues through, so a key a person makes for themselves comes into being
	// by the same rules.
	Issuer *apikeyissue.Issuer
	// Keys is the deployment's key inventory, read to list a person's own.
	Keys KeyLister
	// Revoke removes a key from the store by its stored name, reporting
	// ErrNoSuchKey when the store holds none.
	Revoke func(r *http.Request, storedName string) error
	// Sync brings this replica's copy up to the key store before it is read.
	// Without it a key another replica wrote a moment ago is absent here, and
	// its owner is told they have no such key (#1715).
	Sync func(r *http.Request)
	// Refresh brings this replica's copy up to the store and tells the others,
	// so a revoked key stops authenticating everywhere at once.
	Refresh func(r *http.Request)
	// Caller resolves the authenticated caller: the address they are known by,
	// and how they authenticated. Both are needed -- the address says whose
	// keys these are, and the auth type says whether they may manage keys at
	// all. It answers "" for an unauthenticated request.
	Caller func(*http.Request) (email, authType string)
}

// keyResponse is one of the caller's keys. It never carries a key value: the
// value exists once, in the create response.
type keyResponse struct {
	// Name is the name the person gave the key, not the name it is stored
	// under. They chose it and it is theirs; the scoping is the platform's
	// business.
	Name        string     `json:"name" example:"chatgpt"`
	Description string     `json:"description,omitempty" example:"ChatGPT desktop"`
	Roles       []string   `json:"roles" example:"analyst"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Expired     bool       `json:"expired,omitempty" example:"false"`
}

// listResponse wraps the caller's keys.
type listResponse struct {
	Keys  []keyResponse `json:"keys"`
	Total int           `json:"total" example:"2"`
}

// createRequest is the body for issuing a key for oneself.
//
// There is deliberately no roles field and no field naming whose key this is. A
// key issued here authenticates as its maker and carries the roles they hold,
// so a request that tried to name either would be asking for something the
// route does not do -- and the decoder refuses an unknown field rather than
// quietly ignoring one.
type createRequest struct {
	Name        string `json:"name" example:"chatgpt"`
	Description string `json:"description,omitempty" example:"ChatGPT desktop"`
	// ExpiresIn ends the key after a Go duration (e.g. "720h"). Left out, the
	// key lasts until its owner revokes it.
	ExpiresIn string `json:"expires_in,omitempty" example:"720h"`
}

// createResponse carries the new key, the only time its value is readable.
type createResponse struct {
	Name        string     `json:"name" example:"chatgpt"`
	Description string     `json:"description,omitempty" example:"ChatGPT desktop"`
	Key         string     `json:"key" example:"3f9a1c07e2b84d56a0c3e1f7b9d2468ace13579bdf02468ace13579bdf024681"`
	Roles       []string   `json:"roles" example:"analyst"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Warning     string     `json:"warning" example:"Store this key securely. It will not be shown again."`
}

// Register mounts the self-scoped key routes on mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/portal/api-keys", a.list)
	mux.HandleFunc("POST /api/v1/portal/api-keys", a.create)
	mux.HandleFunc("DELETE /api/v1/portal/api-keys/{name}", a.revoke)
}

// list handles GET /api/v1/portal/api-keys.
//
// @Summary      List my API keys
// @Description  Returns the API keys the calling user has issued for themselves. Key values are never included; a value is readable only in the create response. Server-side self-scope: another person's keys are never listed.
// @Tags         API Keys
// @Produce      json
// @Success      200  {object}  listResponse
// @Failure      401  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/api-keys [get]
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	email := a.callerEmail(w, r)
	if email == "" {
		return
	}
	a.syncKeys(r)
	mine := a.mine(email)
	// A key with no roles of its own carries whatever its owner holds, so the
	// list says so rather than "none" -- which would be the opposite of true
	// for every key issued here.
	owned := a.ownerRoles(r, email)
	out := make([]keyResponse, 0, len(mine))
	for _, k := range mine {
		roles := k.Roles
		if len(roles) == 0 {
			roles = owned
		}
		out = append(out, keyResponse{
			Name:        label(k.Name, email),
			Description: k.Description,
			Roles:       roles,
			ExpiresAt:   k.ExpiresAt,
			Expired:     k.Expired,
		})
	}
	writeJSON(w, http.StatusOK, listResponse{Keys: out, Total: len(out)})
}

// create handles POST /api/v1/portal/api-keys.
//
// @Summary      Issue an API key for myself
// @Description  Issues an API key bound to the calling user's own account. The key authenticates as them and carries the roles they hold, so a client that speaks only bearer tokens reaches the platform as the same identity their signed-in session does. The key value is shown once and never again. The request takes no roles: a person cannot widen their own key.
// @Tags         API Keys
// @Accept       json
// @Produce      json
// @Param        request  body  createRequest  true  "The key to issue"
// @Success      201  {object}  createResponse
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/api-keys [post]
func (a *API) create(w http.ResponseWriter, r *http.Request) {
	email := a.callerEmail(w, r)
	if email == "" {
		return
	}
	req, ok := decodeCreate(w, r)
	if !ok {
		return
	}
	name, ok := validName(w, req.Name)
	if !ok {
		return
	}
	expiresAt, ok := parseExpiry(w, req.ExpiresIn)
	if !ok {
		return
	}
	a.syncKeys(r)
	if len(a.mine(email)) >= maxKeysPerUser {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"you already hold %d API keys, which is the most one account may hold. Revoke one to issue another.",
			maxKeysPerUser))
		return
	}

	issued, err := a.Issuer.Issue(r.Context(), apikeyissue.Request{
		Name:        storedName(name, email),
		Description: req.Description,
		UserEmail:   email,
		ExpiresAt:   expiresAt,
		CreatedBy:   email,
	})
	if err != nil {
		writeIssueError(w, name, err)
		return
	}
	writeJSON(w, http.StatusCreated, createResponse{
		Name:        name,
		Description: req.Description,
		Key:         issued.Key,
		Roles:       issuedRoles(issued),
		ExpiresAt:   expiresAt,
		Warning:     "Store this key securely. It will not be shown again.",
	})
}

// revoke handles DELETE /api/v1/portal/api-keys/{name}.
//
// @Summary      Revoke one of my API keys
// @Description  Revokes an API key the calling user issued for themselves. It stops authenticating on every replica from the moment this returns. Server-side self-scope: the path names only the caller's own key, so another person's key cannot be addressed here.
// @Tags         API Keys
// @Produce      json
// @Param        name  path  string  true  "The name the key was issued under"
// @Success      200  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/api-keys/{name} [delete]
func (a *API) revoke(w http.ResponseWriter, r *http.Request) {
	email := a.callerEmail(w, r)
	if email == "" {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusNotFound, "no key by that name")
		return
	}
	if a.Revoke == nil {
		writeError(w, http.StatusInternalServerError, "this deployment stores no api keys")
		return
	}
	a.syncKeys(r)
	stored := a.find(email, name)
	if stored == "" {
		writeError(w, http.StatusNotFound, "no key by that name")
		return
	}
	if err := a.Revoke(r, stored); err != nil {
		if errors.Is(err, ErrNoSuchKey) {
			writeError(w, http.StatusNotFound, "no key by that name")
			return
		}
		slog.Warn("user api key: revoking failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		writeError(w, http.StatusInternalServerError, "revoking the key failed")
		return
	}
	if a.Refresh != nil {
		a.Refresh(r)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// syncKeys reads the key store before this surface answers from its copy of it.
func (a *API) syncKeys(r *http.Request) {
	if a.Sync != nil {
		a.Sync(r)
	}
}

// mine is every key issued against email, read from the deployment's inventory.
// A key an administrator issued belongs in it as much as one issued here: both
// authenticate as this person.
func (a *API) mine(email string) []auth.APIKeySummary {
	if a.Keys == nil {
		return nil
	}
	var out []auth.APIKeySummary
	for _, k := range a.Keys.ListKeys() {
		if k.UserEmail != "" && strings.EqualFold(k.UserEmail, email) {
			out = append(out, k)
		}
	}
	return out
}

// find returns the caller's key by the name they addressed it as -- the label
// for one issued here, the stored name for one an administrator issued -- or ""
// when they hold no such key. Ownership is what scopes this: a name belonging
// to somebody else is not among the caller's keys and is not found.
//
// A key the caller issued wins over one an administrator happened to name the
// same thing. Both are theirs and both are listed under that name, so the
// choice has to be made somewhere; making it here means it is the same key
// every time rather than whichever the inventory's ordering put first.
func (a *API) find(email, name string) string {
	mine := a.mine(email)
	composed := storedName(name, email)
	for _, k := range mine {
		if k.Name == composed {
			return k.Name
		}
	}
	for _, k := range mine {
		if k.Name == name {
			return k.Name
		}
	}
	return ""
}

// ownerRoles is what a key with no roles of its own carries: the roles the
// platform has recorded for its owner. It is resolved once for the whole list,
// since every key in it belongs to one person, and answers nothing when the
// account cannot be resolved rather than failing the page.
func (a *API) ownerRoles(r *http.Request, email string) []string {
	if a.Issuer == nil || a.Issuer.Principals == nil {
		return nil
	}
	person, err := a.Issuer.Principals.BoundPrincipal(r.Context(), email)
	if err != nil {
		return nil
	}
	return person.Roles
}

// writeIssueError answers a refusal from the issuing core in this surface's own
// words. A person is never told about the stored name they did not choose.
func writeIssueError(w http.ResponseWriter, name string, err error) {
	switch {
	case errors.Is(err, apikeyissue.ErrNameTaken):
		writeError(w, http.StatusConflict, fmt.Sprintf("you already have a key named %q", name))
	case errors.Is(err, apikeyissue.ErrUnknownPerson):
		// The caller is authenticated, so the platform has just seen them: this
		// is a deployment with no directory to resolve an account through.
		writeError(w, http.StatusInternalServerError, "this deployment cannot issue keys against a user account")
	case errors.Is(err, apikeyissue.ErrNoStore):
		writeError(w, http.StatusInternalServerError, "this deployment stores no api keys")
	default:
		slog.Warn("user api key: issuing failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		writeError(w, http.StatusInternalServerError, "issuing the key failed")
	}
}

// callerEmail resolves the person whose keys these are, writing the refusal
// itself and answering "" when there is none.
//
// A key may not manage keys. Every route here acts on the strength of "this is
// the person, at a keyboard, signed in", and an API key is not that: it is a
// credential somebody already holds. Allowing one through would let a key
// issued against an account mint a second key for the same account with none of
// the first one's limits -- an administrator's narrower role set and its expiry
// would both be gone -- and would let a service key configured with a person's
// address act as that person outright. Neither is a thing a key should be able
// to do, so the check is on how the caller authenticated rather than on what
// they asked for.
func (a *API) callerEmail(w http.ResponseWriter, r *http.Request) string {
	email, authType := "", ""
	if a.Caller != nil {
		email, authType = a.Caller(r)
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return ""
	}
	if authType == middleware.AuthTypeAPIKey {
		writeError(w, http.StatusForbidden,
			"API keys are managed from a signed-in session. This request authenticated with an API key, "+
				"which cannot issue, list or revoke keys. Sign in to the portal and use Settings > API Keys.")
		return ""
	}
	return email
}

// issuedRoles is what the new key will carry: the roles its owner holds, which
// the issuing core resolved while checking the account.
func issuedRoles(issued *apikeyissue.Issued) []string {
	if issued.Person == nil {
		return issued.Roles
	}
	return issued.Person.Roles
}

// storedName is the deployment-unique name a person's key is stored under. Key
// names are unique across the deployment, so two people naming a key "laptop"
// must not collide, and neither may one person learn the other's name exists.
func storedName(name, email string) string {
	return namePrefix + email + ":" + name
}

// label is the part of a stored name its owner chose, or the stored name itself
// when it is not one this surface composed.
func label(stored, email string) string {
	prefix := storedName("", email)
	if after, found := strings.CutPrefix(stored, prefix); found {
		return after
	}
	return stored
}

// validName checks the name a person chose, answering the refusal itself.
// Colon is refused because it separates the parts of a stored name: a name
// carrying one could be formed two ways.
func validName(w http.ResponseWriter, name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	switch {
	case trimmed == "":
		writeError(w, http.StatusBadRequest, "name is required")
		return "", false
	case len(trimmed) > maxNameLength:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name must be %d characters or fewer", maxNameLength))
		return "", false
	case strings.ContainsAny(trimmed, ":/\\"):
		writeError(w, http.StatusBadRequest, `name may not contain ":", "/" or "\"`)
		return "", false
	case strings.ContainsFunc(trimmed, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		writeError(w, http.StatusBadRequest, "name may not contain control characters")
		return "", false
	case trimmed == "." || trimmed == "..":
		// The router resolves these away before a request carrying one reaches
		// the revoke handler, so such a key could be issued and never withdrawn.
		writeError(w, http.StatusBadRequest, `name may not be "." or ".."`)
		return "", false
	}
	return trimmed, true
}

// parseExpiry reads the requested lifetime, answering the refusal itself. No
// lifetime is a key that lasts until its owner revokes it.
func parseExpiry(w http.ResponseWriter, expiresIn string) (*time.Time, bool) {
	if expiresIn == "" {
		return nil, true
	}
	dur, err := time.ParseDuration(expiresIn)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid expires_in duration: "+err.Error())
		return nil, false
	}
	if dur <= 0 {
		writeError(w, http.StatusBadRequest, "expires_in must be a positive duration")
		return nil, false
	}
	exp := time.Now().Add(dur)
	return &exp, true
}

// decodeCreate reads the request body, refusing a field this route does not
// have. A request naming roles, or naming whose key it is, is refused rather
// than quietly issued as something else.
func decodeCreate(w http.ResponseWriter, r *http.Request) (createRequest, bool) {
	var req createRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return req, false
	}
	return req, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck // the response is already committed
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
