package scriptflow

import (
	"fmt"
	"slices"

	"go.starlark.net/syntax"
)

// invoke expands one call of a user function in place. The function's steps
// become steps of this expansion, drawn in its box unless it is the script's
// main() or a one-call wrapper, which is folded into its step.
func (a *analyzer) invoke(caller *frame, d *syntax.DefStmt, c *syntax.CallExpr) origins {
	name := d.Name.Name
	if a.expanding(name) {
		// A recursive call: the interpreter refuses it when it is made, so it
		// reaches nothing, and expanding it would never end.
		return a.argOrigins(caller, c)
	}
	a.expansions++
	if len(a.stack) >= maxDepth || a.expansions > maxExpansions {
		a.truncated = true
		return a.argOrigins(caller, c)
	}
	fr := a.calleeFrame(caller, name, int(c.Lparen.Line))
	fr.sites = slices.Clone(caller.sites)
	if c != a.entryCall {
		fr.sites = append(fr.sites, position(c.Lparen))
	}
	a.bindParams(caller, fr, d, c)
	a.stack = append(a.stack, fr)
	a.stmts(fr, d.Body)
	a.stack = a.stack[:len(a.stack)-1]
	if !a.hasSteps[name] {
		// A pure function reshapes the value it returns, and the edge that value
		// travels on names it.
		return fr.ret.via(name)
	}
	return fr.ret
}

// expanding reports whether a function is already being expanded.
func (a *analyzer) expanding(name string) bool {
	for _, fr := range a.stack {
		if fr.fn == name {
			return true
		}
	}
	return false
}

// calleeFrame builds the frame one call of name runs in, deciding the box its
// steps are drawn in.
func (a *analyzer) calleeFrame(caller *frame, name string, line int) *frame {
	fr := newFrame(name)
	fr.group, fr.chain = caller.group, caller.chain
	fr.loops = append([]string(nil), caller.loops...)
	switch {
	case caller.wrapper != "":
		// Inside a folded wrapper: the outermost wrapper names the step.
		fr.wrapper, fr.site = caller.wrapper, caller.site
		fr.chain = caller.chain + ">" + name
	case a.wrapper[name]:
		fr.wrapper, fr.site = name, line
		fr.chain = fmt.Sprintf("%s>%s@%d", caller.chain, name, line)
	case !a.hasSteps[name]:
		// A pure function: its calls are part of the caller's step, if any.
		fr.chain = fmt.Sprintf("%s>%s@%d", caller.chain, name, line)
	case caller == a.top && a.whole == name:
		// main(): the whole script, not a box around it.
	default:
		fr.group = caller.group + "/" + name
		fr.chain = fmt.Sprintf("%s@%d", fr.group, line)
		gi, ok := a.groups[fr.group]
		if !ok {
			gi = &groupInfo{fn: name, calledFrom: map[int]bool{}}
			a.groups[fr.group] = gi
		}
		gi.calledFrom[line] = true
	}
	fr.lexical = a.enclosing(name)
	return fr
}

// enclosing is the frame a nested def sees the variables of: the nearest
// active expansion of the function it is written in, or nil for a module-level
// def.
func (a *analyzer) enclosing(name string) *frame {
	p := a.parentOf[name]
	if p == "" {
		return nil
	}
	for i := len(a.stack) - 1; i >= 0; i-- {
		if a.stack[i].fn == p {
			return a.stack[i]
		}
	}
	return nil
}

// bindParams binds a call's arguments to the callee's parameters, rendering
// each in the caller's scope so a step inside the callee shows what the caller
// passed.
func (a *analyzer) bindParams(caller, fr *frame, d *syntax.DefStmt, c *syntax.CallExpr) {
	names := a.bindDefaults(fr, d)
	pos := 0
	for _, arg := range c.Args {
		pname, val := "", arg
		if b, ok := arg.(*syntax.BinaryExpr); ok && b.Op == syntax.EQ {
			if id, ok := b.X.(*syntax.Ident); ok {
				pname, val = id.Name, b.Y
			}
		} else if pos < len(names) {
			pname = names[pos]
			pos++
		}
		t := a.expr(caller, val)
		if pname == "" {
			continue
		}
		fr.env[pname] = t
		if v, full := a.renderIn(caller, val); full {
			fr.known[pname] = v
		} else {
			fr.shown[pname] = v
		}
		if dict := a.dictIn(caller, val); dict != nil {
			fr.dicts[pname] = dict
		}
	}
}

// bindDefaults binds each parameter's default, evaluated where the def is, and
// returns the parameter names in order.
func (a *analyzer) bindDefaults(fr *frame, d *syntax.DefStmt) []string {
	names := make([]string, 0, len(d.Params))
	for _, p := range d.Params {
		switch p := p.(type) {
		case *syntax.Ident:
			names = append(names, p.Name)
		case *syntax.BinaryExpr:
			id, ok := p.X.(*syntax.Ident)
			if !ok {
				continue
			}
			names = append(names, id.Name)
			fr.env[id.Name] = a.expr(a.top, p.Y)
			if v, full := a.renderIn(a.top, p.Y); full {
				fr.known[id.Name] = v
			}
		}
	}
	return names
}
