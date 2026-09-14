package xmltree

// Find returns the first node the path matches, or nil when nothing matches.
// A path outside the supported subset is an error, never a nil result: the two
// answers mean different things and a caller has to be able to tell them
// apart.
func Find(n *Node, path string) (*Node, error) {
	matches, err := FindAll(n, path)
	if err != nil || len(matches) == 0 {
		return nil, err
	}
	return matches[0], nil
}

// FindAll returns every node the path matches.
func FindAll(n *Node, path string) ([]*Node, error) {
	p, err := Compile(path)
	if err != nil {
		return nil, err
	}
	return p.Select(n), nil
}

// Select evaluates a compiled path against a node.
//
// Each step is applied to every node the previous step produced, and a node
// reached by more than one of them is returned once, in the order it was first
// reached. That can only happen when a descendant step runs from nested
// context nodes; for the child axis the result is document order.
func (p Path) Select(n *Node) []*Node {
	if n == nil {
		return nil
	}
	ctx := []*Node{n}
	for _, s := range p.steps {
		var next []*Node
		seen := map[*Node]bool{}
		for _, c := range ctx {
			for _, m := range s.apply(c) {
				if !seen[m] {
					seen[m] = true
					next = append(next, m)
				}
			}
		}
		if len(next) == 0 {
			return nil
		}
		ctx = next
	}
	return ctx
}

// apply produces the step's matches under one context node: the axis supplies
// the candidates, the name test narrows them, and each predicate filters what
// the one before it left.
func (s step) apply(ctx *Node) []*Node {
	candidates := ctx.Children
	if s.axis == axisDescendant {
		candidates = descendants(ctx)
	}
	matches := make([]*Node, 0, len(candidates))
	for _, c := range candidates {
		if s.name == wildcard || c.Tag == s.name {
			matches = append(matches, c)
		}
	}
	for _, pred := range s.preds {
		matches = pred.filter(matches)
	}
	return matches
}

// filter applies one predicate to a step's matches. A position selects from
// what the predicates before it left, which is how [@type='x'][1] reads: the
// first node that also has that attribute.
func (p predicate) filter(matches []*Node) []*Node {
	if p.index > 0 {
		if p.index > len(matches) {
			return nil
		}
		return matches[p.index-1 : p.index]
	}
	kept := matches[:0:0]
	for _, m := range matches {
		if v, ok := m.Attrs[p.attr]; ok && v == p.value {
			kept = append(kept, m)
		}
	}
	return kept
}

// descendants returns every node below n in document order, excluding n. It
// walks with an explicit stack for the same reason the decoder does: depth is
// a property of the input.
func descendants(n *Node) []*Node {
	var out []*Node
	stack := []*Node{n}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for i := len(cur.Children) - 1; i >= 0; i-- {
			stack = append(stack, cur.Children[i])
		}
		if cur != n {
			out = append(out, cur)
		}
	}
	return out
}
