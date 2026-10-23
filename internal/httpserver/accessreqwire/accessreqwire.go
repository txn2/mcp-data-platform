// Package accessreqwire assembles requests for access (#2090) from the
// platform: the item lookups, the share an approval creates, the emails and
// the settings section.
package accessreqwire

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/iam"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/accessreq"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/resourceshare"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Deps is what the routes are mounted with: the portal's authentication
// (Wrap) and administrator rule, the notification handle the emails go
// through, and the admin API's authentication (AdminWrap, nil mounts no
// settings routes) with the author a settings write is recorded under.
type Deps struct {
	Wrap      func(http.Handler) http.Handler
	IsAdmin   func([]string) bool
	Notify    *notifydelivery.Handle
	AdminWrap func(http.Handler) http.Handler
	Author    func(*http.Request) string
}

// Mount registers requests for access: the page and the request a person who
// cannot open an item makes, the owner's answers, and the admin settings. A
// deployment without a database or a share store has none.
func Mount(mux *http.ServeMux, p *platform.Platform, d Deps) {
	if p.DB() == nil || p.PortalShareStore() == nil {
		return
	}
	register(mux, p.DB(), stores{
		assets: p.PortalAssetStore(), collections: p.PortalCollectionStore(),
		prompts: p.PromptStore(), shares: p.PortalShareStore(), personas: p.PersonaRegistry(),
		resources: resourcesOf(p), resourceShares: resourceshare.NewStore(p.DB()),
	}, p.Config().Portal.PublicBaseURL, d)
}

// resourcesOf is the platform's resource store, nil without one.
func resourcesOf(p *platform.Platform) resourceReader {
	return p.ResourceStore()
}

// register mounts the routes over the database and the stores, with the
// emails linking to base.
func register(mux *http.ServeMux, db *sql.DB, st stores, base string, d Deps) {
	st.isAdmin = d.IsAdmin
	st.checker = access.New(access.Config{
		Assets: st.assets, Collections: st.collections, Shares: st.shares, Prompts: st.prompts, AdminRule: d.IsAdmin,
	})
	settings := sectionSettings{section: settingsjson.New[accessreq.Settings](db, accessreq.SettingsSection)}
	deps := accessreq.Deps{
		Store:    accessreq.NewStore(db),
		Resolve:  st.resolve,
		CanOpen:  st.canOpen,
		IsAdmin:  func(u *access.User) bool { return u != nil && d.IsAdmin(u.Roles) },
		CanShare: canShare(st.personas),
		Settings: settings.Read,
		Share:    st.sharer(),
	}
	if bridge := d.Notify.PortalNotifier(notifydelivery.PortalStores{}, base); bridge != nil {
		deps.NotifyOwner, deps.NotifyRequester = notices(bridge, base)
	}
	accessreq.Register(mux, d.Wrap, deps)
	if d.AdminWrap != nil {
		accessreq.RegisterSettings(mux, d.AdminWrap, settings, d.Author)
	}
}

// canShare is Deps.CanShare over the persona registry: the persona the
// user's roles resolve to lets its people share.
func canShare(reg *persona.Registry) func(u *access.User) bool {
	return func(u *access.User) bool {
		per, ok := reg.GetForRoles(u.Roles)
		return ok && per.SharingAllowed()
	}
}

// stores are the item stores a request reads and the share store an
// approval writes, with the checker that says whether a person opens an item.
type stores struct {
	assets      portaldomain.AssetStore
	collections portaldomain.CollectionStore
	prompts     prompt.Store
	shares      portaldomain.ShareStore
	checker     *access.Checker
	// personas resolves the persona whose sharing an approval is held to.
	personas *persona.Registry
	// resources and resourceShares are the files a request can name and the
	// shares an approval of one writes (#2258); nil resources names none.
	resources      resourceReader
	resourceShares resourceShares
	// isAdmin is the administrator rule, for whether a person opens a file.
	isAdmin func([]string) bool
}

// resourceReader reads a file.
type resourceReader interface {
	Get(ctx context.Context, id string) (*resource.Resource, error)
}

// resourceShares is what a request for a file reads and writes.
type resourceShares interface {
	Insert(ctx context.Context, share *portaldomain.Share) error
	Permission(ctx context.Context, resourceID string, rcpt resourceshare.Recipient) (string, error)
}

var errNoItem = errors.New("no such item")

// resolve reads the item a request names: a live asset or collection, or a
// prompt by its display name when it has one.
func (s stores) resolve(ctx context.Context, targetType, id string) (accessreq.Target, error) {
	var t *accessreq.Target
	switch targetType {
	case accessreq.TargetAsset:
		t = s.asset(ctx, id)
	case accessreq.TargetCollection:
		t = s.collection(ctx, id)
	case accessreq.TargetPrompt:
		t = s.prompt(ctx, id)
	case accessreq.TargetResource:
		t = s.resource(ctx, id)
	}
	if t == nil {
		return accessreq.Target{}, errNoItem
	}
	return *t, nil
}

func (s stores) asset(ctx context.Context, id string) *accessreq.Target {
	a, err := s.assets.Get(ctx, id)
	if err != nil || a == nil || a.DeletedAt != nil {
		return nil
	}
	return &accessreq.Target{Name: a.Name, OwnerID: a.OwnerID, OwnerEmail: a.OwnerEmail}
}

func (s stores) collection(ctx context.Context, id string) *accessreq.Target {
	c, err := s.collections.Get(ctx, id)
	if err != nil || c == nil || c.DeletedAt != nil {
		return nil
	}
	return &accessreq.Target{Name: c.Name, OwnerID: c.OwnerID, OwnerEmail: c.OwnerEmail}
}

func (s stores) resource(ctx context.Context, id string) *accessreq.Target {
	if s.resources == nil {
		return nil
	}
	r, err := s.resources.Get(ctx, id)
	if err != nil || r == nil {
		return nil
	}
	return &accessreq.Target{Name: r.DisplayName, OwnerID: r.UploaderSub, OwnerEmail: r.UploaderEmail}
}

// resourceOpens reports whether the person already reaches the file: through
// a folder they see, authority over its folder, or a share.
func (s stores) resourceOpens(ctx context.Context, u *access.User, id string) bool {
	r, err := s.resources.Get(ctx, id)
	if err != nil || r == nil {
		return false
	}
	name := ""
	if per, ok := s.personas.GetForRoles(u.Roles); ok {
		name = per.Name
	}
	if perm, err := s.resourceShares.Permission(ctx, id, resourceshare.Recipient{UserID: u.UserID, Email: u.Email, Persona: name}); err == nil && perm != "" {
		return true
	}
	claims := resource.BuildClaims(u.UserID, u.Email, name, u.Roles, s.isAdmin != nil && s.isAdmin(u.Roles))
	claims.Personas = iam.PersonasForRoles(s.personas)(u.Roles)
	return resource.CanAccessResource(claims, r)
}

func (s stores) prompt(ctx context.Context, id string) *accessreq.Target {
	pr, err := s.prompts.GetByID(ctx, id)
	if err != nil || pr == nil {
		return nil
	}
	return &accessreq.Target{Name: promptName(pr), OwnerEmail: pr.OwnerEmail}
}

func promptName(pr *prompt.Prompt) string {
	if pr.DisplayName != "" {
		return pr.DisplayName
	}
	return pr.Name
}

// canOpen reports whether the user already opens the item.
func (s stores) canOpen(ctx context.Context, u *access.User, targetType, id string) bool {
	switch targetType {
	case accessreq.TargetAsset:
		a, err := s.assets.Get(ctx, id)
		return err == nil && a != nil && s.checker.CanViewAsset(ctx, id, a, u)
	case accessreq.TargetCollection:
		c, err := s.collections.Get(ctx, id)
		return err == nil && c != nil && s.checker.CanViewCollection(ctx, c, u)
	case accessreq.TargetPrompt:
		pr, err := s.prompts.GetByID(ctx, id)
		return err == nil && pr != nil && s.checker.CanViewPrompt(ctx, u, pr)
	case accessreq.TargetResource:
		return s.resources != nil && s.resourceOpens(ctx, u, id)
	}
	return false
}

// sharer creates the person share an approval grants, recorded with origin
// access_request, and returns its id.
func (s stores) sharer() func(ctx context.Context, targetType, id, createdBy, recipient, permission string) (string, error) {
	return func(ctx context.Context, targetType, id, createdBy, recipient, permission string) (string, error) {
		share, err := portaldomain.BuildShare(shareTarget(targetType, id), createdBy, portaldomain.ShareSpec{
			RecipientEmail: recipient, Permission: permission,
		})
		if err != nil {
			return "", err //nolint:wrapcheck // the caller reports it
		}
		share.Origin = portaldomain.OriginAccessRequest
		insert := s.shares.Insert
		if targetType == accessreq.TargetResource {
			insert = s.resourceShares.Insert
		}
		if err := insert(ctx, &share); err != nil {
			return "", err //nolint:wrapcheck // the caller reports it
		}
		return share.ID, nil
	}
}

func shareTarget(targetType, id string) portaldomain.ShareTarget {
	switch targetType {
	case accessreq.TargetCollection:
		return portaldomain.ShareTarget{CollectionID: id}
	case accessreq.TargetPrompt:
		return portaldomain.ShareTarget{PromptID: id}
	case accessreq.TargetResource:
		return portaldomain.ShareTarget{ResourceID: id}
	default:
		return portaldomain.ShareTarget{AssetID: id}
	}
}

// notifier is the email side of a request: the owner told a request waits,
// the requester told how it was answered.
type notifier interface {
	NotifyAccessRequest(ctx context.Context, a notifydelivery.AccessNotice)
	NotifyAccessDecision(ctx context.Context, a notifydelivery.AccessNotice)
}

// notices are the owner's and the requester's emails; an approval links the
// requester to the item at base.
func notices(n notifier, base string) (owner, requester func(context.Context, *accessreq.Request, string)) {
	owner = func(ctx context.Context, req *accessreq.Request, name string) {
		n.NotifyAccessRequest(ctx, notifydelivery.AccessNotice{
			To: req.OwnerEmail, Actor: req.RequesterEmail, ItemTitle: name, ItemID: req.TargetID, Message: req.Message,
		})
	}
	requester = func(ctx context.Context, req *accessreq.Request, name string) {
		outcome, link := "Your request was declined.", ""
		if req.Status == accessreq.StatusApproved {
			outcome = "Your request was approved as " + req.Permission + "."
			link = notification.PortalLink(base, "/"+req.TargetType+"s/"+req.TargetID)
		}
		n.NotifyAccessDecision(ctx, notifydelivery.AccessNotice{
			To: req.RequesterEmail, Actor: req.DecidedBy, ItemTitle: name, ItemID: req.TargetID, Message: outcome, Link: link,
		})
	}
	return owner, requester
}

// sectionSettings reads the section, falling back to the defaults.
type sectionSettings struct {
	section *settingsjson.Section[accessreq.Settings]
}

func (s sectionSettings) Read(ctx context.Context) accessreq.Settings {
	stored, err := s.section.Get(ctx)
	if err != nil || stored.Value.Validate() != nil {
		return accessreq.Defaults()
	}
	return stored.Value
}

func (s sectionSettings) Write(ctx context.Context, v accessreq.Settings, author string) error {
	return s.section.Set(ctx, v, author) //nolint:wrapcheck // the section names the write
}
