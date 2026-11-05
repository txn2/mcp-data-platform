package apichange

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/pathtemplate"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
	"github.com/txn2/mcp-data-platform/internal/untrusted"
)

// Caller kinds, and how a caller was found (#2485).
const (
	CallerScript   = "script"
	CallerEndpoint = "endpoint"
	CallerPrompt   = "prompt"

	ViaSource = "source" // the script's saved source names the operation
	ViaRun    = "run"    // a run of the script called it
	ViaPrompt = "prompt" // the prompt's text names it
)

// Caller is one script, endpoint or prompt that uses an operation.
type Caller struct {
	Kind       string     `json:"kind"`
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Via        string     `json:"via"`
	Connection string     `json:"connection,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// OperationIndex resolves an address a caller uses to the operation a report
// names: an operation id, or a method and a concrete or template path.
type OperationIndex struct {
	byID  map[string]Op
	byKey map[string][]Op // method -> operations, for template matching
}

// NewOperationIndex indexes the operations of each spec content. A content
// that does not parse contributes nothing: the index answers for what can be
// read.
func NewOperationIndex(contents ...string) OperationIndex {
	idx := OperationIndex{byID: map[string]Op{}, byKey: map[string][]Op{}}
	for _, c := range contents {
		ops, _, err := operationsOf(c)
		if err != nil {
			continue
		}
		for id, o := range ops {
			idx.byID[id] = o.op
			idx.byKey[o.op.Method] = append(idx.byKey[o.op.Method], o.op)
		}
	}
	return idx
}

// Ops is every indexed operation, in id order.
func (x OperationIndex) Ops() []Op {
	out := make([]Op, 0, len(x.byID))
	for _, o := range x.byID {
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b Op) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// maxPrefixSegments is how many leading segments a caller's path may carry
// beyond the spec's own paths: the base path a catalog spec or connection
// prepends, which a call addresses and the spec does not write.
const maxPrefixSegments = 3

// Resolve returns the operation an address reaches. An id is looked up as
// written; a method and path match the operation whose template the path
// fits, the most literal one when several do, and failing that the path with
// its leading base-path segments removed.
func (x OperationIndex) Resolve(operationID, method, path string) (Op, bool) {
	if operationID != "" {
		o, ok := x.byID[operationID]
		return o, ok
	}
	method = strings.ToUpper(method)
	path = pathtemplate.StripQuery(path)
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for drop := 0; drop <= maxPrefixSegments && drop < len(segs); drop++ {
		if o, ok := x.match(method, "/"+strings.Join(segs[drop:], "/")); ok {
			return o, true
		}
	}
	return Op{}, false
}

// match is the operation of method whose template path fits.
func (x OperationIndex) match(method, path string) (Op, bool) {
	var best Op
	found, bestHoles := false, 0
	for _, o := range x.byKey[method] {
		if o.Path == path {
			return o, true
		}
		if !pathtemplate.Match(path, o.Path) {
			continue
		}
		if holes := pathtemplate.CountPlaceholders(o.Path); !found || holes < bestHoles {
			best, found, bestHoles = o, true, holes
		}
	}
	return best, found
}

// Usage answers which scripts, endpoints and prompts use the operations of
// a set of connections.
type Usage struct {
	DB *sql.DB
}

// callerKey is a caller's identity within one operation's list.
type callerKey struct{ kind, id, via, connection string }

// collected gathers callers per operation, each once.
type collected struct {
	byOp map[string][]Caller
	seen map[string]map[callerKey]bool
}

func newCollected() *collected {
	return &collected{byOp: map[string][]Caller{}, seen: map[string]map[callerKey]bool{}}
}

func (c *collected) add(op string, caller Caller) {
	k := callerKey{caller.Kind, caller.ID, caller.Via, caller.Connection}
	if c.seen[op] == nil {
		c.seen[op] = map[callerKey]bool{}
	}
	if c.seen[op][k] {
		return
	}
	c.seen[op][k] = true
	c.byOp[op] = append(c.byOp[op], caller)
}

// Callers returns, for each operation of idx that connections reach, what
// uses it: scripts whose source names it, scripts a run of which called it,
// the endpoints bound to either, and prompts whose text names it. An
// operation nothing uses is absent.
func (u Usage) Callers(ctx context.Context, connections []string, idx OperationIndex) (map[string][]Caller, error) {
	out := newCollected()
	if len(connections) == 0 || u.DB == nil {
		return out.byOp, nil
	}
	if err := u.fromSources(ctx, connections, idx, out); err != nil {
		return nil, err
	}
	if err := u.fromRuns(ctx, connections, out); err != nil {
		return nil, err
	}
	if err := u.fromPrompts(ctx, connections, idx, out); err != nil {
		return nil, err
	}
	for _, list := range out.byOp {
		slices.SortFunc(list, func(a, b Caller) int {
			return strings.Compare(a.Kind+a.Name+a.Via, b.Kind+b.Name+b.Via)
		})
	}
	return out.byOp, nil
}

// scriptsSQL is every script with its saved source and the endpoint bound to
// it, if any.
const scriptsSQL = `SELECT s.id::text, s.name, s.source_code, COALESCE(e.name, '')
	FROM scripts s LEFT JOIN script_endpoints e ON e.script_id = s.id
	ORDER BY s.name`

// fromSources reads each script's source for the API calls it names.
func (u Usage) fromSources(ctx context.Context, connections []string, idx OperationIndex, out *collected) error {
	rows, err := u.DB.QueryContext(ctx, scriptsSQL)
	if err != nil {
		return fmt.Errorf("listing scripts for the used-by index: %w", err)
	}
	defer func() { _ = rows.Close() }()
	reach := set(connections)
	for rows.Next() {
		var id, name, source, endpoint string
		if err := rows.Scan(&id, &name, &source, &endpoint); err != nil {
			return fmt.Errorf("scanning a script for the used-by index: %w", err)
		}
		addSourceCalls(out, reach, idx, scriptRow{id: id, name: name, source: source, endpoint: endpoint})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("listing scripts for the used-by index: %w", err)
	}
	return nil
}

// scriptRow is one script as the used-by index reads it.
type scriptRow struct{ id, name, source, endpoint string }

// addSourceCalls files each API call sc's source names on a connection in
// reach under the operation it resolves to, with the endpoint bound to it.
func addSourceCalls(out *collected, reach map[string]bool, idx OperationIndex, sc scriptRow) {
	for _, call := range scriptflow.APICalls(sc.source) {
		if !reach[call.Connection] {
			continue
		}
		op, ok := idx.Resolve(call.OperationID, call.Method, call.Path)
		if !ok {
			continue
		}
		out.add(op.ID, Caller{Kind: CallerScript, ID: sc.id, Name: sc.name, Via: ViaSource, Connection: call.Connection})
		if sc.endpoint != "" {
			out.add(op.ID, Caller{Kind: CallerEndpoint, ID: sc.endpoint, Name: sc.endpoint, Via: ViaSource, Connection: call.Connection})
		}
	}
}

// runCallersSQL is the operations runs of each script called on the named
// connections, with the endpoint bound to the script.
const runCallersSQL = `SELECT c.operation, c.connection, s.id::text, s.name, COALESCE(e.name, ''), c.last_seen_at
	FROM api_operation_callers c
	JOIN scripts s ON s.id::text = c.script_id
	LEFT JOIN script_endpoints e ON e.script_id = s.id
	WHERE c.connection = ANY($1)`

// fromRuns adds the scripts a run of which called an operation.
func (u Usage) fromRuns(ctx context.Context, connections []string, out *collected) error {
	rows, err := u.DB.QueryContext(ctx, runCallersSQL, pq.Array(connections))
	if err != nil {
		return fmt.Errorf("listing the operations script runs called: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var op, conn, id, name, endpoint string
		var seen time.Time
		if err := rows.Scan(&op, &conn, &id, &name, &endpoint, &seen); err != nil {
			return fmt.Errorf("scanning an operation a script run called: %w", err)
		}
		at := seen
		out.add(op, Caller{Kind: CallerScript, ID: id, Name: name, Via: ViaRun, Connection: conn, LastSeenAt: &at})
		if endpoint != "" {
			out.add(op, Caller{Kind: CallerEndpoint, ID: endpoint, Name: endpoint, Via: ViaRun, Connection: conn, LastSeenAt: &at})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("listing the operations script runs called: %w", err)
	}
	return nil
}

// promptsSQL is every prompt whose text names one of the connections, the
// prompts that can name one of their operations.
const promptsSQL = `SELECT id::text, name, content FROM prompts
	WHERE EXISTS (SELECT 1 FROM unnest($1::text[]) AS c(name) WHERE prompts.content LIKE '%' || c.name || '%')`

// fromPrompts adds the prompts whose text names an operation: its id as a
// whole word, in a prompt that also names the connection. An operation with
// no id of its own is named as "METHOD path".
func (u Usage) fromPrompts(ctx context.Context, connections []string, idx OperationIndex, out *collected) error {
	rows, err := u.DB.QueryContext(ctx, promptsSQL, pq.Array(connections))
	if err != nil {
		return fmt.Errorf("listing prompts for the used-by index: %w", err)
	}
	defer func() { _ = rows.Close() }()
	m := newPromptMatcher(idx.Ops())
	for rows.Next() {
		var id, name, content string
		if err := rows.Scan(&id, &name, &content); err != nil {
			return fmt.Errorf("scanning a prompt for the used-by index: %w", err)
		}
		for _, conn := range connections {
			if strings.Contains(content, conn) {
				m.file(out, Caller{Kind: CallerPrompt, ID: id, Name: untrusted.Line(name), Via: ViaPrompt, Connection: conn}, content)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("listing prompts for the used-by index: %w", err)
	}
	return nil
}

// promptMatcher finds the operations a prompt's text names as whole words.
type promptMatcher struct {
	ops   []Op
	words []*regexp.Regexp
}

func newPromptMatcher(ops []Op) promptMatcher {
	m := promptMatcher{ops: ops, words: make([]*regexp.Regexp, len(ops))}
	for i, op := range ops {
		m.words[i] = wordPattern(op.ID)
	}
	return m
}

// file adds caller under every operation content names.
func (m promptMatcher) file(out *collected, caller Caller, content string) {
	for i, op := range m.ops {
		if m.words[i] != nil && m.words[i].MatchString(content) {
			out.add(op.ID, caller)
		}
	}
}

// wordPattern matches word with no letter, digit or underscore on either
// side; nil for an empty word.
func wordPattern(word string) *regexp.Regexp {
	if word == "" {
		return nil
	}
	return regexp.MustCompile(`(^|[^\w])` + regexp.QuoteMeta(word) + `($|[^\w])`)
}

func set(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
