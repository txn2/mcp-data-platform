// Package notifywire assembles the notification substrate for the HTTP
// composition root: the delivery handle, the branded templates, the review and
// connection alerts, and the portal's notifier and self-scoped preference
// routes.
//
// It was extracted from internal/httpserver (#1759) when that package reached
// its size budget. The seam is a real one: everything here takes a
// *platform.Platform and returns an assembled subsystem handle, and nothing in
// it touches a mux, a route or a middleware, which is what the composition root
// itself is about.
package notifywire

import (
	"log"
	"net/http"
	"net/mail"

	"github.com/txn2/mcp-data-platform/internal/httpserver/notifyhttp"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyrender"
	"github.com/txn2/mcp-data-platform/internal/platform/branding"
	"github.com/txn2/mcp-data-platform/internal/platform/connalert"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
)

// reviewAlertStore builds one review queue's alert persistence (#803, #1287),
// or nil when the alert cannot exist in this deployment: no database, or
// notifications turned off in YAML. The checker and the admin API each build
// one; it is stateless over the pool, so there is nothing to share, and
// threading a handle through the admin wiring would imply a lifecycle it does
// not have.
func reviewAlertStore(p *platform.Platform, target reviewalert.Target) *reviewalert.PostgresStore {
	if p == nil || p.DB() == nil || !p.Config().Notifications.IsEnabled() {
		return nil
	}
	return reviewalert.NewPostgresStore(p.DB(), target)
}

// ReviewAlertSettings narrows one queue's store to the half the admin settings
// surface needs, or nil when the alert cannot exist here. A nil result unmounts
// the admin routes, matching what the SMTP section already does in the same
// states: an operator must not be able to configure an alert nothing will
// ever send. The explicit nil check keeps a typed nil out of the interface.
func ReviewAlertSettings(p *platform.Platform, target reviewalert.Target) reviewalert.SettingsStore {
	store := reviewAlertStore(p, target)
	if store == nil {
		return nil
	}
	return store
}

// BuildReviewAlert assembles the scheduled knowledge review-queue staleness
// check. Returns nil (a no-op checker) when anything it needs is absent: no
// database, notifications off, no knowledge insight store -- an alert with
// nowhere to send is not an alert.
func BuildReviewAlert(p *platform.Platform, notify *notifydelivery.Handle) *reviewalert.Checker {
	target := reviewalert.KnowledgeTarget()
	store := reviewAlertStore(p, target)
	if store == nil {
		return nil
	}
	var source reviewalert.Source
	// A typed nil in the interface would read as a live source and the check
	// would fail on every tick instead of never running.
	if insights := p.KnowledgeInsightStore(); insights != nil {
		source = reviewalert.InsightSource{Insights: insights}
	}
	checker := reviewalert.New(reviewalert.Config{
		Target:   target,
		Settings: store,
		State:    store,
		Source:   source,
		Enqueuer: notify.Enqueuer(),
		BaseURL:  p.Config().Portal.PublicBaseURL,
	})
	if checker != nil {
		log.Println("Knowledge review-queue staleness alert enabled")
	}
	return checker
}

// ConnAlertStore builds the connection-revocation alert's persistence (#1694),
// or nil when the alert cannot exist in this deployment: no database, or
// notifications turned off in YAML. Like the review alert's store it is
// stateless over the pool, so the admin API and the sweep each build one rather
// than sharing a handle whose lifecycle it does not have.
func ConnAlertStore(p *platform.Platform) *connalert.PostgresStore {
	if p == nil || p.DB() == nil || !p.Config().Notifications.IsEnabled() {
		return nil
	}
	return connalert.NewPostgresStore(p.DB())
}

// ConnAlertSettings narrows the store to the half the admin settings surface
// needs, or nil when the alert cannot exist here. A nil result unmounts the
// admin routes: an operator must not be able to name recipients for an alert
// nothing will ever send. The explicit nil check keeps a typed nil out of the
// interface.
func ConnAlertSettings(p *platform.Platform) connalert.SettingsStore {
	store := ConnAlertStore(p)
	if store == nil {
		return nil
	}
	return store
}

// buildConnAlertConfig assembles what the revocation alert and its escalation
// both need, or the zero config when anything is absent.
func buildConnAlertConfig(p *platform.Platform, notify *notifydelivery.Handle) connalert.Config {
	store := ConnAlertStore(p)
	if store == nil || notify == nil {
		return connalert.Config{}
	}
	return connalert.Config{
		Settings: store,
		Alerts:   store,
		Enqueuer: notify.Enqueuer(),
		BaseURL:  p.Config().Portal.PublicBaseURL,
	}
}

// WireConnRevocationAlert tells the auth-event writer where to announce a
// discarded credential (#1694), and returns the sweep that escalates one nobody
// has acted on. Both are nil when the alert cannot exist here.
//
// The writer is built with the token store, long before the notification
// substrate exists, so the sink is attached here rather than at construction.
// It is the one dependency every connoauth.Source already carries, which is why
// the announcement rides it instead of being threaded a second time through
// every toolkit that wires OAuth.
func WireConnRevocationAlert(p *platform.Platform, notify *notifydelivery.Handle) *connalert.Escalator {
	cfg := buildConnAlertConfig(p, notify)
	alerter := connalert.NewAlerter(cfg)
	if alerter == nil {
		return nil
	}
	p.AuthEventWriter().WithRevocations(alerter)
	log.Println("Connection revocation alerts enabled")
	return connalert.NewEscalator(cfg)
}

// Brand is what an email says it is from and where its footer links go. The
// composition root resolves both -- the brand name has fallbacks that belong to
// the portal's config, and the unsubscribe link needs the browser-session
// signing key -- so they arrive here rather than being derived twice.
type Brand struct {
	// Name is the deployment's brand name, already resolved through its
	// fallbacks.
	Name string
	// UnsubscribeURL builds the no-login opt-out link for an address, or nil
	// when the endpoint cannot be served.
	UnsubscribeURL func(email string) string
}

// BuildNotifications assembles the email-notification substrate from the
// platform's database, encryptor, and the brand supplied. Returns nil when the
// feature is unavailable (no platform, no database) or disabled by config;
// every consumer of the handle is nil-safe.
func BuildNotifications(p *platform.Platform, brand Brand) *notifydelivery.Handle {
	if p == nil || !p.Config().Notifications.IsEnabled() {
		return nil
	}
	handle, err := notifydelivery.New(notifydelivery.Config{
		DB:        p.DB(),
		DSN:       p.Config().Database.DSN,
		Encryptor: p.RestEncryptor(),
		Branding: notifyrender.Branding{
			Name:            brand.Name,
			BaseURL:         p.Config().Portal.PublicBaseURL,
			ImplementorName: p.Config().Portal.Implementor.Name,
			ImplementorURL:  p.Config().Portal.Implementor.URL,
			TermsURL:        p.Config().Portal.TermsURL,
			PrivacyURL:      p.Config().Portal.PrivacyURL,
			AboutText:       p.Config().Portal.AboutText,
			SupportContact:  p.Config().Portal.SupportContact,
			ReplyTo:         emailReplyTo(p.Config().Portal.ReplyTo),
			LogoPNG:         emailLogo(p.Config().Portal.LogoEmail),
		},
		DigestHourUTC:  p.Config().Notifications.DigestHour(),
		UnsubscribeURL: brand.UnsubscribeURL,
	})
	if err != nil {
		// A renderer build failure means broken embedded templates — a build
		// defect, not an operator error. Degrade to no notifications.
		log.Println("WARNING: email notifications unavailable:", err)
		return nil
	}
	if handle != nil {
		log.Println("Email notifications enabled (queue + send worker)")
	}
	return handle
}

// emailReplyTo validates the configured portal.reply_to once at startup. An
// invalid address is dropped with a warning rather than failing every send
// with an opaque per-message error.
func emailReplyTo(addr string) string {
	if addr == "" {
		return ""
	}
	if _, err := mail.ParseAddress(addr); err != nil {
		log.Println("WARNING: portal.reply_to is not a valid email address; leaving Reply-To off:", err)
		return ""
	}
	return addr
}

// emailLogo resolves the raster logo for notification emails once at startup,
// so no send path pays a fetch or races on a shared cache. An unset URL is the
// normal case and a failed fetch is not fatal: both leave the logo empty and
// emails render the text wordmark alone.
func emailLogo(url string) []byte {
	if url == "" {
		return nil
	}
	png, err := branding.FetchEmailLogoPNG(url)
	if err != nil {
		log.Println("WARNING: email logo unavailable, using text wordmark:", err)
		return nil
	}
	return png
}

// feedbackNotificationSink is satisfied by the portal toolkit, which serves
// the MCP feedback tool.
type feedbackNotificationSink interface {
	SetFeedbackNotifications(portal.Notifier, portal.MentionResolver)
}

// wireFeedbackToolNotifications hands the MCP feedback tool the same trigger
// and mention resolver the portal REST handlers use, so a reply an agent
// writes notifies exactly the people a reply written in the portal does.
// Toolkits are constructed before the notification substrate exists, so this
// runs here rather than at toolkit construction.
//
// It runs only on the HTTP path, which is where that substrate lives: under
// stdio the feedback tool stores replies but mails nothing (see
// portal.Toolkit.SetFeedbackNotifications).
func wireFeedbackToolNotifications(p *platform.Platform, notifier portal.Notifier, audience *mention.Audience) {
	registry := p.ToolkitRegistry()
	if registry == nil {
		return
	}
	var resolver portal.MentionResolver
	if audience != nil {
		resolver = mention.NewService(audience)
	}
	for _, tk := range registry.GetByKind(PortalToolkitKind) {
		if sink, ok := tk.(feedbackNotificationSink); ok {
			sink.SetFeedbackNotifications(notifier, resolver)
		}
	}
}

// PortalToolkitKind is the registry kind of the asset-portal toolkit.
const PortalToolkitKind = "portal"

// WirePortalNotifications attaches the notification substrate to the portal
// dependency set: the share/thread trigger bridge and the self-scoped
// preference routes. A nil handle leaves both unset (feature unavailable).
//
// The mention audience is supplied rather than built here: the composition root
// builds one and hands the same one to every surface that resolves a mention.
func WirePortalNotifications(deps *portal.Deps, p *platform.Platform, notify *notifydelivery.Handle, audience *mention.Audience) {
	if notify == nil {
		return
	}
	stores := notifydelivery.PortalStores{
		Assets:         p.PortalAssetStore(),
		Collections:    p.PortalCollectionStore(),
		Prompts:        p.PromptStore(),
		KnowledgePages: p.PortalKnowledgePageStore(),
	}
	// Assign only a live audience: a typed nil in the interface field would
	// read as wired and panic on the first lookup.
	if audience != nil {
		stores.Grantees = audience
	}
	if bridge := notify.PortalNotifier(stores, p.Config().Portal.PublicBaseURL); bridge != nil {
		deps.Notifier = bridge
		wireFeedbackToolNotifications(p, bridge, audience)
	}
	callerEmail := func(r *http.Request) string {
		if user := portal.GetUser(r.Context()); user != nil {
			return user.Email
		}
		return ""
	}
	prefsAPI := &notifyhttp.PrefsAPI{
		Store: notify.Prefs(),
		// Backs delivery_available: the settings page states plainly when no
		// SMTP path exists rather than offering live controls over a
		// preference nothing can act on (#1099).
		Settings:  notify.Settings(),
		UserEmail: callerEmail,
	}
	historyAPI := &notifyhttp.HistoryAPI{
		Store:     notify.History(),
		UserEmail: callerEmail,
		Retention: notifydelivery.HistoryRetention,
	}
	// Both surfaces are self-scoped to the same caller identity, so they
	// resolve it through one function rather than two spellings of it.
	deps.NotificationRegistrar = func(mux *http.ServeMux) {
		prefsAPI.Register(mux)
		historyAPI.Register(mux)
	}
}
