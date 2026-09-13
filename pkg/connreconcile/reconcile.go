// Package connreconcile owns the single copy of the "materialize a DB-managed
// connection onto the live toolkits" mechanics shared by the admin hot-reload
// path and the cross-replica reload bus.
//
// Both callers previously carried their own copy of the same loop — find every
// registered toolkit of a kind that implements toolkit.ConnectionManager, then
// remove-then-add under a HasConnection guard — and the copies had drifted:
// the admin path touched only the first matching toolkit while the reload bus
// touched all of them. Centralizing the loop here keeps the two paths from
// diverging again. Callers retain their own observability and broadcast policy;
// the reconciler reports which toolkit operations failed and leaves logging,
// log level, and peer notification to the caller.
package connreconcile

import (
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Phase identifies which toolkit operation produced a Failure so callers can
// tailor the log message: a failed remove-before-re-add reads differently from
// a failed add.
type Phase int

const (
	// PhaseRemove marks a failure returned by RemoveConnection.
	PhaseRemove Phase = iota
	// PhaseAdd marks a failure returned by AddConnection.
	PhaseAdd
	// PhaseUpdate marks a failure returned by UpdateConnection.
	PhaseUpdate
	// PhaseAdopt marks a failure returned by AdoptConnection.
	PhaseAdopt
)

// String renders the phase for structured log output.
func (p Phase) String() string {
	switch p {
	case PhaseAdd:
		return "add"
	case PhaseUpdate:
		return "update"
	case PhaseAdopt:
		return "adopt"
	default:
		return "remove"
	}
}

// Failure is one toolkit operation that returned an error during a reconcile.
// The reconciler reports what failed; the caller owns logging policy (level,
// message, structured fields).
type Failure struct {
	Phase Phase
	Err   error
}

// ToolkitSource is the minimal view of the toolkit registry the reconciler
// needs. Both *registry.Registry and the admin handler's registry seam satisfy
// it via their All method.
type ToolkitSource interface {
	All() []registry.Toolkit
}

// Reconciler applies connection changes to every registered toolkit of a given
// kind that implements toolkit.ConnectionManager. It holds no mutable state of
// its own: the source is the authority on which toolkits currently exist, so a
// reconciler is cheap to construct per call.
type Reconciler struct {
	source ToolkitSource
}

// New returns a Reconciler bound to the given toolkit source. A nil source is
// tolerated (every operation becomes a no-op), matching the platform and admin
// wiring where the toolkit registry may be absent.
func New(source ToolkitSource) *Reconciler {
	return &Reconciler{source: source}
}

// Remove drops name from every matching toolkit that currently holds it.
// Toolkits that do not hold the connection are skipped, because removing an
// absent connection is not a state-corrupting error (several toolkits return a
// not-found error for it). Returns one Failure per toolkit whose
// RemoveConnection returned an error, in registration order.
func (r *Reconciler) Remove(kind, name string) []Failure {
	var failures []Failure
	for _, cm := range r.managers(kind) {
		if !cm.HasConnection(name) {
			continue
		}
		if err := cm.RemoveConnection(name); err != nil {
			failures = append(failures, Failure{Phase: PhaseRemove, Err: err})
		}
	}
	return failures
}

// Upsert makes config the live config for name on every matching toolkit.
// A toolkit that implements toolkit.ConnectionUpdater and already holds the
// connection is handed the change as one: a removal on such a toolkit is a
// deletion, dropping what it keeps for the connection beyond its
// registration (#1676). Any other toolkit has an existing registration
// removed first so the changed config replaces the old one rather than
// layering on top of it; one whose remove fails is skipped (its add is not
// attempted, to avoid stacking on stale state) but the loop continues so
// other toolkits of the same kind are still updated. Returns one Failure per
// failed operation, in registration order.
func (r *Reconciler) Upsert(kind, name string, config map[string]any) []Failure {
	var failures []Failure
	for _, cm := range r.managers(kind) {
		if f, ok := upsertOne(cm, name, config); !ok {
			failures = append(failures, f)
		}
	}
	return failures
}

// Adopt makes config the live config for name on every matching toolkit, as a
// connection another replica saved: it is how the reload bus applies a peer's
// announcement. A toolkit that implements toolkit.ConnectionAdopter is handed
// the change whole, so it installs the state the saving replica stored rather
// than deriving it again (#1714); any other toolkit is reconciled exactly as
// Upsert reconciles it. Returns one Failure per failed operation, in
// registration order.
func (r *Reconciler) Adopt(kind, name string, config map[string]any) []Failure {
	var failures []Failure
	for _, cm := range r.managers(kind) {
		if adopter, adopts := cm.(toolkit.ConnectionAdopter); adopts {
			if err := adopter.AdoptConnection(name, config); err != nil {
				failures = append(failures, Failure{Phase: PhaseAdopt, Err: err})
			}
			continue
		}
		if f, ok := upsertOne(cm, name, config); !ok {
			failures = append(failures, f)
		}
	}
	return failures
}

// upsertOne applies one connection change to one toolkit.
func upsertOne(cm toolkit.ConnectionManager, name string, config map[string]any) (failure Failure, ok bool) {
	if cm.HasConnection(name) {
		if updater, updates := cm.(toolkit.ConnectionUpdater); updates {
			if err := updater.UpdateConnection(name, config); err != nil {
				return Failure{Phase: PhaseUpdate, Err: err}, false
			}
			return Failure{}, true
		}
		if err := cm.RemoveConnection(name); err != nil {
			return Failure{Phase: PhaseRemove, Err: err}, false
		}
	}
	if err := cm.AddConnection(name, config); err != nil {
		return Failure{Phase: PhaseAdd, Err: err}, false
	}
	return Failure{}, true
}

// managers returns every registered toolkit of kind that implements
// toolkit.ConnectionManager, in registration order.
func (r *Reconciler) managers(kind string) []toolkit.ConnectionManager {
	if r.source == nil {
		return nil
	}
	var managers []toolkit.ConnectionManager
	for _, tk := range r.source.All() {
		if tk.Kind() != kind {
			continue
		}
		if cm, ok := tk.(toolkit.ConnectionManager); ok {
			managers = append(managers, cm)
		}
	}
	return managers
}
