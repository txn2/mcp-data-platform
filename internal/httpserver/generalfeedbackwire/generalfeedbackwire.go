// Package generalfeedbackwire assembles who hears about a General feedback
// post (#2603) from the platform: the settings section, its seed, the people a
// persona reaches and the channels a list may name; it mounts the admin
// settings routes and the portal read the composer asks.
package generalfeedbackwire

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/portal/generalfeedback"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// defaultAdminPersona is the administrator persona when the configuration
// names none, as the admin API reads it.
const defaultAdminPersona = "admin"

// Service builds the list over the platform. members is the persona-member
// lookup the portal resolves shares with; nil reaches nobody through a persona.
func Service(p *platform.Platform, notify *notifydelivery.Handle, members func(ctx context.Context, name string) []string) *generalfeedback.Service {
	admin := p.Config().Admin.Persona
	if admin == "" {
		admin = defaultAdminPersona
	}
	d := generalfeedback.Deps{
		Seed:     generalfeedback.Seed(p.Config().Portal.Feedback.StandaloneRecipients, admin),
		Members:  members,
		Personas: personas(p.PersonaRegistry()),
		Channels: notify.Channels(),
	}
	if p.DB() != nil {
		d.Section = settingsjson.New[generalfeedback.Settings](p.DB(), generalfeedback.SettingsSection)
	}
	return generalfeedback.New(d)
}

// personas lists the registry's personas by name, as the list offers them.
func personas(reg *persona.Registry) func() []generalfeedback.PersonaOption {
	return func() []generalfeedback.PersonaOption {
		if reg == nil {
			return nil
		}
		all := reg.All()
		out := make([]generalfeedback.PersonaOption, 0, len(all))
		for _, p := range all {
			out = append(out, generalfeedback.PersonaOption{Name: p.Name, DisplayName: p.DisplayName})
		}
		slices.SortFunc(out, func(a, b generalfeedback.PersonaOption) int { return strings.Compare(a.Name, b.Name) })
		return out
	}
}

// Mount registers the portal read behind wrap and, when the admin API is on,
// the settings routes behind adminWrap.
func Mount(mux *http.ServeMux, svc *generalfeedback.Service, wrap, adminWrap func(http.Handler) http.Handler, author func(*http.Request) string) {
	if svc == nil {
		return
	}
	generalfeedback.RegisterReach(mux, wrap, svc)
	if adminWrap != nil {
		generalfeedback.RegisterSettings(mux, adminWrap, svc, author)
	}
}
