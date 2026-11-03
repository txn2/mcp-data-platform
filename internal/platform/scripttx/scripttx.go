// Package scripttx is a managed script's transactions and batches on an
// application database (#2402): platform.transaction, which opens one
// transaction, calls the script's function with it and commits when the
// function returns, and platform.batch, which sends statements pipelined in one
// transaction of their own.
//
// The bindings hold no connection. Every step -- opening the transaction, each
// statement in it, the commit or rollback -- is one call through the run's call
// path, answered by the session against the connection's pool
// (scriptsession's appdb calls). That is what makes a draft refuse a write
// inside a transaction, a recording keep each statement and how the
// transaction ended, and a script test replay it with the database stopped.
// Extracted from internal/platform/scriptrun for its size budget.
package scripttx

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptfail"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsql"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// The platform members this package binds.
const (
	CapabilityTransaction = "platform.transaction"
	CapabilityBatch       = "platform.batch"
)

// MaxAttempts is how many times a retried transaction is tried: the first try
// and two retries.
const MaxAttempts = 3

// retryBase is the pause before the first retry; each later one doubles it,
// and each is jittered down to half so two runs that collided do not collide
// again on the same schedule.
const retryBase = 50 * time.Millisecond

// retryStates are the SQLSTATEs a retried transaction is tried again on:
// serialization_failure and deadlock_detected. Both say the same transaction
// run again is expected to succeed.
var retryStates = map[string]bool{"40001": true, "40P01": true}

// OutsideEffects are the bindings a retried transaction's function may not
// call (#2402): each has an effect outside the transaction, which a retry
// would repeat. A function that is tried again must do nothing but read and
// write through its transaction.
var OutsideEffects = map[string]bool{
	"platform.call": true, "platform.query": true, "platform.execute": true, CapabilityBatch: true,
	"platform.export": true, "platform.publish_data": true, "platform.notify": true, "platform.publish": true,
	"platform.save_state": true, "platform.mint_token": true,
}

// Host is the two members as a run binds them. One Host serves one run and
// holds whether a transaction's function is running, which Refuse reads.
type Host struct {
	// Ctx is the run's context.
	Ctx context.Context //nolint:containedctx // the run's lifetime, which the retry pause reads
	// Invoke issues one call where the run's calls go: the write barrier, the
	// call, and the record of what it wrote.
	Invoke func(b *starlark.Builtin, tool string, args map[string]any) (map[string]any, error)
	// Rows is platform.query's handling of a statement's rows: the row cap,
	// the byte cap and the truncation check.
	Rows func(name string, out map[string]any) (starlark.Value, error)
	// Failed hands a failed call to the script as on_error asked.
	Failed func(b *starlark.Builtin, err error, returnFailure bool) (starlark.Value, error)
	// Replaying is a test or a replay, whose answers are recorded: a retry
	// is tried again without pausing.
	Replaying bool

	// open is whether a transaction's function is running, and retrying
	// whether that transaction is retried.
	open, retrying bool
}

// Refuse refuses a binding a retried transaction's function may not call.
func (h *Host) Refuse(name string) error {
	if h == nil || !h.retrying || !OutsideEffects[name] {
		return nil
	}
	return fmt.Errorf("in %s: not allowed inside a platform.transaction with retry = True: the function may be "+
		"run again, and %s has an effect outside the transaction that would be repeated; run it after the "+
		"transaction returns, or read and write through tx", name, name)
}

// Transaction is platform.transaction(connection, fn, isolation =
// "read_committed", read_only = False, retry = False, on_error = ""): fn's
// return value, after the transaction it was handed committed.
func (h *Host) Transaction(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		connection, isolation, onError string
		fn                             starlark.Callable
		readOnly, retry                bool
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "connection", &connection, "fn", &fn,
		"isolation?", &isolation, "read_only?", &readOnly, "retry?", &retry, argOnError, &onError); err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	returnFailure, err := scriptfail.OnError(b.Name(), onError)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the binding
	}
	if h.open {
		return nil, fmt.Errorf("in %s: a transaction is already open in this function; run its statements with tx, "+
			"since transactions do not nest", b.Name())
	}
	begin := map[string]any{"connection": connection, "isolation": isolation, "read_only": readOnly}
	for attempt := 1; ; attempt++ {
		begin["attempt"] = attempt
		value, err := h.try(thread, b, fn, begin, retry)
		if err == nil {
			return value, nil
		}
		if !retry || attempt == MaxAttempts || !Retryable(err) {
			return h.Failed(b, err, returnFailure)
		}
		if err := h.pause(attempt); err != nil {
			return nil, err
		}
	}
}

// try runs one attempt: open, call fn, commit, or roll back when fn failed.
func (h *Host) try(thread *starlark.Thread, b *starlark.Builtin, fn starlark.Callable, begin map[string]any, retry bool) (starlark.Value, error) {
	out, err := h.Invoke(b, scriptsession.AppDBBegin, begin)
	if err != nil {
		return nil, err
	}
	id, _ := out[argTransaction].(string)
	tx := &txValue{host: h, id: id}
	h.open, h.retrying = true, retry
	value, ferr := starlark.Call(thread, fn, starlark.Tuple{tx.value()}, nil)
	h.open, h.retrying = false, false
	tx.ended = true
	if ferr != nil {
		_, _ = h.Invoke(b, scriptsession.AppDBRollback, map[string]any{argTransaction: id})
		return nil, tx.cause(ferr)
	}
	if _, err := h.Invoke(b, scriptsession.AppDBCommit, map[string]any{argTransaction: id}); err != nil {
		return nil, tx.cause(err)
	}
	return value, nil
}

// pause waits before the next attempt, jittered, never past the run's end.
func (h *Host) pause(attempt int) error {
	if h.Replaying {
		return nil
	}
	d := retryBase << (attempt - 1)
	d = d/2 + jitter(d/2)
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-h.Ctx.Done():
		return fmt.Errorf("halted while waiting to retry a transaction: %w", h.Ctx.Err())
	}
}

// jitter is a random duration below limit, so two runs that collided pause
// for different times.
func jitter(limit time.Duration) time.Duration {
	var b [8]byte
	if limit <= 0 {
		return 0
	}
	if _, err := rand.Read(b[:]); err != nil {
		return limit / 2
	}
	return time.Duration(binary.BigEndian.Uint64(b[:]) % uint64(limit)) // #nosec G115 -- limit is a positive duration
}

// Retryable reports whether err is a serialization failure or a deadlock, the
// failures a retried transaction is tried again on.
func Retryable(err error) bool {
	var refusal *scriptsession.RefusalError
	if !errors.As(err, &refusal) {
		return false
	}
	pg, _ := refusal.Envelope["postgres"].(map[string]any)
	state, _ := pg["sqlstate"].(string)
	return retryStates[state]
}

// Batch is platform.batch(connection, statements, on_error = ""): the
// statements pipelined in one transaction that commits when every one
// succeeds, and their results in order.
func (h *Host) Batch(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		connection, onError string
		list                *starlark.List
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "connection", &connection, "statements", &list, argOnError, &onError); err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	returnFailure, err := scriptfail.OnError(b.Name(), onError)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the binding
	}
	stmts, err := statements(list)
	if err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	out, err := h.Invoke(b, scriptsession.AppDBBatch, map[string]any{"connection": connection, "statements": stmts})
	if err != nil {
		return h.Failed(b, err, returnFailure)
	}
	return results(b, out)
}

// txValue is the tx a transaction's function is handed.
type txValue struct {
	host  *Host
	id    string
	ended bool
	// retryable is whether a call on it failed in a way a retry answers,
	// kept because a function that took the failure with on_error =
	// "return" and returned leaves only "the transaction is over" for the
	// commit to say.
	retryable bool
	failure   error
}

// cause is the error an attempt ended with: a retryable failure of a call in
// it, when there was one, over what followed from it.
func (t *txValue) cause(err error) error {
	if t.retryable && !Retryable(err) {
		return t.failure
	}
	return err
}

func (t *txValue) value() starlark.Value {
	return starlarkstruct.FromStringDict(starlark.String("tx"), starlark.StringDict{
		"query":   starlark.NewBuiltin("tx.query", t.statement(scriptsession.AppDBTxQuery)),
		"execute": starlark.NewBuiltin("tx.execute", t.statement(scriptsession.AppDBTxExecute)),
		"batch":   starlark.NewBuiltin("tx.batch", t.batch),
	})
}

// statement is tx.query(sql, params = None, timeout_seconds = 0, on_error =
// "") or tx.execute with the same arguments.
func (t *txValue) statement(tool string) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var (
			sql, onError string
			params       *starlark.Dict
			timeout      int
		)
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, argSQL, &sql, "params?", &params,
			"timeout_seconds?", &timeout, argOnError, &onError); err != nil {
			return nil, fmt.Errorf(inBinding, b.Name(), err)
		}
		returnFailure, err := scriptfail.OnError(b.Name(), onError)
		if err != nil {
			return nil, err //nolint:wrapcheck // names the binding
		}
		if err := t.usable(b); err != nil {
			return nil, err
		}
		call, err := t.statementCall(sql, params, timeout)
		if err != nil {
			return nil, fmt.Errorf(inBinding, b.Name(), err)
		}
		out, err := t.host.Invoke(b, tool, call)
		if err != nil {
			t.note(err)
			return t.host.Failed(b, err, returnFailure)
		}
		if tool == scriptsession.AppDBTxQuery {
			return t.host.Rows(b.Name(), out)
		}
		return starlarkconv.ResultToStarlark(out) //nolint:wrapcheck // a conversion of the platform's own answer
	}
}

// statementCall is the call one tx statement makes: its SQL with :name
// rewritten to $n, the values bound, and the timeout it asked for.
func (t *txValue) statementCall(sql string, params *starlark.Dict, timeout int) (map[string]any, error) {
	positional, values, err := scriptsql.Positional(sql, params)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the placeholder; the binding names itself around it
	}
	call := map[string]any{argTransaction: t.id, argSQL: positional}
	if len(values) > 0 {
		call["params"] = values
	}
	if timeout != 0 {
		call["timeout_seconds"] = timeout
	}
	return call, nil
}

// Argument names and the error frame the bindings share.
const (
	argSQL         = "sql"
	argTransaction = "transaction"
	argOnError     = "on_error?"
	inBinding      = "in %s: %w"
)

// batch is tx.batch(statements, on_error = ""): the statements pipelined in
// the transaction, and their results in order.
func (t *txValue) batch(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		list    *starlark.List
		onError string
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "statements", &list, argOnError, &onError); err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	returnFailure, err := scriptfail.OnError(b.Name(), onError)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the binding
	}
	if err := t.usable(b); err != nil {
		return nil, err
	}
	stmts, err := statements(list)
	if err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	out, err := t.host.Invoke(b, scriptsession.AppDBTxBatch, map[string]any{argTransaction: t.id, "statements": stmts})
	if err != nil {
		t.note(err)
		return t.host.Failed(b, err, returnFailure)
	}
	return results(b, out)
}

// usable refuses a call on a tx whose transaction ended: one kept past the
// function it was handed to.
func (t *txValue) usable(b *starlark.Builtin) error {
	if t.ended {
		return fmt.Errorf("in %s: this transaction has ended; a tx is used only inside the function "+
			"platform.transaction hands it to", b.Name())
	}
	return nil
}

// note remembers a retryable failure of a call on the transaction.
func (t *txValue) note(err error) {
	if Retryable(err) && !t.retryable {
		t.retryable, t.failure = true, err
	}
}

// statements reads a batch's list: each element is a statement's SQL, or a
// dict holding its sql and params.
func statements(list *starlark.List) ([]any, error) {
	if list == nil || list.Len() == 0 {
		return nil, errors.New("statements is empty; pass a list of SQL strings or of dicts with sql and params")
	}
	out := make([]any, list.Len())
	for i := range list.Len() {
		sql, params, err := statementOf(list.Index(i))
		if err != nil {
			return nil, fmt.Errorf("statement %d: %w", i+1, err)
		}
		positional, values, err := scriptsql.Positional(sql, params)
		if err != nil {
			return nil, fmt.Errorf("statement %d: %w", i+1, err)
		}
		stmt := map[string]any{argSQL: positional}
		if len(values) > 0 {
			stmt["params"] = values
		}
		out[i] = stmt
	}
	return out, nil
}

// statementOf reads one element of a batch's list.
func statementOf(v starlark.Value) (string, *starlark.Dict, error) {
	switch s := v.(type) {
	case starlark.String:
		return string(s), nil, nil
	case *starlark.Dict:
		sql, _, _ := s.Get(starlark.String(argSQL))
		text, ok := sql.(starlark.String)
		if !ok || text == "" {
			return "", nil, errors.New(`a dict statement holds its SQL under "sql"`)
		}
		raw, found, _ := s.Get(starlark.String("params"))
		if !found || raw == starlark.None {
			return string(text), nil, nil
		}
		params, ok := raw.(*starlark.Dict)
		if !ok {
			return "", nil, fmt.Errorf("params is a %s; pass a dict of named values", raw.Type())
		}
		return string(text), params, nil
	default:
		return "", nil, fmt.Errorf("a statement is a SQL string or a dict with sql and params, got a %s", v.Type())
	}
}

// results is a batch's list of results.
func results(b *starlark.Builtin, out map[string]any) (starlark.Value, error) {
	v, err := starlarkconv.ToStarlark(out["results"])
	if err != nil {
		return nil, fmt.Errorf("converting the result of %s: %w", b.Name(), err)
	}
	return v, nil
}
