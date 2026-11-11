package sitehttp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// maxListedForms bounds the endpoints read for the Copy a site form picker.
const maxListedForms = 500

// Registrar is the portal's Sites routes, nil where the deployment serves no
// sites (#2559).
func Registrar(p *platform.Platform, resolver portal.PersonaResolver, notify *notifydelivery.Handle) func(*http.ServeMux) {
	if !serving(p) {
		return nil
	}
	d := PortalDeps{
		Runtime: p.Endpoints().Sites, Persona: resolver,
		IsAdmin: p.PersonaRegistry().AdminRule(p.Config().Admin.Persona),
		Forms:   siteForms(p),
		Personas: func() []string {
			return p.PersonaRegistry().Names()
		},
	}
	if s := p.Audit().Store(); s != nil {
		d.Audit = s
	}
	if n := notify.Enqueuer(); n != nil {
		d.Sharer = n
	}
	return PortalRegistrar(d)
}

// siteForms lists the endpoints whose scripts are forms, which a copied page's
// forms can post to.
func siteForms(p *platform.Platform) func(ctx context.Context) ([]string, error) {
	bindings, scripts := p.Endpoints().Bindings, scriptstore.New(p.DB())
	return func(ctx context.Context) ([]string, error) {
		list, _, err := bindings.List(ctx, epbinding.Filter{Limit: maxListedForms})
		if err != nil {
			return nil, fmt.Errorf("listing endpoints: %w", err)
		}
		names := []string{}
		for i := range list {
			sc, err := scripts.GetByID(ctx, list[i].ScriptID)
			if err == nil && epform.DefinesSource(sc.Source) {
				names = append(names, list[i].Name)
			}
		}
		return names, nil
	}
}
