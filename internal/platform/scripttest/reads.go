package scripttest

import (
	"fmt"
	"slices"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// reads is what a source's tests produced and which of it they read (#1952):
// each export's columns (or its body, or the export itself when it has no
// rows), the staged state, platform.result, each notification and each
// published data region. A test reads a value by reaching it from
// testing.outputs() -- reading a row's column, handing a row or an output to
// an assertion, reading .state, .result or .body -- and what the tests
// produced that none of them read is an output no assertion looked at.
//
// Outputs are identified by name across tests, so one test may produce an
// output another asserts on.
type reads struct {
	order []string
	desc  map[string]string
	read  map[string]bool
}

func newReads() *reads { return &reads{desc: map[string]string{}, read: map[string]bool{}} }

// produce registers one output a test's execution produced.
func (r *reads) produce(id, desc string) {
	if r == nil {
		return
	}
	if _, ok := r.desc[id]; !ok {
		r.order = append(r.order, id)
		r.desc[id] = desc
	}
}

// mark records that a test read the output id.
func (r *reads) mark(id string) {
	if r != nil {
		r.read[id] = true
	}
}

// unread is every output produced that no test read, in the order first
// produced, each as the author reads it.
func (r *reads) unread() []string {
	out := []string{}
	for _, id := range r.order {
		if !r.read[id] {
			out = append(out, r.desc[id])
		}
	}
	return out
}

// Output identities.
func exportID(name string) string         { return "export\x00" + name }
func columnID(name, column string) string { return "column\x00" + name + "\x00" + column }
func publishID(name string) string        { return "publish\x00" + name }
func notifyID(channel, title string, n int) string {
	return fmt.Sprintf("notify\x00%s\x00%s\x00%d", channel, title, n)
}

const (
	stateID  = "state"
	resultID = "result"
)

// register records every output one test's execution produced.
func (p *produced) register(r *reads) {
	if r == nil {
		return
	}
	p.registerExports(r)
	if p.state != nil {
		r.produce(stateID, "the staged state")
	}
	if len(p.live.Result()) > 0 {
		r.produce(resultID, "platform.result")
	}
	for _, pub := range p.publishes {
		r.produce(publishID(pub.Name), fmt.Sprintf("published data %q", pub.Name))
	}
	ids := p.notifyIDs()
	for i := range p.replay.Made() {
		if n, ok := ids[i]; ok {
			r.produce(n.id, n.desc)
		}
	}
}

// registerExports records each export: by column when it has rows, as a
// whole when it has none.
func (p *produced) registerExports(r *reads) {
	for _, e := range p.exports {
		if cols := exportColumns(e.Columns, e.Rows); len(e.Rows) > 0 && len(cols) > 0 {
			for _, c := range cols {
				r.produce(columnID(e.Name, c), fmt.Sprintf("output %q column %q", e.Name, c))
			}
			continue
		}
		r.produce(exportID(e.Name), fmt.Sprintf("output %q", e.Name))
	}
}

type notifyRef struct{ id, desc string }

// notifyIDs names each notification the execution sent, by the position of
// its call among the calls made: by channel and title (a publish by the asset
// it posts) and, where one repeats, by its turn. A notify call that failed
// sent nothing and is not an output.
func (p *produced) notifyIDs() map[int]notifyRef {
	seen := map[string]int{}
	out := map[int]notifyRef{}
	for i, m := range p.replay.Made() {
		if m.Tool != toolNotify || m.Error != "" {
			continue
		}
		channel, _ := m.Args["channel"].(string)
		title, _ := m.Args["title"].(string)
		if title == "" {
			title, _ = m.Args["asset"].(string)
		}
		key := channel + "\x00" + title
		seen[key]++
		desc := fmt.Sprintf("notification %q to %s", title, channel)
		if seen[key] > 1 {
			desc += fmt.Sprintf(" (%d)", seen[key])
		}
		out[i] = notifyRef{id: notifyID(channel, title, seen[key]), desc: desc}
	}
	return out
}

// exportColumns is the columns an export's rows carry, in the order it
// declared them and then in name order.
func exportColumns(declared []string, rows []any) []string {
	present := map[string]bool{}
	for _, row := range rows {
		if m, ok := row.(map[string]any); ok {
			for k := range m {
				present[k] = true
			}
		}
	}
	var out []string
	for _, c := range declared {
		if present[c] && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	rest := make([]string, 0, len(present))
	for k := range present {
		if !slices.Contains(out, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// tracked is a dict an output hands a test that says when it is read: a row
// by the column read, any other dict as a whole. It is its own type, never
// "dict": starlark's dict comparison takes its operand to be a *Dict, and a
// test that compares one with == reads False rather than crashing; the
// assertions compare its contents.
type tracked struct {
	d    *starlark.Dict
	typ  string
	mark func(key string)
}

func newTracked(d *starlark.Dict, typ string, mark func(string)) *tracked {
	d.Freeze()
	return &tracked{d: d, typ: typ, mark: mark}
}

// all marks every key read.
func (t *tracked) all() {
	for _, k := range t.d.Keys() {
		if s, ok := k.(starlark.String); ok {
			t.mark(string(s))
		}
	}
	t.mark("")
}

// String, Type, Freeze, Truth, Hash and Len make tracked read as the dict it
// holds.
func (t *tracked) String() string { return t.d.String() }

// Type is the output's kind: row, state, notify, data or result.
func (t *tracked) Type() string { return t.typ }

// Freeze has nothing left to freeze: the dict was frozen when made.
func (*tracked) Freeze() {}

// Truth is the dict's.
func (t *tracked) Truth() starlark.Bool { return t.d.Truth() }

// Hash refuses, as a dict's does.
func (t *tracked) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: %s", t.typ) }

// Len is the dict's; counting a row's columns reads none of them.
func (t *tracked) Len() int { return t.d.Len() }

// Iterate is the dict's keys; reading a key is not reading its value.
func (t *tracked) Iterate() starlark.Iterator { return t.d.Iterate() }

// Get reads one key.
func (t *tracked) Get(k starlark.Value) (starlark.Value, bool, error) {
	if s, ok := k.(starlark.String); ok {
		t.mark(string(s))
	}
	return t.d.Get(k) //nolint:wrapcheck // the dict's own error
}

// Items reads every key.
func (t *tracked) Items() []starlark.Tuple {
	t.all()
	return t.d.Items()
}

// CompareSameType compares the dicts two tracked values hold, reading both.
func (t *tracked) CompareSameType(op syntax.Token, y starlark.Value, depth int) (bool, error) {
	o, _ := y.(*tracked)
	if o == nil {
		return false, fmt.Errorf("comparing %s %s %s is not implemented", t.Type(), op, y.Type())
	}
	t.all()
	o.all()
	return starlark.CompareDepth(op, t.d, o.d, depth) //nolint:wrapcheck // the dict comparison's own error
}

// AttrNames is a dict's reading methods.
func (*tracked) AttrNames() []string { return []string{"get", "items", "keys", "values"} }

// Attr is a dict's reading methods; get reads its key, items and values every
// key, and keys none.
func (t *tracked) Attr(name string) (starlark.Value, error) {
	switch name {
	case "get":
		return starlark.NewBuiltin(t.typ+".get", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var key, dflt starlark.Value = nil, starlark.None
			if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &key, &dflt); err != nil {
				return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
			}
			v, found, err := t.Get(key)
			if err != nil || !found {
				return dflt, err
			}
			return v, nil
		}), nil
	case "items", "values", "keys":
		return starlark.NewBuiltin(t.typ+"."+name, func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 0); err != nil {
				return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
			}
			return t.method(name), nil
		}), nil
	}
	return nil, nil //nolint:nilnil // starlark.HasAttrs: nil, nil is "no such field", which the interpreter words
}

func (t *tracked) method(name string) starlark.Value {
	switch name {
	case "keys":
		return starlark.NewList(t.d.Keys())
	case "values":
		t.all()
		vals := make([]starlark.Value, 0, t.d.Len())
		for _, it := range t.d.Items() {
			vals = append(vals, it[1])
		}
		return starlark.NewList(vals)
	}
	items := t.Items()
	out := make([]starlark.Value, 0, len(items))
	for _, it := range items {
		out = append(out, it)
	}
	return starlark.NewList(out)
}

// watched is an output struct -- testing.outputs() itself, an export, a
// published region -- that says which of its fields a test read.
type watched struct {
	typ    string
	fields starlark.StringDict
	onAttr func(name string)
}

// String, Type, Freeze, Truth and Hash make watched read as a struct.
func (w *watched) String() string {
	parts := make([]string, 0, len(w.fields))
	for _, k := range w.AttrNames() {
		parts = append(parts, k+" = "+w.fields[k].String())
	}
	return w.typ + "(" + strings.Join(parts, ", ") + ")"
}

// Type is the output's kind: outputs, export or publish.
func (w *watched) Type() string { return w.typ }

// Freeze freezes the fields.
func (w *watched) Freeze() { w.fields.Freeze() }

// Truth is a struct's.
func (*watched) Truth() starlark.Bool { return starlark.True }

// Hash refuses: an output is not a key.
func (w *watched) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: %s", w.typ) }

// AttrNames is the fields, sorted.
func (w *watched) AttrNames() []string { return w.fields.Keys() }

// Attr reads one field.
func (w *watched) Attr(name string) (starlark.Value, error) {
	v, ok := w.fields[name]
	if !ok {
		return nil, nil //nolint:nilnil // starlark.HasAttrs: nil, nil is "no such field", which the interpreter words
	}
	if w.onAttr != nil {
		w.onAttr(name)
	}
	return v, nil
}

// plain is v with every tracked dict inside it replaced by the dict it holds
// and read whole, and every watched struct read whole: what an assertion is
// handed is read, and compares as the values it holds.
func plain(v starlark.Value) starlark.Value {
	switch v := v.(type) {
	case *tracked:
		v.all()
		return plainDict(v.d)
	case *watched:
		for _, k := range v.AttrNames() {
			if v.onAttr != nil {
				v.onAttr(k)
			}
			plain(v.fields[k])
		}
		return v
	case *starlark.List:
		items := make([]starlark.Value, v.Len())
		for i := range items {
			items[i] = plain(v.Index(i))
		}
		return starlark.NewList(items)
	case starlark.Tuple:
		return plainTuple(v)
	case *starlark.Dict:
		return plainDict(v)
	}
	return v
}

func plainTuple(t starlark.Tuple) starlark.Tuple {
	items := make(starlark.Tuple, len(t))
	for i := range t {
		items[i] = plain(t[i])
	}
	return items
}

func plainDict(d *starlark.Dict) *starlark.Dict {
	out := starlark.NewDict(d.Len())
	for _, it := range d.Items() {
		_ = out.SetKey(plain(it[0]), plain(it[1]))
	}
	return out
}
