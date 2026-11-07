// Package peopleapi serves who is behind an asset (#2531): its owner and the
// other people who wrote a version of it, to everyone who can open it, and a
// summary of whom it is shared with, to the organization's own people.
//
// The share summary names personas, people and links by kind, who made each
// link and how often it was opened. It never carries a share's token, a link
// address or anything else that opens the asset: those stay with the owner, in
// the Share dialog (GET /assets/{id}/shares). A caller whose persona is
// external is refused it, and a share-link viewer has no portal session to ask
// with. Revoked and expired shares are left out. It is built only by
// pkg/portal, which mounts it.
package peopleapi

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// maxVersionsRead bounds the versions read to list contributors; an asset
// past it lists the contributors of its newest versions.
const maxVersionsRead = 2000

// Config is what the routes read.
type Config struct {
	Assets interface {
		Get(ctx context.Context, id string) (*portaldomain.Asset, error)
	}
	Versions interface {
		ListByAsset(ctx context.Context, assetID string, limit, offset int) ([]portaldomain.AssetVersion, int, error)
	}
	Shares interface {
		ListByAsset(ctx context.Context, assetID string) ([]portaldomain.Share, error)
	}
	Access *access.Checker
}

// Register mounts the routes the configured stores support.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Assets == nil || cfg.Access == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/portal/assets/{id}/people", h.people)
	if cfg.Shares != nil {
		mux.HandleFunc("GET /api/v1/portal/assets/{id}/share-summary", h.shareSummary)
	}
}

type handler struct{ cfg Config }

// person is someone named on an asset.
type person struct {
	Email string `json:"email" example:"alice@example.com"`
}

// Contributor is a person other than the owner who wrote a version.
type Contributor struct {
	Email string `json:"email" example:"bob@example.com"`
	// Versions is how many versions they wrote, and LastChange when they
	// wrote the newest.
	Versions   int       `json:"versions" example:"3"`
	LastChange time.Time `json:"last_change"`
}

// People is who is behind an asset.
type People struct {
	Owner        person        `json:"owner"`
	Contributors []Contributor `json:"contributors"`
}

// personaShare is a persona an asset is shared with.
type personaShare struct {
	Persona    string                       `json:"persona" example:"finance"`
	Permission portaldomain.SharePermission `json:"permission" example:"viewer"`
}

// personShare is a person an asset is shared with.
type personShare struct {
	Email      string                       `json:"email" example:"bob@example.com"`
	Permission portaldomain.SharePermission `json:"permission" example:"viewer"`
	ExpiresAt  *time.Time                   `json:"expires_at,omitempty"`
}

// linkShare is a link an asset is shared by: its kind, who made it and how
// often it was opened. Never its token or address.
type linkShare struct {
	// AccessMode is who may open it: public, authenticated or restricted.
	AccessMode portaldomain.ShareAccessMode `json:"access_mode" example:"authenticated"`
	Origin     portaldomain.ShareOrigin     `json:"origin,omitempty" example:"explicit"`
	// PostedTo names the channel a channel post's link went to.
	PostedTo  string     `json:"posted_to,omitempty"`
	CreatedBy string     `json:"created_by" example:"alice@example.com"`
	Opened    int        `json:"opened" example:"14"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// ShareSummary is whom an asset is shared with.
type ShareSummary struct {
	Total    int            `json:"total" example:"23"`
	Personas []personaShare `json:"personas"`
	People   []personShare  `json:"people"`
	Links    []linkShare    `json:"links"`
}

// asset reads the asset the caller can open, writing the refusal itself.
func (h *handler) asset(w http.ResponseWriter, r *http.Request) (*access.User, *portaldomain.Asset, bool) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
		return nil, nil, false
	}
	id := r.PathValue("id")
	a, err := h.cfg.Assets.Get(r.Context(), id)
	if err != nil || a == nil || a.DeletedAt != nil {
		httpjson.WriteError(w, http.StatusNotFound, "asset not found")
		return nil, nil, false
	}
	if !h.cfg.Access.CanManageAsset(a, user) && !h.cfg.Access.CanViewAsset(r.Context(), id, a, user) {
		httpjson.WriteError(w, http.StatusForbidden, "you do not have access to this asset")
		return nil, nil, false
	}
	return user, a, true
}

// people handles GET /api/v1/portal/assets/{id}/people.
//
// @Summary      Who is behind an asset
// @Description  The asset's owner and every other person who wrote a version of it, each with how many versions and when they wrote the newest, newest first. For everyone who can open the asset.
// @Tags         Assets
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  People
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/people [get]
func (h *handler) people(w http.ResponseWriter, r *http.Request) {
	_, a, ok := h.asset(w, r)
	if !ok {
		return
	}
	out := People{Owner: person{Email: a.OwnerEmail}, Contributors: []Contributor{}}
	if h.cfg.Versions != nil {
		versions, _, err := h.cfg.Versions.ListByAsset(r.Context(), a.ID, maxVersionsRead, 0)
		if err != nil {
			httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the asset's versions")
			return
		}
		out.Contributors = contributors(versions, a.OwnerEmail)
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// contributors groups versions by author, leaving out the owner, newest
// change first.
func contributors(versions []portaldomain.AssetVersion, owner string) []Contributor {
	by := map[string]*Contributor{}
	for _, v := range versions {
		who := strings.TrimSpace(v.CreatedBy)
		if who == "" || strings.EqualFold(who, owner) {
			continue
		}
		key := strings.ToLower(who)
		c, seen := by[key]
		if !seen {
			c = &Contributor{Email: who}
			by[key] = c
		}
		c.Versions++
		if v.CreatedAt.After(c.LastChange) {
			c.LastChange = v.CreatedAt
		}
	}
	out := make([]Contributor, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastChange.Equal(out[j].LastChange) {
			return out[i].LastChange.After(out[j].LastChange)
		}
		return out[i].Email < out[j].Email
	})
	return out
}

// shareSummary handles GET /api/v1/portal/assets/{id}/share-summary.
//
// @Summary      Whom an asset is shared with
// @Description  The asset's active shares: the personas and people it is shared with, with their permission and any expiry, and its links by kind, with who made each and how often it was opened. Never a share's token or a link's address. For the organization's own people who can open the asset; a caller whose persona is external is refused. Revoked and expired shares are left out.
// @Tags         Assets
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  ShareSummary
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/share-summary [get]
func (h *handler) shareSummary(w http.ResponseWriter, r *http.Request) {
	user, a, ok := h.asset(w, r)
	if !ok {
		return
	}
	if h.cfg.Access.Portal(user).External {
		httpjson.WriteError(w, http.StatusForbidden, "whom an asset is shared with is not shown to an external account")
		return
	}
	shares, err := h.cfg.Shares.ListByAsset(r.Context(), a.ID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the asset's shares")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, summarize(shares))
}

// summarize sorts active shares into personas, people and links, copying
// only the fields a summary may carry.
func summarize(shares []portaldomain.Share) ShareSummary {
	out := ShareSummary{Personas: []personaShare{}, People: []personShare{}, Links: []linkShare{}}
	for _, s := range shares {
		if !access.IsShareActive(s) {
			continue
		}
		switch {
		case s.SharedWithPersona != "":
			out.Personas = append(out.Personas, personaShare{Persona: s.SharedWithPersona, Permission: s.Permission})
		case s.SharedWithEmail != "" || s.SharedWithUserID != "":
			email := s.SharedWithEmail
			if email == "" {
				email = s.SharedWithUserID
			}
			out.People = append(out.People, personShare{Email: email, Permission: s.Permission, ExpiresAt: s.ExpiresAt})
		default:
			out.Links = append(out.Links, linkShare{
				AccessMode: s.AccessMode, Origin: s.Origin, PostedTo: s.PostedTo, CreatedBy: s.CreatedBy,
				Opened: s.AccessCount, ExpiresAt: s.ExpiresAt,
			})
		}
	}
	sort.Slice(out.Personas, func(i, j int) bool { return out.Personas[i].Persona < out.Personas[j].Persona })
	sort.Slice(out.People, func(i, j int) bool {
		return strings.ToLower(out.People[i].Email) < strings.ToLower(out.People[j].Email)
	})
	out.Total = len(out.Personas) + len(out.People) + len(out.Links)
	return out
}
