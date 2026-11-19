// Package assetbulk serves bulk actions on assets (#2507): one request that
// adds or removes tags, files assets into a collection section, sets or clears
// their status, shares them with a person or a persona, or deletes them, for up
// to MaxAssets assets at once.
//
// Each asset is acted on through the portal's own single-asset route, served
// in process with the caller's identity: the tag change through PUT
// /assets/{id}, the status through /assets/{id}/status, the share through
// /assets/{id}/shares, the filing through /collections/{id}/items and the
// delete through DELETE /assets/{id}. So every item is authorized by exactly
// the rule its single route applies, never by a second copy of it, and an
// asset the caller may not act on is left exactly as it was.
//
// An asset the route refuses is reported skipped. When the caller cannot open
// it, or it does not exist, the reason is the one sentence ReasonHidden for
// both, so a bulk request carrying ids the caller cannot open learns no more
// than a single request would. When the caller can open it, the reason is the
// route's own refusal ("only the owner can share this asset").
package assetbulk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// MaxAssets is the most assets one bulk request acts on.
const MaxAssets = 500

// ReasonHidden is the reason an asset the caller cannot open, or that does not
// exist, is skipped for. One sentence for both, whichever is true.
const ReasonHidden = "not found or no access"

// The actions a bulk request names.
const (
	ActionAddTags         = "add_tags"
	ActionRemoveTags      = "remove_tags"
	ActionAddToCollection = "add_to_collection"
	ActionSetStatus       = "set_status"
	ActionClearStatus     = "clear_status"
	ActionShare           = "share"
	ActionDelete          = "delete"
)

// Path is the bulk route.
const Path = "/api/v1/portal/assets/bulk"

// workers bounds how many assets one request acts on at once.
const workers = 8

// maxBody bounds a bulk request body: 500 ids and the action's arguments.
const maxBody = 256 << 10

// maxAnswer bounds what is read of one in-process answer: only a refusal's
// sentence is used.
const maxAnswer = 16 << 10

// Refusals the route answers with.
const (
	errAuthRequired = "authentication required"
	errBadBody      = "invalid request body"
	errNoAssets     = "asset_ids is required: name at least one asset"
)

// Request is a bulk action.
type Request struct {
	// Action is add_tags, remove_tags, add_to_collection, set_status,
	// clear_status, share or delete.
	Action string `json:"action" example:"add_tags"`
	// AssetIDs are the assets to act on, at most 500.
	AssetIDs []string `json:"asset_ids"`
	// Tags are added or removed (add_tags, remove_tags).
	Tags []string `json:"tags,omitempty" example:"finance"`
	// CollectionID and SectionID name where add_to_collection files the
	// assets; an empty section is the collection's last.
	CollectionID string `json:"collection_id,omitempty"`
	SectionID    string `json:"section_id,omitempty"`
	// Status and Note are what set_status sets.
	Status string `json:"status,omitempty" example:"deprecated"`
	Note   string `json:"note,omitempty"`
	// Share is who share shares with. A bulk share sends no email.
	Share *ShareSpec `json:"share,omitempty"`
}

// ShareSpec names one recipient: a person by address or id, or a persona.
type ShareSpec struct {
	SharedWithEmail   string `json:"shared_with_email,omitempty" example:"colleague@example.com"`
	SharedWithUserID  string `json:"shared_with_user_id,omitempty"`
	SharedWithPersona string `json:"shared_with_persona,omitempty" example:"finance"`
	// Permission is viewer (the default) or editor.
	Permission string `json:"permission,omitempty" example:"viewer"`
}

// Skipped is one asset the action was not applied to, and why.
type Skipped struct {
	AssetID string `json:"asset_id"`
	Reason  string `json:"reason"`
}

// Result is what one bulk request did.
type Result struct {
	Action    string    `json:"action"`
	Requested int       `json:"requested"`
	Applied   []string  `json:"applied"`
	Skipped   []Skipped `json:"skipped"`
}

// API serves the bulk route.
type API struct {
	routes http.Handler
	assets portaldomain.AssetStore
	access *access.Checker
}

// Mount returns the hook pkg/portal registers the bulk route through. The
// route acts on each asset through the mux it is registered on.
func Mount(assets portaldomain.AssetStore) func(*http.ServeMux, *access.Checker) {
	return func(mux *http.ServeMux, checker *access.Checker) {
		mux.HandleFunc("POST "+Path, New(mux, assets, checker).bulk)
	}
}

// New builds the API over routes, the handler single-asset requests are
// served by.
func New(routes http.Handler, assets portaldomain.AssetStore, checker *access.Checker) *API {
	return &API{routes: routes, assets: assets, access: checker}
}

// bulk handles POST /api/v1/portal/assets/bulk.
//
// @Summary      Act on several assets at once
// @Description  Applies one action to up to 500 assets in one request (#2507): add_tags or remove_tags (tags), add_to_collection (collection_id, optional section_id), set_status (status, optional note) or clear_status, share with one person or persona (share; a bulk share sends no email), or delete (to the trash). Each asset is acted on through its own single-asset route with the caller's identity, so each is authorized by that route's rule, and nothing changes on an asset the caller may not act on. applied lists the assets acted on; skipped lists the rest with the reason, which is "not found or no access" for an asset that does not exist or the caller cannot open, and the single route's refusal otherwise.
// @Tags         Assets
// @Accept       json
// @Produce      json
// @Param        body  body  Request  true  "The action and the assets"
// @Success      200  {object}  Result
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/bulk [post]
func (a *API) bulk(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	var req Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, errBadBody)
		return
	}
	req.AssetIDs = Distinct(req.AssetIDs)
	if err := req.Validate(); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, a.Apply(r.Context(), user, req))
}

// Distinct trims the ids and drops blanks and repeats, keeping the first
// occurrence's order.
func Distinct(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// Validate checks the request names a known action, its arguments and between
// one and MaxAssets assets.
func (req Request) Validate() error {
	if len(req.AssetIDs) == 0 {
		return errors.New(errNoAssets)
	}
	if len(req.AssetIDs) > MaxAssets {
		return fmt.Errorf("at most %d assets per request; this one names %d", MaxAssets, len(req.AssetIDs))
	}
	check, ok := argChecks[req.Action]
	if !ok {
		return fmt.Errorf("unknown action %q: use add_tags, remove_tags, add_to_collection, set_status, clear_status, share or delete", req.Action)
	}
	return check(req)
}

// argChecks holds, per action, the check of the arguments it needs.
var argChecks = map[string]func(Request) error{
	ActionAddTags:         needTags,
	ActionRemoveTags:      needTags,
	ActionAddToCollection: needArg(func(r Request) string { return r.CollectionID }, "add_to_collection needs collection_id"),
	ActionSetStatus:       needArg(func(r Request) string { return r.Status }, "set_status needs status"),
	ActionShare:           needRecipient,
	ActionClearStatus:     func(Request) error { return nil },
	ActionDelete:          func(Request) error { return nil },
}

func needTags(r Request) error {
	if len(r.Tags) == 0 {
		return fmt.Errorf("action %s needs tags", r.Action)
	}
	return nil
}

func needArg(field func(Request) string, msg string) func(Request) error {
	return func(r Request) error {
		if strings.TrimSpace(field(r)) == "" {
			return errors.New(msg)
		}
		return nil
	}
}

func needRecipient(r Request) error {
	if r.Share == nil || r.Share.recipients() != 1 {
		return errors.New("share needs exactly one of share.shared_with_email, shared_with_user_id or shared_with_persona")
	}
	return nil
}

func (s *ShareSpec) recipients() int {
	n := 0
	for _, v := range []string{s.SharedWithEmail, s.SharedWithUserID, s.SharedWithPersona} {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	return n
}

// Apply acts on every asset the request names and reports each one.
func (a *API) Apply(ctx context.Context, user *access.User, req Request) Result {
	// Every single-asset route reads the caller from the context it is served
	// with: this is the identity each item is authorized as.
	ctx = access.ContextWithUser(ctx, user)
	reasons := make([]string, len(req.AssetIDs))
	var g errgroup.Group
	g.SetLimit(workers)
	var mu sync.Mutex
	for i, id := range req.AssetIDs {
		g.Go(func() error {
			reason := a.applyOne(ctx, user, req, id)
			mu.Lock()
			reasons[i] = reason
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // every item reports its own outcome; none returns an error
	out := Result{Action: req.Action, Requested: len(req.AssetIDs), Applied: []string{}, Skipped: []Skipped{}}
	for i, id := range req.AssetIDs {
		if reasons[i] == "" {
			out.Applied = append(out.Applied, id)
			continue
		}
		out.Skipped = append(out.Skipped, Skipped{AssetID: id, Reason: reasons[i]})
	}
	return out
}

// call is one in-process request to a single-asset route.
type call struct {
	method, path string
	body         any
}

// applyOne acts on one asset, returning "" when it was applied and the reason
// it was skipped otherwise.
func (a *API) applyOne(ctx context.Context, user *access.User, req Request, id string) string {
	c, ok := a.callFor(ctx, req, id)
	if !ok {
		return ReasonHidden
	}
	status, answer := a.serve(ctx, c)
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return ""
	}
	return a.reason(ctx, user, id, status, answer)
}

// callFor is the single-asset request that applies req to one asset. A tag
// change needs the asset's current tags; an asset that cannot be read reports
// false, and is skipped as hidden without any route being asked.
func (a *API) callFor(ctx context.Context, req Request, id string) (call, bool) {
	assetPath := "/api/v1/portal/assets/" + id
	switch req.Action {
	case ActionAddTags, ActionRemoveTags:
		asset, err := a.assets.Get(ctx, id)
		if err != nil || asset == nil {
			return call{}, false
		}
		return call{http.MethodPut, assetPath, map[string]any{"tags": Retagged(asset.Tags, req.Tags, req.Action == ActionAddTags)}}, true
	case ActionAddToCollection:
		return call{
			http.MethodPost, "/api/v1/portal/collections/" + req.CollectionID + "/items",
			map[string]string{"asset_id": id, "section_id": req.SectionID},
		}, true
	case ActionSetStatus:
		return call{http.MethodPut, assetPath + "/status", map[string]string{"status": req.Status, "note": req.Note}}, true
	case ActionClearStatus:
		return call{http.MethodDelete, assetPath + "/status", nil}, true
	case ActionShare:
		return call{http.MethodPost, assetPath + "/shares", map[string]any{
			"shared_with_email": req.Share.SharedWithEmail, "shared_with_user_id": req.Share.SharedWithUserID,
			"shared_with_persona": req.Share.SharedWithPersona, "permission": req.Share.Permission, "notify": false,
		}}, true
	default: // ActionDelete; Validate admits nothing else
		return call{http.MethodDelete, assetPath, nil}, true
	}
}

// Retagged is current with tags added or removed. Tags compare without case:
// adding a tag the asset already carries in another case keeps the asset's
// spelling, and removing one removes every spelling of it.
func Retagged(current, tags []string, add bool) []string {
	out := make([]string, 0, len(current))
	has := func(list []string, t string) bool {
		return slices.ContainsFunc(list, func(c string) bool { return strings.EqualFold(c, t) })
	}
	for _, t := range current {
		if !add && has(tags, t) {
			continue
		}
		out = append(out, t)
	}
	if !add {
		return out
	}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" && !has(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// serve answers one single-asset request in process, with the caller's
// identity carried on ctx.
func (a *API) serve(ctx context.Context, c call) (status int, answer []byte) {
	var sent bytes.Buffer
	if c.body != nil {
		if err := json.NewEncoder(&sent).Encode(c.body); err != nil {
			return http.StatusInternalServerError, nil
		}
	}
	r, err := http.NewRequestWithContext(ctx, c.method, c.path, &sent)
	if err != nil {
		return http.StatusInternalServerError, nil
	}
	r.Header.Set("Content-Type", "application/json")
	rec := &recorder{header: http.Header{}}
	a.routes.ServeHTTP(rec, r)
	return rec.statusCode(), rec.body.Bytes()
}

// reason is why a refused asset was skipped: ReasonHidden unless the caller
// can open the asset, the route's own refusal when they can.
func (a *API) reason(ctx context.Context, user *access.User, id string, status int, answer []byte) string {
	if status == http.StatusNotFound || status == http.StatusGone || !a.canOpen(ctx, user, id) {
		return ReasonHidden
	}
	var p httpjson.ProblemDetail
	if json.Unmarshal(answer, &p) == nil && strings.TrimSpace(p.Detail) != "" {
		return p.Detail
	}
	return strings.ToLower(http.StatusText(status))
}

// canOpen reports whether the caller can open a live asset: the rule every
// portal read applies.
func (a *API) canOpen(ctx context.Context, user *access.User, id string) bool {
	asset, err := a.assets.Get(ctx, id)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		return false
	}
	return a.access.CanViewAsset(ctx, id, asset, user)
}

// recorder captures one in-process answer: its status and the start of its
// body.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

// Header implements http.ResponseWriter.
func (r *recorder) Header() http.Header { return r.header }

// WriteHeader implements http.ResponseWriter: the first status written wins.
func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

// Write implements http.ResponseWriter, keeping at most maxAnswer bytes.
func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if room := maxAnswer - r.body.Len(); room > 0 {
		_, _ = r.body.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (r *recorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}
