// Package formpublishwire mounts what a form's owner publishes it with
// (#2494): the preview the script page opens, and the Approvals queue
// (#2437) a public form's version is approved through, with the emails each
// request and decision sends.
package formpublishwire

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/approval/apprhttp"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eppreview"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/callrun"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// Deps is what the routes are mounted with.
type Deps struct {
	Wrap    func(http.Handler) http.Handler
	IsAdmin func([]string) bool
	Notify  *notifydelivery.Handle
}

// Mount registers the preview route and the Approvals routes, and points the
// queue's emails at the notification handle. Nothing without endpoints.
func Mount(mux *http.ServeMux, p *platform.Platform, d Deps) {
	e := p.Endpoints()
	if e == nil || p.DB() == nil {
		return
	}
	if bridge := d.Notify.PortalNotifier(notifydelivery.PortalStores{}, p.Config().Portal.PublicBaseURL); bridge != nil {
		wireNotices(e.Approvals, bridge, user.NewPostgresStore(p.DB()), d.IsAdmin, keyAdmins(p.Config().Auth.APIKeys.Keys, d.IsAdmin))
	}
	register(mux, d, routes{
		approvals: e.Approvals, previews: e.Previews, db: p.DB(), personaOf: personaName(p.PersonaRegistry()),
		audit: p.Audit().Logger(), recent: recentCalls(e.CallHandlers, p.DB()),
	})
}

// wireNotices points the queue's emails at bridge, telling the
// administrators users names.
func wireNotices(svc *approval.Service, bridge Bridge, users Users, isAdmin func([]string) bool, configured []string) {
	svc.Notifier = Notifier{Bridge: bridge}
	svc.Approvers = Approvers(users, isAdmin, configured...)
}

// keyAdmins are the addresses of the configured API keys whose roles make
// them administrators: a deployment administered by keys, whose people never
// sign in through the identity provider, still has someone to tell.
func keyAdmins(keys []platform.APIKeyDef, isAdmin func([]string) bool) []string {
	var out []string
	for _, k := range keys {
		if k.Email != "" && isAdmin(k.Roles) {
			out = append(out, k.Email)
		}
	}
	return out
}

// personaName is the persona a caller's roles resolve to, "" for none.
func personaName(reg *persona.Registry) func(roles []string) string {
	return func(roles []string) string {
		if reg == nil {
			return ""
		}
		if found, ok := reg.GetForRoles(roles); ok && found != nil {
			return found.Name
		}
		return ""
	}
}

// routes is what the routes are served from.
type routes struct {
	approvals *approval.Service
	previews  *eppreview.Previews
	db        *sql.DB
	personaOf func([]string) string
	audit     middleware.AuditLogger
	recent    func(ctx context.Context, r *approval.Request) ([]apprhttp.RecentCall, error)
}

// recentCalls reads a call request's requester's recent calls through the
// tool-call handlers' evaluator, nil with none.
func recentCalls(rt *callrun.Runtime, db *sql.DB) func(ctx context.Context, r *approval.Request) ([]apprhttp.RecentCall, error) {
	if rt == nil || rt.Evaluator == nil {
		return nil
	}
	return func(ctx context.Context, r *approval.Request) ([]apprhttp.RecentCall, error) {
		got, err := rt.Evaluator.RecentCalls(ctx, db, r)
		if err != nil {
			return nil, err //nolint:wrapcheck // the route answers 500 without reading it
		}
		out := make([]apprhttp.RecentCall, 0, len(got))
		for _, c := range got {
			out = append(out, apprhttp.RecentCall{At: c.At, Items: c.Items, Outcome: c.Outcome})
		}
		return out, nil
	}
}

// register mounts the preview route and the Approvals routes behind d.Wrap.
func register(mux *http.ServeMux, d Deps, r routes) {
	(&eppreview.Portal{Previews: r.previews, Scripts: scriptstore.New(r.db), Opener: Opener(d.IsAdmin)}).Register(mux, d.Wrap)
	apprhttp.Register(mux, d.Wrap, apprhttp.Config{Service: r.approvals, Actor: Actor(d.IsAdmin, r.personaOf), Audit: r.audit, Recent: r.recent})
}

// Opener is the portal caller as the opener of a preview, nil when signed out.
func Opener(isAdmin func([]string) bool) func(r *http.Request) *eppreview.Opener {
	return func(r *http.Request) *eppreview.Opener {
		u := portal.GetUser(r.Context())
		if u == nil {
			return nil
		}
		return &eppreview.Opener{UserID: u.UserID, Email: u.Email, Roles: u.Roles, AuthType: u.AuthType, Admin: isAdmin(u.Roles)}
	}
}

// Actor is the portal caller as an approval actor, nil when signed out.
func Actor(isAdmin func([]string) bool, personaOf func([]string) string) func(r *http.Request) *approval.Actor {
	return func(r *http.Request) *approval.Actor {
		u := portal.GetUser(r.Context())
		if u == nil {
			return nil
		}
		return &approval.Actor{Email: u.Email, Admin: isAdmin(u.Roles), Persona: personaOf(u.Roles), Roles: u.Roles}
	}
}

// Users lists the people the platform knows.
type Users interface {
	List(ctx context.Context, filter user.Filter) ([]user.User, int, error)
}

// Approvers lists the administrators to notify, each once: the people whose
// roles, as their identity provider last reported them, make them
// administrators, and the configured addresses.
func Approvers(users Users, isAdmin func([]string) bool, configured ...string) func(ctx context.Context) []string {
	return func(ctx context.Context) []string {
		seen := map[string]bool{}
		out := []string{}
		add := func(email string) {
			key := strings.ToLower(email)
			if email != "" && !seen[key] {
				seen[key] = true
				out = append(out, email)
			}
		}
		for _, email := range configured {
			add(email)
		}
		people, _, err := users.List(ctx, user.Filter{ConfirmedOnly: true, Limit: user.MaxListLimit})
		if err != nil {
			slog.WarnContext(ctx, "approvals: listing administrators failed", "error", logsan.SanitizeForLog(err.Error()))
			return out
		}
		for _, u := range people {
			if isAdmin(u.Roles) {
				add(u.Email)
			}
		}
		return out
	}
}

// Bridge sends the portal's emails (notifydelivery.PortalNotifier).
type Bridge interface {
	NotifyApprovalRequest(ctx context.Context, approvers []string, a notifydelivery.AccessNotice)
	NotifyApprovalDecision(ctx context.Context, a notifydelivery.AccessNotice)
}

// Notifier sends the queue's two emails through the portal's notifier.
type Notifier struct{ Bridge Bridge }

// Requested tells each approver a request waits.
func (n Notifier) Requested(ctx context.Context, r *approval.Request, approvers []string) {
	n.Bridge.NotifyApprovalRequest(ctx, approvers, notifydelivery.AccessNotice{
		Actor: r.RequesterEmail, ItemID: r.ID, ItemTitle: subject(r), Message: r.Reason,
	})
}

// Decided tells the requester how their request was decided.
func (n Notifier) Decided(ctx context.Context, r *approval.Request) {
	msg := fmt.Sprintf("%s %s it.", r.DecidedBy, r.Status)
	if r.DecisionComment != "" {
		msg += " " + r.DecisionComment
	}
	n.Bridge.NotifyApprovalDecision(ctx, notifydelivery.AccessNotice{
		To: r.RequesterEmail, Actor: r.DecidedBy, ItemID: r.ID, ItemTitle: subject(r), Message: msg,
	})
}

// subject names what a request asks for, in a sentence's words.
func subject(r *approval.Request) string {
	switch r.Kind {
	case approval.KindCall:
		op := r.CallOperation
		if op == "" {
			op = r.CallTool
		}
		return fmt.Sprintf("%d %s call(s) on %s, which a policy held", r.CallsAsked, op, r.CallConnection)
	case approval.KindHandlerVersion:
		return fmt.Sprintf("version %d of %s to run as a policy, transform or watch", r.Version, r.ScriptName)
	}
	return fmt.Sprintf("version %d of the form %s for use without signing in", r.Version, r.ScriptName)
}
