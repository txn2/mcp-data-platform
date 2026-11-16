package integration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"time"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// Errors a caller reads by identity.
var (
	// ErrUnknown is an operation the platform does not register.
	ErrUnknown = errors.New("unknown integration")
	// ErrNotAllowed is a caller who may not change an integration.
	ErrNotAllowed = errors.New("not allowed")
	// ErrInvalid is a request that cannot be carried out as it is.
	ErrInvalid = errors.New("invalid integration request")
	// ErrNothingFailed is a reader's retry when nothing they can see failed.
	ErrNothingFailed = errors.New("nothing it shows failed")
	// ErrTooSoon is a reader's retry within RetryInterval of the last run.
	ErrTooSoon = errors.New("asked too soon")
)

// LockKey is the advisory lock one replica's pass holds.
const LockKey int64 = 4713210405

// The interval bounds an operation's timer may be set within, in seconds.
const (
	DefaultInterval = 15 * 60
	MinInterval     = 60
	MaxInterval     = 24 * 60 * 60
)

// Source computes an operation's items now.
type Source interface {
	Items(ctx context.Context) ([]Item, error)
}

// Scripts reads a bound script and its versions.
type Scripts interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
	GetVersion(ctx context.Context, scriptID string, version int) (*script.Version, error)
}

// Approvals is what a bound script's version is approved and run through
// (approval.Service): the same approval every handler kind runs on.
type Approvals interface {
	HandlerRunAs(ctx context.Context, scriptID string) (approval.RunAs, bool)
	ApproveForHandlers(ctx context.Context, actor approval.Actor, sc *script.Script, version int) error
}

// Bindings is where bindings, items and an operation's runs are kept
// (*Store).
type Bindings interface {
	Binding(ctx context.Context, op string) (*Binding, error)
	Bind(ctx context.Context, op, scriptID string, intervalSeconds int, by string) (*Binding, error)
	Clear(ctx context.Context, op string) (bool, error)
	RequestRetry(ctx context.Context, op string) error
	Items(ctx context.Context, op string) ([]StoredItem, error)
	ItemsByKey(ctx context.Context, op string, keys []string) (map[string]StoredItem, error)
	StartRun(ctx context.Context, op string, r NewRun) error
	RecordRefusal(ctx context.Context, op, reason string) error
	RunOutcome(ctx context.Context, runID string) (RunOutcome, error)
	Apply(ctx context.Context, op string, a Applied) error
	// Lock takes the lock one replica's pass holds; ok is false when another
	// replica holds it.
	Lock(ctx context.Context) (release func(), ok bool, err error)
}

// Service binds operations to scripts and runs them.
type Service struct {
	Store     Bindings
	Scripts   Scripts
	Approvals Approvals
	// Sources computes each operation's items; an operation with none is
	// never run.
	Sources map[string]Source
	now     func() time.Time
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Bind chooses the script an operation calls. Only an administrator binds,
// and only a script whose version that will run defines the operation's
// function.
func (s *Service) Bind(ctx context.Context, a Actor, req BindRequest) (*Binding, error) {
	spec, err := adminSpec(a, req.Operation)
	if err != nil {
		return nil, err
	}
	sc, err := s.Scripts.GetByID(ctx, req.ScriptID)
	if err != nil || sc == nil {
		return nil, fmt.Errorf("no script %q: %w", req.ScriptID, ErrInvalid)
	}
	if sc.Library {
		return nil, fmt.Errorf("script %s is a library, which never runs: %w", sc.Name, ErrInvalid)
	}
	version, source := s.runSource(ctx, sc, req.Approve)
	if version == 0 {
		return nil, fmt.Errorf("no version of script %s is approved for handlers: approve its version as you choose it: %w", sc.Name, ErrInvalid)
	}
	if err := spec.CheckFor(source); err != nil {
		return nil, fmt.Errorf("version %d of script %s cannot serve %s: %w: %w", version, sc.Name, spec.Label, err, ErrInvalid)
	}
	interval, err := s.interval(ctx, spec.Name, req.IntervalSeconds)
	if err != nil {
		return nil, err
	}
	if req.Approve {
		if err := s.Approvals.ApproveForHandlers(ctx, approval.Actor{Email: a.Email, Roles: a.Roles, Admin: a.Admin}, sc, 0); err != nil {
			return nil, fmt.Errorf("approving version %d of %s: %w", sc.Version, sc.Name, err)
		}
	}
	return s.Store.Bind(ctx, spec.Name, sc.ID, interval, a.Email) //nolint:wrapcheck // the store names what it wrote
}

// runSource is the version an operation's runs of sc execute and its source:
// the latest when it is approved now, else the one approved for handlers, 0
// for none.
func (s *Service) runSource(ctx context.Context, sc *script.Script, approve bool) (version int, source string) {
	if approve {
		return sc.Version, sc.Source
	}
	runAs, ok := s.Approvals.HandlerRunAs(ctx, sc.ID)
	if !ok {
		return 0, ""
	}
	if runAs.Version == sc.Version {
		return sc.Version, sc.Source
	}
	v, err := s.Scripts.GetVersion(ctx, sc.ID, runAs.Version)
	if err != nil || v == nil {
		return 0, ""
	}
	return v.Version, v.Source
}

// interval is the timer a bind sets: the one asked for, else the current
// one, else the default.
func (s *Service) interval(ctx context.Context, op string, asked int) (int, error) {
	if asked != 0 {
		if asked < MinInterval || asked > MaxInterval {
			return 0, fmt.Errorf("interval_seconds %d: between %d and %d: %w", asked, MinInterval, MaxInterval, ErrInvalid)
		}
		return asked, nil
	}
	b, err := s.Store.Binding(ctx, op)
	if err != nil {
		return 0, err //nolint:wrapcheck // the store names what it read
	}
	if b != nil {
		return b.IntervalSeconds, nil
	}
	return DefaultInterval, nil
}

// Clear stops an operation's calls and drops every status it kept: the
// deployment is back to behaving as it did with no script.
func (s *Service) Clear(ctx context.Context, a Actor, op string) error {
	spec, err := adminSpec(a, op)
	if err != nil {
		return err
	}
	cleared, err := s.Store.Clear(ctx, spec.Name)
	if err != nil {
		return err //nolint:wrapcheck // the store names what it cleared
	}
	if !cleared {
		return ErrNotBound
	}
	return nil
}

// Retry asks for a run with every item at once. The caller has been allowed
// it by its surface: an administrator, or someone who can see a failed item.
func (s *Service) Retry(ctx context.Context, op string) error {
	if _, ok := SpecOf(op); !ok {
		return fmt.Errorf("operation %q: %w", op, ErrUnknown)
	}
	return s.Store.RequestRetry(ctx, op) //nolint:wrapcheck // read by identity (ErrNotBound)
}

// RetryInterval is how long after a run starts a reader's retry is refused.
const RetryInterval = 15 * time.Second

// RetryFailed is a retry asked for by someone who is not an administrator
// and can see keys (#2659): honored only when one of keys failed, and not
// within RetryInterval of the last run's start, so a reader can ask again for
// what failed but cannot keep the script running.
func (s *Service) RetryFailed(ctx context.Context, op string, keys []string) error {
	if _, ok := SpecOf(op); !ok {
		return fmt.Errorf("operation %q: %w", op, ErrUnknown)
	}
	b, err := s.Store.Binding(ctx, op)
	if err != nil {
		return err //nolint:wrapcheck // the store names what it read
	}
	if b == nil {
		return ErrNotBound
	}
	stored, err := s.Store.ItemsByKey(ctx, op, keys)
	if err != nil {
		return err //nolint:wrapcheck // the store names what it read
	}
	if !slices.ContainsFunc(keys, func(k string) bool { it, ok := stored[k]; return ok && it.Status == StatusFailed }) {
		return fmt.Errorf("retrying: %w", ErrNothingFailed)
	}
	if b.LastStartedAt != nil {
		if wait := b.LastStartedAt.Add(RetryInterval).Sub(s.clock()); wait > 0 {
			return fmt.Errorf("the script last started under %s ago; ask again in %s: %w",
				RetryInterval, wait.Round(time.Second), ErrTooSoon)
		}
	}
	return s.Store.RequestRetry(ctx, op) //nolint:wrapcheck // read by identity (ErrNotBound)
}

// adminSpec is the operation once the actor may change it.
func adminSpec(a Actor, op string) (Spec, error) {
	spec, ok := SpecOf(op)
	if !ok {
		return Spec{}, fmt.Errorf("operation %q: %w", op, ErrUnknown)
	}
	if !a.Admin {
		return Spec{}, fmt.Errorf("an administrator chooses an integration's script: %w", ErrNotAllowed)
	}
	return spec, nil
}

// Overview is every operation with its binding and items. Only an
// administrator reads it: it carries the run error.
func (s *Service) Overview(ctx context.Context) ([]Overview, error) {
	out := make([]Overview, 0, len(specs))
	for _, spec := range specs {
		o, err := s.overviewOf(ctx, spec)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// overviewOf is one operation as the settings page shows it.
func (s *Service) overviewOf(ctx context.Context, spec Spec) (Overview, error) {
	o := Overview{Spec: spec, Counts: map[string]int{}, Items: []StoredItem{}}
	if src := s.Sources[spec.Name]; src != nil {
		if items, err := src.Items(ctx); err == nil {
			o.Total = len(items)
		}
	}
	b, err := s.Store.Binding(ctx, spec.Name)
	if err != nil || b == nil {
		return o, err //nolint:wrapcheck // the store names what it read
	}
	o.Binding = b
	s.describeScript(ctx, &o)
	items, err := s.Store.Items(ctx, spec.Name)
	if err != nil {
		return o, err //nolint:wrapcheck // the store names what it read
	}
	for _, it := range items {
		if !it.Removing {
			o.Counts[it.Status]++
			o.Items = append(o.Items, it)
		}
	}
	if pending := o.Total - len(o.Items); pending > 0 {
		o.Counts[StatusPending] += pending
	}
	return o, nil
}

// describeScript names the bound script and the version approved for
// handlers, and who approved it.
func (s *Service) describeScript(ctx context.Context, o *Overview) {
	if sc, err := s.Scripts.GetByID(ctx, o.Binding.ScriptID); err == nil && sc != nil {
		o.ScriptName = sc.Name
	}
	if runAs, ok := s.Approvals.HandlerRunAs(ctx, o.Binding.ScriptID); ok {
		o.Running, o.ApprovedBy = runAs.Version, runAs.Approver
	}
}

// Statuses is each key's status for an operation, nil when no script is
// chosen (the field is then omitted everywhere). A key the script has not
// answered for yet is pending. admin adds the run error to an item the script
// itself failed.
func (s *Service) Statuses(ctx context.Context, op string, keys []string, admin bool) (map[string]ItemStatus, error) {
	if s == nil || s.Store == nil {
		return nil, nil //nolint:nilnil // no integrations: no statuses, the field omitted
	}
	b, err := s.Store.Binding(ctx, op)
	if err != nil || b == nil {
		return nil, err //nolint:wrapcheck // the store names what it read; no binding is no statuses
	}
	stored, err := s.Store.ItemsByKey(ctx, op, keys)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names what it read
	}
	out := make(map[string]ItemStatus, len(keys))
	for _, k := range keys {
		it, ok := stored[k]
		if !ok {
			out[k] = ItemStatus{Status: StatusPending, Message: ""}
			continue
		}
		out[k] = statusOf(it, b, admin)
	}
	return out, nil
}

// statusOf is one stored item's status; an administrator also reads the
// failing run's error when the script itself failed.
func statusOf(it StoredItem, b *Binding, admin bool) ItemStatus {
	st := ItemStatus{Status: it.Status, Message: it.Message, CheckedAt: it.CheckedAt, ScriptFailed: it.FailedRun != ""}
	if !admin || it.FailedRun == "" {
		return st
	}
	st.RunID, st.RunScriptID = it.FailedRun, b.ScriptID
	if b.LastRunID == it.FailedRun {
		st.RunError = b.LastError
	}
	return st
}

// Pass runs every bound operation once, on one replica at a time: it applies
// a finished run, then starts the next when the items changed, a retry was
// asked for, or the timer is due.
func (s *Service) Pass(ctx context.Context) error {
	release, ok, err := s.Store.Lock(ctx)
	if err != nil {
		return fmt.Errorf("taking the integration lock: %w", err)
	}
	if !ok {
		return nil
	}
	defer release()
	var errs []error
	for _, spec := range specs {
		if err := s.passOne(ctx, spec); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...) //nolint:wrapcheck // each operation's error names it
}

// passOne is one operation's pass.
func (s *Service) passOne(ctx context.Context, spec Spec) error {
	src := s.Sources[spec.Name]
	if src == nil {
		return nil
	}
	b, err := s.Store.Binding(ctx, spec.Name)
	if err != nil || b == nil {
		return err //nolint:wrapcheck // the store names what it read
	}
	if b.CurrentRunID != "" {
		done, err := s.finish(ctx, spec, b.CurrentRunID)
		if err != nil || !done {
			return err
		}
		if b, err = s.Store.Binding(ctx, spec.Name); err != nil || b == nil {
			return err //nolint:wrapcheck // the store names what it read
		}
	}
	return s.maybeStart(ctx, spec, src, b)
}

// finish applies a run that ended, reporting whether it had.
func (s *Service) finish(ctx context.Context, spec Spec, runID string) (bool, error) {
	out, err := s.Store.RunOutcome(ctx, runID)
	if err != nil || !out.Finished {
		return false, err //nolint:wrapcheck // the store names what it read
	}
	var items, removed []string
	if _, req, err := FromRunCall(out.Call); err == nil {
		items, removed = RequestKeys(spec, req)
	}
	a := Applied{RunID: runID, Removed: removed, Status: out.Status}
	if out.Status == script.RunStatusSucceeded {
		results, readErr := readStored(out.Result, items)
		if readErr == nil {
			a.Results = results
		} else {
			a.Status, a.Error = script.RunStatusFailed, "the run's result could not be read: "+readErr.Error()
			a.Failed, a.FailWith = items, spec.FailedMessage
		}
	} else {
		a.Error = out.Error
		if a.Error == "" {
			a.Error = "the run ended " + out.Status
		}
		a.Failed, a.FailWith = items, spec.FailedMessage
	}
	return true, s.Store.Apply(ctx, spec.Name, a) //nolint:wrapcheck // the store names what it wrote
}

// maybeStart starts the operation's next run when it is owed one.
func (s *Service) maybeStart(ctx context.Context, spec Spec, src Source, b *Binding) error {
	now, err := src.Items(ctx)
	if err != nil {
		return fmt.Errorf("computing the %s items: %w", spec.Name, err)
	}
	stored, err := s.Store.Items(ctx, spec.Name)
	if err != nil {
		return err //nolint:wrapcheck // the store names what it read
	}
	added, removed := diff(now, stored)
	due := b.RetryRequested || b.LastStartedAt == nil ||
		!s.clock().Before(b.LastStartedAt.Add(time.Duration(b.IntervalSeconds)*time.Second))
	if !due && len(added) == 0 && len(removed) == 0 {
		return nil
	}
	reason := ReasonTimer
	if len(added) > 0 || len(removed) > 0 {
		reason = ReasonChange
	}
	return s.start(ctx, spec, b, change{now: now, added: added, removed: removed, reason: reason})
}

// start queues one run of the bound script's approved version.
// change is what a run is made for: the items now, the ones added and
// removed since the last run, and why.
type change struct {
	now     []Item
	added   []Item
	removed []StoredItem
	reason  string
}

func (s *Service) start(ctx context.Context, spec Spec, b *Binding, c change) error {
	now, added, removed, reason := c.now, c.added, c.removed, c.reason
	runAs, ok := s.Approvals.HandlerRunAs(ctx, b.ScriptID)
	if !ok {
		return s.Store.RecordRefusal(ctx, spec.Name, "no version of the chosen script is approved for handlers") //nolint:wrapcheck // the store names what it wrote
	}
	v, err := s.Scripts.GetVersion(ctx, b.ScriptID, runAs.Version)
	if err != nil || v == nil {
		return s.Store.RecordRefusal(ctx, spec.Name, fmt.Sprintf("the approved version %d of the chosen script could not be read", runAs.Version)) //nolint:wrapcheck // the store names what it wrote
	}
	if err := spec.CheckFor(v.Source); err != nil {
		return s.Store.RecordRefusal(ctx, spec.Name, fmt.Sprintf("version %d: %s", v.Version, err)) //nolint:wrapcheck // the store names what it wrote
	}
	req := Request{Reason: reason, Added: entries(added), Items: entries(now), Removed: make([]map[string]any, 0, len(removed))}
	removedKeys := make([]string, 0, len(removed))
	for _, it := range removed {
		req.Removed = append(req.Removed, it.Entry)
		removedKeys = append(removedKeys, it.Key)
	}
	id, err := pkgsession.GenerateScriptSessionID()
	if err != nil {
		return fmt.Errorf("naming the %s run: %w", spec.Name, err)
	}
	slog.InfoContext(ctx, "integrations: calling the script", "operation", spec.Name, "reason", reason,
		"added", len(added), "removed", len(removed), "items", len(now), "run_id", id)
	return s.Store.StartRun(ctx, spec.Name, NewRun{ //nolint:wrapcheck // the store names what it wrote
		ID: id, ScriptID: b.ScriptID, VersionID: v.ID, Version: v.Version, Approver: runAs.Approver, Roles: runAs.Roles,
		Call: RunCall(spec, req), Reason: reason, Added: added, Removed: removedKeys,
	})
}

// diff is the items now that were not handed to the script, and the ones it
// was handed that are gone. An item whose removal is in flight counts as
// gone, so one that comes back is added again.
func diff(now []Item, stored []StoredItem) (added []Item, removed []StoredItem) {
	had := make(map[string]bool, len(stored))
	for _, it := range stored {
		if !it.Removing {
			had[it.Key] = true
		}
	}
	has := make(map[string]bool, len(now))
	for _, it := range now {
		has[it.Key] = true
		if !had[it.Key] {
			added = append(added, it)
		}
	}
	for _, it := range stored {
		if !it.Removing && !has[it.Key] {
			removed = append(removed, it)
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].Key < added[j].Key })
	return added, removed
}

// entries is items as the request lists them.
func entries(items []Item) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, it.Entry)
	}
	return out
}

// logFailure reports a pass that failed, which the next pass retries.
func logFailure(ctx context.Context, err error) {
	if err != nil {
		slog.WarnContext(ctx, "integrations: a pass failed; the next one tries again", "error", logsan.SanitizeForLog(err.Error()))
	}
}

// maxCandidatesLooked bounds the scripts read for the picker, and
// maxCandidates the ones it lists.
const (
	maxCandidatesLooked = 500
	maxCandidates       = 50
)

// scriptLister is the script store's listing.
type scriptLister interface {
	List(ctx context.Context, f script.ListFilter) ([]script.Script, error)
}

// Candidates is the scripts an administrator may choose for an operation,
// matching search.
func (s *Service) Candidates(ctx context.Context, a Actor, op, search string) ([]Candidate, error) {
	spec, err := adminSpec(a, op)
	if err != nil {
		return nil, err
	}
	out := []Candidate{}
	lister, ok := s.Scripts.(scriptLister)
	if !ok {
		return out, nil
	}
	notLibrary := false
	all, err := lister.List(ctx, script.ListFilter{Search: search, Library: &notLibrary, Limit: maxCandidatesLooked})
	if err != nil {
		return nil, fmt.Errorf("listing scripts: %w", err)
	}
	for i := range all {
		sc := &all[i]
		if spec.CheckFor(sc.Source) != nil {
			continue
		}
		c := Candidate{ID: sc.ID, Name: sc.Name, OwnerEmail: sc.OwnerEmail, Version: sc.Version}
		if runAs, ok := s.Approvals.HandlerRunAs(ctx, sc.ID); ok {
			c.Running, c.ApprovedBy = runAs.Version, runAs.Approver
			v, src := s.runSource(ctx, sc, false)
			c.RunningServes = v != 0 && spec.CheckFor(src) == nil
		}
		out = append(out, c)
		if len(out) == maxCandidates {
			break
		}
	}
	return out, nil
}
