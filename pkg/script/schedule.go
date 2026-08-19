package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Schedule bounds.
const (
	// MaxCronSpecLength bounds a cron expression. A standard five-field spec
	// and every descriptor form fit inside it many times over; the cap keeps a
	// pathological string out of the parser and the review surface.
	MaxCronSpecLength = 200

	// MinFireInterval is the closest together two fires of one schedule may
	// be. Standard cron cannot express anything finer than a minute, so this
	// only ever bites the @every descriptor — where "@every 5s" would turn a
	// governed automation into a load generator against the query engine it is
	// approved to reach.
	MinFireInterval = time.Minute

	// DefaultTimezone is the zone a schedule is interpreted in when it names
	// none. UTC rather than the host's local zone: a schedule means the same
	// thing on every replica, and a deployment that moves regions does not
	// silently move its reports.
	DefaultTimezone = "UTC"
)

// FireDateToken is the one token a schedule's bound parameters may carry. It
// expands, at materialization, to the date of the fire in the schedule's own
// timezone.
//
// The vocabulary is deliberately one entry long. Its whole job is to pin
// time-dependence into the run record: a script that computed today's date
// itself would produce a different answer every time it ran, and a run nobody
// can reproduce is not a governed run. Everything else a date needs — a
// previous day, a month boundary — is arithmetic the script does on this value
// through the date module, where it is visible in the source a reviewer read.
const FireDateToken = "${fire_date}" // #nosec G101 -- a parameter token, not a credential

// tokenPattern matches any ${...} token, so an unrecognized one is refused at
// authoring time rather than reaching a script as a literal.
var tokenPattern = regexp.MustCompile(`\$\{[^}]*\}`)

// Schedule errors.
var (
	// ErrScheduleNotFound reports a lookup for a schedule that does not exist.
	ErrScheduleNotFound = errors.New("this script has no schedule")

	// ErrUnknownToken marks a parameter binding that carries a token the
	// vocabulary does not define.
	ErrUnknownToken = errors.New("unknown schedule token")

	// ErrUnknownTimezone marks a zone the runtime could not load. It is a
	// separate sentinel from a bad cron expression because the two have
	// opposite causes: an expression that will not parse is a property of the
	// schedule, while a zone that will not load is a property of the BINARY —
	// the database is compiled in (time/tzdata) and a build that omits it fails
	// every named zone at once. A caller must not treat the second as a reason
	// to change the schedule.
	ErrUnknownTimezone = errors.New("unknown timezone")
)

// Schedule is the cadence one script runs on: the cron expression, the zone it
// is read in, and the parameter values every fire binds.
//
// A schedule is not an authority. It names when an already-approved version
// runs and with which parameters, and nothing else; the version it executes,
// the roles it presents, and the connections it may reach all come from the
// approval, which a schedule cannot touch. That is why setting one is an
// owner-or-admin action rather than a reviewer's.
type Schedule struct {
	ID       string `json:"id"`
	ScriptID string `json:"script_id"`

	// CronSpec is a standard five-field cron expression or one of the
	// descriptors (@daily, @hourly, @every 30m).
	CronSpec string `json:"cron_spec" example:"0 7 * * 1-5"`
	// Timezone is the IANA zone the spec is read in.
	Timezone string `json:"timezone" example:"America/Los_Angeles"`

	// Params are the values every fire binds, with tokens unexpanded. They are
	// stored as written so the schedule reads as what it means ("report on the
	// day it fires") rather than as whatever date happened to be current when
	// somebody set it.
	Params map[string]any `json:"params,omitempty"`

	Enabled bool `json:"enabled"`

	// NextRunAt is when the next fire is due, and zero when the expression has
	// no further fire at all. It is the materializer's efficiency index, not
	// its correctness guarantee: two replicas may read the same due schedule,
	// and what stops them producing two runs is the unique index on the run
	// they insert.
	//
	// While the schedule is disabled the field still holds the fire it was
	// paused on, because resuming picks up from there. What a READER is told is
	// DueAt, which is empty for a paused schedule; see MarshalJSON.
	NextRunAt time.Time `json:"next_run_at,omitzero"`
	// LastFireAt is the fire time of the most recent run this schedule
	// produced, empty until it has produced one.
	LastFireAt *time.Time `json:"last_fire_at,omitempty"`
	// MissedFires counts fires that came due while the platform was not
	// materializing them, cumulatively. Catching up on them is deliberately
	// not attempted (see NextFire), so the count is the visible record of the
	// gap.
	MissedFires int `json:"missed_fires"`

	CreatedBy string    `json:"created_by,omitempty" example:"jane@example.com"`
	UpdatedBy string    `json:"updated_by,omitempty" example:"jane@example.com"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DueAt reports when this schedule's next fire is due, and the zero time when
// no fire is due at all.
//
// A disabled schedule has no next fire. Its stored NextRunAt is the fire it was
// paused on, kept so that resuming picks up where the pause began, and stating
// it to a reader announces a fire that will not happen: the materializer's own
// predicate requires the schedule to be enabled. Every surface that tells
// somebody when a schedule fires next asks this rather than reading the field.
func (s Schedule) DueAt() time.Time {
	if !s.Enabled {
		return time.Time{}
	}
	return s.NextRunAt
}

// MarshalJSON renders the schedule as a reader may act on it, which differs
// from the stored row in exactly one field: next_run_at is DueAt.
//
// It lives here rather than in each of the surfaces that serve a Schedule
// because the rule is a property of the schedule, not of any one payload — the
// admin API, the portal API, and the manage_script response all serve this
// struct, and a rule applied in three places is a rule that drifts in one.
func (s Schedule) MarshalJSON() ([]byte, error) {
	type schedulePayload Schedule
	out := schedulePayload(s)
	out.NextRunAt = s.DueAt()
	//nolint:wrapcheck // a MarshalJSON returns the encoder's own error; wrapping it
	// would change what json.Marshal reports about the value it could not encode.
	return json.Marshal(out)
}

// Cron is a parsed cron expression bound to its timezone.
//
// Parsing is all this domain takes from the cron library: the schedule's
// timing is computed from the row on demand, and no goroutine anywhere waits
// on a cron ticker. What fires a run is the run queue's own due predicate, so
// there is one scheduler in the platform and it is the queue.
type Cron struct {
	schedule cron.Schedule
	loc      *time.Location
}

// Next returns the first fire strictly after t.
func (c Cron) Next(t time.Time) time.Time {
	return c.schedule.Next(t.In(c.loc))
}

// Location returns the zone the expression is read in.
func (c Cron) Location() *time.Location { return c.loc }

// ParseCron parses a cron expression in a timezone. An empty timezone is UTC.
func ParseCron(spec, timezone string) (Cron, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Cron{}, errors.New("a schedule needs a cron expression, for example \"0 7 * * 1-5\" for 07:00 on weekdays")
	}
	if len(spec) > MaxCronSpecLength {
		return Cron{}, fmt.Errorf("the cron expression is %d characters, over the %d-character limit", len(spec), MaxCronSpecLength)
	}
	loc, err := loadTimezone(timezone)
	if err != nil {
		return Cron{}, err
	}
	parsed, err := cron.ParseStandard(spec)
	if err != nil {
		return Cron{}, fmt.Errorf("the cadence %q is not a cron expression: %w", spec, err)
	}
	c := Cron{schedule: parsed, loc: loc}
	if err := c.checkCadence(); err != nil {
		return Cron{}, err
	}
	return c, nil
}

// checkCadence refuses an expression that fires faster than MinFireInterval,
// and one that never fires at all.
//
// A spec like "0 0 30 2 *" (February 30th) parses and then returns a zero time
// forever. Accepting it would store a schedule that silently does nothing,
// which is worse than refusing it: the author believes they scheduled
// something.
func (c Cron) checkCadence() error {
	base := time.Now().In(c.loc)
	first := c.schedule.Next(base)
	if first.IsZero() {
		return errors.New("this cron expression never fires; check the day and month fields")
	}
	second := c.schedule.Next(first)
	if !second.IsZero() && second.Sub(first) < MinFireInterval {
		return fmt.Errorf("this schedule would fire every %s; a script may fire at most once a minute", second.Sub(first))
	}
	return nil
}

// loadTimezone resolves an IANA timezone name, defaulting to UTC.
//
// The zone database is compiled into the binary (cmd/mcp-data-platform imports
// time/tzdata), because the release image is built FROM scratch and carries no
// /usr/share/zoneinfo: without it every named zone would resolve on a
// developer's machine and fail on the deployed one.
func loadTimezone(name string) (*time.Location, error) {
	if strings.TrimSpace(name) == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("the zone %q is not a known timezone (%w); use an IANA name such as America/Los_Angeles or UTC",
			name, ErrUnknownTimezone)
	}
	return loc, nil
}

// Validate checks that a schedule is well-formed against the parameter
// contract of the version it will execute.
//
// The parameter check is done here, when the schedule is set, rather than at
// the fire — a schedule whose bindings do not satisfy the contract would
// otherwise fail silently every night, with nobody watching.
func (s *Schedule) Validate(params []Param) error {
	if s.ScriptID == "" {
		return errors.New("a schedule needs a script")
	}
	cronSpec, err := ParseCron(s.CronSpec, s.Timezone)
	if err != nil {
		return err
	}
	// Bind against a representative fire so a token that expands to a value
	// the contract refuses (a ${fire_date} bound to an int parameter) is
	// caught now rather than at the first fire.
	_, err = BindScheduleParams(params, s.Params, time.Now().In(cronSpec.Location()), cronSpec.Location())
	return err
}

// ScheduleRequest is a surface's request to set a script's schedule.
type ScheduleRequest struct {
	CronSpec string
	Timezone string
	// Params are the bindings, with tokens as written.
	Params map[string]any
	// Enabled is a pointer so a request that does not mention it leaves an
	// existing schedule's state alone; a new schedule with nothing said is on,
	// because setting a schedule is asking for it to run.
	Enabled *bool
	// Actor is who is making the change, recorded on the row.
	Actor string
}

// BuildSchedule turns a request into the schedule to store, validated against
// the parameter contract its fires will bind against and with its first fire
// computed.
//
// The contract is the APPROVED version's when there is one, and the live
// record's otherwise. Both cases are right for the same reason: a schedule
// binds against whatever will actually execute, and until a version is approved
// nothing will, so the live record is the only contract there is to check
// against — which lets an author prepare a schedule before review without
// pretending it will fire.
//
// prev, when non-nil, is the schedule being replaced: its identity and its
// creator survive an edit of the cadence, because the runs that point at it
// point at the same automation.
func BuildSchedule(sc *Script, approved *Version, prev *Schedule, req ScheduleRequest, now time.Time) (*Schedule, error) {
	contract := sc.Params
	if approved != nil {
		contract = approved.Params
	}
	sched := &Schedule{
		ScriptID:  sc.ID,
		CronSpec:  strings.TrimSpace(req.CronSpec),
		Timezone:  strings.TrimSpace(req.Timezone),
		Params:    req.Params,
		Enabled:   true,
		CreatedBy: req.Actor,
		UpdatedBy: req.Actor,
	}
	if sched.Timezone == "" {
		sched.Timezone = DefaultTimezone
	}
	if prev != nil {
		sched.ID, sched.CreatedBy, sched.Enabled = prev.ID, prev.CreatedBy, prev.Enabled
	}
	if req.Enabled != nil {
		sched.Enabled = *req.Enabled
	}
	if err := sched.Validate(contract); err != nil {
		return nil, err
	}
	cronSpec, err := ParseCron(sched.CronSpec, sched.Timezone)
	if err != nil {
		return nil, err
	}
	if err := checkScheduledConnections(contract, sched.Params, approved, now, cronSpec.Location()); err != nil {
		return nil, err
	}
	// The first fire is computed from now, not carried over from the schedule
	// being replaced: changing a cadence means the old cadence's next fire is
	// no longer a fire this schedule has.
	sched.NextRunAt = cronSpec.Next(now)
	return sched, nil
}

// checkScheduledConnections refuses a cadence whose bindings name a connection
// the approved grant does not permit (#1361). Every fire of such a schedule
// would fail identically, unattended, and the owner setting it is the last
// person in a position to notice.
//
// Nothing is checked before approval: there is no grant to check against, and
// a schedule on an unapproved script fires nothing anyway.
func checkScheduledConnections(
	contract []Param,
	bindings map[string]any,
	approved *Version,
	now time.Time,
	loc *time.Location,
) error {
	if approved == nil {
		return nil
	}
	bound, err := BindScheduleParams(contract, bindings, now.In(loc), loc)
	if err != nil {
		return err
	}
	return CheckConnectionParams(contract, bound, approved.Grants)
}

// ScheduleFire is what one materialization pass concluded about a schedule:
// which fire to materialize, how many were stepped over to reach it, and where
// the schedule moves to.
type ScheduleFire struct {
	// At is the fire to materialize, meaningful only when Due is true.
	At time.Time
	// Missed counts the fires stepped over without materializing.
	Missed int
	// Next is the due time to store, zero when the expression has no further
	// fire.
	Next time.Time
	// Due reports whether At should be materialized at all.
	Due bool
}

// NextFire returns the fire a due schedule should materialize now, the number
// of fires that were missed before it, and the next due time to store.
//
// The misfire policy is fire-once-latest. After a gap — a stopped worker, a
// restored database — the schedule materializes ONE run, for the most recent
// fire that has come due, and counts the rest as missed. Catching up would
// mean a burst of identical reports against the warehouse the moment the
// platform came back, each computing a date nobody is waiting on any more,
// which is a worse failure than a visible gap. A backfill somebody actually
// wants is a run_script call with the parameters they want.
//
// Due is false when nothing is due yet, and when the schedule is further
// behind than one pass will walk.
func (s *Schedule) NextFire(c Cron, now time.Time) ScheduleFire {
	if s.NextRunAt.IsZero() || s.NextRunAt.After(now) {
		return ScheduleFire{Next: s.NextRunAt}
	}
	fire, missed := s.NextRunAt, 0
	for range maxCatchupWalk {
		candidate := c.Next(fire)
		if candidate.IsZero() || candidate.After(now) {
			return ScheduleFire{At: fire, Missed: missed, Next: candidate, Due: true}
		}
		missed++
		fire = candidate
	}
	// Further behind than one pass will walk. Nothing is materialized for a
	// fire this stale: the pass records how far it got and the next one
	// continues from there, so catching up converges without ever producing
	// the storm the policy exists to prevent.
	return ScheduleFire{Missed: missed, Next: fire}
}

// maxCatchupWalk bounds how many fires one materialization pass steps over. A
// minute-granularity schedule reaches it after roughly a year of downtime.
const maxCatchupWalk = 600000

// BindScheduleParams expands the schedule's tokens against a fire time and
// binds the result to the script's parameter contract.
//
// Expansion happens here, at materialization, and the expanded values are what
// the run row stores. That is what makes a scheduled run reproducible: the run
// records the date it was computing for, so re-running it later with the same
// parameters asks the same question, rather than asking about whatever day the
// re-run happens on.
func BindScheduleParams(defs []Param, raw map[string]any, fire time.Time, loc *time.Location) (map[string]any, error) {
	expanded := make(map[string]any, len(raw))
	for name, value := range raw {
		v, err := expandToken(value, fire, loc)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", name, err)
		}
		expanded[name] = v
	}
	return BindParams(defs, expanded)
}

// expandToken substitutes the token vocabulary inside one bound value. Only
// strings carry tokens; every other JSON scalar passes through untouched.
func expandToken(value any, fire time.Time, loc *time.Location) (any, error) {
	s, ok := value.(string)
	if !ok {
		return value, nil
	}
	s = strings.ReplaceAll(s, FireDateToken, fire.In(loc).Format(DateLayout))
	if leftover := tokenPattern.FindString(s); leftover != "" {
		return nil, fmt.Errorf("the value contains %s, which is not a token this platform defines (%w); the only one a schedule may use is %s",
			leftover, ErrUnknownToken, FireDateToken)
	}
	return s, nil
}

// Materialization is what one attempt to create a scheduled run produced.
type Materialization string

// Materialization outcomes.
const (
	// MaterializedRun means this caller inserted the run.
	MaterializedRun Materialization = "run"
	// MaterializedSkippedOverlap means the previous run of this schedule was
	// still open, so a skipped_overlap row was recorded instead. The skip is a
	// row rather than a log line because a report that quietly stopped
	// producing is exactly the failure a schedule is supposed to make visible.
	MaterializedSkippedOverlap Materialization = "skipped_overlap"
	// MaterializedDuplicate means another replica materialized this fire
	// first. It is the normal outcome of racing materializers, not a fault.
	MaterializedDuplicate Materialization = "duplicate"
)

// ScheduleFilter selects schedules for a listing.
type ScheduleFilter struct {
	// ScriptID scopes the listing to one script.
	ScriptID string
	// ScriptIDs scopes the listing to a set of scripts, which is how a caller
	// lists the schedules it may see without reading the rest and discarding
	// them. An EMPTY, non-nil slice means "no scripts" and matches nothing —
	// the distinction matters, because a caller who can see nothing must not
	// fall through to an unfiltered listing.
	ScriptIDs []string
	// Enabled, when set, scopes the listing to enabled or disabled schedules.
	Enabled *bool
	// Limit caps the rows returned; zero means the store default.
	Limit int
}

// ScheduleStore persists schedules and materializes the runs they produce.
//
// Materialization lives here rather than on RunStore because its correctness
// is a property of the schedule, not of the queue: what makes a fire happen
// once across every replica is the unique index this store's insert conflicts
// against.
type ScheduleStore interface {
	// SetSchedule creates or replaces the schedule of one script, assigning ID
	// when empty. A script has at most one schedule: a second cadence is a
	// second script, which keeps the run history of "the 07:00 report" from
	// interleaving with another cadence's.
	SetSchedule(ctx context.Context, s *Schedule) error

	// GetSchedule returns one script's schedule, or ErrScheduleNotFound.
	GetSchedule(ctx context.Context, scriptID string) (*Schedule, error)

	// ListSchedules returns schedules matching the filter.
	ListSchedules(ctx context.Context, filter ScheduleFilter) ([]Schedule, error)

	// SetScheduleEnabled turns one script's schedule on or off, recording who
	// did it. Disabling is how a schedule is retired: the row stays, because a
	// schedule that produced runs is part of the explanation of those runs.
	SetScheduleEnabled(ctx context.Context, scriptID string, enabled bool, actor string) error

	// DueSchedules returns enabled schedules whose next fire has arrived.
	DueSchedules(ctx context.Context, now time.Time, limit int) ([]Schedule, error)

	// MaterializeRun inserts one scheduled run, reporting what happened: the
	// run was created, it was skipped because the previous one is still open,
	// or another replica got there first.
	MaterializeRun(ctx context.Context, r *Run) (Materialization, error)

	// AdvanceSchedule moves a schedule forward after a materialization pass.
	// It is conditional on the schedule still being where the caller found it,
	// so two replicas that walked the same fire do not double-count the misses
	// or move the schedule twice. It reports whether the row moved.
	AdvanceSchedule(ctx context.Context, adv ScheduleAdvance) (bool, error)
}

// ScheduleAdvance is one conditional step forward of a schedule.
type ScheduleAdvance struct {
	// ID names the schedule and From is where the caller found NextRunAt. The
	// update applies only if the row still carries From, which is what makes
	// the step idempotent across replicas.
	ID   string
	From time.Time
	// Next is the fire time to store — zero when the expression has no further
	// fire, which parks the schedule rather than leaving it perpetually due —
	// and Fired, when non-zero, is the fire this pass materialized.
	Next  time.Time
	Fired time.Time
	// Missed is added to the schedule's cumulative missed-fire count.
	Missed int
}
