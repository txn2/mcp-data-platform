package scripttx

import (
	"errors"
	"fmt"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptfail"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsql"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// CapabilityQueryIter is platform.query_iter (#2454).
const CapabilityQueryIter = "platform.query_iter"

// QueryIter is platform.query_iter(connection, sql, params = None, page_rows =
// 0, timeout_seconds = 0, on_error = ""): the query's rows, read in pages of
// page_rows from a cursor in a READ ONLY transaction of its own, as an
// iterable a script loops over. Each page is charged against the run's memory
// budget, so a loop holds one page at a time.
func (h *Host) QueryIter(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var connection string
	call, returnFailure, err := iterCall(b, args, kwargs, &connection)
	if err != nil {
		return nil, err
	}
	call[argConnection] = connection
	return h.openIter(thread, b, call, returnFailure, nil)
}

// queryIter is tx.query_iter(sql, params = None, page_rows = 0,
// timeout_seconds = 0, on_error = ""): the same, from a cursor in the
// transaction.
func (t *txValue) queryIter(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	call, returnFailure, err := iterCall(b, args, kwargs, nil)
	if err != nil {
		return nil, err
	}
	if err := t.usable(b); err != nil {
		return nil, err
	}
	call[argTransaction] = t.id
	return t.host.openIter(thread, b, call, returnFailure, t)
}

// iterCall reads query_iter's arguments into the cursor_open call, connection
// first when it takes one.
func iterCall(b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple, connection *string) (call map[string]any, returnFailure bool, err error) {
	var (
		sql, onError      string
		params            *starlark.Dict
		pageRows, timeout int
		pairs             []any
	)
	if connection != nil {
		pairs = append(pairs, argConnection, connection)
	}
	pairs = append(pairs, argSQL, &sql, "params?", &params, "page_rows?", &pageRows, "timeout_seconds?", &timeout, argOnError, &onError)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, pairs...); err != nil {
		return nil, false, fmt.Errorf(inBinding, b.Name(), err)
	}
	returnFailure, err = scriptfail.OnError(b.Name(), onError)
	if err != nil {
		return nil, false, err //nolint:wrapcheck // names the binding
	}
	if pageRows < 0 {
		return nil, false, fmt.Errorf("in %s: page_rows is %d; pass a positive number of rows per page, or leave it out for the connection's max_rows", b.Name(), pageRows)
	}
	positional, values, err := scriptsql.Positional(sql, params)
	if err != nil {
		return nil, false, fmt.Errorf(inBinding, b.Name(), err)
	}
	call = map[string]any{argSQL: positional}
	if len(values) > 0 {
		call["params"] = values
	}
	if pageRows > 0 {
		call["page_rows"] = pageRows
	}
	if timeout != 0 {
		call["timeout_seconds"] = timeout
	}
	return call, returnFailure, nil
}

// openIter declares the cursor now, so a refused query is the call's failure
// and on_error applies to it, and returns the rows to loop over.
func (h *Host) openIter(thread *starlark.Thread, b *starlark.Builtin, call map[string]any, returnFailure bool, tx *txValue) (starlark.Value, error) {
	out, err := h.Invoke(b, scriptsession.AppDBCursorOpen, call)
	if err != nil {
		if tx != nil {
			tx.note(err)
		}
		return h.Failed(b, err, returnFailure)
	}
	id, _ := out["cursor"].(string)
	return &rows{host: h, thread: thread, b: b, id: id, tx: tx}, nil
}

// rows is what query_iter returns: a query's rows, read once.
type rows struct {
	host   *Host
	thread *starlark.Thread
	b      *starlark.Builtin
	id     string
	tx     *txValue
	used   bool
}

var _ starlark.Iterable = (*rows)(nil)

// String implements starlark.Value.
func (r *rows) String() string { return "<query_iter " + r.id + ">" }

// Type implements starlark.Value.
func (*rows) Type() string { return "query_iter" }

// Freeze implements starlark.Value: the rows hold nothing a script can change.
func (*rows) Freeze() {}

// Truth implements starlark.Value.
func (*rows) Truth() starlark.Bool { return starlark.True }

// Hash implements starlark.Value: a result is not a dict key.
func (*rows) Hash() (uint32, error) { return 0, errors.New("unhashable type: query_iter") }

// Iterate returns the loop over the rows. A second loop over the same result
// stops the run: the cursor has moved past the rows the first one read.
func (r *rows) Iterate() starlark.Iterator {
	if r.used {
		r.host.stop(r.thread, r.b, errors.New("a query_iter result is read by one loop; call it again to read the query again"))
		return &rowLoop{r: r, done: true, closed: true}
	}
	r.used = true
	return &rowLoop{r: r}
}

// rowLoop reads a page whenever the one it holds runs out, handing each row
// on and keeping no reference to a row it handed on.
type rowLoop struct {
	r      *rows
	page   []starlark.Value
	next   int
	pages  int
	done   bool
	closed bool
}

// Next hands on the next row, reading a page when the one held runs out.
func (l *rowLoop) Next(p *starlark.Value) bool { //nolint:gocritic // the starlark.Iterator signature
	for l.next >= len(l.page) {
		if l.done || !l.fetch() {
			return false
		}
	}
	*p = l.page[l.next]
	l.page[l.next] = nil
	l.next++
	return true
}

// fetch reads the next page. A failure (the read, the loop's row bound, the
// memory budget) stops the run naming it: a loop that ended early would hand
// the script a partial result as if it were whole.
func (l *rowLoop) fetch() bool {
	r := l.r
	if err := r.host.check(r.thread, r.b.Name()); err != nil {
		r.host.stop(r.thread, r.b, err)
		return false
	}
	l.pages++
	out, err := r.host.Invoke(r.b, scriptsession.AppDBCursorFetch, map[string]any{argCursor: r.id, "page": l.pages})
	if err != nil {
		if r.tx != nil {
			r.tx.note(err)
		}
		r.host.stop(r.thread, r.b, err)
		return false
	}
	raw, _ := out["rows"].([]any)
	page, err := starlarkconv.RowsToStarlark(raw, starlarkconv.ColumnNames(out["columns"]))
	if err != nil {
		r.host.stop(r.thread, r.b, fmt.Errorf("converting page %d: %w", l.pages, err))
		return false
	}
	list, _ := page.(*starlark.List)
	r.host.hold(list)
	if err := r.host.check(r.thread, r.b.Name()); err != nil {
		r.host.stop(r.thread, r.b, err)
		return false
	}
	l.page, l.next = make([]starlark.Value, 0, list.Len()), 0
	for i := range list.Len() {
		l.page = append(l.page, list.Index(i))
	}
	l.done, _ = out["done"].(bool)
	return true
}

// Done closes the cursor when the loop ends, however it ends.
func (l *rowLoop) Done() {
	if l.closed {
		return
	}
	l.closed = true
	l.page = nil
	l.r.host.hold(nil)
	_, _ = l.r.host.Invoke(l.r.b, scriptsession.AppDBCursorClose, map[string]any{argCursor: l.r.id})
}

// argCursor names the cursor a fetch or close reads.
const argCursor = "cursor"

// check and hold are the memory meter's, when the run installed it.
func (h *Host) check(thread *starlark.Thread, at string) error {
	if h.Check == nil {
		return nil
	}
	return h.Check(thread, at)
}

func (h *Host) hold(page starlark.Value) {
	if h.Hold != nil {
		h.Hold(page)
	}
}

// stop ends the run from inside a loop, which has no error of its own to
// return: the thread is canceled naming the failure, and the failure is kept
// for the run's error (Stopped).
func (h *Host) stop(thread *starlark.Thread, b *starlark.Builtin, err error) {
	if h.stopped == nil {
		h.stopped = fmt.Errorf(inBinding, b.Name(), err)
	}
	thread.Cancel(h.stopped.Error())
}

// Stopped is the failure that stopped the run from inside a loop, nil when
// none did.
func (h *Host) Stopped() error { return h.stopped }
