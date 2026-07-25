package apigen

import (
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- deterministic fixture generation from a fixed seed is the point; crypto/rand would break reproducibility
	"math/rand"
	"time"
)

// The perishable-knowledge study's world model (#1054). The insights
// surface serves an account whose listening state is perishable: monitors
// are provisioned (or removed) by actors outside the agent's session, so a
// belief captured about that state can be false by the time it is
// delivered. A world is selected by name from the committed registry below
// and mutated between sessions through the fixture service's /_bench/world
// control plane; that mutation is how the study sets a cell's staleness
// probability.

// Access is a credential's entitlement to one product area.
type Access string

const (
	// AccessGranted serves the area normally.
	AccessGranted Access = "granted"
	// AccessForbidden serves HTTP 403 on every operation in the area.
	AccessForbidden Access = "forbidden"
)

// Contract versions. Durable-contract behavior changes only when the
// version does: the fixture's analog of a vendor release.
const (
	Contract20261 = "2026.1"
	Contract20262 = "2026.2"
)

// World is the mutable account state behind the insights surface.
type World struct {
	// Profile is the registry name this world was built from.
	Profile string `json:"profile"`
	// Monitors is how many of the monitor pool are provisioned. A world
	// with N monitors has exactly the pool's first N, so raising the count
	// adds monitors without disturbing the identity of the existing ones.
	Monitors int `json:"monitors"`
	// Listening is the credential's entitlement to the listening area
	// (monitors and their trend series).
	Listening Access `json:"listening"`
	// Contract is the vendor release the API behaves as.
	Contract string `json:"contract"`
	// WorkspaceScoped requires a workspace_id on list_monitors, which
	// raises a state recheck from one call to one per workspace plus the
	// lookup that lists them. With Workspaces it is the study's
	// recheck-cost dial: proving nothing is provisioned means looking
	// everywhere it could be.
	WorkspaceScoped bool `json:"workspace_scoped"`
	// Workspaces is how many of the workspace pool the account exposes.
	// Monitors only ever live in the first two, so the rest are real but
	// empty, exactly as an account accumulates workspaces nobody has set a
	// monitor up in. A scoped recheck costs 1 + Workspaces calls.
	Workspaces int `json:"workspaces"`
}

// worldProfiles is the committed world-profile registry: every world state
// the study may place an account in, named for what it is. Cells are built
// by naming a profile at reset and (for a stale cell) a second profile at
// the session boundary; nothing else varies. The registry is drift-checked
// against the committed dump, so a cell's meaning cannot change silently.
var worldProfiles = []World{
	{Profile: "monitors-0", Monitors: 0, Listening: AccessGranted, Contract: Contract20261, Workspaces: baseWorkspaces},
	{Profile: "monitors-1", Monitors: 1, Listening: AccessGranted, Contract: Contract20261, Workspaces: baseWorkspaces},
	{Profile: "monitors-3", Monitors: 3, Listening: AccessGranted, Contract: Contract20261, Workspaces: baseWorkspaces},
	{Profile: "monitors-6", Monitors: 6, Listening: AccessGranted, Contract: Contract20261, Workspaces: baseWorkspaces},
	// The two forbidden worlds differ only in state the credential cannot
	// see. Their responses must be identical: an unentitled credential
	// learns nothing about provisioning, which is the distinction an agent
	// must not collapse.
	{Profile: "monitors-0-forbidden", Monitors: 0, Listening: AccessForbidden, Contract: Contract20261, Workspaces: baseWorkspaces},
	{Profile: "monitors-3-forbidden", Monitors: 3, Listening: AccessForbidden, Contract: Contract20261, Workspaces: baseWorkspaces},
	{Profile: "monitors-0-released", Monitors: 0, Listening: AccessGranted, Contract: Contract20262, Workspaces: baseWorkspaces},
	{Profile: "monitors-3-released", Monitors: 3, Listening: AccessGranted, Contract: Contract20262, Workspaces: baseWorkspaces},
	{Profile: "monitors-0-scoped", Monitors: 0, Listening: AccessGranted, Contract: Contract20261, WorkspaceScoped: true, Workspaces: baseWorkspaces},
	{Profile: "monitors-3-scoped", Monitors: 3, Listening: AccessGranted, Contract: Contract20261, WorkspaceScoped: true, Workspaces: baseWorkspaces},
	// The recheck-cost sweep. Same empty account, same belief, and the
	// only thing that moves is how many calls it takes to establish that
	// nothing is provisioned: 1 unscoped, then 1 + Workspaces scoped.
	{Profile: "monitors-0-scoped-5", Monitors: 0, Listening: AccessGranted, Contract: Contract20261, WorkspaceScoped: true, Workspaces: 5},
	{Profile: "monitors-0-scoped-10", Monitors: 0, Listening: AccessGranted, Contract: Contract20261, WorkspaceScoped: true, Workspaces: 10},
	// The same sweep for an account that does have monitors. Counting them
	// still costs one listing per workspace: monitors live in the first
	// two, but nothing tells an agent that, so being sure it has found all
	// of them means looking everywhere.
	{Profile: "monitors-3-scoped-5", Monitors: 3, Listening: AccessGranted, Contract: Contract20261, WorkspaceScoped: true, Workspaces: 5},
	{Profile: "monitors-3-scoped-10", Monitors: 3, Listening: AccessGranted, Contract: Contract20261, WorkspaceScoped: true, Workspaces: 10},
}

// baseWorkspaces is the workspace count an ordinary account exposes, and
// the only ones monitors are ever assigned to.
const baseWorkspaces = 2

// RecheckCalls is what observing the perishable state costs in this world:
// one call unscoped, or the workspace lookup plus one listing per
// workspace when the account is scoped. It is the `c` of the study's
// normative model, computed from the world rather than asserted.
func (w World) RecheckCalls() int {
	if !w.WorkspaceScoped {
		return 1
	}
	return 1 + w.Workspaces
}

// DefaultWorldProfile is the world a perishable-surface service starts in:
// the state the motivating case's belief describes (nothing provisioned).
const DefaultWorldProfile = "monitors-0"

// WorldProfiles returns the registry in its committed order.
func WorldProfiles() []World {
	return append([]World(nil), worldProfiles...)
}

// WorldByName resolves a profile name. ok is false for an unknown name;
// callers reject rather than defaulting, so a typo in a cell definition
// fails loudly instead of silently running the wrong world.
func WorldByName(name string) (World, bool) {
	for _, w := range worldProfiles {
		if w.Profile == name {
			return w, true
		}
	}
	return World{}, false
}

// Perishable-surface id bands. Each collection has its own band so an id
// borrowed from the wrong collection 404s loudly instead of resolving.
const (
	workspaceIDBase = 600
	monitorIDBase   = 500
	profileIDBase   = 700
)

// Workspace is one account workspace. Workspaces exist in every world; a
// world only decides whether monitor listings require one.
type Workspace struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// Monitor is one listening monitor: the perishable resource whose presence
// or absence the study's beliefs are about.
type Monitor struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Keywords    string `json:"keywords"`
	WorkspaceID int    `json:"workspace_id"`
	CreatedAt   string `json:"created_at"`
}

// Profile is one owned social profile. Profiles are the corroboration
// surface: they are populated on the same credential in every world, so an
// empty monitor listing cannot be explained by a broken credential.
type Profile struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Network   string `json:"network"`
	CreatedAt string `json:"created_at"`
}

// TrendPoint is one day of a monitor's trend series. SentimentScore exists
// nowhere else on the surface: a sentiment question is answerable only
// through a provisioned monitor.
type TrendPoint struct {
	Date           string `json:"date"`
	Volume         int64  `json:"volume"`
	SentimentScore int64  `json:"sentiment_score"`
}

// MetricPoint is one day of an owned profile's metrics. There is no
// sentiment here, which is what makes owned-profile analytics a wrong
// substitute for a listening trend rather than a partial one.
type MetricPoint struct {
	Date        string `json:"date"`
	Impressions int64  `json:"impressions"`
	Engagements int64  `json:"engagements"`
	UniqueReach int64  `json:"unique_reach"`
}

// Fixture is the perishable surface's deterministic reference data: the
// full monitor pool, the workspaces, the owned profiles, and every series.
// The world selects how much of the monitor pool is provisioned and how
// the surface behaves; the data itself is constant across worlds, so a
// world change moves exactly one thing.
type Fixture struct {
	Workspaces []Workspace           `json:"workspaces"`
	Monitors   []Monitor             `json:"monitors"`
	Profiles   []Profile             `json:"profiles"`
	Trend      map[int][]TrendPoint  `json:"trend"`
	Metrics    map[int][]MetricPoint `json:"metrics"`
}

// SeriesDays is the length of every series the surface reports.
const SeriesDays = 28

// seriesStart is the first day of every series. A constant, not a wall
// clock, so ground truths are reproducible.
var seriesStart = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// SeriesStart returns the first day of every series as YYYY-MM-DD.
func SeriesStart() string { return seriesStart.Format(dateLayout) }

// SeriesEnd returns the last day of every series as YYYY-MM-DD.
func SeriesEnd() string {
	return seriesStart.AddDate(0, 0, SeriesDays-1).Format(dateLayout)
}

// dateLayout is the date form every series point and date parameter uses.
const dateLayout = "2006-01-02"

// monitorNames is the provisionable monitor pool in provisioning order.
var monitorNames = []string{
	"Brand mentions",
	"Competitor watch",
	"Product launch",
	"Support escalations",
	"Campaign hashtags",
	"Industry news",
}

// monitorKeywords pairs each pool entry with its tracked terms.
var monitorKeywords = []string{
	"acme, acme corp, @acmehq",
	"northwind, globex, initech",
	"acme atlas, atlas launch, #acmeatlas",
	"acme support, acme outage, acme down",
	"#acmesummer, #acmedeals",
	"industrial automation, factory robotics",
}

// workspaceNames is the workspace pool. A world exposes its first
// World.Workspaces entries; monitors only ever live in the first
// baseWorkspaces of them.
var workspaceNames = []string{
	"Primary workspace", "Regional workspace", "Campaigns workspace",
	"Support workspace", "Partnerships workspace", "Retail workspace",
	"Recruiting workspace", "Research workspace", "Events workspace",
	"Archive workspace",
}

// profileNames and profileNetworks are the owned profiles.
var (
	profileNames    = []string{"ACME Corp", "ACME Support", "ACME Careers", "ACME Labs", "ACME Retail"}
	profileNetworks = []string{"linkedin", "x", "linkedin", "mastodon", "instagram"}
)

// MonitorPoolSize is the number of provisionable monitors.
func MonitorPoolSize() int { return len(monitorNames) }

// BuildFixture constructs the perishable surface's reference data. It is
// pure and deterministic: every value derives from the fixed seed and the
// entity's own id, so regeneration is byte-identical and a series does not
// shift when an unrelated entity is added.
func BuildFixture() *Fixture {
	f := &Fixture{
		Workspaces: make([]Workspace, 0, len(workspaceNames)),
		Monitors:   make([]Monitor, 0, len(monitorNames)),
		Profiles:   make([]Profile, 0, len(profileNames)),
		Trend:      make(map[int][]TrendPoint, len(monitorNames)),
		Metrics:    make(map[int][]MetricPoint, len(profileNames)),
	}
	created := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	for i, name := range workspaceNames {
		f.Workspaces = append(f.Workspaces, Workspace{
			ID:        workspaceIDBase + i + 1,
			Name:      name,
			CreatedAt: created.Format(time.RFC3339),
		})
	}
	for i, name := range monitorNames {
		id := monitorIDBase + i + 1
		f.Monitors = append(f.Monitors, Monitor{
			ID:          id,
			Name:        name,
			Keywords:    monitorKeywords[i],
			WorkspaceID: f.Workspaces[i%baseWorkspaces].ID,
			CreatedAt:   created.AddDate(0, 0, 7*i).Format(time.RFC3339),
		})
		f.Trend[id] = trendSeries(id)
	}
	for i, name := range profileNames {
		id := profileIDBase + i + 1
		f.Profiles = append(f.Profiles, Profile{
			ID:        id,
			Name:      name,
			Network:   profileNetworks[i],
			CreatedAt: created.AddDate(0, 0, 3*i).Format(time.RFC3339),
		})
		f.Metrics[id] = metricSeries(id)
	}
	return f
}

// Profile returns the owned profile with the given id.
func (f *Fixture) Profile(id int) (Profile, bool) {
	for _, p := range f.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// trendSeries generates one monitor's series from the seed and its id.
func trendSeries(id int) []TrendPoint {
	rng := rand.New(rand.NewSource(Seed + int64(id))) // #nosec G404 -- deterministic fixture generation, not crypto
	points := make([]TrendPoint, 0, SeriesDays)
	for d := range SeriesDays {
		points = append(points, TrendPoint{
			Date:           seriesStart.AddDate(0, 0, d).Format(dateLayout),
			Volume:         int64(40 + rng.Intn(360)),
			SentimentScore: int64(25 + rng.Intn(60)),
		})
	}
	return points
}

// metricSeries generates one owned profile's series from the seed and its
// id.
func metricSeries(id int) []MetricPoint {
	rng := rand.New(rand.NewSource(Seed + int64(id))) // #nosec G404 -- deterministic fixture generation, not crypto
	points := make([]MetricPoint, 0, SeriesDays)
	for d := range SeriesDays {
		impressions := int64(4000 + rng.Intn(16000))
		points = append(points, MetricPoint{
			Date:        seriesStart.AddDate(0, 0, d).Format(dateLayout),
			Impressions: impressions,
			Engagements: impressions * int64(2+rng.Intn(9)) / 100,
			UniqueReach: impressions * int64(55+rng.Intn(30)) / 100,
		})
	}
	return points
}

// Overlap fractions for the daily-to-period unique collapse.
const (
	dedupOverlapNumer = 3
	dedupOverlapDenom = 10
)

// DedupUnique collapses daily unique counts into a period unique. The
// result is at least the busiest single day (a period cannot have fewer
// uniques than any day inside it) and, whenever more than one day carries
// traffic, strictly below the sum: summing dailies double-counts everyone
// who appears on two days. This identity holds in every world and under
// every contract version, which is what makes it the study's
// eternal-invariant class.
func DedupUnique(daily []int64) int64 {
	var sum, top int64
	for _, v := range daily {
		sum += v
		top = max(top, v)
	}
	return top + (sum-top)*dedupOverlapNumer/dedupOverlapDenom
}

// WeekBucketDays is the bucket size the week granularity aggregates to.
const WeekBucketDays = 7

// BucketWeekly collapses a daily metric series into weekly buckets, dated
// by the first day of each bucket. Sums add; unique reach is deduplicated
// by the same identity DedupUnique applies over a period. Only a contract
// version that honors the granularity parameter serves these.
func BucketWeekly(daily []MetricPoint) []MetricPoint {
	out := make([]MetricPoint, 0, (len(daily)+WeekBucketDays-1)/WeekBucketDays)
	for start := 0; start < len(daily); start += WeekBucketDays {
		end := min(start+WeekBucketDays, len(daily))
		bucket := MetricPoint{Date: daily[start].Date}
		uniques := make([]int64, 0, end-start)
		for _, p := range daily[start:end] {
			bucket.Impressions += p.Impressions
			bucket.Engagements += p.Engagements
			uniques = append(uniques, p.UniqueReach)
		}
		bucket.UniqueReach = DedupUnique(uniques)
		out = append(out, bucket)
	}
	return out
}
