package appsvc

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The states a part is read in (#2527).
const (
	// PartOK is a part the caller reads.
	PartOK = "ok"
	// PartDeleted is a part whose script, asset or webhook source is gone.
	PartDeleted = "deleted"
	// PartNotVisible is an asset the caller cannot open: its name is not
	// told.
	PartNotVisible = "not_visible"
)

// Schedules reads a script's schedule; script.ErrScheduleNotFound for none.
type Schedules interface {
	GetSchedule(ctx context.Context, scriptID string) (*script.Schedule, error)
}

// SourceActivity counts the requests a webhook source accepted since a time.
type SourceActivity interface {
	AcceptedSince(ctx context.Context, name string, since time.Time) (int64, error)
}

// AssetViewer reports whether the caller of a request opens an asset.
type AssetViewer func(ctx context.Context, asset *portaldomain.Asset) bool

// PartSchedule is a script part's schedule, which the page puts in words.
type PartSchedule struct {
	CronSpec string `json:"cron_spec" example:"0 2 * * *"`
	Timezone string `json:"timezone" example:"UTC"`
	Enabled  bool   `json:"enabled" example:"true"`
}

// PartView is one part as an application's page shows it (#2527): its name
// and the facts its description line is built from, never only its id. A
// part the caller cannot open, or one that is gone, carries its state and no
// name.
type PartView struct {
	Kind  string `json:"kind" example:"script"`
	ID    string `json:"id" example:"5b0c..."`
	State string `json:"state" example:"ok"`
	Name  string `json:"name,omitempty" example:"Nightly order sync"`
	// Description is the script's or asset's own description.
	Description string `json:"description,omitempty"`
	// LatestVersion is the script's or asset's newest version; nil for a
	// webhook source and for a part not read.
	LatestVersion *int          `json:"latest_version"`
	OwnerEmail    string        `json:"owner_email,omitempty"`
	Schedule      *PartSchedule `json:"schedule,omitempty"`
	// EventSource is the webhook source a script runs for, empty for none.
	EventSource string `json:"event_source,omitempty"`
	// ContentType is an asset's media type.
	ContentType string     `json:"content_type,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	// RequestsLastDay is how many requests a webhook source accepted in the
	// last 24 hours; told to administrators, who manage sources.
	RequestsLastDay *int64    `json:"requests_last_day,omitempty"`
	AddedBy         string    `json:"added_by"`
	AddedAt         time.Time `json:"added_at"`
}

// Describe reads each of an application's parts for the actor: a script
// (whose definition everyone signed in reads), an asset only when view says
// the caller opens it, and a webhook source by its name.
func (s *Service) Describe(ctx context.Context, actor application.Actor, parts []application.Part, view AssetViewer) []PartView {
	out := make([]PartView, 0, len(parts))
	for _, p := range parts {
		v := PartView{Kind: p.Kind, ID: p.ID, State: PartOK, AddedBy: p.AddedBy, AddedAt: p.AddedAt}
		switch p.Kind {
		case application.KindScript:
			s.describeScript(ctx, &v)
		case application.KindAsset:
			s.describeAsset(ctx, actor, &v, view)
		case application.KindSource:
			s.describeSource(ctx, actor, &v)
		case application.KindSchema, application.KindSite:
			v.Name = p.ID
		}
		out = append(out, v)
	}
	return out
}

func (s *Service) describeScript(ctx context.Context, v *PartView) {
	sc, err := s.script(ctx, v.ID)
	if err != nil {
		v.State = PartDeleted
		return
	}
	v.Name, v.Description, v.OwnerEmail, v.EventSource = scriptName(sc), sc.Description, sc.OwnerEmail, sc.EventSource
	v.LatestVersion = new(sc.Version)
	updated := sc.UpdatedAt
	v.UpdatedAt = &updated
	if s.Schedules == nil {
		return
	}
	if sched, err := s.Schedules.GetSchedule(ctx, sc.ID); err == nil && sched != nil {
		v.Schedule = &PartSchedule{CronSpec: sched.CronSpec, Timezone: sched.Timezone, Enabled: sched.Enabled}
	}
}

func (s *Service) describeAsset(ctx context.Context, actor application.Actor, v *PartView, view AssetViewer) {
	as, err := s.asset(ctx, v.ID)
	if err != nil {
		v.State = PartDeleted
		return
	}
	if !actor.Admin && (view == nil || !view(ctx, as)) {
		v.State = PartNotVisible
		return
	}
	v.Name, v.Description, v.OwnerEmail, v.ContentType = as.Name, as.Description, as.OwnerEmail, as.ContentType
	v.LatestVersion = new(as.CurrentVersion)
	updated := as.UpdatedAt
	v.UpdatedAt = &updated
}

func (s *Service) describeSource(ctx context.Context, actor application.Actor, v *PartView) {
	if s.Sources != nil {
		if ok, err := s.Sources.Exists(ctx, v.ID); err == nil && !ok {
			v.State = PartDeleted
			return
		}
	}
	v.Name = v.ID
	if !actor.Admin || s.Activity == nil {
		return
	}
	if n, err := s.Activity.AcceptedSince(ctx, v.ID, time.Now().Add(-24*time.Hour)); err == nil {
		v.RequestsLastDay = &n
	}
}

// scriptName is what a script is called on a page: its display name, else
// its name.
func scriptName(sc *script.Script) string {
	if sc.DisplayName != "" {
		return sc.DisplayName
	}
	return sc.Name
}

// MaxCandidates bounds the candidates listed of each kind.
const MaxCandidates = 50

// Candidate is one script, asset, webhook source or site environment the
// actor may add.
type Candidate struct {
	Kind        string     `json:"kind" example:"script"`
	ID          string     `json:"id" example:"5b0c..."`
	Name        string     `json:"name" example:"Monthly revenue close"`
	OwnerEmail  string     `json:"owner_email,omitempty"`
	ContentType string     `json:"content_type,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	// Application is the application the part already belongs to, which
	// refuses it here; nil for none.
	Application *AppRef `json:"application,omitempty"`
}

// AppRef names an application.
type AppRef struct {
	ID   string `json:"id" example:"app_01J9..."`
	Name string `json:"name" example:"billing"`
}

// Candidates is a search for parts to add, with how many of each kind
// matched.
type Candidates struct {
	Candidates []Candidate    `json:"candidates"`
	Counts     map[string]int `json:"counts"`
}

// CandidateLister lists the scripts and assets a search reads.
type CandidateLister interface {
	ListScripts(ctx context.Context, filter script.ListFilter) ([]script.Script, error)
	ListAssets(ctx context.Context, filter portaldomain.AssetFilter) ([]portaldomain.Asset, int, error)
	// ListSources lists webhook source names matching query.
	ListSources(ctx context.Context, query string, limit int) ([]string, error)
}

// FindCandidates lists the parts the actor may add to an application whose
// name matches query, by the rules AddPart applies: their own scripts, assets
// and site environments (every one for an administrator) and, for an
// administrator, webhook sources. A part already in this application is left out; one in another
// application is listed with that application, which refuses it. kind
// narrows the list but not the counts.
func (s *Service) FindCandidates(ctx context.Context, actor application.Actor, ref, query, kind string) (*Candidates, error) {
	a, err := s.editable(ctx, actor, ref)
	if err != nil {
		return nil, err
	}
	if s.Lister == nil {
		return &Candidates{Candidates: []Candidate{}, Counts: map[string]int{}}, nil
	}
	held, err := s.holders(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	all := []Candidate{}
	for _, read := range []func(context.Context, application.Actor, string) ([]Candidate, error){
		s.scriptCandidates, s.assetCandidates, s.sourceCandidates, s.siteCandidates,
	} {
		found, err := read(ctx, actor, query)
		if err != nil {
			return nil, err
		}
		all = append(all, found...)
	}
	return arrange(all, held, a.ID, kind), nil
}

// arrange leaves out this application's parts, marks another's, counts every
// kind and keeps the kind asked for.
func arrange(all []Candidate, held map[[2]string]AppRef, appID, kind string) *Candidates {
	out := &Candidates{Candidates: []Candidate{}, Counts: map[string]int{}}
	for _, c := range all {
		owner, inApp := held[[2]string{c.Kind, c.ID}]
		if inApp && owner.ID == appID {
			continue
		}
		if inApp {
			c.Application = &owner
		}
		out.Counts[c.Kind]++
		if kind == "" || kind == c.Kind {
			out.Candidates = append(out.Candidates, c)
		}
	}
	return out
}

// holders maps each part to the application it belongs to.
func (s *Service) holders(ctx context.Context) (map[[2]string]AppRef, error) {
	apps, err := s.Store.List(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	held := map[[2]string]AppRef{}
	for _, a := range apps {
		for _, p := range a.Parts {
			held[[2]string{p.Kind, p.ID}] = AppRef{ID: a.ID, Name: a.Name}
		}
	}
	return held, nil
}

func (s *Service) scriptCandidates(ctx context.Context, actor application.Actor, query string) ([]Candidate, error) {
	f := script.ListFilter{Search: query, Limit: MaxCandidates}
	if !actor.Admin {
		f.OwnerEmail = actor.Email
	}
	scripts, err := s.Lister.ListScripts(ctx, f)
	if err != nil {
		return nil, err //nolint:wrapcheck // a store failure is the request's failure
	}
	out := make([]Candidate, 0, len(scripts))
	for i := range scripts {
		sc := &scripts[i]
		updated := sc.UpdatedAt
		out = append(out, Candidate{Kind: application.KindScript, ID: sc.ID, Name: scriptName(sc), OwnerEmail: sc.OwnerEmail, UpdatedAt: &updated})
	}
	return out, nil
}

func (s *Service) assetCandidates(ctx context.Context, actor application.Actor, query string) ([]Candidate, error) {
	f := portaldomain.AssetFilter{Search: query, Limit: MaxCandidates}
	if !actor.Admin {
		f.Owners = []string{actor.Email}
	}
	assets, _, err := s.Lister.ListAssets(ctx, f)
	if err != nil {
		return nil, err //nolint:wrapcheck // a store failure is the request's failure
	}
	out := make([]Candidate, 0, len(assets))
	for i := range assets {
		as := &assets[i]
		if as.DeletedAt != nil {
			continue
		}
		updated := as.UpdatedAt
		out = append(out, Candidate{
			Kind: application.KindAsset, ID: as.ID, Name: as.Name, OwnerEmail: as.OwnerEmail,
			ContentType: as.ContentType, UpdatedAt: &updated,
		})
	}
	return out, nil
}

func (s *Service) sourceCandidates(ctx context.Context, actor application.Actor, query string) ([]Candidate, error) {
	if !actor.Admin {
		return nil, nil
	}
	names, err := s.Lister.ListSources(ctx, query, MaxCandidates)
	if err != nil {
		return nil, err //nolint:wrapcheck // a store failure is the request's failure
	}
	slices.Sort(names)
	out := make([]Candidate, 0, len(names))
	for _, n := range names {
		out = append(out, Candidate{Kind: application.KindSource, ID: n, Name: n})
	}
	return out, nil
}

// siteCandidates lists, by site and then environment, the site environments
// whose "<site>/<environment>" holds query that the actor may add, by the rule
// mayAddSite applies: the environments of their own sites, every site's for an
// administrator, none where the deployment has no sites (#2613).
func (s *Service) siteCandidates(ctx context.Context, actor application.Actor, query string) ([]Candidate, error) {
	if s.Sites == nil {
		return nil, nil
	}
	envs, err := s.Sites.SiteEnvironments(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // a store failure is the request's failure
	}
	slices.SortFunc(envs, func(a, b application.SiteEnvironment) int {
		return cmp.Or(strings.Compare(a.Site, b.Site), strings.Compare(a.Environment, b.Environment))
	})
	query = strings.ToLower(query)
	out := []Candidate{}
	for _, e := range envs {
		id := application.SitePartID(e.Site, e.Environment)
		if (!actor.Admin && !strings.EqualFold(e.Owner, actor.Email)) || !strings.Contains(strings.ToLower(id), query) {
			continue
		}
		c := Candidate{Kind: application.KindSite, ID: id, Name: id, OwnerEmail: e.Owner}
		if !e.UpdatedAt.IsZero() {
			at := e.UpdatedAt
			c.UpdatedAt = &at
		}
		if out = append(out, c); len(out) == MaxCandidates {
			break
		}
	}
	return out, nil
}
