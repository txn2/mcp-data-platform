// Package scriptxml is the XML reading a managed script is given, as a
// Starlark module.
//
// It is separate from the engine for the reason internal/scriptdate is: every
// function is a pure transformation of its arguments, there is no run, no
// caller and no state, and the engine's only use of it is to predeclare the
// module. The parsing itself is internal/xmltree, which api_invoke_endpoint
// also decodes through, so a script and a tool call read one document the same
// way (#1735).
package scriptxml

import (
	"fmt"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// Module is the XML module the engine predeclares.
//
// The four functions are the whole of it. There is deliberately no
// pretty-printer, no schema validation and no namespace registry: an element's
// namespace is on the element, names match on their local part, and a script
// that needs a document rendered for a person is exporting it, not formatting
// it.
var Module = &starlarkstruct.Module{
	Name: "xml",
	Members: starlark.StringDict{
		"decode":  starlark.NewBuiltin("xml.decode", xmlDecode),
		"encode":  starlark.NewBuiltin("xml.encode", xmlEncode),
		"find":    starlark.NewBuiltin("xml.find", xmlFind),
		"findall": starlark.NewBuiltin("xml.findall", xmlFindAll),
	},
}

// inCall is the wrap every refusal from one of these builtins carries: an
// author reads which call they got wrong rather than a bare message.
const inCall = "in %s: %w"

// argErr wraps an argument-unpacking failure with the binding it came from,
// following internal/scriptdate: an author reads which call they got wrong
// rather than a bare argument name.
func argErr(b *starlark.Builtin, err error) error {
	return fmt.Errorf(inCall, b.Name(), err)
}

// xmlDecode parses a document into its root element.
func xmlDecode(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var doc string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "s", &doc); err != nil {
		return nil, argErr(b, err)
	}
	root, err := xmltree.Decode(doc, xmltree.DefaultLimits)
	if err != nil {
		return nil, fmt.Errorf(inCall, b.Name(), err)
	}
	return &Element{node: root}, nil
}

// xmlEncode writes an element back to a document. It accepts an element as
// decode returned it, or the same shape built as a dict, so a script can
// assemble a request body from data without first parsing one.
func xmlEncode(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var tree starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "tree", &tree); err != nil {
		return nil, argErr(b, err)
	}
	node, err := toNode(tree, 0)
	if err != nil {
		return nil, fmt.Errorf(inCall, b.Name(), err)
	}
	doc, err := xmltree.Encode(node, xmltree.DefaultLimits)
	if err != nil {
		return nil, fmt.Errorf(inCall, b.Name(), err)
	}
	return starlark.String(doc), nil
}

// xmlFind returns the first match, or None. None rather than an empty element
// so `if xml.find(...)` reads the way an author expects and a missing node
// cannot be mistaken for an empty one.
func xmlFind(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	node, path, err := unpackNodeAndPath(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	match, err := xmltree.Find(node, path)
	if err != nil {
		return nil, fmt.Errorf(inCall, b.Name(), err)
	}
	if match == nil {
		return starlark.None, nil
	}
	return &Element{node: match}, nil
}

// xmlFindAll returns every match as a list, empty when nothing matches.
func xmlFindAll(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	node, path, err := unpackNodeAndPath(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	matches, err := xmltree.FindAll(node, path)
	if err != nil {
		return nil, fmt.Errorf(inCall, b.Name(), err)
	}
	return elementList(matches), nil
}

// unpackNodeAndPath reads the (node, path) arguments the two search functions
// share.
func unpackNodeAndPath(b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (*xmltree.Node, string, error) {
	var (
		value starlark.Value
		path  string
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "node", &value, "path", &path); err != nil {
		return nil, "", argErr(b, err)
	}
	el, ok := value.(*Element)
	if !ok {
		return nil, "", fmt.Errorf("in %s: node is %s; pass an element from xml.decode, xml.find or a node's children",
			b.Name(), value.Type())
	}
	return el.node, path, nil
}

// elementList wraps matched nodes as a Starlark list.
func elementList(nodes []*xmltree.Node) *starlark.List {
	values := make([]starlark.Value, 0, len(nodes))
	for _, n := range nodes {
		values = append(values, &Element{node: n})
	}
	return starlark.NewList(values)
}
