package personaaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/admin/personarules"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// Path is the route the persona editor asks.
const Path = "/api/v1/admin/personas/effective-access"

// maxBody bounds a draft: a persona with every rule an editor would write.
const maxBody = 1 << 20

// Draft is a persona as the editor holds it, saved or not: the body the
// persona create and update routes take.
type Draft struct {
	Name                      string                 `json:"name" example:"analyst"`
	DisplayName               string                 `json:"display_name,omitempty"`
	Description               string                 `json:"description,omitempty"`
	Roles                     []string               `json:"roles"`
	AllowTools                []string               `json:"allow_tools"`
	DenyTools                 []string               `json:"deny_tools,omitempty"`
	AllowConnections          []string               `json:"allow_connections,omitempty"`
	DenyConnections           []string               `json:"deny_connections,omitempty"`
	Priority                  int                    `json:"priority,omitempty"`
	DescriptionPrefix         string                 `json:"description_prefix,omitempty"`
	DescriptionOverride       string                 `json:"description_override,omitempty"`
	AgentInstructionsSuffix   string                 `json:"agent_instructions_suffix,omitempty"`
	AgentInstructionsOverride string                 `json:"agent_instructions_override,omitempty"`
	APIRoutes                 []persona.APIRouteRule `json:"api_routes,omitempty"`
	ServiceAccount            bool                   `json:"service_account,omitempty"`
	Portal                    *persona.PortalAccess  `json:"portal,omitempty"`
	External                  bool                   `json:"external,omitempty"`
	AllowSharing              *bool                  `json:"allow_sharing,omitempty"`
}

// Persona is the draft as the registry would hold it.
func (d Draft) Persona() *persona.Persona {
	var portal *persona.PortalAccess
	if d.Portal != nil {
		block := *d.Portal
		block.External = false
		portal = &block
	}
	return &persona.Persona{
		Name: d.Name, DisplayName: d.DisplayName, Description: d.Description, Roles: d.Roles,
		Tools:       persona.ToolRules{Allow: d.AllowTools, Deny: d.DenyTools},
		Connections: persona.ConnectionRules{Allow: d.AllowConnections, Deny: d.DenyConnections},
		APIRoutes:   personarules.Normalize(d.APIRoutes),
		Context: persona.ContextOverrides{
			DescriptionPrefix: d.DescriptionPrefix, DescriptionOverride: d.DescriptionOverride,
			AgentInstructionsSuffix: d.AgentInstructionsSuffix, AgentInstructionsOverride: d.AgentInstructionsOverride,
		},
		Priority: d.Priority, ServiceAccount: d.ServiceAccount, Portal: portal,
		External: d.External, AllowSharing: d.AllowSharing,
	}
}

// Config is what the evaluation reads.
type Config struct {
	// Personas is the live registry: the other personas and their roles.
	Personas *persona.Registry
	// AdminPersona is the configured administrator persona's name.
	AdminPersona string
	// RoleMapping is personas.role_mapping.oidc_to_persona.
	RoleMapping map[string]string
	// ExcludedFromCalls is calls.exclude_personas.
	ExcludedFromCalls []string
	// Inventory reads what the deployment serves now.
	Inventory func(ctx context.Context) Inventory
	// DB holds the references; nil reports none.
	DB *sql.DB
	// FileKeyRoles are the roles of the API keys the configuration declares.
	FileKeyRoles [][]string
}

// Service answers for a persona.
type Service struct{ cfg Config }

// New returns the service.
func New(cfg Config) *Service { return &Service{cfg: cfg} }

// Report is what p reaches, with the references naming it.
func (s *Service) Report(ctx context.Context, p *persona.Persona) (Report, error) {
	var inv Inventory
	if s.cfg.Inventory != nil {
		inv = s.cfg.Inventory(ctx)
	}
	report := Evaluate(Subject{
		Persona: p, Admin: s.isAdmin(p), Others: s.cfg.Personas.All(),
		RoleMapping: s.cfg.RoleMapping, ExcludedFromCalls: s.cfg.ExcludedFromCalls,
	}, inv)
	report.AdminPersona = s.cfg.AdminPersona
	refs, unscoped, err := s.readReferences(ctx, p)
	report.References, report.Unscoped = refs, unscoped
	return report, err
}

// References are the grants other objects hold that name the stored persona.
func (s *Service) References(ctx context.Context, name string) (References, error) {
	p, ok := s.cfg.Personas.Get(name)
	if !ok {
		return emptyRefs(), nil
	}
	refs, _, err := s.readReferences(ctx, p)
	return refs, err
}

func (s *Service) readReferences(ctx context.Context, p *persona.Persona) (References, Unscoped, error) {
	if s.cfg.DB == nil || p.Name == "" {
		return emptyRefs(), Unscoped{}, nil
	}
	return readRefs(ctx, s.cfg.DB, refScope{name: p.Name, admin: s.isAdmin(p), resolves: s.resolvesTo(p), fileKeyRoles: s.cfg.FileKeyRoles})
}

// isAdmin is the administrator rule for a draft: named as the administrator
// persona and not external, as Registry.AdminRule decides it saved.
func (s *Service) isAdmin(p *persona.Persona) bool {
	return s.cfg.AdminPersona != "" && p.Name == s.cfg.AdminPersona && !p.External
}

// resolvesTo reports whether a set of roles would resolve to p were it saved:
// the registry's resolution over the other personas with p in place.
func (s *Service) resolvesTo(p *persona.Persona) func([]string) bool {
	reg := persona.NewRegistry()
	for _, other := range s.cfg.Personas.All() {
		if other.Name != p.Name {
			_ = reg.Register(other)
		}
	}
	_ = reg.Register(p)
	reg.SetRoleMapping(maps.Clone(s.cfg.RoleMapping))
	return func(roles []string) bool {
		got, ok := reg.GetForRoles(roles)
		return ok && got.Name == p.Name
	}
}

func emptyRefs() References {
	return References{Secrets: SecretRefs{Named: []string{}}, WebhookSources: []string{}}
}

// Register mounts the route behind wrap, the admin API's authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, s *Service) {
	mux.Handle("POST "+Path, wrap(http.HandlerFunc(s.serve)))
}

// serve handles POST /api/v1/admin/personas/effective-access.
//
// @Summary      What a persona reaches
// @Description  Evaluates a persona, saved or not, as every request would: the tools its people can call and on which connections, the API operations api_invoke_endpoint may call, the scratch tables they can list, register or change through SQL, the portal as stored and as External leaves it, sharing, the resource folders they read, the grants other objects hold that name the persona (stored secrets, script run grants, script versions whose author roles resolve to it, prompts, shares, webhook sources, external accounts, API keys), and the signed-in endpoints no persona governs (#2257). The body is the persona as the create route takes it.
// @Tags         Personas
// @Accept       json
// @Produce      json
// @Param        body  body  Draft  true  "The persona, saved or not"
// @Success      200  {object}  Report
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/personas/effective-access [post]
func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	var d Draft
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid persona: "+err.Error())
		return
	}
	report, err := s.Report(r.Context(), d.Persona())
	if err != nil {
		// The evaluation stands without the stores: an editor still shows
		// every decision, with the references read as none.
		slog.Warn("persona effective access: references unread", "error", logsan.SanitizeForLog(err.Error()))
	}
	httpjson.WriteJSON(w, http.StatusOK, report)
}
