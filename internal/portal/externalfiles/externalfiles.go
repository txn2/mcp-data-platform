// Package externalfiles is External files (#2290): the personal folders of the
// people outside the organization, which a persona's External files level lets
// its people reach. View lists every external person's folder and opens what
// is in it; manage also renames, versions, moves, deletes and shares it. An
// administrator has manage.
//
// An external person is somebody whose recorded roles resolve to a persona
// marked external. Their folder is the user library their identity-provider
// subject keys, or their address when the platform has not seen the subject.
// Both are decided when the folder is opened, so a person whose role changes
// stops being external, and their folder leaves External files, at once.
package externalfiles

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Path is the route listing the external people's folders.
const Path = "/api/v1/resources/external-folders"

// Personas resolves a role set to its persona.
type Personas func(roles []string) (*persona.Persona, bool)

// Service decides External files and lists the folders.
type Service struct {
	source   Source
	personas Personas
	claims   resource.ClaimsExtractor
}

// New returns the service.
func New(source Source, personas Personas) *Service {
	return &Service{source: source, personas: personas}
}

// Level is the External files level the caller has: manage for an
// administrator, the persona's for anyone else, none without a persona.
func (s *Service) Level(c *resource.Claims) string {
	if c == nil {
		return persona.PortalNone
	}
	if c.IsAdmin {
		return persona.PortalManage
	}
	p, ok := s.personas(c.Roles)
	if !ok {
		return persona.PortalNone
	}
	return p.PortalPermissions(nil).ExternalFiles
}

// Bind wraps the resources API's claims so a caller whose level is view or
// manage reaches the external people's folders, and makes them the claims
// the listing route authenticates with.
func (s *Service) Bind(base resource.ClaimsExtractor) resource.ClaimsExtractor {
	bound := func(r *http.Request) (*resource.Claims, error) {
		claims, err := base(r)
		if err != nil || claims == nil {
			return claims, err
		}
		level := s.Level(claims)
		if level == persona.PortalNone {
			return claims, nil
		}
		claims.ExternalFolder = s.folderCheck(r.Context())
		claims.ManageExternalFiles = level == persona.PortalManage
		return claims, nil
	}
	s.claims = bound
	return bound
}

// folderCheck reports whether a user library belongs to an external person,
// each library read once per request. A read that fails reaches nothing.
func (s *Service) folderCheck(ctx context.Context) func(scopeID string) bool {
	var mu sync.Mutex
	seen := make(map[string]bool)
	return func(scopeID string) bool {
		mu.Lock()
		defer mu.Unlock()
		if v, ok := seen[scopeID]; ok {
			return v
		}
		v := s.isExternal(ctx, scopeID)
		seen[scopeID] = v
		return v
	}
}

func (s *Service) isExternal(ctx context.Context, scopeID string) bool {
	sets, err := s.source.RolesOf(ctx, scopeID)
	if err != nil {
		return false
	}
	for _, roles := range sets {
		if p, ok := s.personas(roles); ok && p.External {
			return true
		}
	}
	return false
}

// Folder is one external person's personal folder.
type Folder struct {
	// ScopeID is the user library the folder is: list it with
	// scope=user&scope_id=<scope_id>.
	ScopeID string `json:"scope_id" example:"6f1c2a9e-3b7d-4e1a-9c55-0d2f8b7a1e44"`
	Email   string `json:"email" example:"dana@client-a.example.com"`
	Name    string `json:"name,omitempty" example:"Dana Reyes"`
	// Persona is the external persona the person resolves to.
	Persona            string `json:"persona" example:"client-a"`
	PersonaDisplayName string `json:"persona_display_name,omitempty" example:"Client A"`
}

// folderList is the body of GET /api/v1/resources/external-folders.
type folderList struct {
	Folders []Folder `json:"folders"`
	// Manage reports whether the caller also changes what is in them.
	Manage bool `json:"manage"`
}

// Folders lists every external person's folder.
func (s *Service) Folders(ctx context.Context) ([]Folder, error) {
	people, err := s.source.People(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing external people's folders: %w", err)
	}
	out := make([]Folder, 0)
	for _, person := range people {
		p, ok := s.personas(person.Roles)
		if !ok || !p.External {
			continue
		}
		scope := person.Subject
		if scope == "" {
			scope = person.Email
		}
		out = append(out, Folder{
			ScopeID: scope, Email: person.Email,
			Name:    strings.TrimSpace(person.FirstName + " " + person.LastName),
			Persona: p.Name, PersonaDisplayName: p.DisplayName,
		})
	}
	return out, nil
}

// Register mounts the listing route. Bind must have run.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+Path, s.list)
}

// list handles GET /api/v1/resources/external-folders.
//
// @Summary      List external people's folders
// @Description  The personal folder of every person whose role resolves to an external persona (#2290), for a caller whose persona has External files at view or manage; an administrator has manage. List a folder's files with scope=user and its scope_id.
// @Tags         Resources
// @Produce      json
// @Success      200  {object}  folderList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/external-folders [get]
func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	claims, err := s.claims(r)
	if err != nil || claims == nil {
		status := http.StatusUnauthorized
		if errors.Is(err, resource.ErrForbidden) {
			status = http.StatusForbidden
		}
		httpjson.WriteError(w, status, "authentication required")
		return
	}
	level := s.Level(claims)
	if level == persona.PortalNone {
		httpjson.WriteError(w, http.StatusForbidden, "your persona does not reach external people's files")
		return
	}
	folders, err := s.Folders(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list external people's folders")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, folderList{Folders: folders, Manage: level == persona.PortalManage})
}
