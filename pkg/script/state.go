package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// A script's state (#1537).
//
// A script carries one JSON object from one run to the next. A run reads it as
// run.state, the state as it stood when the run was created, and writes it with
// platform.save_state, which replaces the whole object. The platform knows
// nothing about the keys: a watermark is state["synced_through"], a cursor is
// state["cursor"], and a script that keeps a table keeps a resource, because
// the object is bounded at MaxStateBytes.
//
// The write is applied when the run succeeds, in the transaction that marks it
// succeeded, with a compare-and-set on the revision the run read. A failed run
// leaves the state where it was, so a watermark never moves past work that did
// not happen. A run whose compare-and-set fails, because another run of the
// same script wrote state after this one read it, fails with a message naming
// the writer; its outputs stand, since they were produced from the state it
// read, and the failure is what makes the interleaving visible instead of
// silently losing one of the two writes. It holds across replicas because it
// is a row predicate, not a lock.

// The serialization overhead ValidateState counts around the values: the
// braces of the object, and per entry the key's quotes, the colon and a comma.
const (
	stateObjectOverhead = 2
	stateEntryOverhead  = 4
)

// MaxStateBytes bounds the serialized state object. State is a cursor or a
// summary, not a dataset; a script that wants to keep a table keeps a resource.
const MaxStateBytes = 64 << 10

// State is one script's state as the platform holds it: the object, the
// revision that identifies this version of it, and who wrote it.
type State struct {
	ScriptID string `json:"script_id"`
	// Value is the object itself, {} for a script that has never saved any.
	Value map[string]any `json:"state"`
	// Revision counts writes. Zero is the revision of a script that has never
	// saved state and never had it reset; every write, by a run or by a person,
	// moves it forward by one, which is what a run's compare-and-set compares.
	Revision int64 `json:"revision" example:"3"`
	// RunID names the run that wrote this revision, empty when a person set
	// or cleared it instead.
	RunID string `json:"run_id,omitempty" example:"dpx_a1b2c3d4"`
	// UpdatedBy names the person who set or cleared this revision, empty when
	// a run wrote it.
	UpdatedBy string `json:"updated_by,omitempty" example:"jane@example.com"`
	// UpdatedAt is when this revision was written; zero for revision 0.
	UpdatedAt time.Time `json:"updated_at"`
}

// EmptyState is the state of a script that has never saved any: revision 0
// and an empty object. It is what a read returns when no row exists, so every
// reader sees one shape.
func EmptyState(scriptID string) *State {
	return &State{ScriptID: scriptID, Value: map[string]any{}}
}

// WrittenBy names who wrote this revision, for a message: the run, or the
// person who reset it.
func (s *State) WrittenBy() string {
	switch {
	case s == nil:
		return "nobody"
	case s.RunID != "":
		return "run " + s.RunID
	case s.UpdatedBy != "":
		return s.UpdatedBy
	default:
		return "nobody"
	}
}

// StateWrite is the object a run staged with platform.save_state, carried on
// the run's result to the store that applies it. Value is the whole object,
// which may be empty: saving {} is a write, and is distinct from not saving.
type StateWrite struct {
	Value map[string]any `json:"value"`
}

// ValidateState checks that an object can be held as state: every value is
// JSON-representable, and the whole serializes under MaxStateBytes. A refusal
// names the key it is about, because the author reads it inside a run that has
// already done its work.
func ValidateState(value map[string]any) error {
	keys := make([]string, 0, len(value))
	for k := range value {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	total := stateObjectOverhead
	for _, k := range keys {
		encoded, err := json.Marshal(value[k])
		if err != nil {
			return fmt.Errorf("state key %q holds a value that cannot be represented as JSON: %w", k, err)
		}
		total += len(k) + len(encoded) + stateEntryOverhead
	}
	if total > MaxStateBytes {
		return fmt.Errorf("the state is about %d bytes, over the %d-byte limit; state is a cursor or a summary, so keep a table as a resource instead", total, MaxStateBytes)
	}
	return nil
}

// ErrStateConflict marks a run's state write refused because the state moved
// after the run read it. The typed error carrying the details wraps it, so a
// caller matches on the sentinel and reads the rest from the message.
var ErrStateConflict = errors.New("the script's state changed after this run read it")

// StateConflictError is one refused compare-and-set: what the run read, what
// the state is now, and who moved it.
type StateConflictError struct {
	// Read is the revision the run read at creation, Current the revision the
	// state holds now.
	Read, Current int64
	// WrittenBy names the writer of the current revision, as State.WrittenBy
	// renders it.
	WrittenBy string
}

// Error states the conflict in the terms the run's owner reads it in: what
// happened, and what stands.
func (e *StateConflictError) Error() string {
	return fmt.Sprintf("%s: this run read revision %d and %s wrote revision %d in between; "+
		"this run's outputs stand, its state was not saved, and the next run reads the state as it is now",
		ErrStateConflict.Error(), e.Read, e.WrittenBy, e.Current)
}

// Unwrap lets errors.Is match the sentinel.
func (*StateConflictError) Unwrap() error { return ErrStateConflict }

// StateUse is what a source does with state, read statically by the
// validator: whether it reads run.state and whether it calls
// platform.save_state.
type StateUse struct {
	Reads bool `json:"reads_state"`
	Saves bool `json:"saves_state"`
}

// StateStore holds each script's state.
//
// A run's own write is not here: it is applied by RunStore.Finish, in the
// transaction that marks the run succeeded, because the two are one fact. What
// this contract offers is the read every surface makes and the reset the
// owner and an administrator make.
type StateStore interface {
	// GetState returns the script's state, EmptyState for a script that has
	// never saved any.
	GetState(ctx context.Context, scriptID string) (*State, error)

	// SetState replaces the script's state with value, recording by as the
	// person who did it, and moves the revision forward. An empty value is a
	// clear. A run in flight that read the old revision fails at its write,
	// which is correct: the reset was after its premise.
	SetState(ctx context.Context, scriptID string, value map[string]any, by string) (*State, error)
}
