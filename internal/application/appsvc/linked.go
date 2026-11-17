package appsvc

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// Linked and coupled site parts (#2679). Being a part of an application gives
// a site environment same-origin endpoints, the application's cookies and its
// viewer; whether an application release ships it is a second switch, the
// part's release mode. A coupled part moves one step with every release and
// returns with every rollback, as every site part did before. A linked part
// moves only by the site's own promote and rollback, unless a release names it
// in MoveSites, and a rollback returns it only when the release rolled back
// moved it.
//
// The application declares a contract version, raised by hand when a release
// breaks what its front ends call, and a site release may declare the range
// it works with in its _application file. A release is refused while a site
// part would serve a release whose range excludes the contract, unless an
// administrator forces it, which the release records.

// ContractSites is what the contract check reads of a site part.
type ContractSites interface {
	// ServedContract is the release an environment serves and the contract
	// it declares, nil for none.
	ServedContract(ctx context.Context, siteName, environment string) (int, *site.Contract, error)
	// ReleaseContract is the contract a release declares, nil for none.
	ReleaseContract(ctx context.Context, siteName string, number int) (*site.Contract, error)
}

// ReleaseOptions is how a release is made.
type ReleaseOptions struct {
	Note   string
	Action string
	// MoveSites are site parts ("<site>/<environment>") this release moves one
	// step besides the coupled ones; a rollback of the release returns them.
	MoveSites []string
	// Contract is the contract version the release declares; nil keeps the
	// application's.
	Contract *int
	// Force releases over a site part whose release does not work with the
	// contract; the release records each such part.
	Force bool
}

// ReleaseWith is Release with the options #2679 adds.
func (s *Service) ReleaseWith(ctx context.Context, actor application.Actor, ref string, o ReleaseOptions) (*application.Release, error) {
	if o.Action == "" {
		o.Action = application.ActionRelease
	}
	return s.makeRelease(ctx, actor, ref, o)
}

// moving is the set of site parts a release moves besides the coupled ones,
// refusing a name that is not one of the application's site parts.
func moving(a *application.Application, names []string) (map[string]bool, error) {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		if !slices.ContainsFunc(a.Parts, func(p application.Part) bool { return p.Kind == application.KindSite && p.ID == n }) {
			return nil, refused("move_sites names %q, which is not a site part of %s", n, a.Name)
		}
		out[n] = true
	}
	return out, nil
}

// moves reports whether a release moves a part: every part but a linked site
// environment the release does not name.
func moves(p application.Part, named map[string]bool) bool {
	return p.Kind != application.KindSite || p.ReleaseMode != application.ModeLinked || named[p.ID]
}

// unmoved is the site parts a release leaves alone, by id.
func unmoved(a *application.Application, named map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, p := range a.Parts {
		if !moves(p, named) {
			out[p.ID] = true
		}
	}
	return out
}

// withoutSites is pins without the site parts in skip: what a diff compares,
// so a linked part the release leaves alone is not read as unpinned.
func withoutSites(pins []application.Pin, skip map[string]bool) []application.Pin {
	out := make([]application.Pin, 0, len(pins))
	for _, p := range pins {
		if p.Kind == application.KindSite && skip[p.PartID] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// contractProblems is each site part that would serve, after the release, a
// release declaring a range that excludes version.
func (s *Service) contractProblems(ctx context.Context, a *application.Application, pins []application.Pin) []string {
	cs := s.Contracts
	if cs == nil || a.ContractVersion == 0 {
		return nil
	}
	pinned := map[string]int{}
	for _, p := range pins {
		if p.Kind == application.KindSite {
			pinned[p.PartID] = p.Version
		}
	}
	var out []string
	for _, p := range a.Parts {
		if problem := partContractProblem(ctx, cs, a, p, pinned[p.ID]); problem != "" {
			out = append(out, problem)
		}
	}
	return out
}

// partContractProblem is why one part breaks the contract, "" when it does
// not: a site part whose release after the release excludes version.
func partContractProblem(ctx context.Context, cs ContractSites, a *application.Application, p application.Part, pinned int) string {
	app, version := a.Name, a.ContractVersion
	name, env, ok := application.ParseSitePartID(p.ID)
	if p.Kind != application.KindSite || !ok {
		return ""
	}
	number, c, err := contractAfter(ctx, cs, name, env, pinned)
	if err != nil || number == 0 || !c.For(app) || c.Accepts(version) {
		return ""
	}
	return fmt.Sprintf("%s would serve release %d, which works with contract %s, not %d", p.ID, number, c.Range, version)
}

// contractAfter is the release a site part serves after a release and its
// contract: the pinned one when the release moves it, else the one it serves.
func contractAfter(ctx context.Context, cs ContractSites, name, env string, pinned int) (int, *site.Contract, error) {
	if pinned == 0 {
		return cs.ServedContract(ctx, name, env) //nolint:wrapcheck // the check reads it as data
	}
	c, err := cs.ReleaseContract(ctx, name, pinned)
	return pinned, c, err //nolint:wrapcheck // the check reads it as data
}

// rollbackSitePins is the site pins a rollback applies: a coupled part's pin
// in the target release, and a linked part the rolled-back release moved,
// returned to the release it served before that move. A linked part the
// rolled-back release left alone stays where the site put it.
func rollbackSitePins(a *application.Application, current, target *application.Release) []application.Pin {
	mode := map[string]string{}
	for _, p := range a.Parts {
		if p.Kind == application.KindSite {
			mode[p.ID] = p.ReleaseMode
		}
	}
	out := []application.Pin{}
	for _, p := range target.Pins {
		if p.Kind == application.KindSite && mode[p.PartID] == application.ModeLinked {
			continue
		}
		out = append(out, p)
	}
	for _, c := range current.Changes.Parts {
		if c.Kind == application.KindSite && mode[c.PartID] == application.ModeLinked && c.From > 0 {
			out = append(out, application.Pin{Kind: application.KindSite, PartID: c.PartID, Version: c.From})
		}
	}
	return out
}

// SetPartMode changes a site environment part's release mode (#2679): the
// owner or an administrator. It moves nothing: the next release reads it.
func (s *Service) SetPartMode(ctx context.Context, actor application.Actor, ref, id, mode string) (*application.Application, error) {
	a, err := s.editable(ctx, actor, ref)
	if err != nil {
		return nil, err
	}
	if !application.ValidMode(mode) {
		return nil, refused("release_mode %q: linked or coupled", mode)
	}
	if !slices.ContainsFunc(a.Parts, func(p application.Part) bool { return p.Kind == application.KindSite && p.ID == id }) {
		return nil, refused("%s has no site part %q", a.Name, id)
	}
	if err := s.Store.SetPartMode(ctx, a.ID, id, mode); err != nil {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	return s.Store.Get(ctx, a.ID) //nolint:wrapcheck // the store's errors are read by identity
}

// SetContract sets the contract version the application declares (#2679):
// the owner or an administrator. A release checks it; setting it moves
// nothing.
func (s *Service) SetContract(ctx context.Context, actor application.Actor, ref string, version int) (*application.Application, error) {
	a, err := s.editable(ctx, actor, ref)
	if err != nil {
		return nil, err
	}
	if version < 0 {
		return nil, refused("contract_version is a whole number, 0 for none")
	}
	a.ContractVersion = version
	if err := s.Store.Update(ctx, a); err != nil {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	return a, nil
}

// partMode is the release mode a part of kind is added with: what the caller
// asked for, linked when nothing (#2679), refusing a kind that is none.
func partMode(kind, mode string) (string, error) {
	if !application.ValidKind(kind) {
		return "", refused("kind %q: one of script, asset, source, schema, site", kind)
	}
	if kind != application.KindSite {
		if mode != "" {
			return "", refused("release_mode is a site part's; a %s part has none", kind)
		}
		return "", nil
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return application.ModeLinked, nil
	}
	if !application.ValidMode(mode) {
		return "", refused("release_mode %q: linked or coupled", mode)
	}
	return mode, nil
}

// SitePartView is one site part as the Parts card and the release dialog read
// it (#2679).
type SitePartView struct {
	PartID string `json:"part_id" example:"store-web/prod"`
	Mode   string `json:"release_mode" example:"linked"`
	// Serves is the release the environment serves now, 0 for none.
	Serves int `json:"serves" example:"22"`
	// Step is the release a release moving it would point it at, 0 for none.
	Step int `json:"step" example:"24"`
	// Contract is the range the release it serves declares for this
	// application, "" for none.
	Contract string `json:"contract" example:">=2 <4"`
	// Compatible is whether that release works with the application's
	// contract.
	Compatible bool `json:"compatible"`
}

// sitePartViews is each site part's view; a part that cannot be read is
// left out, and named as a problem by StatusOf's pins.
func (s *Service) sitePartViews(ctx context.Context, a *application.Application) []SitePartView {
	out := []SitePartView{}
	cs := s.Contracts
	if cs == nil {
		return out
	}
	for _, p := range a.Parts {
		name, env, okID := application.ParseSitePartID(p.ID)
		if p.Kind != application.KindSite || !okID {
			continue
		}
		serves, c, err := cs.ServedContract(ctx, name, env)
		if err != nil {
			continue
		}
		v := SitePartView{PartID: p.ID, Mode: p.ReleaseMode, Serves: serves, Compatible: true}
		if step, err := s.Sites.StepRelease(ctx, name, env); err == nil {
			v.Step = step
		}
		if c.For(a.Name) {
			v.Contract, v.Compatible = c.Range, c.Accepts(a.ContractVersion)
		}
		out = append(out, v)
	}
	return out
}

// servedBefore fills From on each site part a release moves that the previous
// release did not pin, with the release the environment serves before the
// move, so a rollback of the release returns it there (#2679). A part whose
// environment cannot be read keeps 0 and a rollback leaves it alone.
func (s *Service) servedBefore(ctx context.Context, changes []application.PinChange) {
	cs := s.Contracts
	if cs == nil {
		return
	}
	for i, c := range changes {
		name, env, okID := application.ParseSitePartID(c.PartID)
		if c.Kind != application.KindSite || c.From != 0 || !okID {
			continue
		}
		if n, _, err := cs.ServedContract(ctx, name, env); err == nil {
			changes[i].From = n
		}
	}
}

// currentSitePart reads a site part named by dev's old name (#2604) as dev,
// for one release; every other part is as given.
func currentSitePart(kind, id string) string {
	if name, env, ok := application.ParseSitePartID(id); ok && kind == application.KindSite && env == "default" {
		return name + "/dev"
	}
	return id
}

// releasePins is what a release moves (#2679): the parts it pins, the linked
// site parts named in o.MoveSites, and the site parts an administrator's
// force releases over the contract; the contract o declares is set on a.
func (s *Service) releasePins(ctx context.Context, a *application.Application, o ReleaseOptions) (*releasePlan, error) {
	named, err := moving(a, o.MoveSites)
	if err != nil {
		return nil, err
	}
	pins, problems := s.newestPins(ctx, a, named)
	if len(problems) > 0 {
		return nil, refused("nothing was released: %s", strings.Join(problems, "; "))
	}
	if o.Contract != nil {
		if *o.Contract < 0 {
			return nil, refused("contract is a whole number, 0 for none")
		}
		a.ContractVersion = *o.Contract
	}
	forced := s.contractProblems(ctx, a, pins)
	if len(forced) > 0 && !o.Force {
		return nil, refused("nothing was released: contract %d breaks a site part: %s. Publish and promote a release "+
			"whose _application file accepts it, move the part in this release, or release with force",
			a.ContractVersion, strings.Join(forced, "; "))
	}
	return &releasePlan{named: named, pins: pins, forced: forced}, nil
}

// releasePlan is what a release moves: the linked parts it names, every pin,
// and the parts a force releases over the contract.
type releasePlan struct {
	named  map[string]bool
	pins   []application.Pin
	forced []string
}
