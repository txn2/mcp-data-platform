package scriptxml

import (
	"fmt"
	"sort"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// Element is a decoded element as a script sees it: a read-only view onto one
// node of the tree xml.decode produced.
//
// It is its own value type rather than a dict so that `el.tag` reads as an
// attribute and so xml.find can be handed a node without the caller
// reassembling one. Because it implements HasAttrs, json.encode renders it as
// an object with the same five fields, which is what lets a script hand a
// decoded document straight to platform.export or print it in a run log.
type Element struct {
	node *xmltree.Node
}

// The element fields a dict may set and an element exposes, spelled once so
// the reader and the writer name them identically.
const (
	fieldTag      = "tag"
	fieldNS       = "ns"
	fieldAttrs    = "attrs"
	fieldText     = "text"
	fieldChildren = "children"
)

// elementFields are the attributes an element exposes, in the order they are
// worth reading. They are also its AttrNames, so the JSON form of an element
// and the attributes an author can reach are one list.
var elementFields = []string{fieldTag, fieldNS, fieldAttrs, fieldText, fieldChildren}

// String renders the element the way an author identifies it in a run log:
// the tag, and the namespace when there is one. The document itself is what
// xml.encode is for, so this stays short however large the subtree is.
func (e *Element) String() string {
	if e.node.NS != "" {
		return fmt.Sprintf("<%s xmlns=%q>", e.node.Tag, e.node.NS)
	}
	return fmt.Sprintf("<%s>", e.node.Tag)
}

// Type is the name an error message calls this value.
func (*Element) Type() string { return "xml.element" }

// Freeze is a no-op: an element is immutable whether or not it has been
// frozen. Nothing a script can do reaches the tree behind it — the dict and
// list Attr hands back are fresh values, and they are frozen there rather than
// here so a mutation attempt is an error at the point it is written instead of
// a silent write to a copy.
func (*Element) Freeze() {}

// Truth reports an element as true, so `if xml.find(...)` distinguishes a
// match from the None returned for no match rather than from an empty element.
func (*Element) Truth() starlark.Bool { return starlark.True }

// Hash refuses: an element is not a dict key. Matching elements by identity
// would be the only available meaning and it is not one an author would want.
func (e *Element) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", e.Type())
}

// AttrNames lists the element's readable attributes.
func (*Element) AttrNames() []string {
	names := make([]string, len(elementFields))
	copy(names, elementFields)
	return names
}

// Attr reads one of the element's five fields. An unknown name returns nil,
// which is how Starlark asks a value to report that it has no such attribute
// and produces the standard "has no .foo field" error.
func (e *Element) Attr(name string) (starlark.Value, error) {
	switch name {
	case fieldTag:
		return starlark.String(e.node.Tag), nil
	case fieldNS:
		return starlark.String(e.node.NS), nil
	case fieldAttrs:
		d := attrDict(e.node.Attrs)
		d.Freeze()
		return d, nil
	case fieldText:
		return starlark.String(e.node.Text), nil
	case fieldChildren:
		l := elementList(e.node.Children)
		l.Freeze()
		return l, nil
	default:
		// Starlark's own contract for a missing attribute: nil with no
		// error is what produces the standard "has no .foo field"
		// message, and returning an error here would replace it with a
		// worse one.
		return nil, nil //nolint:nilnil // HasAttrs.Attr reports "no such field" this way
	}
}

// attrDict renders an element's attributes, in name order so a script that
// iterates them behaves the same on every run.
func attrDict(attrs map[string]string) *starlark.Dict {
	names := make([]string, 0, len(attrs))
	for k := range attrs {
		names = append(names, k)
	}
	sort.Strings(names)
	d := starlark.NewDict(len(names))
	for _, k := range names {
		// SetKey fails only on an unhashable key or a frozen dict, and
		// this dict is fresh with string keys.
		_ = d.SetKey(starlark.String(k), starlark.String(attrs[k]))
	}
	return d
}

// toNode reads the tree argument of xml.encode: an element as decode returned
// it, or the same shape written as a dict, so a script can build a document
// from data instead of from string concatenation.
//
// Depth is bounded because a dict may reach itself through a list, which would
// otherwise be an unbounded walk rather than an error.
func toNode(v starlark.Value, depth int) (*xmltree.Node, error) {
	if depth >= xmltree.DefaultLimits.MaxDepth {
		return nil, fmt.Errorf("an element dict reaches itself: %w of %d",
			xmltree.ErrTooDeep, xmltree.DefaultLimits.MaxDepth)
	}
	switch t := v.(type) {
	case *Element:
		return t.node, nil
	case *starlark.Dict:
		return dictToNode(t, depth)
	default:
		return nil, fmt.Errorf("tree is %s; pass an element from xml.decode or a dict with a %q key", v.Type(), fieldTag)
	}
}

// dictToNode converts the dict convenience form. Only `tag` is required;
// `ns`, `attrs`, `text` and `children` default to the empty element.
func dictToNode(d *starlark.Dict, depth int) (*xmltree.Node, error) {
	n := &xmltree.Node{}
	var err error
	if n.Tag, err = dictString(d, fieldTag); err != nil {
		return nil, err
	}
	if n.Tag == "" {
		return nil, fmt.Errorf("an element dict needs a non-empty %q", fieldTag)
	}
	if n.NS, err = dictString(d, fieldNS); err != nil {
		return nil, err
	}
	if n.Text, err = dictString(d, fieldText); err != nil {
		return nil, err
	}
	if n.Attrs, err = dictAttrs(d); err != nil {
		return nil, err
	}
	n.Children, err = dictChildren(d, depth)
	return n, err
}

// dictGet reads one key, naming the dict it came from when the lookup itself
// fails. A Starlark dict lookup fails only on an unhashable key, which a string
// is not, so this is the unreachable arm reported rather than swallowed.
func dictGet(d *starlark.Dict, key string) (starlark.Value, bool, error) {
	v, found, err := d.Get(starlark.String(key))
	if err != nil {
		return nil, false, fmt.Errorf("reading %q from an element dict: %w", key, err)
	}
	return v, found, nil
}

// dictString reads an optional string-valued key.
func dictString(d *starlark.Dict, key string) (string, error) {
	v, found, err := dictGet(d, key)
	if err != nil || !found {
		return "", err
	}
	s, ok := starlark.AsString(v)
	if !ok {
		return "", fmt.Errorf("the %q of an element dict is %s; it has to be a string", key, v.Type())
	}
	return s, nil
}

// dictAttrs reads the optional `attrs` key as a dict of strings.
func dictAttrs(d *starlark.Dict) (map[string]string, error) {
	v, found, err := dictGet(d, fieldAttrs)
	if err != nil || !found {
		return nil, err
	}
	src, ok := v.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("the %q of an element dict is %s; it has to be a dict of strings",
			fieldAttrs, v.Type())
	}
	out := make(map[string]string, src.Len())
	for _, item := range src.Items() {
		name, nameOK := starlark.AsString(item[0])
		value, valueOK := starlark.AsString(item[1])
		if !nameOK || !valueOK {
			return nil, fmt.Errorf("the %q of an element dict holds %s: %s; "+
				"every attribute name and value has to be a string",
				fieldAttrs, item[0].Type(), item[1].Type())
		}
		out[name] = value
	}
	return out, nil
}

// dictChildren reads the optional `children` key as a list of elements or
// element dicts.
func dictChildren(d *starlark.Dict, depth int) ([]*xmltree.Node, error) {
	v, found, err := dictGet(d, fieldChildren)
	if err != nil || !found {
		return nil, err
	}
	list, ok := v.(*starlark.List)
	if !ok {
		return nil, fmt.Errorf("the %q of an element dict is %s; it has to be a list", fieldChildren, v.Type())
	}
	out := make([]*xmltree.Node, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		child, childErr := toNode(list.Index(i), depth+1)
		if childErr != nil {
			return nil, fmt.Errorf("in %q[%d]: %w", fieldChildren, i, childErr)
		}
		out = append(out, child)
	}
	return out, nil
}
